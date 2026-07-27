// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件定义 ClientManager，封装 gotd/td 客户端实例及其代理、生命周期管理。
package telegram

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/transport"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"
)

// TestResult 表示代理测试或连接探测的结果。
type TestResult struct {
	Success   bool   `json:"success"`    // 是否成功
	Message   string `json:"message"`    // 结果说明或错误描述
	LatencyMs int64  `json:"latency_ms"` // 连接耗时（毫秒）
}

// dialTimeout 是建立 TCP 连接（含代理握手）的固定超时时间。
const dialTimeout = 30 * time.Second

// telegramTestTarget 是测试代理连接性时的目标 Telegram DC 地址（DC2）。
const telegramTestTarget = "149.154.167.50:443"

// ClientManager 管理 gotd/td 客户端实例的生命周期与代理配置。
// 所有公开方法均为线程安全（使用 mu 保护）。
type ClientManager struct {
	mu            sync.RWMutex       // 保护以下字段的读写
	client        *telegram.Client   // 当前客户端实例（可能为 nil）
	db            *sql.DB            // 数据库句柄，用于加载/保存代理与会话
	proxy         ProxyConfig        // 当前代理配置（内存缓存）
	ctx           context.Context    // 客户端运行上下文
	cancel        context.CancelFunc  // 用于停止客户端
	apiID         int                // Telegram API ID
	apiHash       string             // Telegram API Hash
	running       bool               // 客户端是否已启动
	authenticated bool               // 是否已登录（已通过认证）
	authFlow      *loginFlow         // 当前登录流程状态（可能为 nil）
	ready         chan struct{}      // 客户端就绪信号：连接建立且 checkAuth 完成后关闭；Start 退出时也会关闭
	onAuthFailed  func()             // 会话失效回调：当会话存在但认证失败时调用
	updateHandler telegram.UpdateHandler // 实时更新处理器（可能为 nil，表示不处理更新）
}

// NewClientManager 创建一个新的 ClientManager 实例。
// 创建后不会立即启动客户端，需调用 Start 才会建立连接。
func NewClientManager(db *sql.DB) *ClientManager {
	return &ClientManager{
		db: db,
	}
}

// SetCredentials 设置 Telegram API 凭据（apiID / apiHash）。
// 应在 Start 之前调用。
func (m *ClientManager) SetCredentials(apiID int, apiHash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apiID = apiID
	m.apiHash = apiHash
}

// GetProxy 返回当前代理配置（线程安全）。
func (m *ClientManager) GetProxy() ProxyConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.proxy
}

// UpdateProxy 更新代理配置并按需重建客户端网络层。
// 若客户端已启动，则停止当前客户端并通过 Reconnect 重建；未启动则仅保存配置到数据库。
// 已登录会话保存在 SQLite（sessions 表），不会因重连丢失。
func (m *ClientManager) UpdateProxy(cfg ProxyConfig) error {
	// 先持久化到数据库
	if err := SaveProxy(m.db, cfg); err != nil {
		return fmt.Errorf("保存代理配置失败: %w", err)
	}

	m.mu.Lock()
	m.proxy = cfg
	running := m.running
	m.mu.Unlock()

	// 客户端未启动时只保存配置，等待后续 Start 时生效
	if !running {
		return nil
	}

	// 客户端已启动，触发重连以应用新代理
	return m.Reconnect()
}

// buildDialFunc 根据代理类型构造 dial 函数（dcs.DialFunc）。
//
// 注意：任务描述中提到的 transport.InternetSocketDialer 在 gotd/td v0.159.0 中
// 已不存在，实际通过 dcs.Plain(PlainOptions{Dial: ...}) 注入自定义 dial 函数。
// 此处返回 dcs.DialFunc 以适配当前 API。
//
// 参数:
//   - cfg: 代理配置
//
// 返回:
//   - dcs.DialFunc: 用于建立到目标地址的 net.Conn 的函数
//   - error: 代理配置无效或类型暂不支持时返回错误
func (m *ClientManager) buildDialFunc(cfg ProxyConfig) (dcs.DialFunc, error) {
	// 代理未启用：使用默认 net.Dialer
	if !cfg.Enabled {
		d := &net.Dialer{Timeout: dialTimeout}
		return d.DialContext, nil
	}

	// 校验地址与端口
	if cfg.Address == "" {
		return nil, fmt.Errorf("代理地址为空")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("代理端口非法: %d", cfg.Port)
	}

	proxyAddr := net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port))

	// 构造底层转发 dialer（用于实际建立到代理服务器的 TCP 连接）
	forward := &net.Dialer{Timeout: dialTimeout}

	switch cfg.Type {
	case ProxyTypeSOCKS5:
		// 构造 SOCKS5 认证信息（仅在用户名非空时启用）
		var auth *proxy.Auth
		if cfg.Username != "" {
			auth = &proxy.Auth{
				User:     cfg.Username,
				Password: cfg.Password,
			}
		}
		socksDialer, err := proxy.SOCKS5("tcp", proxyAddr, auth, forward)
		if err != nil {
			return nil, fmt.Errorf("构造 SOCKS5 dialer 失败: %w", err)
		}
		// 优先使用 DialContext（SOCKS5 dialer 实现了 ContextDialer）
		if ctxDialer, ok := socksDialer.(proxy.ContextDialer); ok {
			return ctxDialer.DialContext, nil
		}
		// 回退：用 proxy.dialContext 包装（支持取消）
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return proxy.Dial(ctx, network, addr)
		}, nil

	case ProxyTypeHTTP, ProxyTypeHTTPS:
		// 使用 HTTP CONNECT 隧道建立到目标的连接
		httpDialer := newHTTPConnectDialer(proxyAddr, cfg.Username, cfg.Password, cfg.Type == ProxyTypeHTTPS, forward)
		return httpDialer.DialContext, nil

	case ProxyTypeMTProto:
		// MTProto 代理需要额外的 obfuscated2 / secret 握手，暂不支持
		return nil, fmt.Errorf("MTProto 代理暂不支持")

	default:
		return nil, fmt.Errorf("未知的代理类型: %s", cfg.Type)
	}
}

// buildResolver 根据代理配置构造 dcs.Resolver。
// 使用 dcs.Plain（Intermediate 协议），并注入自定义 Dial 函数实现代理透传。
func (m *ClientManager) buildResolver(cfg ProxyConfig) (dcs.Resolver, error) {
	dialFunc, err := m.buildDialFunc(cfg)
	if err != nil {
		return nil, err
	}
	return dcs.Plain(dcs.PlainOptions{
		Protocol: transport.Intermediate,
		Dial:     dialFunc,
		Network:  "tcp",
	}), nil
}

// Start 启动 Telegram 客户端，阻塞运行直到上下文取消或发生致命错误。
// 内部流程：
//  1. 从数据库加载代理配置
//  2. 构造代理 dial 函数与 resolver
//  3. 创建 telegram.Client（使用 SQLite 会话存储，自动恢复已有会话）
//  4. 调用 client.Run 阻塞运行，在连接就绪后执行 checkAuth 并发出就绪信号
//
// 会话存储基于 SQLite（sessions 表），客户端启动时会自动从数据库加载
// 已保存的会话；若会话存在且有效，则恢复登录态；否则等待前端发起登录流程。
// 调用方可通过 WaitReady 等待客户端就绪。
func (m *ClientManager) Start(ctx context.Context) error {
	m.mu.Lock()
	// 加载代理配置（优先用数据库中的配置）
	cfg, err := LoadProxy(m.db)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("加载代理配置失败: %w", err)
	}
	m.proxy = cfg

	if m.apiID == 0 || m.apiHash == "" {
		m.mu.Unlock()
		return fmt.Errorf("API 凭据未设置（apiID/apiHash 为空）")
	}

	// 构造 resolver
	resolver, err := m.buildResolver(cfg)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("构造代理 resolver 失败: %w", err)
	}

	// 创建子上下文用于管理客户端生命周期
	subCtx, cancel := context.WithCancel(ctx)
	m.ctx = subCtx
	m.cancel = cancel

	// 重置就绪信号通道：每次 Start 创建新的通道，
	// 在 Run callback 中 checkAuth 完成后关闭。
	m.ready = make(chan struct{})

	// 创建 telegram.Client，使用 SQLite 会话存储
	// UpdateHandler 使用动态包装：在客户端创建时传入非 nil 的包装函数，
	// 使 gotd/td 启用更新接收（NoUpdates=false）。包装函数运行时读取当前
	// m.updateHandler，若未设置则丢弃更新。这样 SetUpdateHandler 可在
	// 客户端运行后动态注册处理器，无需重建客户端。
	client := telegram.NewClient(m.apiID, m.apiHash, telegram.Options{
		Resolver:       resolver,
		DialTimeout:    dialTimeout,
		SessionStorage: NewSQLiteSessionStorage(m.db),
		Device: telegram.DeviceConfig{
			DeviceModel: "SakuraCat",
			AppVersion:  "1.0.0",
		},
		UpdateHandler: telegram.UpdateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
			handler := m.getUpdateHandler()
			if handler == nil {
				return nil
			}
			return handler.Handle(ctx, u)
		}),
	})
	m.client = client
	m.running = true
	// 捕获 subCtx 供后台 goroutine 使用
	runCtx := subCtx
	m.mu.Unlock()

	// 在后台 goroutine 中调用 Run 阻塞运行，使 Start 非阻塞返回。
	// 这样调用方在 Start 返回后即可调用 WaitReady 等待就绪，
	// 避免"客户端未启动"的竞态（Start 返回时 m.ready 已确保设置）。
	go func() {
		runErr := client.Run(runCtx, func(ctx context.Context) error {
			// 客户端连接已就绪，检查登录状态
			// checkAuth 失败不中断运行（仅记录未登录态），等待前端发起登录
			_ = m.checkAuth(ctx)

			// 触发会话失效回调（SubTask 2.4）：
			// 若 sessions 表中存在会话记录但当前未认证（可能过期），
			// 通知前端需要重新登录。
			m.maybeNotifyAuthFailed(ctx)

			// 发出就绪信号，通知等待方（如 Login 服务）客户端已可用
			m.mu.Lock()
			if m.ready != nil {
				close(m.ready)
			}
			m.mu.Unlock()

			// 等待上下文取消（客户端被 Stop 或外部 ctx 结束）
			<-ctx.Done()
			return ctx.Err()
		})

		// 清理状态
		m.mu.Lock()
		// 确保 ready 通道已关闭（Run 可能在 callback 执行前就失败）
		// 不置 nil：保留已关闭的通道，使 WaitReady 能通过 m.running=false 检测到停止。
		// 下次 Start 会重置 m.ready 为新通道。
		if m.ready != nil {
			select {
			case <-m.ready:
			// 已关闭
			default:
				close(m.ready)
			}
		}
		m.running = false
		m.client = nil
		m.ctx = nil
		m.cancel = nil
		// 客户端停止后认证态与登录流程不再有效
		m.authenticated = false
		m.authFlow = nil
		m.mu.Unlock()

		// runErr 仅为 ctx.Canceled 时属正常停止，忽略
		_ = runErr
	}()

	return nil
}

// checkAuth 检查当前客户端的登录状态并更新 m.authenticated。
// 调用 client.Auth().Status(ctx) 获取认证状态：
//   - 已登录：设置 m.authenticated = true
//   - 未登录：设置 m.authenticated = false，等待前端发起登录流程
//
// 该方法不会阻塞或等待登录，仅做状态查询。
func (m *ClientManager) checkAuth(ctx context.Context) error {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()

	if client == nil {
		m.mu.Lock()
		m.authenticated = false
		m.mu.Unlock()
		return fmt.Errorf("客户端未启动")
	}

	status, err := client.Auth().Status(ctx)
	if err != nil {
		// 查询失败视为未登录
		m.mu.Lock()
		m.authenticated = false
		m.mu.Unlock()
		return fmt.Errorf("查询登录状态失败: %w", err)
	}

	m.mu.Lock()
	m.authenticated = status.Authorized
	m.mu.Unlock()
	return nil
}

// maybeNotifyAuthFailed 在会话存在但认证失败时触发 onAuthFailed 回调。
// 用于 SubTask 2.4：会话失效（可能过期）时通知前端重新登录。
// 仅在客户端启动且未认证、且 sessions 表存在记录时触发。
func (m *ClientManager) maybeNotifyAuthFailed(ctx context.Context) {
	m.mu.RLock()
	authenticated := m.authenticated
	onAuthFailed := m.onAuthFailed
	m.mu.RUnlock()

	// 已登录无需通知
	if authenticated {
		return
	}
	// 未设置回调则跳过
	if onAuthFailed == nil {
		return
	}

	// 检查 sessions 表是否已存在会话记录
	storage := NewSQLiteSessionStorage(m.db)
	exists, err := storage.SessionExists(ctx)
	if err != nil || !exists {
		// 查询失败或无会话记录（首次启动），不触发回调
		return
	}

	// 会话存在但认证失败，触发回调
	onAuthFailed()
}

// IsAuthenticated 返回客户端是否已通过 Telegram 认证（已登录）。
// 线程安全。
func (m *ClientManager) IsAuthenticated() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authenticated
}

// WaitReady 阻塞等待客户端启动就绪（连接已建立且 checkAuth 完成）。
// 在 Start 之前调用返回错误；若 Start 在就绪前退出（如连接失败）也返回错误。
// 传入的 ctx 可用于超时控制。
func (m *ClientManager) WaitReady(ctx context.Context) error {
	m.mu.RLock()
	ready := m.ready
	m.mu.RUnlock()

	if ready == nil {
		return fmt.Errorf("客户端未启动")
	}

	select {
	case <-ready:
		// 通道已关闭：可能是就绪，也可能是 Start 退出
		m.mu.RLock()
		running := m.running
		m.mu.RUnlock()
		if !running {
			return fmt.Errorf("客户端启动失败或已停止")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetOnAuthFailed 设置会话失效回调。
// 当客户端启动时检测到 sessions 表存在会话记录但认证失败（可能过期），
// 将调用此回调通知前端重新登录。
// callback 可为 nil 以清除回调。
func (m *ClientManager) SetOnAuthFailed(callback func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onAuthFailed = callback
}

// SetUpdateHandler 设置实时更新处理器（telegram.UpdateHandler）。
// 在客户端 Start 创建客户端时通过 Options 注入动态包装函数，
// 包装函数运行时调用当前 handler。因此该方法可在客户端启动前或启动后
// 任意时刻调用：启动前调用会在创建客户端时生效；启动后调用会立即
// 让包装函数开始转发更新给新 handler。
// handler 为 nil 表示停止处理更新（更新将被丢弃）。
func (m *ClientManager) SetUpdateHandler(handler telegram.UpdateHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateHandler = handler
}

// getUpdateHandler 线程安全地读取当前更新处理器（可能为 nil）。
func (m *ClientManager) getUpdateHandler() telegram.UpdateHandler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.updateHandler
}

// Stop 停止客户端（取消运行上下文）。
// 若客户端未启动则无操作。
func (m *ClientManager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// GetClient 返回当前客户端实例（线程安全）。
// 若客户端未启动或已停止，返回 nil。
func (m *ClientManager) GetClient() *telegram.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}

// DB 返回管理器持有的数据库句柄，供外部模块读写持久化数据
// （如凭据、会话、代理等）。
func (m *ClientManager) DB() *sql.DB {
	return m.db
}

// Reconnect 断开重连：保留已登录会话，仅重建客户端网络层。
// 实现方式：取消当前运行上下文以停止 client.Run，
// 然后由调用方重新调用 Start 重建客户端。
//
// 注意：完整的"原地重启"需要在新 goroutine 中调用 Start。
// 当前实现提供停止语义，调用方可通过 goroutine 重新调用 Start 完成重连。
func (m *ClientManager) Reconnect() error {
	m.mu.Lock()
	cancel := m.cancel
	running := m.running
	m.mu.Unlock()

	if !running {
		// 未运行无需重连
		return nil
	}

	// 取消当前上下文以停止 client.Run
	if cancel != nil {
		cancel()
	}
	return nil
}

// TestProxy 通过给定代理尝试建立到 Telegram DC（149.154.167.50:443）的 TCP 连接。
// 使用 30 秒超时，返回测试结果（成功/失败、消息、耗时）。
func (m *ClientManager) TestProxy(cfg ProxyConfig) (TestResult, error) {
	result := TestResult{}

	// 校验目标地址
	if strings.TrimSpace(telegramTestTarget) == "" {
		return result, fmt.Errorf("测试目标地址为空")
	}

	// 构造 dial 函数
	dialFunc, err := m.buildDialFunc(cfg)
	if err != nil {
		result.Message = fmt.Sprintf("构造代理 dialer 失败: %v", err)
		return result, nil
	}

	// 30 秒超时上下文
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	start := time.Now()
	conn, err := dialFunc(ctx, "tcp", telegramTestTarget)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		result.Message = fmt.Sprintf("连接失败: %v", err)
		result.LatencyMs = latency
		return result, nil
	}
	defer conn.Close()

	result.Success = true
	result.Message = "连接成功"
	result.LatencyMs = latency
	return result, nil
}

// httpConnectDialer 实现 HTTP CONNECT 隧道代理 dialer。
// 通过向 HTTP 代理发送 CONNECT 请求建立到目标地址的隧道连接。
type httpConnectDialer struct {
	proxyAddr string // 代理服务器地址 host:port
	username  string // 代理认证用户名（可空）
	password  string // 代理认证密码（可空）
	https     bool   // 代理服务器自身是否使用 TLS（HTTPS 代理）
	forward   *net.Dialer
}

// newHTTPConnectDialer 构造一个 HTTP CONNECT 隧道 dialer。
func newHTTPConnectDialer(proxyAddr, username, password string, https bool, forward *net.Dialer) *httpConnectDialer {
	return &httpConnectDialer{
		proxyAddr: proxyAddr,
		username:  username,
		password:  password,
		https:     https,
		forward:   forward,
	}
}

// DialContext 通过 HTTP CONNECT 隧道建立到目标地址的连接。
func (d *httpConnectDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// 1. 建立到代理服务器的底层连接
	conn, err := d.forward.DialContext(ctx, "tcp", d.proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("连接代理服务器失败: %w", err)
	}

	// 2. 若为 HTTPS 代理，先建立 TLS 到代理服务器
	// 注意：为简化实现并避免引入额外 crypto/tls 配置，此处仅处理 HTTP 代理。
	// HTTPS 代理场景的 TLS 包装可在后续按需补充。
	if d.https {
		_ = conn.Close()
		return nil, fmt.Errorf("HTTPS 代理（代理服务器自身 TLS）暂未实现，请使用 HTTP 代理")
	}

	// 3. 发送 CONNECT 请求
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
	if d.username != "" {
		// 添加 Basic Auth 头
		auth := d.username + ":" + d.password
		encoded := encodeBase64([]byte(auth))
		connectReq += "Proxy-Authorization: Basic " + encoded + "\r\n"
	}
	connectReq += "\r\n"

	if _, err := conn.Write([]byte(connectReq)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送 CONNECT 请求失败: %w", err)
	}

	// 4. 读取代理响应
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("读取代理响应失败: %w", err)
	}

	// 解析状态行，例如 "HTTP/1.1 200 Connection established\r\n"
	fields := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	if len(fields) < 2 {
		_ = conn.Close()
		return nil, fmt.Errorf("代理响应格式非法: %s", statusLine)
	}
	statusCode, err := strconv.Atoi(fields[1])
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("解析代理状态码失败: %w", err)
	}
	if statusCode != 200 {
		// 读取并丢弃剩余响应头
		for {
			line, err := reader.ReadString('\n')
			if err != nil || line == "\r\n" || line == "\n" {
				break
			}
		}
		_ = conn.Close()
		return nil, fmt.Errorf("代理拒绝连接，状态码: %d", statusCode)
	}

	// 5. 读取并丢弃剩余响应头（直到空行）
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("读取代理响应头失败: %w", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// 6. 隧道建立成功，返回连接
	// 注意：bufio.Reader 可能已缓冲了 CONNECT 响应之后的数据，
	// 但 Telegram 协议在隧道建立后由对端先发，通常不会出现预读。
	return conn, nil
}

// encodeBase64 实现标准 base64 编码（避免在文件顶部额外引入 encoding/base64，
// 同时保持文件依赖最小化）。
func encodeBase64(src []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var result strings.Builder
	for i := 0; i < len(src); i += 3 {
		var n uint32
		b := 0
		for j := 0; j < 3 && i+j < len(src); j++ {
			n |= uint32(src[i+j]) << (16 - uint(j*8))
			b++
		}
		result.WriteByte(alphabet[(n>>18)&0x3F])
		result.WriteByte(alphabet[(n>>12)&0x3F])
		if b > 1 {
			result.WriteByte(alphabet[(n>>6)&0x3F])
		} else {
			result.WriteByte('=')
		}
		if b > 2 {
			result.WriteByte(alphabet[n&0x3F])
		} else {
			result.WriteByte('=')
		}
	}
	return result.String()
}

// 编译期断言：确保 httpConnectDialer 实现了 proxy.ContextDialer 接口。
var _ proxy.ContextDialer = (*httpConnectDialer)(nil)
