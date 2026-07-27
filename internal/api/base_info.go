// Package api 提供视频提交服务 API 的 Go 客户端封装。
// 本文件定义视频基本信息（分类、语种、标签）聚合类型。
package api

// BaseInfo 视频基本信息聚合数据。
// 一次请求返回分类、语种、标签三组下拉选项，供新建视频源表单使用。
type BaseInfo struct {
	Categories []VideoCategory `json:"categories"` // 分类列表
	Languages  []VideoLanguage  `json:"languages"`  // 语种列表
	Tags       []VideoTag      `json:"tags"`       // 标签列表
}

// VideoCategory 视频分类项。
type VideoCategory struct {
	ID   int    `json:"vc_id"`   // 分类 ID
	Name string `json:"vc_name"` // 分类名称
}

// VideoLanguage 视频语种项。
type VideoLanguage struct {
	ID   int    `json:"vl_id"`   // 语种 ID
	Name string `json:"vl_name"` // 语种名称
}

// VideoTag 视频标签项。
type VideoTag struct {
	ID   int    `json:"vt_id"`   // 标签 ID
	Name string `json:"vt_name"` // 标签名称
}
