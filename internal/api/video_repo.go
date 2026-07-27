// Package api 提供视频提交服务 API 的 Go 客户端封装。
// 本文件定义新建视频源信息与批量上传分集相关类型。
package api

// ============ /video/repo/create（POST 新建视频源信息）============

// CreateVideoRepoRequest 新建视频源信息请求体。
// 字段名直接使用数据库列名，后端直接消费。
type CreateVideoRepoRequest struct {
	Name          string  `json:"vr_name"`          // 视频标题（必填）
	Category      string  `json:"vr_category"`      // 分类（必填，枚举：anime/movie/tv/ova/special）
	Desc          string  `json:"vr_desc"`          // 简介（可选）
	Cover         string  `json:"vr_cover"`         // 封面图相对路径（可选，由 /resource/upload 返回）
	EpisodeCount  int     `json:"vr_episode_count"` // 集数（可选，0 表示未知/连载中）
	Year          int     `json:"vr_year"`          // 年份（可选，0 表示不筛选）
	Season        string  `json:"vr_season"`        // 季度（可选，枚举：winter/spring/summer/autumn）
	Status        int     `json:"vr_status"`        // 状态（可选，默认 3=连载中）
	Language      int     `json:"vr_language"`     // 语种 ID（可选）
	BgmID         int64   `json:"vr_bgm_id"`        // bangumi 条目 ID（可选，0 表示未关联）
	Tags          []int64 `json:"tags"`             // 标签 ID 数组（可选）
}

// CreateVideoRepoResponse 新建视频源信息响应。
type CreateVideoRepoResponse struct {
	VideoID int64 `json:"vr_id"` // 新建视频的唯一标识
}

// ============ /video/repo/uploadEpisode（POST 批量上传分集）============

// EpisodeUploadItem 单条分集上传项。
type EpisodeUploadItem struct {
	ChannelID int64  `json:"vs_channel_id"` // 频道 ID（TG peer_id）
	MessageID int64  `json:"ve_message_id"` // TG 消息 ID
	Title     string `json:"ve_title"`       // 分集标题
	Collect   int    `json:"ve_collect"`     // 集数（0 表示未识别）
}

// UploadEpisodesRequest 批量上传分集请求体。
type UploadEpisodesRequest struct {
	VideoID  int64               `json:"vr_id"`     // 视频ID
	Episodes []EpisodeUploadItem `json:"episodes"`  // 分集列表
}

// UploadEpisodesResponse 批量上传分集响应。
// 后端仅返回成功/失败，不返回分集列表。
type UploadEpisodesResponse struct {
}
