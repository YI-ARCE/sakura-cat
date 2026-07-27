// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 ApiService，向 Wails 绑定暴露视频提交服务 API。
//
// 提供：
//   - ListRepoSources   获取带订阅状态的视频源频道列表
//   - TestAPIConnection 测试 API 连通性
package services

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"tg-download/internal/api"
	"tg-download/internal/telegram"
)

// ApiService 是视频提交服务 API 的 Wails 绑定服务。
type ApiService struct {
	db      *sql.DB                 // 用于读取本地频道列表做匹配
	manager *telegram.ClientManager // 用于订阅频道（SubscribeRepoSource 调用 SubscribeChannel）
}

// NewApiService 创建 ApiService 实例。
// baseURL 参数已废弃（本地模式不再连接服务器），保留签名仅为兼容 main.go 调用。
// db 用于读取本地频道列表，在 ListRepoSources 中匹配订阅状态。
// manager 用于在 SubscribeRepoSource 中调用 telegram 层订阅频道。
func NewApiService(baseURL string, db *sql.DB, manager *telegram.ClientManager) *ApiService {
	s := &ApiService{
		db:      db,
		manager: manager,
	}
	if err := EnsureDictSeeds(db); err != nil {
		log.Printf("[api] 初始化分类种子失败: %v", err)
	}
	return s
}

// ToolLoginResponse 工具登录响应。
type ToolLoginResponse struct {
	Token string `json:"token"` // 访问令牌
}

// GetUserInfo 获取当前登录用户信息。
// 已滞空：返回零值。本地模式无用户体系，debug 恒为 false。
func (s *ApiService) GetUserInfo() (*api.UserInfo, error) {
	return &api.UserInfo{}, nil
}

// ToolLogin 工具登录。
// 已滞空：本地模式无需登录服务器，直接返回空 token。
func (s *ApiService) ToolLogin(key string) (ToolLoginResponse, error) {
	return ToolLoginResponse{Token: ""}, nil
}

// EnsureLoggedIn 确保 API 客户端已登录（token 已设置）。
// 已滞空：本地模式无需登录服务器，直接返回空 token。
func (s *ApiService) EnsureLoggedIn() (ToolLoginResponse, error) {
	return ToolLoginResponse{Token: ""}, nil
}

// RepoSourceWithSubscription 带订阅状态的视频源频道。
// 由 ApiService.ListRepoSources 返回，在服务器源基础上叠加本地频道匹配结果。
type RepoSourceWithSubscription struct {
	Name         string `json:"vs_name"`       // 服务器设置的频道名称
	ChannelID    int64  `json:"vs_channel_id"` // 频道 ID
	Username     string `json:"vs_username"`   // 频道用户名（@xxx，可空）
	IsSubscribed bool   `json:"is_subscribed"` // 用户是否已订阅
	DialogName   string `json:"dialog_name"`   // 用户本地频道名称
}

// ListRepoSources 获取带订阅状态的视频源频道列表。
// 已滞空：不再请求服务器，直接返回空列表。
// 后续将改为本地 video_source 表检索 + 本地 dialogs 匹配订阅状态。
func (s *ApiService) ListRepoSources() ([]RepoSourceWithSubscription, error) {
	return []RepoSourceWithSubscription{}, nil
}

// GetHome 获取首页聚合数据（Banner、最新上架、热门播放）。
// 已滞空：首页已移除，返回空数据。
func (s *ApiService) GetHome() (*api.HomeData, error) {
	return &api.HomeData{
		Banner: []api.HomeBannerItem{},
		Latest: []api.HomeVideoItem{},
		Hot:    []api.HomeVideoItem{},
	}, nil
}

// ListHistory 获取最近观看列表（按 vr_id 去重，最多 10 条）。
// 已改为本地检索：从本地 video_user_history 表按 vr_id 去重取最近一条，按 update_time 倒序。
func (s *ApiService) ListHistory() ([]api.HistoryListItem, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return ListHistoryLocal(s.db)
}

// SearchVideos 分类检索视频。
// 已改为本地检索：从本地 video_repo 表按分类/标签/年份/季度/关键词分页检索。
func (s *ApiService) SearchVideos(req api.SearchRequest) (*api.SearchResult, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return SearchVideosLocal(s.db, req)
}

// TestAPIConnection 测试 API 服务连通性。
// 已滞空：本地模式不再连接服务器，直接返回成功。
func (s *ApiService) TestAPIConnection() (string, error) {
	return "本地模式无需连接服务器", nil
}

// GetBaseURL 返回当前 API 服务地址。
// 已滞空：本地模式不再连接服务器，返回空串。
func (s *ApiService) GetBaseURL() string {
	return ""
}

// SetBaseURL 设置 API 服务地址。
// 已滞空：本地模式不再连接服务器，空实现。
func (s *ApiService) SetBaseURL(baseURL string) {
}

// SubscribeRepoSource 订阅视频源频道。
// 通过 username 调用 telegram 层 SubscribeChannel，订阅成功后刷新本地 dialogs 缓存。
// 刷新缓存失败时仅日志警告，不回滚订阅（订阅已成功）。
//
// 参数：
//   - channelID: 频道 ID（保留供后续扩展使用，当前实现仅按 username 订阅）
//   - username:  频道用户名（@xxx 或 xxx 形式，去 @ 前缀由 telegram 层处理）
func (s *ApiService) SubscribeRepoSource(channelID int64, username string) error {
	if username == "" {
		return fmt.Errorf("username 不能为空")
	}
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return fmt.Errorf("未登录，请先完成登录")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 订阅频道
	if err := s.manager.SubscribeChannel(ctx, username); err != nil {
		return err
	}

	// 订阅成功后刷新本地 dialogs 缓存
	if _, err := s.manager.RefreshDialogs(ctx); err != nil {
		log.Printf("[api] 订阅成功但刷新本地频道列表失败: %v", err)
		// 不返回错误，订阅已成功，缓存刷新失败不回滚
	}
	return nil
}

// SaveRepoChannel 保存频道到服务器。
// 已滞空：本地不收录到服务器，直接返回成功。后续将改为本地 video_source 表写入。
func (s *ApiService) SaveRepoChannel(channelID int64, username string, name string) error {
	return nil
}

// LocalDialog 本地频道（带服务器收录状态）。
// 由 ApiService.ListLocalDialogs 返回，在本地 DialogInfo 基础上叠加服务器收录标记。
type LocalDialog struct {
	PeerID         int64  `json:"peer_id"`          // 频道 ID
	Name           string `json:"name"`             // 频道名称
	Username       string `json:"username"`         // 频道用户名（@xxx，可空）
	Type           string `json:"type"`             // 对话类型（仅 channel）
	IsRepoUploaded bool   `json:"is_repo_uploaded"` // 是否已收录到服务器
}

// ListLocalDialogs 列出当前账户已订阅频道。
// 仅返回 type=channel 的对话（过滤掉 user/private chat）。
// keyword 为空时返回全部频道，非空时按 name 模糊搜索（内存过滤，不区分大小写）。
//
// is_repo_uploaded 通过本地 video_source 表匹配：
//   - vs_channel_id > 0 时按 channel_id 精确匹配
//   - 否则按 vs_username 小写匹配（去掉 @ 前缀）
//
// 结果按 is_repo_uploaded 降序排序（已收录优先），同名内保持原 dialogs 顺序。
//
// 流程：
//  1. 调用 manager.FetchDialogs 实时拉取当前账户的频道列表
//  2. 仅保留 type=channel 的对话，按 keyword 内存过滤
//  3. 查询 video_source 表构建已收录集合，匹配后置 is_repo_uploaded=true
func (s *ApiService) ListLocalDialogs(keyword string) ([]LocalDialog, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return nil, fmt.Errorf("未登录，请先完成登录")
	}

	// 1. 实时拉取当前账户的频道列表
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	dialogs, err := s.manager.FetchDialogs(ctx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("拉取频道列表失败: %w", err)
	}

	// 2. 仅保留 type=channel，按 keyword 内存过滤
	keyword = strings.TrimSpace(keyword)
	keywordLower := strings.ToLower(keyword)
	result := make([]LocalDialog, 0, len(dialogs))
	for _, d := range dialogs {
		if d.Type != telegram.DialogTypeChannel {
			continue
		}
		if keywordLower != "" && !strings.Contains(strings.ToLower(d.Name), keywordLower) {
			continue
		}
		item := LocalDialog{
			PeerID:         d.PeerID,
			Name:           d.Name,
			Username:       d.Username,
			Type:           string(d.Type),
			IsRepoUploaded: false,
		}
		result = append(result, item)
	}

	// 3. 查询 video_source 表，构建已收录的 channel_id 和 username 集合
	repoChannelIDs := make(map[int64]struct{})
	repoUsernames := make(map[string]struct{})
	if s.db != nil {
		rows, err := s.db.Query(`SELECT vs_channel_id, vs_username FROM video_source`)
		if err == nil {
			for rows.Next() {
				var cid int64
				var uname string
				if err := rows.Scan(&cid, &uname); err == nil {
					if cid > 0 {
						repoChannelIDs[cid] = struct{}{}
					}
					if uname != "" {
						repoUsernames[strings.ToLower(strings.TrimPrefix(uname, "@"))] = struct{}{}
					}
				}
			}
			_ = rows.Close()
		}
	}

	// 4. 匹配并标记 is_repo_uploaded
	for i := range result {
		d := &result[i]
		if _, ok := repoChannelIDs[d.PeerID]; ok {
			d.IsRepoUploaded = true
			continue
		}
		if d.Username != "" {
			if _, ok := repoUsernames[strings.ToLower(strings.TrimPrefix(d.Username, "@"))]; ok {
				d.IsRepoUploaded = true
			}
		}
	}

	return result, nil
}

// ChannelMessageSearchResult 频道消息搜索结果。
type ChannelMessageSearchResult struct {
	List         []telegram.RecentMessage `json:"list"`           // 当前页消息列表
	NextOffsetID int64                    `json:"next_offset_id"` // 下一页 OffsetID，0 表示无更多
	Total        int                      `json:"total"`          // 本页返回条数
}

// SearchChannelMessages 在指定频道内按关键字搜索消息。
// 用于频道收录页面：用户输入关键字后，前端按分页调用本方法拉取匹配消息。
//
// 参数：
//   - peerID:   频道 ID（本地 dialogs 表的 peer_id）
//   - keyword:  搜索关键字（必填，空则返回错误）
//   - offsetID: 分页偏移消息 ID（0 表示从最新开始）
//   - limit:    单页条数（≤0 或 >50 由 telegram 层强制为 50）
func (s *ApiService) SearchChannelMessages(peerID int64, keyword string, offsetID int64, limit int) (ChannelMessageSearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return ChannelMessageSearchResult{}, fmt.Errorf("keyword 不能为空")
	}
	if s.db == nil {
		return ChannelMessageSearchResult{}, fmt.Errorf("数据库未初始化")
	}
	if s.manager == nil {
		return ChannelMessageSearchResult{}, fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return ChannelMessageSearchResult{}, fmt.Errorf("未登录，请先完成登录")
	}

	// 从 db 查询 DialogInfo 获取 accessHash
	dialog, err := telegram.LoadDialogByPeerID(s.db, peerID)
	if err != nil {
		return ChannelMessageSearchResult{}, fmt.Errorf("未找到频道 (peer_id=%d)，请先刷新频道列表: %w", peerID, err)
	}

	// 60 秒超时 context
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 调用 telegram 层搜索
	list, nextOffsetID, err := s.manager.SearchChannelMessages(ctx, peerID, dialog.AccessHash, keyword, offsetID, limit)
	if err != nil {
		return ChannelMessageSearchResult{}, fmt.Errorf("搜索频道消息失败: %w", err)
	}

	// 空 list 处理
	if list == nil {
		list = []telegram.RecentMessage{}
	}

	return ChannelMessageSearchResult{
		List:         list,
		NextOffsetID: nextOffsetID,
		Total:        len(list),
	}, nil
}

// ListEpisodes 获取视频分集列表。
// 已改为本地检索：从本地 video_episode 表按集数升序读取。
func (s *ApiService) ListEpisodes(videoID int64) ([]api.EpisodeItem, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return ListEpisodesLocal(s.db, videoID)
}

// GetVideoDetail 获取视频简介详情。
// 已改为本地检索：从本地 video_repo 表读取详情，并查 video_follow 判断追番状态。
func (s *ApiService) GetVideoDetail(videoID int64) (*api.VideoDetail, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return GetVideoDetailLocal(s.db, videoID)
}

// FollowVideo 追番/取消追番（toggle 语义）。
// 已改为本地写入：本地 video_follow 表 toggle，并返回最新追番状态与追番总数。
func (s *ApiService) FollowVideo(req api.VideoFollowRequest) (*api.VideoFollowResponse, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return FollowVideoLocal(s.db, req)
}

// IncrementViewCount 播放数自增。
// 已改为本地写入：本地 video_repo.vr_view_count 自增 1。
func (s *ApiService) IncrementViewCount(vrId int64) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	return IncrementViewCountLocal(s.db, vrId)
}

// ListDiscuss 获取分集评论列表（游标分页）。
// 已改为本地检索：从本地 video_discuss 表读取，顶级评论按 hot/latest 排序，回复按 vrd_id 升序。
func (s *ApiService) ListDiscuss(req api.ListDiscussRequest) ([]api.DiscussItem, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return ListDiscussLocal(s.db, req)
}

// PostDiscuss 发表评论或回复。
// 已改为本地写入：写入本地 video_discuss 表。
func (s *ApiService) PostDiscuss(req api.DiscussPostRequest) (*api.DiscussItem, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return PostDiscussLocal(s.db, req)
}

// ListDanmaku 获取分集弹幕列表。
// 已改为本地检索：从本地 video_danmaku 表按时间升序读取。
func (s *ApiService) ListDanmaku(episodeID int64) ([]api.DanmakuItem, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return ListDanmakuLocal(s.db, episodeID)
}

// PostDanmaku 发送弹幕。
// 已改为本地写入：写入本地 video_danmaku 表。
func (s *ApiService) PostDanmaku(req api.DanmakuPostRequest) (*api.DanmakuItem, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return PostDanmakuLocal(s.db, req)
}

// GetHistory 获取分集续播进度。
// 已改为本地检索：从本地 video_user_history 表取该分集最新一条进度。
func (s *ApiService) GetHistory(episodeID int64) (*api.HistoryProgress, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return GetHistoryLocal(s.db, episodeID)
}

// ReportHistory 上报播放进度。
// 已改为本地写入：按 (vr_id, ve_id) UPSERT 写入本地 video_user_history 表。
func (s *ApiService) ReportHistory(req api.HistoryReportRequest) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	return ReportHistoryLocal(s.db, req)
}

// GetBaseInfo 获取视频基本信息（分类、语种、标签）。
// 已改为本地检索：从本地 video_category/video_language/video_tag 字典表读取。
func (s *ApiService) GetBaseInfo() (*api.BaseInfo, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	return GetBaseInfoLocal(s.db)
}

// CreateVideoRepo 新建视频源信息。
// 已改为本地写入：写入本地 video_repo 表及 video_tag_relation 标签关联。
func (s *ApiService) CreateVideoRepo(req api.CreateVideoRepoRequest) (*api.CreateVideoRepoResponse, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	id, err := CreateVideoRepoLocal(s.db, req)
	if err != nil {
		return nil, err
	}
	return &api.CreateVideoRepoResponse{VideoID: id}, nil
}

// UploadEpisodes 批量上传分集。
// 已改为本地写入：按 vs_channel_id+ve_message_id 去重 UPSERT 到本地 video_episode 表，
// 并同步更新 video_repo.vr_now_count。
func (s *ApiService) UploadEpisodes(req api.UploadEpisodesRequest) (*api.UploadEpisodesResponse, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if err := UploadEpisodesLocal(s.db, req); err != nil {
		return nil, err
	}
	return &api.UploadEpisodesResponse{}, nil
}

// DeleteVideoRepo 批量删除视频源及其关联数据（分集/标签关联/播放历史/追番/弹幕/评论）。
// 在事务中手动级联清理（SQLite schema 未声明 FOREIGN KEY）。
func (s *ApiService) DeleteVideoRepo(ids []int64) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	return DeleteVideoRepoLocal(s.db, ids)
}
