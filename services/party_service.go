// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 PartyService，为"一起看"功能的核心协调层。
//
// 设计要点：
//   - Wails 绑定服务，提供 StartParty / StopParty / GetPartyState / GetEpisodeList 方法
//   - 纯做服务提供方：启动/停止下载器、HTTP 流服务、WS 服务
//   - 不参与业务决策：不解析视频、不维护时钟、不主动控制播放
//   - 房间状态纯内存管理，不写数据库
//   - 通过 Downloader 接口引用 party_downloader.go 的产物（确保并行编译）
//   - 通过 EventPusher 回调推送 Wails Event 给前端
package services

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tg-download/internal/telegram"
)

// Downloader 由 party_downloader.go 的 PartyDownloader 类型实现。
// 本接口声明在此处，使 PartyService 可在 party_downloader.go 完成前编译通过。
//
// 方法语义：
//   - GetBlocks: 返回 240 块位图（已下载标记）、当前播放块索引、正在下载块索引、下载速度（字节/秒）
//   - SetPlayingBlock: 设置当前播放块索引（影响预载策略）
//   - SwitchToModeB: 切换到追播放头模式（从指定块开始顺序下载）
//   - ReadRange: 读取文件指定字节范围 [start, end]，end=-1 表示读到 EOF
//   - Cancel: 取消所有下载任务
//   - SetOnBlocksUpdate: 设置块下载完成回调，供 PartyService 推送 Wails Event
type Downloader interface {
	Start()
	GetBlocks() (downloaded [240]bool, playingBlock, downloadingBlock int, speed int64)
	SetPlayingBlock(blockIndex int)
	SwitchToModeB(blockIndex int)
	SwitchToModeBByTime(currentTime, duration float64)
	ReadRange(start, end int64) (io.Reader, error)
	GetFileSize() int64
	Cancel()
	SetOnBlocksUpdate(cb func())
}

// EventPusher Wails 事件推送接口。
// 由 events.WailsEmitter 实现（鸭子类型），避免本文件直接依赖 events 包。
type EventPusher interface {
	PushEvent(eventName string, data ...any)
}

// PartyEpisode 一起看房间内的分集信息（精简版，仅含播放所需字段）。
type PartyEpisode struct {
	VEID        int64  `json:"ve_id"`
	VECollect   int    `json:"ve_collect"`
	VETitle     string `json:"ve_title"`
	VSChannelID int64  `json:"vs_channel_id"`
	VEMessageID int64  `json:"ve_message_id"`
}

// PartyRoom 一起看房间状态（纯内存）。
type PartyRoom struct {
	RoomID       string
	HostToken    string
	VR_ID        int64
	CacheDir     string
	StreamServer *PartyStreamServer
	WSServer     *PartyWSServer
	Downloader   Downloader
	EpisodeList  []PartyEpisode
	CurrentVEID  int64
	// lastDuration 最近一次 report_duration 上报的视频时长(秒),供 OnSeek 计算 blockIndex
	lastDuration float64

	// 预载下一集(ModeC):由当前集下载完成 100% 触发,独立 PartyDownloader 实例
	// PreloadDownloader 预载下载器实例,nil 表示无预载
	// PreloadVEID 预载的分集 ID,0 表示无预载
	PreloadDownloader Downloader
	PreloadVEID       int64
}

// PartyStartResult StartParty 方法的返回值。
type PartyStartResult struct {
	RoomID      string         `json:"roomId"`
	WSUrl       string         `json:"wsUrl"`
	StreamUrl   string         `json:"streamUrl"`
	InviteUrl   string         `json:"inviteUrl"`
	LanIp       string         `json:"lanIp"`
	VEID        int64          `json:"ve_id"`
	EpisodeList []PartyEpisode `json:"episodeList"`
}

// PartyState GetPartyState 方法的返回值，供 PartyConsole 初始化。
type PartyState struct {
	RoomID      string         `json:"roomId"`
	VR_ID       int64          `json:"vr_id"`
	CurrentVEID int64          `json:"current_ve_id"`
	LanIp       string         `json:"lanIp"`
	WSUrl       string         `json:"wsUrl"`
	StreamUrl   string         `json:"streamUrl"`
	InviteUrl   string         `json:"inviteUrl"`
	EpisodeList []PartyEpisode `json:"episodeList"`
	// Clients 当前在线客户端列表(供 PartyConsole 初始化展示)
	Clients []WSClientInfo `json:"clients"`
	// HostName 当前房主名称
	HostName string `json:"hostName"`
}

// PartyService 一起看的 Wails 绑定服务。
type PartyService struct {
	db      *sql.DB
	manager *telegram.ClientManager
	rooms   map[string]*PartyRoom
	mu      sync.Mutex
	app     *application.App
	emitter EventPusher
}

// NewPartyService 创建 PartyService 实例。
func NewPartyService(db *sql.DB, manager *telegram.ClientManager) *PartyService {
	return &PartyService{
		db:      db,
		manager: manager,
		rooms:   make(map[string]*PartyRoom),
	}
}

// SetApp 注入 Wails 应用实例（main.go 阶段 3 调用）。
func (s *PartyService) SetApp(app *application.App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.app = app
}

// SetEmitter 注入事件推送器（main.go 阶段 3 调用）。
func (s *PartyService) SetEmitter(emitter EventPusher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emitter = emitter
}

// StartParty 开启一起看。
// 流程：
//  1. 生成 8 位 roomId（大写字母+数字）+ 32 位 hostToken
//  2. 查询分集列表，取第 1 集元数据
//  3. 创建缓存目录 <configDir>/TGDownload/party_cache/<roomId>/
//  4. 启动 PartyStreamServer（0.0.0.0:0）
//  5. 启动 PartyWSServer（0.0.0.0:0）
//  6. 返回 PartyStartResult（含 wsUrl/streamUrl/inviteUrl/lanIp）
//
// 注：downloader 由其他 sub-agent 实现的 party_downloader.go 提供，
// 此处仅在 rooms 表中预留 Downloader 字段为 nil，由外部 setter 注入。
// 文件大小由 downloader 内部 fetch，本方法不预先查询。
func (s *PartyService) StartParty(vrID int64) (*PartyStartResult, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	// 1. 生成 roomId 与 hostToken
	roomID, err := generateRoomID()
	if err != nil {
		return nil, fmt.Errorf("生成 roomId 失败: %w", err)
	}
	hostToken, err := generateHostToken()
	if err != nil {
		return nil, fmt.Errorf("生成 hostToken 失败: %w", err)
	}

	// 2. 查询分集列表
	episodes, err := s.GetEpisodeList(vrID)
	if err != nil {
		return nil, fmt.Errorf("查询分集列表失败: %w", err)
	}
	if len(episodes) == 0 {
		return nil, fmt.Errorf("视频 #%d 无分集数据", vrID)
	}
	firstEpisode := episodes[0]

	// 3. 创建缓存目录
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	cacheDir := filepath.Join(configDir, "TGDownload", "party_cache", roomID)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("创建缓存目录失败: %w", err)
	}

	// 4. 启动 PartyStreamServer
	// DownloaderProvider 闭包：通过 s.rooms 查找对应 downloader
	streamServer := NewPartyStreamServer(func(roomId string) Downloader {
		s.mu.Lock()
		defer s.mu.Unlock()
		if room, ok := s.rooms[roomId]; ok {
			return room.Downloader
		}
		return nil
	})

	// 5. 创建 PartyWSServer 并 Attach 到 streamServer 的 mux（共用同一端口）
	// 观看端 HTML 从 /party/<roomId> 加载后，用页面 origin 推断 wsUrl（/ws?roomId=xxx），
	// 因此 WS 必须与 stream 同端口，否则浏览器连不上。
	wsServer := NewPartyWSServer(s.buildWSHooks())
	wsServer.Attach(streamServer.Mux())

	if _, err := streamServer.Start(); err != nil {
		_ = os.RemoveAll(cacheDir)
		return nil, fmt.Errorf("启动流服务失败: %w", err)
	}

	// 6. 构造 room 并存入 rooms
	room := &PartyRoom{
		RoomID:       roomID,
		HostToken:    hostToken,
		VR_ID:        vrID,
		CacheDir:     cacheDir,
		StreamServer: streamServer,
		WSServer:     wsServer,
		Downloader:   nil, // 下面创建并注入
		EpisodeList:  episodes,
		CurrentVEID:  firstEpisode.VEID,
	}
	s.mu.Lock()
	s.rooms[roomID] = room
	s.mu.Unlock()

	// 7. 创建并启动 PartyDownloader(fileSize=0 由 downloader 内部 fetch)
	dl, err := NewPartyDownloader(s.db, s.manager, cacheDir, firstEpisode.VEID, firstEpisode.VSChannelID, firstEpisode.VEMessageID, 0)
	if err != nil {
		// 创建失败回滚
		_ = s.StopParty(roomID)
		return nil, fmt.Errorf("创建下载器失败: %w", err)
	}
	s.SetDownloader(roomID, dl)
	dl.Start()

	// 8. 计算对外可访问的 URL（用 LAN IP 替换 0.0.0.0）
	// WS 与 stream 共用同一端口，wsUrl 用 streamServer.Port()
	lanIP := GetLanIP()
	wsURL := fmt.Sprintf("ws://%s:%d/ws?roomId=%s&token=%s", lanIP, streamServer.Port(), roomID, hostToken)
	streamURL := fmt.Sprintf("http://%s:%d/stream/%s/%d", lanIP, streamServer.Port(), roomID, firstEpisode.VEID)
	inviteURL := fmt.Sprintf("http://%s:%d/party/%s", lanIP, streamServer.Port(), roomID)

	log.Printf("[party] 一起看已启动 roomId=%s vr_id=%d ve_id=%d lanIp=%s", roomID, vrID, firstEpisode.VEID, lanIP)

	return &PartyStartResult{
		RoomID:      roomID,
		WSUrl:       wsURL,
		StreamUrl:   streamURL,
		InviteUrl:   inviteURL,
		LanIp:       lanIP,
		VEID:        firstEpisode.VEID,
		EpisodeList: episodes,
	}, nil
}

// StopParty 结束一起看。
// 流程：
//  1. WS 广播 host_leaving
//  2. 关闭 WS server、Stream server
//  3. 取消 downloader
//  4. 删除整个缓存目录 os.RemoveAll(cacheDir)
func (s *PartyService) StopParty(roomID string) error {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("房间 %s 不存在", roomID)
	}
	delete(s.rooms, roomID)
	s.mu.Unlock()

	// 1. 广播 host_leaving
	room.WSServer.BroadcastHostLeaving(roomID)

	// 2. 关闭 servers
	room.StreamServer.Stop()
	room.WSServer.Stop()

	// 3. 取消 downloader(当前集 + 预载)
	if room.Downloader != nil {
		room.Downloader.Cancel()
	}
	if room.PreloadDownloader != nil {
		room.PreloadDownloader.Cancel()
	}

	// 4. 删除缓存目录
	if err := os.RemoveAll(room.CacheDir); err != nil {
		log.Printf("[party] 删除缓存目录失败 roomId=%s dir=%s: %v", roomID, room.CacheDir, err)
	}

	log.Printf("[party] 一起看已结束 roomId=%s", roomID)
	return nil
}

// GetPartyState 获取一起看房间状态，供 PartyConsole 初始化。
func (s *PartyService) GetPartyState(roomID string) (*PartyState, error) {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("房间 %s 不存在", roomID)
	}

	lanIP := GetLanIP()
	currentVEID := room.CurrentVEID
	// WS 与 stream 共用同一端口
	wsURL := fmt.Sprintf("ws://%s:%d/ws?roomId=%s", lanIP, room.StreamServer.Port(), roomID)
	streamURL := fmt.Sprintf("http://%s:%d/stream/%s/%d", lanIP, room.StreamServer.Port(), roomID, currentVEID)
	inviteURL := fmt.Sprintf("http://%s:%d/party/%s", lanIP, room.StreamServer.Port(), roomID)

	// 拉取当前在线客户端列表与房主名称,供 PartyConsole 初始化展示
	clients, hostName := room.WSServer.GetRoomClients(roomID)

	return &PartyState{
		RoomID:      roomID,
		VR_ID:       room.VR_ID,
		CurrentVEID: currentVEID,
		LanIp:       lanIP,
		WSUrl:       wsURL,
		StreamUrl:   streamURL,
		InviteUrl:   inviteURL,
		EpisodeList: room.EpisodeList,
		Clients:     clients,
		HostName:    hostName,
	}, nil
}

// GetEpisodeList 查询视频的分集列表，转为 PartyEpisode 格式。
func (s *PartyService) GetEpisodeList(vrID int64) ([]PartyEpisode, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	items, err := ListEpisodesLocal(s.db, vrID)
	if err != nil {
		return nil, err
	}
	result := make([]PartyEpisode, 0, len(items))
	for _, it := range items {
		result = append(result, PartyEpisode{
			VEID:        it.EpisodeID,
			VECollect:   it.EpisodeNumber,
			VETitle:     it.Title,
			VSChannelID: it.ChannelID,
			VEMessageID: it.MessageID,
		})
	}
	return result, nil
}

// SwitchEpisode 切换一起看房间的当前分集。
// 由 WS handler 在收到房主 episode_change 消息时调用。
// 返回新的 streamUrl 供 WS 广播给所有观看端。
//
// 副作用:
//   - 取消旧 Downloader(停止旧集下载)
//   - 如果新集正好是预载的下一集 → 升级预载 DL 为当前 DL,启动新的下一集预载
//   - 否则取消预载 DL + 删预载文件 + 创建全新 DL
//   - 删除旧集缓存文件(仅删除旧 veID 对应的 .mp4,不删预载文件)
//   - 重置 lastDuration(等待新集 report_duration 上报)
func (s *PartyService) SwitchEpisode(roomID string, veID int64) (string, error) {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	oldVEID := int64(0)
	preloadDL := Downloader(nil)
	preloadVEID := int64(0)
	if ok {
		oldVEID = room.CurrentVEID
		preloadDL = room.PreloadDownloader
		preloadVEID = room.PreloadVEID
	}
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("房间 %s 不存在", roomID)
	}

	// 校验 veID 是否合法
	var found *PartyEpisode
	for i := range room.EpisodeList {
		if room.EpisodeList[i].VEID == veID {
			found = &room.EpisodeList[i]
			break
		}
	}
	if found == nil {
		return "", fmt.Errorf("分集 ve_id=%d 不存在于房间 %s 的分集列表", veID, roomID)
	}

	// 取消旧当前集 Downloader
	s.mu.Lock()
	oldDL := room.Downloader
	s.mu.Unlock()
	if oldDL != nil {
		oldDL.Cancel()
	}

	// 命中预载:veID == preloadVEID 且预载 DL 存在 → 升级预载为当前,无需删旧预载文件
	// 未命中:取消预载 DL + 删预载文件
	hitPreload := veID == preloadVEID && preloadDL != nil

	var newDL Downloader
	if hitPreload {
		// 升级预载 DL 为当前 DL
		newDL = preloadDL
		// 清空预载字段(下面会触发新的预载)
		s.mu.Lock()
		room.PreloadDownloader = nil
		room.PreloadVEID = 0
		s.mu.Unlock()
		log.Printf("[party] 切集命中预载 roomId=%s ve_id=%d", roomID, veID)
	} else {
		// 未命中:取消预载 DL + 删预载文件
		if preloadDL != nil {
			preloadDL.Cancel()
		}
		if preloadVEID > 0 && preloadVEID != veID {
			preloadFile := filepath.Join(room.CacheDir, fmt.Sprintf("%d.mp4", preloadVEID))
			if err := os.Remove(preloadFile); err != nil && !os.IsNotExist(err) {
				log.Printf("[party] 删除预载文件失败 roomId=%s file=%s: %v", roomID, preloadFile, err)
			}
		}
		// 创建全新 DL
		dl, err := NewPartyDownloader(s.db, s.manager, room.CacheDir, found.VEID, found.VSChannelID, found.VEMessageID, 0)
		if err != nil {
			return "", fmt.Errorf("创建新集下载器失败: %w", err)
		}
		newDL = dl
	}

	// 删除旧当前集文件(切到新集后,旧集文件不再需要)
	if oldVEID != veID && oldVEID > 0 && oldVEID != preloadVEID {
		oldFile := filepath.Join(room.CacheDir, fmt.Sprintf("%d.mp4", oldVEID))
		if err := os.Remove(oldFile); err != nil && !os.IsNotExist(err) {
			log.Printf("[party] 删除旧集文件失败 roomId=%s file=%s: %v", roomID, oldFile, err)
		}
	}

	// 更新房间状态
	s.mu.Lock()
	room.CurrentVEID = veID
	room.lastDuration = 0
	s.mu.Unlock()

	// 注入并启动(命中预载时 DL 已在运行,Start 幂等;未命中时是新 DL)
	s.SetDownloader(roomID, newDL)
	newDL.Start()

	// 推送 Wails Event
	s.pushEvent("party:episode_changed", map[string]interface{}{
		"roomId": roomID,
		"ve_id":  veID,
	})

	// 构造新的 streamUrl
	lanIP := GetLanIP()
	streamURL := fmt.Sprintf("http://%s:%d/stream/%s/%d", lanIP, room.StreamServer.Port(), roomID, veID)
	log.Printf("[party] 切集 roomId=%s old_ve_id=%d new_ve_id=%d hitPreload=%v streamUrl=%s", roomID, oldVEID, veID, hitPreload, streamURL)
	return streamURL, nil
}

// SetDownloader 由 party_downloader.go 完成后注入 downloader 实例。
// 供外部 sub-agent 在创建 PartyDownloader 后调用。
// 注入时会自动设置 blocks 更新回调，用于推送 party:download_blocks Wails Event。
func (s *PartyService) SetDownloader(roomID string, dl Downloader) {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	s.mu.Unlock()
	if !ok {
		return
	}
	room.Downloader = dl
	// 设置 blocks 更新回调：每完成一块下载就推送 Wails Event,并检测当前集 100% 完成 → 启动预载
	dl.SetOnBlocksUpdate(func() {
		downloaded, playingBlock, downloadingBlock, speed := dl.GetBlocks()
		// 转为 []int 便于 JSON 序列化（前端 15×16 grid 渲染）
		blocks := make([]int, len(downloaded))
		for i, v := range downloaded {
			if v {
				blocks[i] = 1
			}
		}
		s.pushEvent("party:download_blocks", map[string]interface{}{
			"roomId":            roomID,
			"ve_id":             room.CurrentVEID,
			"downloaded":        blocks,
			"playing_block":     playingBlock,
			"downloading_block": downloadingBlock,
			"speed":             speed,
		})
		// ModeC 预载触发:当前集 100% 完成 && 无预载 && 有下一集
		allDone := true
		for _, v := range downloaded {
			if !v {
				allDone = false
				break
			}
		}
		if allDone {
			s.maybeStartPreload(roomID)
		}
	})
}

// maybeStartPreload 启动下一集预载(ModeC)。
// 调用前需检查:当前集 100% 完成 && 预载未启动 && 有下一集。
// 此函数内部自检并加锁,可安全并发调用。
func (s *PartyService) maybeStartPreload(roomID string) {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	if !ok || room.PreloadDownloader != nil {
		s.mu.Unlock()
		return
	}
	// 找下一集(基于 CurrentVEID 在 EpisodeList 中的索引)
	nextIdx := -1
	for i := range room.EpisodeList {
		if room.EpisodeList[i].VEID == room.CurrentVEID {
			nextIdx = i + 1
			break
		}
	}
	if nextIdx < 0 || nextIdx >= len(room.EpisodeList) {
		s.mu.Unlock()
		return // 已是最后一集,无下一集可预载
	}
	nextEp := room.EpisodeList[nextIdx]
	// 占位标记,防止并发重复启动
	room.PreloadVEID = nextEp.VEID
	s.mu.Unlock()

	// 创建预载下载器(独立文件,不影响当前集)
	preloadDL, err := NewPartyDownloader(s.db, s.manager, room.CacheDir, nextEp.VEID, nextEp.VSChannelID, nextEp.VEMessageID, 0)
	if err != nil {
		log.Printf("[party] 预载下一集失败 roomId=%s ve_id=%d: %v", roomID, nextEp.VEID, err)
		s.mu.Lock()
		room.PreloadVEID = 0
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	room.PreloadDownloader = preloadDL
	s.mu.Unlock()
	preloadDL.Start()
	log.Printf("[party] 预载下一集启动 roomId=%s preload_ve_id=%d", roomID, nextEp.VEID)
}

// buildWSHooks 构造 WS 事件钩子集合。
// 所有钩子都通过 PartyService 的方法/字段处理副作用与 Wails Event 推送。
func (s *PartyService) buildWSHooks() WSHooks {
	return WSHooks{
		OnSeek: func(roomID string, currentTime float64) {
			s.mu.Lock()
			room, ok := s.rooms[roomID]
			duration := float64(0)
			if ok {
				duration = room.lastDuration
			}
			s.mu.Unlock()
			if !ok || room.Downloader == nil {
				return
			}
			// 按播放时间估算块索引并切到模式 B
			room.Downloader.SwitchToModeBByTime(currentTime, duration)
		},
		OnEpisodeChange: func(roomID string, veID int64) (string, error) {
			return s.SwitchEpisode(roomID, veID)
		},
		OnReportDuration: func(roomID string, veID int64, duration float64) {
			// 记录 duration 供 OnSeek 计算 blockIndex
			s.mu.Lock()
			room, ok := s.rooms[roomID]
			if ok {
				room.lastDuration = duration
			}
			s.mu.Unlock()
			// 通过独立的 Wails Event 推送 duration 给 PartyConsole
			// 不复用 party:playback_state,避免覆盖 currentTime/playing/rate
			s.pushEvent("party:duration_reported", map[string]interface{}{
				"roomId":   roomID,
				"ve_id":    veID,
				"duration": duration,
			})
		},
		OnClientsChanged: func(roomID string, clients []WSClientInfo, hostName string) {
			clientInfos := make([]map[string]interface{}, 0, len(clients))
			for _, c := range clients {
				clientInfos = append(clientInfos, map[string]interface{}{
					"name":   c.Name,
					"isHost": c.IsHost,
				})
			}
			s.pushEvent("party:clients_changed", map[string]interface{}{
				"roomId":   roomID,
				"clients":  clientInfos,
				"hostName": hostName,
			})
		},
		OnStateSync: func(roomID string, veID int64, currentTime float64, playing bool, rate float64) {
			s.pushEvent("party:playback_state", map[string]interface{}{
				"roomId":      roomID,
				"ve_id":       veID,
				"currentTime": currentTime,
				"playing":     playing,
				"rate":        rate,
			})
		},
		OnGetStreamUrl: func(roomID string) (string, int64) {
			s.mu.Lock()
			room, ok := s.rooms[roomID]
			s.mu.Unlock()
			if !ok {
				return "", 0
			}
			lanIP := GetLanIP()
			streamURL := fmt.Sprintf("http://%s:%d/stream/%s/%d", lanIP, room.StreamServer.Port(), roomID, room.CurrentVEID)
			return streamURL, room.CurrentVEID
		},
		OnGetEpisodeList: func(roomID string) []PartyEpisode {
			s.mu.Lock()
			room, ok := s.rooms[roomID]
			s.mu.Unlock()
			if !ok {
				return nil
			}
			// 返回副本,避免并发修改
			eps := make([]PartyEpisode, len(room.EpisodeList))
			copy(eps, room.EpisodeList)
			return eps
		},
	}
}

// pushEvent 推送 Wails Event 给前端。
// 若 emitter 未注入则静默跳过。
func (s *PartyService) pushEvent(eventName string, data ...any) {
	s.mu.Lock()
	emitter := s.emitter
	s.mu.Unlock()
	if emitter == nil {
		return
	}
	emitter.PushEvent(eventName, data...)
}

// generateRoomID 生成 8 位 roomId（大写字母+数字）。
// 字符集：A-Z + 0-9，共 36 个字符。
func generateRoomID() (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 8
	result := make([]byte, length)
	max := big.NewInt(int64(len(charset)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}

// generateHostToken 生成 32 位随机 hostToken（小写十六进制）。
func generateHostToken() (string, error) {
	b := make([]byte, 16) // 16 字节 = 32 个十六进制字符
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
