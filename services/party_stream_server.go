// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 PartyStreamServer，为"一起看"房间提供 HTTP 流服务。
//
// 设计要点：
//   - 绑定 0.0.0.0:0（完全开放端口，供局域网观看端访问）
//   - /stream/<roomId>/<veId> 路由：从 PartyService 查找对应 downloader，
//     解析 Range 请求，调 Downloader.ReadRange 输出（支持 206 Partial Content）
//   - /party/<roomId> 路由：返回内嵌的 party_client.html（观看端入口页面）
//   - 通过 //go:embed 嵌入 HTML，避免外部文件依赖
package services

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// partyClientHTML 内嵌观看端 HTML 页面。
// 由 party_client.html 提供，通过 //go:embed 嵌入二进制。
//
//go:embed party_client.html
var partyClientHTML []byte

// DownloaderProvider 由 PartyService 注入，根据 roomId 返回对应 Downloader。
// 返回 nil 表示房间不存在或 downloader 尚未就绪。
type DownloaderProvider func(roomId string) Downloader

// PartyStreamServer 一起看房间的 HTTP 流服务。
// 提供：
//   - GET /stream/<roomId>/<veId>  视频流（支持 Range 请求，206 Partial Content）
//   - GET /party/<roomId>          观看端 HTML 入口页面
type PartyStreamServer struct {
	server        *http.Server
	addr          string
	port          int
	mux           *http.ServeMux
	getDownloader DownloaderProvider
}

// NewPartyStreamServer 创建 PartyStreamServer 实例。
// getDownloader 由 PartyService 注入，用于按 roomId 查找对应 Downloader。
func NewPartyStreamServer(getDownloader DownloaderProvider) *PartyStreamServer {
	return &PartyStreamServer{
		getDownloader: getDownloader,
		mux:           http.NewServeMux(),
	}
}

// Mux 返回内部 mux，供外部（如 PartyWSServer）注册额外路由（如 /ws）。
// 必须在 Start 之前调用；Start 时 mux 已就绪，注册的路由会被一并启用。
func (s *PartyStreamServer) Mux() *http.ServeMux {
	return s.mux
}

// Start 启动 HTTP server，监听 0.0.0.0 随机端口。
// 返回基础地址 http://<lanIp>:<port>。
func (s *PartyStreamServer) Start() (string, error) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return "", fmt.Errorf("监听一起看流服务端口失败: %w", err)
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.addr = fmt.Sprintf("http://0.0.0.0:%d", s.port)

	mux := s.mux
	mux.HandleFunc("/stream/", s.handleStream)
	mux.HandleFunc("/party/", s.handlePartyPage)

	s.server = &http.Server{Handler: mux}
	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[party-stream-server] serve 异常: %v", err)
		}
	}()
	log.Printf("[party-stream-server] 已启动，监听 %s", s.addr)
	return s.addr, nil
}

// Stop 关闭 HTTP server。
func (s *PartyStreamServer) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	}
}

// BaseURL 返回基础地址（0.0.0.0 形式，仅供日志）。
func (s *PartyStreamServer) BaseURL() string {
	return s.addr
}

// Port 返回监听端口。
func (s *PartyStreamServer) Port() int {
	return s.port
}

// handleStream 处理视频流请求。
// 路由：GET /stream/<roomId>/<veId>
// 流程：解析路径 -> 查找 downloader -> 解析 Range -> 调 ReadRange 输出（支持 206）
func (s *PartyStreamServer) handleStream(w http.ResponseWriter, r *http.Request) {
	log.Printf("[party-stream] 收到请求 %s %s Range=%s from=%s", r.Method, r.URL.Path, r.Header.Get("Range"), r.RemoteAddr)
	// 路径格式：/stream/<roomId>/<veId>
	// 去掉 /stream/ 前缀后按 / 切分
	path := strings.TrimPrefix(r.URL.Path, "/stream/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeJSONError(w, http.StatusBadRequest, "路径格式非法，应为 /stream/<roomId>/<veId>")
		return
	}
	roomID := parts[0]
	veIDStr := parts[1]
	veID, err := strconv.ParseInt(veIDStr, 10, 64)
	if err != nil || veID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "veId 参数非法")
		return
	}

	if s.getDownloader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "downloader provider 未注入")
		return
	}
	dl := s.getDownloader(roomID)
	if dl == nil {
		log.Printf("[party-stream] downloader 未就绪 roomId=%s", roomID)
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("房间 %s 的 downloader 未就绪", roomID))
		return
	}

	// 默认视频 MIME，downloader 内部缓存的是 TG 媒体原始字节
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")

	// 文件总大小(从 downloader 获取,用于 Content-Range 与 Content-Length)
	totalSize := dl.GetFileSize()
	log.Printf("[party-stream] 查询 downloader roomId=%s veId=%d totalSize=%d", roomID, veID, totalSize)

	// 解析 Range 请求
	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		// 无 Range 头:返回 200 全量(从 0 读到 EOF)
		reader, err := dl.ReadRange(0, -1)
		if err != nil {
			log.Printf("[party-stream] ReadRange(0,-1) 失败 roomId=%s: %v", roomID, err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("读取失败: %v", err))
			return
		}
		defer func() { _ = closeReader(reader) }()
		if totalSize > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
		}
		w.WriteHeader(http.StatusOK)
		n, _ := io.Copy(w, reader)
		log.Printf("[party-stream] 响应完成(200) roomId=%s written=%d", roomID, n)
		return
	}

	// 解析 Range
	start, end, err := parsePartyRange(rangeHeader)
	if err != nil {
		if totalSize > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", totalSize))
		} else {
			w.Header().Set("Content-Range", "bytes */*")
		}
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	// end=-1 表示读到 EOF,用 totalSize 裁剪
	if end < 0 {
		if totalSize > 0 {
			end = totalSize - 1
		} else {
			// totalSize 未知,无法返回标准 Content-Range,降级为 200 全量
			reader, err := dl.ReadRange(start, -1)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("读取失败: %v", err))
				return
			}
			defer func() { _ = closeReader(reader) }()
			w.WriteHeader(http.StatusOK)
			_, _ = io.Copy(w, reader)
			return
		}
	}

	reader, err := dl.ReadRange(start, end)
	if err != nil {
		log.Printf("[party-stream] ReadRange(%d,%d) 失败 roomId=%s: %v", start, end, roomID, err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("读取范围失败: %v", err))
		return
	}
	defer func() { _ = closeReader(reader) }()

	rangeLength := end - start + 1
	w.Header().Set("Content-Length", strconv.FormatInt(rangeLength, 10))
	// 标准 Content-Range 格式:bytes <start>-<end>/<total>
	if totalSize > 0 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
	} else {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/*", start, end))
	}
	w.WriteHeader(http.StatusPartialContent)
	n, _ := io.Copy(w, reader)
	log.Printf("[party-stream] 响应完成(206) roomId=%s start=%d end=%d rangeLength=%d written=%d", roomID, start, end, rangeLength, n)
}

// handlePartyPage 返回观看端 HTML 页面。
// 路由：GET /party/<roomId>
func (s *PartyStreamServer) handlePartyPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(partyClientHTML)
}

// closeReader 安全关闭 Reader（若实现了 io.Closer）。
func closeReader(r io.Reader) error {
	if c, ok := r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// parsePartyRange 解析一起看场景的 Range 头。
// 与 video_stream.parseRange 不同的是：本场景文件总大小未知（downloader 不暴露），
// 因此仅支持 bytes=start-end 与 bytes=start- 两种格式，不支持 bytes=-suffix。
// 返回 [start, end] 闭区间，end=-1 表示读到 EOF。
func parsePartyRange(rangeHeader string) (int64, int64, error) {
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

	if startStr == "" {
		return 0, 0, fmt.Errorf("不支持的 Range 格式（缺少 start）: %s", rangeHeader)
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, fmt.Errorf("Range 起始位置非法: %s", rangeHeader)
	}

	// bytes=start-：从 start 读到 EOF
	if endStr == "" {
		return start, -1, nil
	}

	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return 0, 0, fmt.Errorf("Range 结束位置非法: %s", rangeHeader)
	}
	return start, end, nil
}

// GetLanIP 获取本机首个非 loopback 的 IPv4 地址。
// 遍历 net.Interfaces，返回首个符合条件的 IPv4，未找到返回空串。
func GetLanIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		// 跳过未启用或 loopback 接口
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return ""
}
