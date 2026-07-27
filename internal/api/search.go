// Package api 提供视频提交服务 API 的 Go 客户端封装。
// 本文件定义分类检索相关类型。
package api

// SearchRequest 分类检索查询参数。
// 通过 JSON body 提交到 POST /video/repo/search。
type SearchRequest struct {
	Category string  `json:"category"`  // 分类：anime/movie/tv/ova/special，空表示全部
	Tags     []int64 `json:"tags"`      // 标签 ID 数组，空表示不筛选
	Year     int     `json:"year"`      // 年份（如 2025），0 表示不筛选
	Season   string  `json:"season"`    // 季度：winter/spring/summer/autumn，空表示不筛选
	Sort     string  `json:"sort"`      // 排序：latest/hot，空默认 latest
	Keyword  string  `json:"keyword"`   // 标题关键词（服务器端模糊匹配），空表示不搜索
	Page     int     `json:"page"`      // 页码（从 1 开始），0 默认 1
	PageSize int     `json:"page_size"` // 每页数量（≤50），0 默认 20
}

// SearchResult 分类检索结果，包含列表数据与分页信息。
type SearchResult struct {
	List     []VideoSearchItem `json:"list"`      // 当前页数据
	Total    int64             `json:"total"`     // 总条数（用于分页）
	Page     int               `json:"page"`      // 当前页码
	PageSize int               `json:"page_size"` // 每页数量
	HasMore  bool              `json:"has_more"`  // 是否有下一页
}

// VideoSearchItem 视频检索项。
type VideoSearchItem struct {
	VideoID      int64  `json:"vr_id"`           // 视频唯一标识
	Title        string `json:"vr_name"`         // 视频标题
	Cover        string `json:"vr_cover"`        // 封面图（相对路径，前端自行拼接 CDN）
	EpisodeCount int    `json:"vr_episode_count"` // 集数
	Category     string `json:"vr_category"`     // 分类
	Year         int    `json:"vr_year"`         // 年份
	Season       string `json:"vr_season"`       // 季度
	ViewCount    int64  `json:"vr_view_count"`   // 播放量
	BgmID        int64  `json:"vr_bgm_id"`       // bangumi 条目 ID（0 表示未关联）
	UpdatedAt    int64  `json:"update_time"`     // 更新时间（秒级时间戳）
}
