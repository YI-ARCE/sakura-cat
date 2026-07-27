// Package services 提供 Wails3 应用的后端服务层。
// 本文件实现 Bangumi 图片代理 HTTP handler，解决前端 WebView 无法直连 bangumi 图片域的问题。
//
// 路由：GET /api/bangumi/image?url=<bangumi 图片直链>
// 内部复用 BangumiService 的代理配置（与 API 请求同代理），下载后转发图片字节给前端。
package services

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
)

// BangumiImageProxy Bangumi 图片代理 HTTP handler。
// 复用 BangumiService 的 DownloadImage（含 applyProxy），保证走与 API 一致的代理出口。
type BangumiImageProxy struct {
	svc *BangumiService
}

// NewBangumiImageProxy 创建图片代理 handler。
func NewBangumiImageProxy(svc *BangumiService) *BangumiImageProxy {
	return &BangumiImageProxy{svc: svc}
}

// ServeHTTP 处理 GET /api/bangumi/image?url=xxx。
// 仅允许 lain.bgm.tv 域，防止变成开放代理。
func (h *BangumiImageProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	imageURL := r.URL.Query().Get("url")
	if imageURL == "" {
		http.Error(w, "missing url param", http.StatusBadRequest)
		return
	}
	// 安全：仅允许 bangumi 图片域，避免成为开放代理
	if !isBangumiImageURL(imageURL) {
		http.Error(w, "url not allowed", http.StatusForbidden)
		return
	}

	img, err := h.svc.DownloadImage(imageURL, "")
	if err != nil {
		log.Printf("[bangumi-image-proxy] 下载失败 url=%s: %v", imageURL, err)
		http.Error(w, "下载图片失败", http.StatusBadGateway)
		return
	}

	contentType := img.MimeType
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(img.Size))
	w.Write(img.Bytes)
}

// isBangumiImageURL 校验是否为 bangumi 图片域直链。
// bangumi 图片域名：lain.bgm.tv
func isBangumiImageURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Host == "lain.bgm.tv" && u.Scheme == "https"
}
