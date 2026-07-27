// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义独立流式 HTTP server，绕过 Wails3 AssetServer 对响应体的全量缓冲，
// 通过标准 net/http 原生支持流式响应与 Range 请求，边下边播、支持拖动进度条。
package services

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"tg-download/internal/download"
	"tg-download/internal/telegram"
)

// EpisodeStreamServer 独立分集流式 HTTP server。
// 监听 127.0.0.1 随机端口，绕过 Wails3 AssetServer，原生支持流式响应。
// 支持 HTTP Range 请求（206 Partial Content），浏览器可拖动进度条跳播。
type EpisodeStreamServer struct {
	db      *sql.DB
	manager *telegram.ClientManager
	server  *http.Server
	addr    string
	port    int
}

// NewEpisodeStreamServer 创建独立流式 server 实例。
// cacheDir 参数保留兼容性，当前实现不落盘，忽略该参数。
func NewEpisodeStreamServer(db *sql.DB, manager *telegram.ClientManager, _ string) *EpisodeStreamServer {
	return &EpisodeStreamServer{
		db:      db,
		manager: manager,
	}
}

// Start 启动 HTTP server，监听 127.0.0.1 随机端口。
// 返回基础地址 http://127.0.0.1:<port>。
func (s *EpisodeStreamServer) Start() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("监听独立流服务端口失败: %w", err)
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.addr = fmt.Sprintf("http://127.0.0.1:%d", s.port)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/video/episode/stream", s.handleStream)

	s.server = &http.Server{Handler: mux}
	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[episode-stream-server] serve 异常: %v", err)
		}
	}()
	log.Printf("[episode-stream-server] 已启动，监听 %s", s.addr)
	return s.addr, nil
}

// Stop 关闭 HTTP server。
func (s *EpisodeStreamServer) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	}
}

// BaseURL 返回基础地址。
func (s *EpisodeStreamServer) BaseURL() string {
	return s.addr
}

// handleStream 处理分集流请求。
// 流程：解析参数 -> 校验登录 -> 拉取消息元数据 -> 解析 Range -> 设置响应头 -> 流式输出。
// 支持 Range 请求：有 Range 头返回 206 + 指定区间数据；无 Range 头返回 200 全量。
func (s *EpisodeStreamServer) handleStream(w http.ResponseWriter, r *http.Request) {
	log.Printf("[episode-stream-srv][step0] 收到请求 method=%s url=%s range=%q", r.Method, r.URL.String(), r.Header.Get("Range"))

	channelIDStr := r.URL.Query().Get("channel_id")
	messageIDStr := r.URL.Query().Get("message_id")
	if channelIDStr == "" || messageIDStr == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少 channel_id 或 message_id 参数")
		return
	}
	channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
	if err != nil || channelID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "channel_id 参数非法")
		return
	}
	messageID, err := strconv.ParseInt(messageIDStr, 10, 64)
	if err != nil || messageID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "message_id 参数非法")
		return
	}
	log.Printf("[episode-stream-srv][step1] 参数解析完成 channel_id=%d message_id=%d", channelID, messageID)

	if s.manager == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "客户端管理器未初始化")
		return
	}
	if !s.manager.IsAuthenticated() {
		writeJSONError(w, http.StatusServiceUnavailable, "未登录 Telegram，无法拉流")
		return
	}
	client := s.manager.GetClient()
	if client == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Telegram 客户端未就绪")
		return
	}
	log.Printf("[episode-stream-srv][step2] TG 客户端就绪")

	// 拉取消息元数据
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	log.Printf("[episode-stream-srv][step3] 开始拉取消息元数据...")
	location, mimeType, contentLength, err := download.FetchMessageMediaInfo(ctx, s.db, client.API(), channelID, messageID)
	if err != nil {
		log.Printf("[episode-stream-srv][step3] 拉取消息元数据失败: %v", err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("拉取消息失败: %v", err))
		return
	}
	log.Printf("[episode-stream-srv][step3] 元数据就绪 mime=%q size=%d location_type=%T", mimeType, contentLength, location)

	// 设置公共响应头
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")

	// 解析 Range 请求头
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		start, end, rangeErr := parseRange(rangeHeader, contentLength)
		if rangeErr != nil {
			// Range 非法，返回 416
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", contentLength))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			log.Printf("[episode-stream-srv][step4] 416 Range 非法 range=%q total=%d err=%v", rangeHeader, contentLength, rangeErr)
			return
		}

		// 206 Partial Content
		rangeLength := end - start + 1
		w.Header().Set("Content-Length", strconv.FormatInt(rangeLength, 10))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, contentLength))
		w.WriteHeader(http.StatusPartialContent)
		log.Printf("[episode-stream-srv][step4] 206 响应头已发送 range=%d-%d total=%d length=%d", start, end, contentLength, rangeLength)

		log.Printf("[episode-stream-srv][step5] 开始流式输出（Range）...")
		written, err := download.StreamRangeToWriter(ctx, client.API(), location, start, rangeLength, w)
		if err != nil {
			log.Printf("[episode-stream-srv][step5] 流式输出失败（Range） written=%d: %v", written, err)
			return
		}
		log.Printf("[episode-stream-srv][step5] 流式输出完成（Range） written=%d", written)
		return
	}

	// 无 Range 头：200 全量
	if contentLength > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	log.Printf("[episode-stream-srv][step4] 200 响应头已发送 total=%d", contentLength)

	log.Printf("[episode-stream-srv][step5] 开始流式输出（全量）...")
	written, err := download.StreamMediaToWriter(ctx, client.API(), location, w)
	if err != nil {
		log.Printf("[episode-stream-srv][step5] 流式输出失败（全量） written=%d: %v", written, err)
		return
	}
	log.Printf("[episode-stream-srv][step5] 流式输出完成（全量） written=%d", written)
}
