// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 BangumiService，向 Wails 绑定暴露 Bangumi (bgm.tv) 元数据检索能力。
//
// 提供：
//   - BrowseSubjects  按类型/年月浏览条目（GET /v0/subjects）
//   - GetSubject      获取条目详情（GET /v0/subjects/{id}）
//   - GetEpisodes     获取条目章节（GET /v0/episodes）
//   - DownloadImage   代理下载 bangumi 图片（解决前端 CORS）
package services

import (
	"database/sql"

	"tg-download/internal/api"
	"tg-download/internal/telegram"
)

// BangumiService Bangumi 元数据检索的 Wails 绑定服务。
// bangumi access token 不持久化，由前端在筛选弹窗填入并用 localStorage 缓存，
// 每次调用时通过请求参数 token 透传到后端，后端临时设置到 client 后发请求。
// 代理配置复用 telegram.ProxyConfig（同一份 proxy 表），每次请求前刷新 client transport。
type BangumiService struct {
	bangumi *api.BangumiClient
	db      *sql.DB
}

// NewBangumiService 创建 BangumiService 实例。
// baseURL 为空时使用 api.BangumiDefaultBaseURL。
// db 用于读取代理配置（复用 telegram.ProxyConfig）。
func NewBangumiService(baseURL string, db *sql.DB) *BangumiService {
	return &BangumiService{
		bangumi: api.NewBangumiClient(baseURL),
		db:      db,
	}
}

// applyProxy 从 proxy 表读取代理配置并应用到 bangumi client。
// 无配置或读取失败时走直连，不阻断请求。
func (s *BangumiService) applyProxy() {
	if s.db == nil {
		return
	}
	cfg, err := telegram.LoadProxy(s.db)
	if err != nil {
		return
	}
	_ = s.bangumi.SetProxy(string(cfg.Type), cfg.Address, cfg.Port, cfg.Username, cfg.Password, cfg.Enabled)
}

// BangumiBrowseSubjectsRequest 浏览条目查询参数（Wails 绑定用，字段名与 api 层对齐）。
// Type 必填：1=书籍 2=动画 3=音乐 4=游戏 6=三次元。
type BangumiBrowseSubjectsRequest = api.BangumiBrowseRequest

// BrowseSubjects 按类型/年月浏览条目（GET /v0/subjects）。
// token 为 bangumi access token，空则匿名访问（有速率限制，看不到 NSFW）。
// userAgent 为 User-Agent（必填，格式 用户名/应用名），由前端透传。
func (s *BangumiService) BrowseSubjects(req api.BangumiBrowseRequest, token string, userAgent string) (*api.BangumiPagedSubject, error) {
	s.applyProxy()
	s.bangumi.SetToken(token)
	s.bangumi.SetUserAgent(userAgent)
	return s.bangumi.BrowseSubjects(req)
}

// GetSubject 获取条目详情。
// GET /v0/subjects/{subject_id}，cache 300s。
func (s *BangumiService) GetSubject(subjectID int, token string, userAgent string) (*api.BangumiSubject, error) {
	s.applyProxy()
	s.bangumi.SetToken(token)
	s.bangumi.SetUserAgent(userAgent)
	return s.bangumi.GetSubject(subjectID)
}

// GetEpisodes 获取条目章节列表。
// GET /v0/episodes?subject_id=&type=&limit=&offset=
// epType：0=本篇 1=SP 2=OP 3=ED 4=PV 5=MAD 6=其他；传 -1 表示不按类型筛选。
func (s *BangumiService) GetEpisodes(subjectID int, epType int, limit, offset int, token string, userAgent string) (*api.BangumiPagedEpisode, error) {
	s.applyProxy()
	s.bangumi.SetToken(token)
	s.bangumi.SetUserAgent(userAgent)
	return s.bangumi.GetEpisodes(subjectID, api.BangumiEpType(epType), limit, offset)
}

// BangumiImageResponse 代理下载的图片响应。
type BangumiImageResponse = api.BangumiImageResponse

// DownloadImage 代理下载 bangumi 图片。
// 后端下载后返回字节，前端可自行处理。
// userAgent 为 User-Agent（必填，格式 用户名/应用名），由前端透传。
func (s *BangumiService) DownloadImage(imageURL string, userAgent string) (*BangumiImageResponse, error) {
	s.applyProxy()
	s.bangumi.SetUserAgent(userAgent)
	return s.bangumi.DownloadImage(imageURL)
}
