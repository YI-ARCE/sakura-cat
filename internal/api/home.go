// Package api 提供视频提交服务 API 的 Go 客户端封装。
// 本文件定义首页聚合数据相关类型。
package api

// HomeData 首页聚合数据，包含 Banner、最新上架、热门播放三个板块。
type HomeData struct {
	Banner []HomeBannerItem `json:"banner"` // Banner 轮播（3-5 张推荐位，横幅封面取自 video_banner.vb_cover）
	Latest []HomeVideoItem  `json:"latest"` // 最新上架（按发布时间倒序）
	Hot    []HomeVideoItem  `json:"hot"`    // 热门播放（单一榜单，按播放量排序）
}

// HomeBannerItem 首页横幅项。横幅封面取自 video_banner.vb_cover（与 latest/hot 的 vr_cover 不同）。
type HomeBannerItem struct {
	VideoID      int64  `json:"vr_id"`            // 视频唯一标识
	Title        string `json:"vr_name"`          // 视频标题
	BannerCover  string `json:"vb_cover"`         // 横幅封面（相对路径，前端自行拼接 CDN）
	EpisodeCount int    `json:"vr_episode_count"` // 集数（如 12；0 表示未知/连载中）
}

// HomeVideoItem 首页视频项。
type HomeVideoItem struct {
	VideoID      int64  `json:"vr_id"`           // 视频唯一标识
	Title        string `json:"vr_name"`         // 视频标题
	Cover        string `json:"vr_cover"`        // 封面图（相对路径，前端自行拼接 CDN）
	EpisodeCount int    `json:"vr_episode_count"` // 集数（如 12；0 表示未知/连载中）
}
