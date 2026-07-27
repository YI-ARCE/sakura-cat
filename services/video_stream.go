// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义视频流 HTTP handler，拦截 /api/video/stream 路径，
// 通过 record_id 查询数据库获取 local_path，再读取本地视频文件流式输出，
// 支持 Range 请求以实现视频 seek。
package services

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tg-download/internal/download"
)

// VideoStreamHandler 视频流 HTTP handler。
// 通过 record_id 查询数据库获取 local_path，再读取本地文件流式输出。
// 安全约束：仅允许通过 record_id 查数据库，不接受任意 path 参数，
// 避免路径穿越（local_path 来自数据库，非用户输入）。
type VideoStreamHandler struct {
	db *sql.DB
}

// NewVideoStreamHandler 创建一个新的 VideoStreamHandler 实例。
// db 需已初始化。
func NewVideoStreamHandler(db *sql.DB) *VideoStreamHandler {
	return &VideoStreamHandler{db: db}
}

// writeJSONError 以 JSON 格式输出错误响应。
// 统一错误响应格式：{"error": "错误信息"}
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// mimeTypeByExt 根据扩展名返回对应的 MIME 类型。
// 仅返回浏览器兼容格式（mp4/webm/ogg）的 MIME，不兼容格式返回空字符串。
func mimeTypeByExt(ext string) string {
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ogg":
		return "video/ogg"
	default:
		return ""
	}
}

// ServeHTTP 处理视频流请求。
// 流程：解析 record_id → 查数据库获取 local_path → 校验扩展名 → 处理 Range → 流式输出。
func (h *VideoStreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. 解析 record_id 参数
	idStr := r.URL.Query().Get("record_id")
	if idStr == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少 record_id 参数")
		return
	}
	recordID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || recordID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "record_id 参数非法")
		return
	}

	// 2. 查数据库获取 local_path
	record, err := download.GetRecordByID(h.db, recordID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("记录 #%d 不存在", recordID))
		return
	}
	if record.LocalPath == "" {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("记录 #%d 无本地文件路径", recordID))
		return
	}

	// 3. 校验扩展名（仅 mp4/webm/ogg）
	ext := strings.ToLower(filepath.Ext(record.LocalPath))
	mimeType := mimeTypeByExt(ext)
	if mimeType == "" {
		writeJSONError(w, http.StatusUnsupportedMediaType, fmt.Sprintf("不支持的文件格式: %s", ext))
		return
	}

	// 4. 打开本地文件
	file, err := os.Open(record.LocalPath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("文件不存在: %s", record.LocalPath))
		return
	}
	defer func() { _ = file.Close() }()

	// 5. 获取文件信息（用于 Range 计算与 Content-Length）
	stat, err := file.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取文件信息失败")
		return
	}
	totalSize := stat.Size()

	// 6. 设置通用响应头
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Accept-Ranges", "bytes")

	// 7. 处理 Range 请求
	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		// 无 Range 头：返回 200 + 完整文件流
		w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
		w.WriteHeader(http.StatusOK)
		// 客户端可能中途断开，忽略 Copy 错误
		_, _ = io.Copy(w, file)
		return
	}

	// 解析 Range 头
	start, end, err := parseRange(rangeHeader, totalSize)
	if err != nil {
		writeJSONError(w, http.StatusRequestedRangeNotSatisfiable, err.Error())
		return
	}

	contentLength := end - start + 1

	// 8. 设置 206 Partial Content 响应头
	w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
	w.WriteHeader(http.StatusPartialContent)

	// 9. 流式输出指定范围内的字节
	// 使用 io.NewSectionReader 限定读取范围，避免越界
	_, _ = io.Copy(w, io.NewSectionReader(file, start, contentLength))
}

// parseRange 解析 Range 头，返回 [start, end] 闭区间。
// 支持格式：
//   - bytes=start-end
//   - bytes=start-   （从 start 到文件末尾）
//   - bytes=-suffix  （返回最后 suffix 字节）
//
// 多 Range 请求仅取第一个。
// 越界的 end 会被裁剪到 totalSize-1；越界的 start 返回错误。
func parseRange(rangeHeader string, totalSize int64) (int64, int64, error) {
	const prefix = "bytes="
	if !strings.HasPrefix(rangeHeader, prefix) {
		return 0, 0, fmt.Errorf("不支持的 Range 格式: %s", rangeHeader)
	}
	rangeSpec := strings.TrimPrefix(rangeHeader, prefix)

	// 多 Range 不支持，仅取第一个
	if commaIdx := strings.Index(rangeSpec, ","); commaIdx >= 0 {
		rangeSpec = rangeSpec[:commaIdx]
	}
	rangeSpec = strings.TrimSpace(rangeSpec)

	parts := strings.SplitN(rangeSpec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("Range 格式非法: %s", rangeHeader)
	}
	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])

	var (
		start, end int64
		err        error
	)
	switch {
	case startStr == "" && endStr == "":
		return 0, 0, fmt.Errorf("Range 格式非法: %s", rangeHeader)
	case startStr == "":
		// bytes=-suffix：返回最后 suffix 字节
		var suffix int64
		suffix, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, fmt.Errorf("Range 后缀非法: %s", rangeHeader)
		}
		if suffix > totalSize {
			suffix = totalSize
		}
		start = totalSize - suffix
		end = totalSize - 1
	case endStr == "":
		// bytes=start-：从 start 到文件末尾
		start, err = strconv.ParseInt(startStr, 10, 64)
		if err != nil || start < 0 {
			return 0, 0, fmt.Errorf("Range 起始位置非法: %s", rangeHeader)
		}
		if start >= totalSize {
			return 0, 0, fmt.Errorf("Range 起始位置超出文件大小: %s", rangeHeader)
		}
		end = totalSize - 1
	default:
		// bytes=start-end
		start, err = strconv.ParseInt(startStr, 10, 64)
		if err != nil || start < 0 {
			return 0, 0, fmt.Errorf("Range 起始位置非法: %s", rangeHeader)
		}
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil || end < start {
			return 0, 0, fmt.Errorf("Range 结束位置非法: %s", rangeHeader)
		}
		if start >= totalSize {
			return 0, 0, fmt.Errorf("Range 起始位置超出文件大小: %s", rangeHeader)
		}
		if end >= totalSize {
			end = totalSize - 1
		}
	}
	return start, end, nil
}
