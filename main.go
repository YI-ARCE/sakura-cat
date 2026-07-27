package main

import (
	"embed"
	_ "embed"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tg-download/internal/db"
	"tg-download/internal/download"
	"tg-download/internal/events"
	"tg-download/internal/telegram"
	"tg-download/services"
)

// Wails 使用 Go 的 `embed` 包将前端文件嵌入到二进制中。
// frontend/dist 目录下的所有文件都会被嵌入并提供给前端访问。
// 详见 https://pkg.go.dev/embed

//go:embed all:frontend/dist
var assets embed.FS

// setupLogging 配置全局日志输出：同时写入文件与 stderr。
// 日志文件存放在用户配置目录下 TGDownload/app.log（与 data.db 同目录）。
// 返回日志文件的 Close 函数，由 main 在退出前调用。
func setupLogging(configDir string) func() {
	logDir := filepath.Join(configDir, "TGDownload")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "app.log")

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// 文件创建失败时仅用 stderr，不阻断启动
		log.Printf("[main] 无法打开日志文件 %s: %v，将仅输出到 stderr", logPath, err)
		return func() {}
	}
	// 同时输出到文件与 stderr（stderr 在 GUI 模式下通常无效，但保留以便调试）
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	// 日志前缀包含日期与时间
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("[main] === 应用启动，日志文件: %s ===", logPath)
	return func() { _ = f.Close() }
}

// main 函数是应用程序的入口。它初始化数据库、所有后端依赖与 Wails 应用，
// 注册全部 Service，创建主窗口并运行应用。
// 若运行过程中发生致命错误则记录日志并退出。
func main() {

	// 初始化 SQLite 数据库。
	// 数据库文件存放在用户配置目录下的 TGDownload 子目录，
	// 例如 Windows 下为 %APPDATA%\TGDownload\data.db。
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}

	// 配置日志输出到文件（位于 %APPDATA%\TGDownload\app.log）
	logClose := setupLogging(configDir)
	defer logClose()

	dbPath := filepath.Join(configDir, "TGDownload", "data.db")
	// 确保目录存在（MkdirAll 已对"已存在"做幂等处理，忽略其错误由后续流程报错）
	_ = os.MkdirAll(filepath.Dir(dbPath), 0755)
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	// 阶段 1：创建不依赖 emitter 的组件
	manager := telegram.NewClientManager(database)
	storage := download.NewStorageManager(database)

	// 创建 WailsEmitter，先传 nil 占位，待 app 创建后再注入
	emitter := events.NewWailsEmitter(nil)

	// 阶段 2：创建依赖 emitter 的下载组件
	scanner := download.NewScanner(manager, database, emitter)
	scheduler := download.NewScheduler(database, manager, storage, emitter)
	listener := download.NewListener(database, manager, scheduler, emitter)

	// 创建所有 Service（指针类型，注册到 Wails）
	authService := services.NewAuthService(manager, scheduler, listener, database)
	proxyService := services.NewProxyService(database, manager)
	dialogService := services.NewDialogService(database, manager)
	downloadService := services.NewDownloadService(database, scanner, scheduler)
	listenerService := services.NewListenerService(database, listener)
	templateService := services.NewTemplateService(database)
	settingsService := services.NewSettingsService(database)
	apiService := services.NewApiService("", database, manager)
	bangumiService := services.NewBangumiService("", database)
	sourceListService := services.NewSourceListService(database, manager)
	windowService := services.NewWindowService()
	videoService := services.NewVideoService(database)
	debugService := services.NewDebugService()
	partyService := services.NewPartyService(database, manager)

	// 创建视频流 HTTP handler 与 ServeMux。
	// /__debug 调试路由由独立 HTTP 服务（debug_http_dev.go，仅 dev 模式）处理。
	// 安全约束：流接口仅通过 record_id 查数据库获取 local_path，不接受任意 path 参数。
	// 注：分集流（episode/stream）由独立 EpisodeStreamServer 处理，绕过 Wails3 AssetServer 全量缓冲。
	videoStreamHandler := services.NewVideoStreamHandler(database)
	bangumiImageProxy := services.NewBangumiImageProxy(bangumiService)

	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/video/stream":
			videoStreamHandler.ServeHTTP(w, r)
		case "/api/bangumi/image":
			bangumiImageProxy.ServeHTTP(w, r)

		default:
			application.AssetFileServerFS(assets).ServeHTTP(w, r)
		}
	}))

	// 启动独立分集流 HTTP server，监听 127.0.0.1 随机端口
	// 绕过 Wails3 AssetServer 全量缓冲，原生支持流式响应（边下边播，不落盘）
	episodeStreamServer := services.NewEpisodeStreamServer(database, manager, "")
	episodeStreamBaseURL, err := episodeStreamServer.Start()
	if err != nil {
		log.Fatalf("[main] 启动分集流服务失败: %v", err)
	}
	defer episodeStreamServer.Stop()
	videoService.SetStreamBaseURL(episodeStreamBaseURL)

	// 创建 Wails 应用，注册所有 Service
	app := application.New(application.Options{
		Name:        "TG Download",
		Description: "Telegram MTProto 下载工具",
		Services: []application.Service{
			application.NewService(authService),
			application.NewService(proxyService),
			application.NewService(dialogService),
			application.NewService(downloadService),
			application.NewService(listenerService),
			application.NewService(templateService),
			application.NewService(settingsService),
			application.NewService(apiService),
			application.NewService(bangumiService),
			application.NewService(sourceListService),
			application.NewService(windowService),
			application.NewService(videoService),
			application.NewService(debugService),
			application.NewService(partyService),
		},
		Assets: application.AssetOptions{
			Handler: mux,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// 阶段 3：app 创建后，注入 emitter 与 windowService 的 app 引用
	emitter.SetApp(app)
	windowService.SetApp(app)
	windowService.SetAuthService(authService)
	windowService.SetDB(database)
	debugService.SetEmitter(emitter.Emit)
	debugService.StartDebugHTTPServer()
	partyService.SetApp(app)
	partyService.SetEmitter(emitter)

	// 阶段 4：通过 WindowService 创建启动窗口
	// 有微信 token -> 直接开主窗（OpenMainWindow 内部检测 TG 登录态选 URL）
	// 无微信 token -> 开登录窗（扫码登录）
	// 这样避免有 token 时"登录窗闪一下再开主窗"的问题
	_, _ = windowService.OpenStartupWindow()

	// 运行应用，此调用会阻塞直到应用退出。
	err = app.Run()

	// 若运行应用时发生错误，记录日志并退出。
	if err != nil {
		log.Fatal(err)
	}
}
