// Package api 提供视频提交服务 API 的 Go 客户端封装。
// 本文件定义所有 API 请求/响应的 Go 类型。
package api

// Response 是所有 API 响应的通用包装。
// code=1 表示成功，其他值表示失败；msg 为结果简述；data 为业务数据。
type Response struct {
	Code int         `json:"code"` // 状态码：1=成功，其他=失败
	Msg  string      `json:"msg"`  // 结果简述
	Data interface{} `json:"data"` // 业务数据（具体类型由各接口决定）
}

// IsSuccess 判断响应是否成功。
func (r Response) IsSuccess() bool {
	return r.Code == 1
}

// ============ /video/repo/source（GET 视频源频道列表）============

// RepoSource 服务器收录的视频源频道。
// 字段名直接使用数据库列名，前端无需二次映射。
type RepoSource struct {
	Name      string `json:"vs_name"`       // 服务器设置的频道名称
	ChannelID int64  `json:"vs_channel_id"` // 频道 ID（对应 TG peer_id）
	Username  string `json:"vs_username"`   // 频道用户名（@xxx，可空表示私人频道）
}

// ============ /video/repo/save（POST 保存频道到服务器）============

// SaveRepoChannelRequest 保存频道到服务器的请求体。
type SaveRepoChannelRequest struct {
	ChannelID int64  `json:"vs_channel_id"` // 频道 ID
	Username  string `json:"vs_username"`   // 频道用户名（@xxx，可空表示私人频道）
	Name      string `json:"vs_name"`       // 频道名称
}

// ============ /video/repo/episodes（GET 分集列表）============

// EpisodeItem 分集项。
type EpisodeItem struct {
	EpisodeID     int64  `json:"ve_id"`         // 分集唯一标识
	ChannelID     int64  `json:"vs_channel_id"` // 频道 ID（TG peer_id，客户端回源拉流用）
	MessageID     int64  `json:"ve_message_id"` // TG 消息 ID（客户端回源拉流用）
	Title         string `json:"ve_title"`      // 分集标题
	EpisodeNumber int    `json:"ve_collect"`    // 集数（0 表示未识别）
	Status        int    `json:"ve_status"`     // 状态：1=正常，2=失效
}

// ============ /video/repo/detail（GET 视频简介）============

// VideoDetail 视频简介详情。
type VideoDetail struct {
	VideoID      int64  `json:"vr_id"`            // 视频ID
	Name         string `json:"vr_name"`          // 视频标题
	Desc         string `json:"vr_desc"`          // 描述
	Cover        string `json:"vr_cover"`         // 封面图相对路径
	Category     string `json:"vr_category"`      // 分类
	EpisodeCount int    `json:"vr_episode_count"` // 集数（0 表示未知/连载中）
	NowCount     int    `json:"vr_now_count"`     // 当前更新集数
	Year         int    `json:"vr_year"`          // 年份
	Season       string `json:"vr_season"`        // 季度
	ViewCount    int64  `json:"vr_view_count"`    // 总播放量
	Status       int    `json:"vr_status"`        // 状态：1=完结，2=停更，3=连载中
	Language     int    `json:"vr_language"`      // 语种 ID
	BgmID        int64  `json:"vr_bgm_id"`        // bangumi 条目 ID（0 表示未关联）
	UpdateTime   int64  `json:"update_time"`      // 更新时间（秒级时间戳）
	IsFollowed   bool   `json:"is_followed"`      // 当前用户是否已追番
}

// ============ /video/repo/follow（POST 视频追番 toggle）============

// VideoFollowRequest 视频追番请求体（toggle 语义）。
type VideoFollowRequest struct {
	VrID int64 `json:"vr_id"` // 视频 ID
}

// VideoFollowResponse 视频追番响应。
type VideoFollowResponse struct {
	IsFollowed  bool `json:"is_followed"`  // 操作后的追番状态
	FollowCount int  `json:"follow_count"` // 操作后的追番数
}

// ============ /video/repo/favorites & /video/repo/liked（GET 追番/点赞视频列表）============

// FavoriteItem 追番视频项（也用于点赞视频列表）。
type FavoriteItem struct {
	VrID           int64  `json:"vr_id"`            // 视频 ID
	VrName         string `json:"vr_name"`         // 视频标题
	VrCover        string `json:"vr_cover"`        // 视频封面（相对路径）
	VrEpisodeCount int    `json:"vr_episode_count"` // 集数（0 表示未知/连载中）
	CreateTime     int64  `json:"create_time"`     // 追番/点赞时间（秒级时间戳）
}

// ============ /video/discuss/list（GET 分集评论列表）============

// ListDiscussRequest 评论列表游标分页请求。
type ListDiscussRequest struct {
	EpisodeID   int64  `json:"ve_id"`         // 分集 ID
	ParentID    int64  `json:"parent_id"`     // 父评论 ID（0=顶级评论）
	Sort        string `json:"sort"`          // 排序：latest（默认）/ hot，仅顶级评论生效
	CursorVrdID int64  `json:"cursor_vrd_id"` // 游标 vrd_id，0=第一页
	Limit       int    `json:"limit"`         // 每页条数，默认 20，最大 50
}

// DiscussItem 评论项。
type DiscussItem struct {
	CommentID  int64           `json:"vrd_id"`      // 评论唯一标识
	Content    string          `json:"vrd_content"` // 评论内容
	ReplyCount int             `json:"reply_count"` // 回复数（仅顶级评论返回，回复固定为 0）
	CreatedAt  int64           `json:"create_time"` // 创建时间（秒级时间戳）
	Replies    []*DiscussItem  `json:"replies"`     // 预览回复（仅顶级评论返回，最多 3 条，最早在前）
}

// ============ /video/discuss/post（POST 发表评论）============

// DiscussPostRequest 发表评论请求体。
type DiscussPostRequest struct {
	EpisodeID int64  `json:"ve_id"`       // 分集 ID
	Content   string `json:"vrd_content"`  // 评论内容（1-500 字）
	ParentID  int64  `json:"parent_id"`   // 父评论 ID（0 表示顶级评论）
}

// ============ /video/danmaku/list（GET 弹幕列表）============

// DanmakuItem 弹幕项。
type DanmakuItem struct {
	ID      int64  `json:"vd_id"`      // 弹幕唯一标识
	Content string `json:"vd_content"` // 弹幕内容
	Time    int64  `json:"vd_time"`    // 出现时间（毫秒）
	Mode    int    `json:"vd_mode"`    // 类型：1=滚动，2=顶部，3=底部
	Color   string `json:"vd_color"`   // 颜色（#RRGGBB）
}

// ============ /video/danmaku/post（POST 发送弹幕）============

// DanmakuPostRequest 发送弹幕请求体。
type DanmakuPostRequest struct {
	EpisodeID int64  `json:"ve_id"`      // 分集 ID
	Content   string `json:"vd_content"` // 弹幕内容（1-100 字）
	Time      int64  `json:"vd_time"`    // 出现时间（毫秒）
	Mode      int    `json:"vd_mode"`    // 类型：1=滚动，2=顶部，3=底部
	Color     string `json:"vd_color"`   // 颜色（#RRGGBB）
}

// ============ /video/history/get（GET 获取分集续播进度）============

// HistoryProgress 播放进度。
type HistoryProgress struct {
	HistoryTime int `json:"vuh_history_time"` // 上次播放位置（秒），0=无记录或已看完
}

// ============ /video/history/list（GET 获取最近观看列表）============

// HistoryListItem 最近观看列表项。
type HistoryListItem struct {
	VideoID      int64  `json:"vr_id"`            // 视频 ID
	EpisodeID    int64  `json:"ve_id"`            // 最后观看的分集 ID
	HistoryTime  int    `json:"vuh_history_time"` // 播放进度（秒），0=已看完
	UpdateTime   int    `json:"update_time"`      // 最后观看时间（秒级时间戳）
	Title        string `json:"vr_name"`          // 视频标题
	Cover        string `json:"vr_cover"`         // 封面图（相对路径，前端拼接 CDN）
	EpisodeCount int    `json:"vr_episode_count"` // 集数（0 表示未知/连载中）
	VeCollect    int    `json:"ve_collect"`       // 最后观看的分集集数
}

// ============ /video/history/report（POST 上报播放进度）============

// HistoryReportRequest 上报播放进度请求体。
type HistoryReportRequest struct {
	VideoID     int64 `json:"vr_id"`            // 视频 ID
	EpisodeID   int64 `json:"ve_id"`            // 分集 ID
	HistoryTime int   `json:"vuh_history_time"` // 续播进度（秒），看完时传 0
	Duration    int   `json:"vuh_duration"`     // 实际观看时长（秒），看完时传视频总时长
	Finished    int   `json:"finished"`          // 是否看完（0/1），看完时传 1
}
