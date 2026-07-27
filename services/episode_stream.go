// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义分集流式 HTTP handler，拦截 /api/video/episode/stream 路径，
// 通过 channel_id + message_id 从 Telegram 流式拉取媒体并输出给前端播放器。
package services

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"tg-download/internal/download"
	"tg-download/internal/telegram"
)

// EpisodeStreamHandler 分集流式 HTTP handler。
// 通过 channel_id + message_id 从 Telegram 实时拉取媒体并流式输出。
// 与 VideoStreamHandler（本地文件流）不同，本 handler 不落盘，直接代理 TG 流。
type EpisodeStreamHandler struct {
	db      *sql.DB
	manager *telegram.ClientManager
}

// NewEpisodeStreamHandler 创建 EpisodeStreamHandler 实例。
func NewEpisodeStreamHandler(db *sql.DB, manager *telegram.ClientManager) *EpisodeStreamHandler {
	return &EpisodeStreamHandler{db: db, manager: manager}
}

// ServeHTTP 处理分集流请求。
// 流程：解析 channel_id + message_id -> 校验登录 -> 拉取消息元数据 -> 设置响应头 -> 流式输出。
func (h *EpisodeStreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("[episode-stream][step0] 收到请求 method=%s url=%s remote=%s", r.Method, r.URL.String(), r.RemoteAddr)

	// 1. 解析参数
	channelIDStr := r.URL.Query().Get("channel_id")
	messageIDStr := r.URL.Query().Get("message_id")
	if channelIDStr == "" || messageIDStr == "" {
		log.Printf("[episode-stream][step1] 缺少参数 channel_id=%q message_id=%q", channelIDStr, messageIDStr)
		writeJSONError(w, http.StatusBadRequest, "缺少 channel_id 或 message_id 参数")
		return
	}
	channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
	if err != nil || channelID <= 0 {
		log.Printf("[episode-stream][step1] channel_id 参数非法 raw=%q err=%v", channelIDStr, err)
		writeJSONError(w, http.StatusBadRequest, "channel_id 参数非法")
		return
	}
	messageID, err := strconv.ParseInt(messageIDStr, 10, 64)
	if err != nil || messageID <= 0 {
		log.Printf("[episode-stream][step1] message_id 参数非法 raw=%q err=%v", messageIDStr, err)
		writeJSONError(w, http.StatusBadRequest, "message_id 参数非法")
		return
	}
	log.Printf("[episode-stream][step1] 参数解析完成 channel_id=%d message_id=%d", channelID, messageID)

	// 2. 校验 TG 客户端已登录
	if h.manager == nil {
		log.Printf("[episode-stream][step2] 客户端管理器未初始化")
		writeJSONError(w, http.StatusServiceUnavailable, "客户端管理器未初始化")
		return
	}
	if !h.manager.IsAuthenticated() {
		log.Printf("[episode-stream][step2] 未登录 Telegram")
		writeJSONError(w, http.StatusServiceUnavailable, "未登录 Telegram，无法拉流")
		return
	}
	client := h.manager.GetClient()
	if client == nil {
		log.Printf("[episode-stream][step2] Telegram 客户端未就绪")
		writeJSONError(w, http.StatusServiceUnavailable, "Telegram 客户端未就绪")
		return
	}
	log.Printf("[episode-stream][step2] TG 客户端就绪")

	// 3. 拉取消息元数据（刷新 FileReference、获取 MIME 与文件大小）
	// 超时 10 分钟，适配大文件拉取
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	log.Printf("[episode-stream][step3] 开始拉取消息元数据...")
	location, mimeType, contentLength, err := download.FetchMessageMediaInfo(ctx, h.db, client.API(), channelID, messageID)
	if err != nil {
		log.Printf("[episode-stream][step3] 拉取消息元数据失败 channel_id=%d msg_id=%d: %v", channelID, messageID, err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("拉取消息失败: %v", err))
		return
	}
	log.Printf("[episode-stream][step3] 元数据就绪 mime=%q size=%d location_type=%T", mimeType, contentLength, location)

	// 4. 设置响应头（必须在流式写入前完成）
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	} else {
		// 默认按视频处理
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if contentLength > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	}
	w.Header().Set("Cache-Control", "no-store")
	log.Printf("[episode-stream][step4] 响应头已设置 headers=%v", w.Header())

	// 5. 流式输出
	log.Printf("[episode-stream][step5] 开始流式输出...")
	written, err := download.StreamMediaToWriter(ctx, client.API(), location, w)
	if err != nil {
		// 流式写入中途失败：header 已发送，无法改写状态码，仅记录日志
		log.Printf("[episode-stream][step5] 流式输出失败 channel_id=%d msg_id=%d written=%d: %v", channelID, messageID, written, err)
		return
	}
	log.Printf("[episode-stream][step5] 流式输出完成 written=%d", written)
}
