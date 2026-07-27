// Package events 提供 Wails3 应用层事件推送适配器。
// 本文件实现 WailsEmitter，封装 application.App.Event.Emit，
// 使其满足 internal/download 包定义的 EventEmitter 接口（鸭子类型）。
//
// 设计要点：
//   - events 包不导入 download 包，避免循环依赖（download 已导入 events 反向不成立）；
//     Go 接口为鸭子类型，WailsEmitter 只要方法签名匹配即自动实现 download.EventEmitter。
//   - 采用两阶段初始化：NewWailsEmitter(nil) 创建占位实例，
//     在 application.New 返回 *application.App 后通过 SetApp 注入。
//   - app 字段使用 sync.RWMutex 保护，Emit 可能在 SetApp 之前被并发调用。
package events

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// WailsEmitter 是 download.EventEmitter 接口的 Wails 实现。
// 它把后端事件转发给前端，通过 application.App.Event.Emit 推送。
type WailsEmitter struct {
	mu  sync.RWMutex
	app *application.App
}

// NewWailsEmitter 创建一个新的 WailsEmitter。
// app 可为 nil，表示尚未完成注入；后续通过 SetApp 设置。
func NewWailsEmitter(app *application.App) *WailsEmitter {
	return &WailsEmitter{app: app}
}

// SetApp 在两阶段初始化的第二阶段注入已创建的 *application.App。
// 该方法线程安全，可在 Emit 并发调用时安全设置。
func (e *WailsEmitter) SetApp(app *application.App) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.app = app
}

// Emit 推送一个事件给前端。
// 若 app 尚未注入（SetApp 未调用）则静默跳过，返回 nil 不报错，
// 以便在应用启动早期 emitter 尚未就绪时安全调用。
// 推送本身不会返回错误（Wails Event.Emit 返回 bool 表示是否被取消），
// 此处统一返回 nil。
func (e *WailsEmitter) Emit(eventName string, data interface{}) error {
	e.mu.RLock()
	app := e.app
	e.mu.RUnlock()

	if app == nil {
		return nil
	}
	app.Event.Emit(eventName, data)
	return nil
}

// PushEvent 以变参形式推送事件给前端。
// 与 Emit 的区别：Emit 接受单个 data 参数，PushEvent 接受变参，
// 直接转发给 application.EventManager.Emit（其原生支持变参）。
// 供 PartyService 等需要传递多参数或单 map 的服务使用。
// 若 app 尚未注入则静默跳过。
func (e *WailsEmitter) PushEvent(eventName string, data ...any) {
	e.mu.RLock()
	app := e.app
	e.mu.RUnlock()

	if app == nil {
		return
	}
	app.Event.Emit(eventName, data...)
}
