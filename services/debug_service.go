package services

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// DebugService 提供运行时 DOM 调试能力。
// 通过 mux 拦截 /__debug HTTP 请求，emit 事件给前端，
// 前端执行 DOM 查询后调用 SubmitResult 回传结果。
// HTTP handler 同步等待结果（带超时），返回给调用方。
type DebugService struct {
	mu      sync.Mutex
	pending chan string
	emitter func(eventName string, data interface{}) error
}

func NewDebugService() *DebugService {
	return &DebugService{
		pending: make(chan string, 1),
	}
}

// SetEmitter 注入事件发射器（main.go 阶段 3 调用）。
func (s *DebugService) SetEmitter(emit func(eventName string, data interface{}) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emitter = emit
}

// SubmitResult 是 Wails 绑定方法，由前端调用回传 DOM 查询结果。
func (s *DebugService) SubmitResult(result string) {
	select {
	case s.pending <- result:
	default:
	}
}

// Inspect 发送 debug:inspect 事件给前端，同步等待结果。
// 由 mux /__debug handler 调用。
func (s *DebugService) Inspect(params map[string]interface{}) (string, error) {
	s.mu.Lock()
	emit := s.emitter
	s.mu.Unlock()

	if emit == nil {
		return "", fmt.Errorf("debug service: emitter not initialized")
	}

	// 清空可能残留的结果
	select {
	case <-s.pending:
	default:
	}

	// 发事件给前端
	if err := emit("debug:inspect", params); err != nil {
		return "", fmt.Errorf("emit debug:inspect failed: %w", err)
	}

	// 等待结果（5s 超时）
	select {
	case result := <-s.pending:
		return result, nil
	case <-time.After(5 * time.Second):
		return "", fmt.Errorf("debug:inspect timeout (5s), 前端可能未监听事件或无匹配窗口")
	}
}

// StartDebugHTTPServer 在 dev 模式下启动独立 HTTP 服务器（9255 端口），
// 供外部 curl 调试 DOM。生产模式（无 DEV 环境变量）为空操作。
//
// dev 模式下 Wails 的 mux 无法被外部请求触达（9245 是 Vite dev server），
// 因此需要独立端口。检测 DEV=true 或 WAILS_DEV=true 环境变量决定是否启动。
func (s *DebugService) StartDebugHTTPServer() {
	if os.Getenv("DEV") != "true" && os.Getenv("WAILS_DEV") != "true" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/__debug", func(w http.ResponseWriter, r *http.Request) {
		params := map[string]interface{}{
			"selector": r.URL.Query().Get("selector"),
			"mode":     r.URL.Query().Get("mode"),
			"prop":     r.URL.Query().Get("prop"),
		}
		if d := r.URL.Query().Get("depth"); d != "" {
			if n, err := strconv.Atoi(d); err == nil {
				params["depth"] = n
			}
		}
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				params["limit"] = n
			}
		}

		result, err := s.Inspect(params)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err != nil {
			w.WriteHeader(504)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":    false,
				"error": err.Error(),
			})
			return
		}
		w.Write([]byte(result))
	})

	addr := "127.0.0.1:9255"
	log.Printf("[debug] 调试 HTTP 服务器启动: http://%s/__debug", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[debug] HTTP 服务器异常: %v", err)
		}
	}()
}
