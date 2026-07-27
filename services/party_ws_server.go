// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 PartyWSServer，为"一起看"房间提供 WebSocket 服务。
//
// 设计要点：
//   - 不独立监听端口：通过 Attach 注册到 PartyStreamServer 的 mux 上，共用同一端口
//   - 路由 /ws?roomId=xxx&token=xxx
//   - 纯消息转发，不维护业务状态（state_sync 数据只缓存用于新加入者 welcome）
//   - 房主权威：第一个加入者为房主，离开自动转移给下一个
//   - 事件钩子：seek 触发 downloader.SwitchToModeB，episode_change 触发切集流程
//   - 30s 无心跳断开连接
//
// 房间与客户端模型：
//   - Room: 持有 clients 列表 + hostClient 引用 + 最近一次 state_sync 数据
//   - Client: 持有 conn + clientName + isHost + lastPing
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsHeartbeatTimeout 心跳超时：30s 无心跳断开连接。
const wsHeartbeatTimeout = 30 * time.Second

// wsWriteTimeout 写超时。
const wsWriteTimeout = 5 * time.Second

// wsReadBufferSize 读缓冲区大小。
const wsReadBufferSize = 4096

// WSMessage WS 协议消息体。
// 所有客户端->服务端、服务端->客户端的消息都遵循此结构。
type WSMessage struct {
	Type        string          `json:"type"`
	RoomID      string          `json:"roomId,omitempty"`
	ClientName  string          `json:"clientName,omitempty"`
	IsHost      bool            `json:"isHost,omitempty"`
	IsMe        bool            `json:"isMe,omitempty"`
	CurrentTime float64         `json:"currentTime,omitempty"`
	Playing     bool            `json:"playing,omitempty"`
	Rate        float64         `json:"rate,omitempty"`
	VEID        int64           `json:"ve_id,omitempty"`
	StreamUrl   string          `json:"streamUrl,omitempty"`
	Duration    float64         `json:"duration,omitempty"`
	Clients     []WSClientInfo  `json:"clients,omitempty"`
	HostName    string          `json:"hostName,omitempty"`
	ClientCount int             `json:"clientCount,omitempty"`
	// EpisodeList 分集列表(welcome 消息携带,供观看端启用上一集/下一集按钮)
	EpisodeList []PartyEpisode `json:"episodeList,omitempty"`
	T           int64          `json:"t,omitempty"`
	ServerTs    int64          `json:"serverTs,omitempty"`
	Error       string          `json:"error,omitempty"`
	Raw         json.RawMessage `json:"-"` // 原始 JSON（用于透传未识别字段）
}

// WSClientInfo 客户端信息（welcome/client_joined 等消息中携带）。
type WSClientInfo struct {
	Name   string `json:"name"`
	IsHost bool   `json:"isHost"`
}

// WSClient 观看端 WS 客户端。
type WSClient struct {
	conn       *websocket.Conn
	clientName string
	isHost     bool
	lastPing   time.Time
	mu         sync.Mutex // 保护 conn 写入
}

// WSRoom 一起看房间（WS 维度）。
type WSRoom struct {
	roomID        string
	clients       []*WSClient
	hostClient    *WSClient
	lastStateSync *WSMessage // 最近一次 state_sync 数据（用于新加入者 welcome）
	mu            sync.Mutex
}

// WSHooks 事件钩子集合，由 PartyService 注入。
// 所有钩子均为可选（nil 时跳过），避免循环依赖。
type WSHooks struct {
	// OnSeek 房主 seek 时触发，用于切 downloader 模式 B。
	OnSeek func(roomID string, currentTime float64)
	// OnEpisodeChange 房主切集时触发，用于 PartyService.SwitchEpisode。
	OnEpisodeChange func(roomID string, veID int64) (newStreamUrl string, err error)
	// OnReportDuration 观看端上报 duration，通过 Wails Event 转发。
	OnReportDuration func(roomID string, veID int64, duration float64)
	// OnClientsChanged 客户端列表变化，通过 Wails Event 转发。
	OnClientsChanged func(roomID string, clients []WSClientInfo, hostName string)
	// OnStateSync 房主周期上报状态，通过 Wails Event 转发。
	OnStateSync func(roomID string, veID int64, currentTime float64, playing bool, rate float64)
	// OnGetStreamUrl 查询指定房间的当前 streamUrl，用于 welcome 消息携带给新加入者。
	// 返回 (streamUrl, veId)，veId 为当前播放的分集 ID。
	OnGetStreamUrl func(roomID string) (streamUrl string, veId int64)
	// OnGetEpisodeList 查询指定房间的分集列表,用于 welcome 消息携带给观看端,
	// 使其能启用上一集/下一集按钮。返回 nil 表示无分集列表(单集或未就绪)。
	OnGetEpisodeList func(roomID string) []PartyEpisode
}

// PartyWSServer 一起看房间的 WebSocket 服务。
// 不独立监听端口：通过 Attach 注册到 PartyStreamServer 的 mux 上，
// 与 stream 服务共用同一端口（观看端 HTML 用页面 origin 推断 wsUrl 时自然正确）。
type PartyWSServer struct {
	upgrader websocket.Upgrader
	rooms    map[string]*WSRoom
	mu       sync.Mutex
	hooks    WSHooks
}

// NewPartyWSServer 创建 PartyWSServer 实例。
// hooks 由 PartyService 注入，用于事件钩子回调。
func NewPartyWSServer(hooks WSHooks) *PartyWSServer {
	return &PartyWSServer{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  wsReadBufferSize,
			WriteBufferSize: wsReadBufferSize,
			// 完全开放，不校验 Origin
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		rooms: make(map[string]*WSRoom),
		hooks: hooks,
	}
}

// Attach 把 /ws 路由注册到给定的 mux 上，与 PartyStreamServer 共享同一端口。
// 必须在 PartyStreamServer.Start 之前调用，确保 /ws 路由在 listener 启动时就位。
func (s *PartyWSServer) Attach(mux *http.ServeMux) {
	mux.HandleFunc("/ws", s.handleWS)
}

// Stop 清理所有房间状态。实际 http.Server 由 PartyStreamServer 管理，无需此处关闭。
func (s *PartyWSServer) Stop() {
	s.mu.Lock()
	s.rooms = make(map[string]*WSRoom)
	s.mu.Unlock()
}

// BroadcastHostLeaving 广播 host_leaving 消息给指定房间所有客户端。
// 一起看结束时由 PartyService 调用。
func (s *PartyWSServer) BroadcastHostLeaving(roomID string) {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	s.mu.Unlock()
	if !ok {
		return
	}
	room.mu.Lock()
	clients := make([]*WSClient, len(room.clients))
	copy(clients, room.clients)
	room.mu.Unlock()

	msg := WSMessage{Type: "host_leaving"}
	s.broadcast(clients, msg, nil)
}

// GetRoomClients 查询指定房间当前所有客户端列表与房主名称。
// 供 PartyService.GetPartyState 在 PartyConsole 初始化时拉取在线列表使用。
// 房间不存在时返回空列表与空房主名。
func (s *PartyWSServer) GetRoomClients(roomID string) ([]WSClientInfo, string) {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	s.mu.Unlock()
	if !ok {
		return []WSClientInfo{}, ""
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	infos := make([]WSClientInfo, 0, len(room.clients))
	for _, c := range room.clients {
		infos = append(infos, WSClientInfo{Name: c.clientName, IsHost: c.isHost})
	}
	hostName := ""
	if room.hostClient != nil {
		hostName = room.hostClient.clientName
	}
	return infos, hostName
}

// handleWS 处理 WS 连接请求。
// 路由：/ws?roomId=xxx&token=xxx&clientName=xxx
func (s *PartyWSServer) handleWS(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("roomId")
	clientName := r.URL.Query().Get("clientName")
	if roomID == "" || clientName == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少 roomId 或 clientName 参数")
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[party-ws] upgrade 失败 roomId=%s: %v", roomID, err)
		return
	}

	client := &WSClient{
		conn:       conn,
		clientName: clientName,
		lastPing:   time.Now(),
	}

	// 加入房间（首个客户端自动设为房主）
	room := s.joinRoom(roomID, client)
	defer s.removeClient(room, client)

	// 启动心跳检测 goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.heartbeatWatcher(ctx, room, client)

	// 推送 welcome
	s.sendWelcome(room, client)

	// 广播 client_joined
	s.broadcastClientJoined(room, client)

	// 消息循环
	s.readLoop(room, client)
}

// joinRoom 将客户端加入指定房间，首个客户端自动设为房主。
func (s *PartyWSServer) joinRoom(roomID string, client *WSClient) *WSRoom {
	s.mu.Lock()
	room, ok := s.rooms[roomID]
	if !ok {
		room = &WSRoom{roomID: roomID}
		s.rooms[roomID] = room
	}
	s.mu.Unlock()

	room.mu.Lock()
	defer room.mu.Unlock()
	room.clients = append(room.clients, client)
	if room.hostClient == nil {
		room.hostClient = client
		client.isHost = true
	}
	return room
}

// removeClient 从房间移除客户端，处理房主转移与 client_left 广播。
func (s *PartyWSServer) removeClient(room *WSRoom, client *WSClient) {
	room.mu.Lock()
	wasHost := client.isHost
	// 从 clients 列表移除
	for i, c := range room.clients {
		if c == client {
			room.clients = append(room.clients[:i], room.clients[i+1:]...)
			break
		}
	}
	// 房主离开：自动转移给第一个剩余客户端
	if wasHost && room.hostClient == client {
		room.hostClient = nil
		if len(room.clients) > 0 {
			room.hostClient = room.clients[0]
			room.hostClient.isHost = true
		}
	}
	hostName := ""
	if room.hostClient != nil {
		hostName = room.hostClient.clientName
	}
	clients := make([]*WSClient, len(room.clients))
	copy(clients, room.clients)
	clientName := client.clientName
	clientCount := len(room.clients)
	room.mu.Unlock()

	// 广播 client_left
	s.broadcast(clients, WSMessage{
		Type:        "client_left",
		ClientName:  clientName,
		ClientCount: clientCount,
	}, client)

	// 房主转移：广播 host_changed（仅原房主离开时）
	if wasHost && room.hostClient != nil {
		// 先给新房主单独发一条带 isMe=true 的消息,确保其切换为房主 UI
		s.send(room.hostClient, WSMessage{
			Type:     "host_changed",
			HostName: hostName,
			IsMe:     true,
		})
		// 再给其他客户端广播带 isMe=false 的消息
		others := make([]*WSClient, 0, len(clients))
		for _, c := range clients {
			if c != room.hostClient {
				others = append(others, c)
			}
		}
		s.broadcast(others, WSMessage{
			Type:     "host_changed",
			HostName: hostName,
			IsMe:     false,
		}, nil)
	}

	// 触发 clients_changed 钩子
	if s.hooks.OnClientsChanged != nil {
		infos := make([]WSClientInfo, 0, len(clients))
		for _, c := range clients {
			infos = append(infos, WSClientInfo{Name: c.clientName, IsHost: c.isHost})
		}
		s.hooks.OnClientsChanged(room.roomID, infos, hostName)
	}

	// 房间空了，从 s.rooms 清理（加锁）
	if clientCount == 0 {
		s.mu.Lock()
		// 二次检查：仍然空才删
		room.mu.Lock()
		empty := len(room.clients) == 0
		room.mu.Unlock()
		if empty {
			delete(s.rooms, room.roomID)
		}
		s.mu.Unlock()
	}
}

// sendWelcome 向新加入的客户端推送 welcome 消息。
// 携带 isHost、最近一次 state_sync 状态、当前客户端列表。
func (s *PartyWSServer) sendWelcome(room *WSRoom, client *WSClient) {
	room.mu.Lock()
	clients := make([]WSClientInfo, 0, len(room.clients))
	for _, c := range room.clients {
		clients = append(clients, WSClientInfo{Name: c.clientName, IsHost: c.isHost})
	}
	var state *WSMessage
	if room.lastStateSync != nil {
		// 拷贝一份避免并发修改
		cp := *room.lastStateSync
		state = &cp
	}
	room.mu.Unlock()

	msg := WSMessage{
		Type:    "welcome",
		IsHost:  client.isHost,
		Clients: clients,
	}
	if state != nil {
		msg.CurrentTime = state.CurrentTime
		msg.Playing = state.Playing
		msg.Rate = state.Rate
		msg.VEID = state.VEID
	}
	// 始终通过钩子补充 streamUrl 与 veId，让观看端能立即 setVideoSrc 开始播放。
	// 若有 state_sync 则 veId 已就绪，但 streamUrl 仍需钩子提供（state_sync 不带 streamUrl）。
	if s.hooks.OnGetStreamUrl != nil {
		if streamUrl, veId := s.hooks.OnGetStreamUrl(room.roomID); streamUrl != "" {
			msg.StreamUrl = streamUrl
			if msg.VEID == 0 {
				msg.VEID = veId
			}
		}
	}
	// 通过钩子补充 episodeList,让观看端能启用上一集/下一集按钮
	if s.hooks.OnGetEpisodeList != nil {
		if episodes := s.hooks.OnGetEpisodeList(room.roomID); len(episodes) > 0 {
			msg.EpisodeList = episodes
		}
	}
	// duration 由观看端上报，后端不持久化，welcome 中传 0
	client.mu.Lock()
	_ = client.conn.WriteJSON(msg)
	client.mu.Unlock()
}

// broadcastClientJoined 广播 client_joined 给房间所有客户端（含新加入者）。
// 同时触发 OnClientsChanged 钩子,通知 PartyConsole 更新在线列表。
func (s *PartyWSServer) broadcastClientJoined(room *WSRoom, newClient *WSClient) {
	room.mu.Lock()
	clients := make([]*WSClient, len(room.clients))
	copy(clients, room.clients)
	hostName := ""
	if room.hostClient != nil {
		hostName = room.hostClient.clientName
	}
	clientCount := len(room.clients)
	room.mu.Unlock()

	s.broadcast(clients, WSMessage{
		Type:        "client_joined",
		ClientName:  newClient.clientName,
		ClientCount: clientCount,
		HostName:    hostName,
	}, nil)

	// 触发 clients_changed 钩子,让 PartyConsole 更新在线人数与观看列表
	if s.hooks.OnClientsChanged != nil {
		infos := make([]WSClientInfo, 0, len(clients))
		for _, c := range clients {
			infos = append(infos, WSClientInfo{Name: c.clientName, IsHost: c.isHost})
		}
		s.hooks.OnClientsChanged(room.roomID, infos, hostName)
	}
}

// heartbeatWatcher 定期检查心跳，超时则关闭连接。
func (s *PartyWSServer) heartbeatWatcher(ctx context.Context, room *WSRoom, client *WSClient) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			client.mu.Lock()
			lastPing := client.lastPing
			client.mu.Unlock()
			if time.Since(lastPing) > wsHeartbeatTimeout {
				log.Printf("[party-ws] 心跳超时，断开 roomId=%s client=%s", room.roomID, client.clientName)
				_ = client.conn.Close()
				return
			}
		}
	}
}

// readLoop 读取客户端消息并分发处理。
func (s *PartyWSServer) readLoop(room *WSRoom, client *WSClient) {
	defer func() { _ = client.conn.Close() }()
	for {
		_, data, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[party-ws] read 异常 roomId=%s client=%s: %v", room.roomID, client.clientName, err)
			}
			return
		}
		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.sendError(client, "消息格式非法")
			continue
		}
		s.handleMessage(room, client, &msg)
	}
}

// handleMessage 分发处理各类 WS 消息。
func (s *PartyWSServer) handleMessage(room *WSRoom, client *WSClient, msg *WSMessage) {
	switch msg.Type {
	case "ping":
		// 心跳：更新 lastPing，回复 pong
		client.mu.Lock()
		client.lastPing = time.Now()
		client.mu.Unlock()
		s.send(client, WSMessage{
			Type:     "pong",
			T:        msg.T,
			ServerTs: time.Now().UnixMilli(),
		})
	case "join":
		// join 已在 handleWS 入口处理，这里忽略重复 join
		// 重新发送 welcome 即可
		s.sendWelcome(room, client)
	case "play", "pause", "seek", "rate", "state_sync", "episode_change":
		// 仅房主可发控制消息
		if !client.isHost {
			s.sendError(client, fmt.Sprintf("非房主不能发送 %s 消息", msg.Type))
			return
		}
		s.handleHostControl(room, client, msg)
	case "report_duration":
		// 任意观看端可上报，通过 Wails Event 转发
		if s.hooks.OnReportDuration != nil {
			s.hooks.OnReportDuration(room.roomID, msg.VEID, msg.Duration)
		}
	default:
		// 未知消息类型：忽略
		log.Printf("[party-ws] 未知消息类型 roomId=%s type=%s", room.roomID, msg.Type)
	}
}

// handleHostControl 处理房主控制消息（play/pause/seek/rate/state_sync/episode_change）。
func (s *PartyWSServer) handleHostControl(room *WSRoom, client *WSClient, msg *WSMessage) {
	room.mu.Lock()
	clients := make([]*WSClient, len(room.clients))
	copy(clients, room.clients)
	// state_sync 缓存最新一份用于新加入者 welcome
	if msg.Type == "state_sync" {
		cp := *msg
		room.lastStateSync = &cp
	}
	room.mu.Unlock()

	switch msg.Type {
	case "seek":
		// 触发下载器模式 B
		if s.hooks.OnSeek != nil {
			s.hooks.OnSeek(room.roomID, msg.CurrentTime)
		}
		// 转发给所有 follower（不含发送者）
		s.broadcast(clients, *msg, client)
	case "episode_change":
		// 触发切集流程，由 PartyService 返回新的 streamUrl
		// PartyService.SwitchEpisode 内部会推送 party:episode_changed Wails Event
		var newStreamUrl string
		if s.hooks.OnEpisodeChange != nil {
			url, err := s.hooks.OnEpisodeChange(room.roomID, msg.VEID)
			if err != nil {
				s.sendError(client, fmt.Sprintf("切集失败: %v", err))
				return
			}
			newStreamUrl = url
		}
		// 广播给所有客户端（含发送者），携带新的 streamUrl
		out := *msg
		out.StreamUrl = newStreamUrl
		s.broadcast(clients, out, nil)
	case "state_sync":
		// 转发给所有 follower，并触发 Wails Event
		s.broadcast(clients, *msg, client)
		if s.hooks.OnStateSync != nil {
			s.hooks.OnStateSync(room.roomID, msg.VEID, msg.CurrentTime, msg.Playing, msg.Rate)
		}
	default:
		// play / pause / rate：转发给所有 follower（不含发送者）
		s.broadcast(clients, *msg, client)
	}
}

// broadcast 向给定客户端列表广播消息。
// exclude 指定的客户端不会被发送（可为 nil）。
func (s *PartyWSServer) broadcast(clients []*WSClient, msg WSMessage, exclude *WSClient) {
	for _, c := range clients {
		if c == exclude {
			continue
		}
		s.send(c, msg)
	}
}

// send 向单个客户端发送消息（带写超时）。
func (s *PartyWSServer) send(client *WSClient, msg WSMessage) {
	client.mu.Lock()
	defer client.mu.Unlock()
	_ = client.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	_ = client.conn.WriteJSON(msg)
}

// sendError 向客户端发送 error 消息。
func (s *PartyWSServer) sendError(client *WSClient, errMsg string) {
	s.send(client, WSMessage{Type: "error", Error: errMsg})
}
