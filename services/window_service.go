// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 WindowService，负责向前端暴露窗口创建与管理能力。
package services

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"tg-download/internal/telegram"
)

// WindowOptions 是前端调用 OpenWindow 时提交的窗口配置。
// 仅暴露业务关心的少量字段，其余选项由后端默认值填充。
type WindowOptions struct {
	Title        string // 窗口标题
	Width        int    // 初始宽度（px）
	Height       int    // 初始高度（px）
	URL          string // 路由路径（如 "/download"），同时作为单例窗口的 key
	Resizable    bool   // 是否允许拖拽调整大小（false 时禁用最大化按钮）
	Unique       bool   // 是否唯一窗口：true 时同 URL 已存在则聚焦而非新建
	ShowTitleBar bool   // 是否显示前端自定义顶栏
	IsMain       bool   // 是否主窗口：关闭时退出整个应用
}

// WindowResult 是 OpenWindow 的返回值。
type WindowResult struct {
	AlreadyOpen bool // true 表示单例窗口已存在，本次仅聚焦未新建
}

// tgLoginChecker 是 AuthService 的最小接口，避免把 *AuthService 作为
// WindowService 的公开字段类型（会导致 Wails binding 把 AuthService 同时
// 当作 service 和 model 生成，引发 TS2300 重复定义）。
type tgLoginChecker interface {
	RestoreSession(fallbackAPIID int, fallbackAPIHash string) (bool, error)
	GetLoginStatus() (bool, telegram.LoginStep, error)
}

// WindowService 是 Wails 绑定的窗口管理服务。
// 通过 SetApp 在 main.go 阶段 3 注入 *application.App（与 events.Emitter 同模式）。
//
// 单例窗口（Unique=true）以 URL 为 key 存入 unique map，
// 窗口关闭时通过 WindowClosing 事件自动从 map 中移除。
// 多实例窗口（Unique=false）不入注册表，由前端自行管理。
//
// 主窗口（IsMain=true）关闭时调用 app.Quit() 退出整个应用。
type WindowService struct {
	app               *application.App
	auth              tgLoginChecker // 用于 OpenMainWindow 恢复 TG 会话
	db                *sql.DB        // 用于判断是否第一次使用（查 wechat_token + sessions）
	mu                sync.RWMutex
	unique            map[string]*application.WebviewWindow
	closingMainWindow bool // 退出登录时关闭主窗的标记：跳过 app.Quit()
}

// NewWindowService 创建一个新的 WindowService 实例。
// app 可为 nil，由 main.go 在 app 创建后通过 SetApp 注入。
func NewWindowService() *WindowService {
	return &WindowService{
		app:    nil,
		unique: make(map[string]*application.WebviewWindow),
	}
}

// SetApp 注入 Wails 应用实例。
// 必须在 app 创建后、运行前调用（main.go 阶段 3）。
func (s *WindowService) SetApp(app *application.App) {
	s.app = app
}

// SetAuthService 注入 AuthService 引用，供 OpenMainWindow 恢复 TG 会话。
// 参数用 any 避免 Wails binding 把接口类型当 model 生成（引发 TS 重复定义）。
func (s *WindowService) SetAuthService(auth any) {
	if c, ok := auth.(tgLoginChecker); ok {
		s.auth = c
	}
}

// SetDB 注入数据库引用，供 OpenStartupWindow 判断登录状态。
func (s *WindowService) SetDB(db *sql.DB) {
	s.db = db
}

// OpenWindow 创建或聚焦一个窗口。
//
// 行为：
//   - Unique=true 且同 URL 窗口已存在：调用 Show + Focus，返回 AlreadyOpen=true
//   - Unique=true 且不存在：新建并存入 unique map，注册关闭事件自动清理
//   - Unique=false：直接新建，不入 map
//
// URL 会被拼接为 hash 路由格式 "#<URL>?titlebar=<bool>&resizable=<bool>"，
// 前端路由守卫解析 query 写入 route.meta，供顶栏组件读取。
func (s *WindowService) OpenWindow(opts WindowOptions) (WindowResult, error) {
	if s.app == nil {
		return WindowResult{}, fmt.Errorf("window service: app not initialized")
	}

	// 拼接 hash 路由 URL：前端使用 createWebHashHistory，路径需以 "#" 开头
	// 附加 titlebar/resizable query 供前端路由守卫解析
	// 注意 opts.URL 可能已含 query（如 /preview/123?title=xx），需用 & 连接避免出现两个 ?
	sep := "?"
	if strings.Contains(opts.URL, "?") {
		sep = "&"
	}
	hashURL := fmt.Sprintf("#%s%stitlebar=%t&resizable=%t", opts.URL, sep, opts.ShowTitleBar, opts.Resizable)

	// 单例窗口：已存在则聚焦
	if opts.Unique {
		s.mu.RLock()
		w, ok := s.unique[hashURL]
		s.mu.RUnlock()
		if ok {
			w.Show()
			w.Focus()
			return WindowResult{AlreadyOpen: true}, nil
		}
	}

	// 构造完整窗口选项（默认值由后端填充）
	fullOpts := application.WebviewWindowOptions{
		Title:            opts.Title,
		Width:            opts.Width,
		Height:           opts.Height,
		URL:              hashURL,
		DisableResize:    !opts.Resizable,
		Frameless:        true, // 无边框，UI 由前端自定义
		DevToolsEnabled:  false,
		Hidden:           false, // 创建即显示
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			BackdropType: application.Acrylic,
			Theme:        application.Dark,
		},
		BackgroundType: application.BackgroundTypeTranslucent,
	}

	w := s.app.Window.NewWithOptions(fullOpts)

	// 单例窗口：存入注册表并注册关闭事件自动清理
	if opts.Unique {
		s.mu.Lock()
		s.unique[hashURL] = w
		s.mu.Unlock()
	}

	// 关闭事件处理：单例清理 + 主窗口退出
	w.OnWindowEvent(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if opts.Unique {
			s.mu.Lock()
			delete(s.unique, hashURL)
			s.mu.Unlock()
		}
		if opts.IsMain {
			// OpenLoginWindow 场景：closingMainWindow=true 表示关闭主窗但保留应用
			// 消费标记后重置，避免影响后续正常退出
			s.mu.Lock()
			skipQuit := s.closingMainWindow
			s.closingMainWindow = false
			s.mu.Unlock()
			if !skipQuit {
				s.app.Quit()
			}
		}
	})

	return WindowResult{AlreadyOpen: false}, nil
}

// CloseWindow 关闭指定 URL 的单例窗口。
// 仅对 Unique=true 创建的窗口有效；多实例窗口需在前端自行关闭。
func (s *WindowService) CloseWindow(url string) error {
	// 尝试两种 key 格式：带 query 与不带 query
	// 由于前端可能传纯 URL，这里遍历匹配
	s.mu.RLock()
	defer s.mu.RUnlock()
	for key, w := range s.unique {
		// 去掉 key 的 "#" 前缀和 "?query" 后缀，比较纯 path
		keyPath := key
		if len(keyPath) > 0 && keyPath[0] == '#' {
			keyPath = keyPath[1:]
		}
		if idx := indexOfByte(keyPath, '?'); idx >= 0 {
			keyPath = keyPath[:idx]
		}
		if keyPath == url {
			w.Close()
			return nil
		}
	}
	return fmt.Errorf("window service: unique window not found for url %q", url)
}

// SetWindowTitle 同步更新窗口的系统任务栏标题。
// 前端路由切换时调用此方法，保持任务栏标题与顶栏标题一致。
func (s *WindowService) SetWindowTitle(url string, title string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for key, w := range s.unique {
		keyPath := key
		if len(keyPath) > 0 && keyPath[0] == '#' {
			keyPath = keyPath[1:]
		}
		if idx := indexOfByte(keyPath, '?'); idx >= 0 {
			keyPath = keyPath[:idx]
		}
		if keyPath == url {
			w.SetTitle(title)
			return nil
		}
	}
	return fmt.Errorf("window service: unique window not found for url %q", url)
}

// indexOfByte 返回字节 s 中第一个 c 的索引，未找到返回 -1。
func indexOfByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// MainWindowOptions 是 OpenMainWindow 的入参，仅暴露业务字段。
type MainWindowOptions struct {
	Title        string
	Width        int
	Height       int
	Resizable    bool
	ShowTitleBar bool
}

// OpenMainWindow 在登录流程完成后打开主窗口。
// 主窗统一以 "/" 打开，TG 登录已在登录窗内完成（扫码->检测->电报表单），
// 能调到此方法说明用户已通过完整登录流程。
func (s *WindowService) OpenMainWindow(opts MainWindowOptions) (WindowResult, error) {
	if s.app == nil {
		return WindowResult{}, fmt.Errorf("window service: app not initialized")
	}

	// 尝试恢复 TG 会话（不阻塞，不影响 URL 选择）
	if s.auth != nil {
		_, _ = s.auth.RestoreSession(0, "")
	}

	return s.OpenWindow(WindowOptions{
		Title:        opts.Title,
		Width:        opts.Width,
		Height:       opts.Height,
		URL:          "/",
		Resizable:    opts.Resizable,
		Unique:       true,
		ShowTitleBar: opts.ShowTitleBar,
		IsMain:       true,
	})
}

// OpenPartyConsoleWindow 打开"一起看"控制台窗口。
// 窗口尺寸 800×600,带前端自定义顶栏(showTitleBar=true)且可调整大小(resizable=true)。
// URL 形如 /party-console/<roomId>,前端路由表已注册对应路由。
// 单例窗口:同 roomId 已存在则聚焦而非新建。
func (s *WindowService) OpenPartyConsoleWindow(roomId string) (WindowResult, error) {
	if s.app == nil {
		return WindowResult{}, fmt.Errorf("window service: app not initialized")
	}
	return s.OpenWindow(WindowOptions{
		Title:        "一起看 - 樱花猫",
		Width:        800,
		Height:       600,
		URL:          "/party-console/" + roomId,
		Resizable:    true,
		Unique:       true,
		ShowTitleBar: true,
		IsMain:       false,
	})
}

// OpenStartupWindow 是应用启动时的窗口选择逻辑。
// 统一开登录窗，由前端 Login.vue 检测微信+TG 登录态决定后续流程：
//   - 有微信 token + TG 已登录 -> 前端调 openMainWindow 开主窗
//   - 有微信 token + TG 未登录 -> 前端展示电报登录表单
//   - 无微信 token -> 前端显示二维码
//
// 不在 Go 侧判断 TG 会话，因为会话记录存在不代表会话有效（登录失败也会留记录），
// 真正的有效性检测在前端 getLoginStatus 异步完成，避免 Go 侧阻塞。
func (s *WindowService) OpenStartupWindow() (WindowResult, error) {
	return s.OpenWindow(WindowOptions{
		Title:        "登录",
		Width:        1000,
		Height:       640,
		URL:          "/login",
		Resizable:    false,
		Unique:       true,
		ShowTitleBar: false,
		IsMain:       false,
	})
}

// OpenLoginWindow 退出登录时使用：打开登录窗并关闭主窗，但不退出整个应用。
// 主窗（URL="/"）关闭时默认会触发 app.Quit()，这里通过 closingMainWindow 标记跳过。
// 用于 Settings 页退出登录流程：前端清完会话后调用本方法切回登录窗。
//
// 注意：w.Close() 触发的 WindowClosing 事件是异步的，不能在 Close() 之后立刻
// 重置 closingMainWindow，否则事件回调执行时标记已被重置回 false，仍会 Quit。
// 这里保持 closingMainWindow=true，由主窗的 WindowClosing 事件回调自身重置。
func (s *WindowService) OpenLoginWindow() (WindowResult, error) {
	if s.app == nil {
		return WindowResult{}, fmt.Errorf("window service: app not initialized")
	}

	// 1. 开登录窗（若已存在则聚焦）
	result, err := s.OpenWindow(WindowOptions{
		Title:        "登录",
		Width:        1000,
		Height:       640,
		URL:          "/login",
		Resizable:    false,
		Unique:       true,
		ShowTitleBar: false,
		IsMain:       false,
	})
	if err != nil {
		return result, err
	}

	// 2. 标记"关闭主窗但不退出应用"，然后触发主窗关闭
	//    WindowClosing 事件回调会检查此标记跳过 app.Quit()，并重置标记
	s.mu.Lock()
	s.closingMainWindow = true
	s.mu.Unlock()

	// 3. 找到主窗并关闭
	s.mu.RLock()
	var mainWin *application.WebviewWindow
	for key, w := range s.unique {
		keyPath := key
		if len(keyPath) > 0 && keyPath[0] == '#' {
			keyPath = keyPath[1:]
		}
		if idx := indexOfByte(keyPath, '?'); idx >= 0 {
			keyPath = keyPath[:idx]
		}
		if keyPath == "/" {
			mainWin = w
			break
		}
	}
	s.mu.RUnlock()

	if mainWin != nil {
		mainWin.Close()
	}
	return result, nil
}
