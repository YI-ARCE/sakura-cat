# 前端交互逻辑文档

本文档描述前端与后端的交互机制，涵盖窗口管理、路由守卫、顶栏控制、标题同步等核心逻辑。

## 目录

- [整体架构](#整体架构)
- [窗口管理服务](#窗口管理服务)
  - [后端 WindowService](#后端-windowservice)
  - [前端封装 windowApi](#前端封装-windowapi)
- [路由系统](#路由系统)
  - [Hash 模式](#hash-模式)
  - [全局守卫与 meta 解析](#全局守卫与-meta-解析)
  - [路由 meta 约定](#路由-meta-约定)
- [顶栏组件](#顶栏组件)
  - [显隐控制](#显隐控制)
  - [拖拽移动](#拖拽移动)
  - [三个按钮](#三个按钮)
  - [最大化按钮联动](#最大化按钮联动)
- [标题同步](#标题同步)
- [窗口生命周期](#窗口生命周期)
  - [单例窗口](#单例窗口)
  - [多实例窗口](#多实例窗口)
  - [主窗口](#主窗口)
- [典型场景](#典型场景)
  - [启动流程](#启动流程)
  - [登录跳主窗](#登录跳主窗)
  - [创建业务窗口](#创建业务窗口)
  - [多实例预览窗](#多实例预览窗)

---

## 整体架构

```
前端 (Vue3 + vue-router)
├── services/window.ts        ← 对 bindings 的二次封装
├── composables/useWindowMeta ← 读取路由 meta + 同步系统标题
├── components/WindowTitleBar ← 顶栏（拖拽 + 按钮）
├── layouts/Blank|MainLayout  ← 集成顶栏 + router-view
└── router/index.ts           ← hash 模式 + query 解析守卫
        ↑
        │ URL query 传递
        ↓
后端 (Go + Wails v3)
├── services/window_service.go
│   ├── OpenWindow     ← 创建/聚焦窗口
│   ├── CloseWindow    ← 关闭单例
│   ├── SetWindowTitle ← 同步系统标题
│   └── 关闭事件       ← 单例清理 + 主窗口退出
└── main.go             ← 通过 WindowService 创建主窗

        ↑
        │ Wails runtime
        ↓
@wailsio/runtime (前端窗口 API)
├── Window.Minimise / ToggleMaximise / Close  ← 顶栏按钮直接调
└── Window.SetTitle / IsMaximised             ← 标题同步 + 状态查询
```

## 窗口管理服务

### 后端 WindowService

位置：[services/window_service.go](file:///d:/project/tg-download/services/window_service.go)

**核心字段：**

```go
type WindowOptions struct {
    Title        string // 窗口标题
    Width, Height int    // 初始尺寸
    URL          string // 路由路径（同时作为单例 key）
    Resizable    bool   // 是否允许调整大小（false 时禁用最大化按钮）
    Unique       bool   // 是否唯一窗口
    ShowTitleBar bool   // 是否显示前端自定义顶栏
    IsMain       bool   // 是否主窗口（关闭时退出应用）
}
```

**方法：**

| 方法 | 作用 | 前端调用方式 |
|---|---|---|
| `OpenWindow(opts)` | 创建或聚焦窗口 | `windowApi.open(opts)` |
| `CloseWindow(url)` | 关闭指定 URL 的单例 | `windowApi.close(url)` |
| `SetWindowTitle(url, title)` | 同步系统任务栏标题 | `windowApi.setTitle(url, title)` |

**URL 拼接规则：**

后端创建窗口时，将 `ShowTitleBar` 和 `Resizable` 附加到 hash URL 的 query 中：

```
URL: "/login", ShowTitleBar: false, Resizable: false
→ 加载 URL: #/login?titlebar=false&resizable=false
```

### 前端封装 windowApi

位置：[src/services/window.ts](file:///d:/project/tg-download/frontend/src/services/window.ts)

```ts
import { windowApi, WindowOptions } from '@/services/window'

await windowApi.open({
  title: '下载任务',
  width: 1000,
  height: 700,
  url: '/download',
  resizable: true,
  unique: true,
  showTitleBar: true,
  isMain: false,
})
```

**说明：**
- 封装层对 bindings 做延迟加载，避免编译时报错
- 接口字段名采用驼峰（Wails 自动转换 Go 的 PascalCase 为 camelCase）
- 业务代码统一从 `services/window.ts` 导入，不直接调 bindings

## 路由系统

### Hash 模式

位置：[src/router/index.ts](file:///d:/project/tg-download/frontend/src/router/index.ts)

```ts
const router = createRouter({
  history: createWebHashHistory(),
  routes,
})
```

**原因：**
- 多窗口场景下每个窗口是独立的 webview，加载 `file://` 协议
- Hash 模式不依赖服务器路由，适合桌面应用
- URL 格式：`file:///.../index.html#/login?titlebar=false`

### 全局守卫与 meta 解析

```ts
router.beforeEach((to) => {
  if (to.query.titlebar !== undefined) {
    to.meta.showTitleBar = to.query.titlebar === 'true'
  }
  if (to.query.resizable !== undefined) {
    to.meta.resizable = to.query.resizable === 'true'
  }
})
```

**数据流：**

```
后端 OpenWindow 传 showTitleBar / resizable
  ↓
后端拼 URL：#/<path>?titlebar=<bool>&resizable=<bool>
  ↓
Wails 创建窗口，加载该 URL
  ↓
vue-router 解析 query.titlebar → 写入 route.meta.showTitleBar
  ↓
Layout / TitleBar 组件读 route.meta → 决定渲染
```

### 路由 meta 约定

```ts
meta: {
  showTitleBar: boolean  // 是否显示顶栏（默认 true）
  resizable:    boolean  // 是否允许调整大小（默认 true，控制最大化按钮）
  title:        string   // 顶栏标题（同步到系统任务栏）
}
```

**优先级：**
- URL 有 query → query 覆盖 meta（同一路由不同窗口实例可不同行为）
- URL 无 query → 用路由表 meta 默认值
- 同窗口内路由跳转后 query 丢失 → 用目标路由 meta

## 顶栏组件

位置：[src/components/common/WindowTitleBar.vue](file:///d:/project/tg-download/frontend/src/components/common/WindowTitleBar.vue)

### 显隐控制

- 顶栏组件本身不判断显隐
- 由 `BlankLayout` / `MainLayout` 通过 `v-if="showTitleBar"` 控制
- `showTitleBar` 从 `useWindowMeta()` composable 读取

### 拖拽移动

采用 CSS `app-region` 属性（webview2 标准，与 Electron 一致）：

```css
.title-bar {
  -webkit-app-region: drag;       /* 整条顶栏可拖拽 */
  app-region: drag;
}

.title-bar__controls {
  -webkit-app-region: no-drag;    /* 按钮区不拖拽，保证可点击 */
  app-region: no-drag;
}
```

**说明：**
- 顶栏空白区域可拖拽移动窗口
- 三个按钮区域设置为 `no-drag`，保证点击不被拦截
- 无需后端方法，纯 CSS 实现

### 三个按钮

| 按钮 | 实现 | 说明 |
|---|---|---|
| 最小化 | `Window.Minimise()` | 直接调 Wails runtime |
| 最大化/还原 | `Window.ToggleMaximise()` | 切换最大化状态 |
| 关闭 | `Window.Close()` | 仅关闭当前窗口，不影响其他窗口 |

**为什么直接调 runtime 而非后端 service？**
- Wails runtime 提供了完整的 `Window` API（`Minimise` / `ToggleMaximise` / `Close` / `IsMaximised` / `SetTitle`）
- 前端直接调可减少一次 IPC 往返
- 这些操作都作用于"当前窗口"，runtime 自动识别上下文

### 最大化按钮联动

```ts
const { resizable } = useWindowMeta()

function handleToggleMaximise() {
  if (!resizable.value) return   // resizable=false 时直接拦截
  win.ToggleMaximise().then(() => syncMaximiseState())
}
```

- `resizable=false` → 按钮 `disabled`，点击无效
- `resizable=true` → 正常可点，图标根据当前状态切换（□ ↔ ❐）
- 最大化状态通过 `window.resize` 事件 + `Window.IsMaximised()` 查询同步

## 标题同步

位置：[src/composables/useWindowMeta.ts](file:///d:/project/tg-download/frontend/src/composables/useWindowMeta.ts)

```ts
const win = new Window()

watch(title, (t) => {
  if (t) win.SetTitle(t)
}, { immediate: true })
```

**触发时机：**
- 路由切换 → `route.meta.title` 变化 → `watch` 触发 → 调 `Window.SetTitle`
- 窗口首次加载 → `immediate: true` → 立即同步一次

**同步内容：**
- 前端顶栏标题：`{{ title }}`（来自 `route.meta.title`）
- 系统任务栏标题：`Window.SetTitle(title)`（Wails runtime 调用）
- 两者保持一致

**为什么用前端 runtime 而非后端 SetWindowTitle？**
- `Window.SetTitle` 作用于"当前窗口"，runtime 自动识别上下文
- 后端 `SetWindowTitle(url, title)` 需要传 URL 匹配注册表，多实例窗口无法使用
- 前端调 runtime 更简单，且无 IPC 延迟

**后端 `SetWindowTitle` 的适用场景：**
- 窗口 A 需要修改窗口 B 的标题（跨窗口修改）
- 目前暂未使用，预留给后续业务

## 窗口生命周期

### 单例窗口（Unique=true）

```
OpenWindow({url:'/download', unique:true})
  ↓
检查 unique map → 已存在？
  ├─ 是 → Show + Focus，返回 AlreadyOpen=true
  └─ 否 → 新建窗口
           ↓
           存入 unique map（key = hashURL）
           ↓
           注册 WindowClosing 事件
           ↓
用户关闭窗口
  ↓
WindowClosing 事件触发
  ↓
从 unique map 删除
  ↓
下次 OpenWindow 视为新窗口
```

**特点：**
- 同一 URL 全局只允许一个窗口
- 再次 OpenWindow 自动聚焦已有窗口，不新建
- 关闭后自动从注册表移除，下次可重新创建

### 多实例窗口（Unique=false）

```
OpenWindow({url:'/preview/123', unique:false})
  ↓
直接新建窗口，不入 unique map
  ↓
用户关闭 → 窗口销毁，后端无感知
```

**特点：**
- 每次调用都新建窗口
- 后端不维护注册表，无法通过 `CloseWindow(url)` 关闭
- 关闭由窗口内部调 `Window.Close()` 完成

### 主窗口（IsMain=true）

```
OpenWindow({url:'/', isMain:true})
  ↓
创建窗口，正常使用
  ↓
用户关闭主窗口
  ↓
WindowClosing 事件触发
  ↓
app.Quit() → 关闭所有窗口并退出应用
```

**特点：**
- `IsMain` 与 `Unique` 独立，不强绑
- 主窗口通常也 `Unique=true`（避免开多个主窗）
- 关闭主窗口会强制关闭其他所有窗口（如下载中窗口）
- 多个 `IsMain=true` 窗口：信任前端不重复创建，后端不校验

## 典型场景

### 启动流程

```
main.go
  ↓
NewWindowService() → 注册到 Wails Services
  ↓
app := application.New()
  ↓
windowService.SetApp(app)
  ↓
windowService.OpenWindow({
  url: '/',
  isMain: true,
  unique: true,
  showTitleBar: true,
  resizable: true,
})
  ↓
后端拼 URL：#/?titlebar=true&resizable=true
  ↓
Wails 创建窗口，加载前端
  ↓
Vue 启动，路由守卫解析 query → meta
  ↓
MainLayout 渲染，WindowTitleBar 显示
```

### 登录跳主窗

```ts
// 登录窗中登录成功后
import { windowApi } from '@/services/window'
import { Window } from '@wailsio/runtime'

// 1. 创建主窗
await windowApi.open({
  title: 'TG Download',
  width: 1280,
  height: 800,
  url: '/',
  resizable: true,
  unique: true,
  showTitleBar: true,
  isMain: true,
})

// 2. 关闭登录窗（当前窗口）
new Window().Close()
```

**说明：**
- 登录窗 `isMain=false`，关闭不影响应用
- 主窗 `isMain=true`，后续关闭会退出应用
- 登录窗关闭通过 `Window.Close()` 完成（多实例行为）

### 创建业务窗口

```ts
// 唯一下载窗口（单例）
await windowApi.open({
  title: '下载任务',
  width: 1000,
  height: 700,
  url: '/download',
  resizable: true,
  unique: true,
  showTitleBar: true,
  isMain: false,
})

// 第二次调用同样参数 → AlreadyOpen=true，聚焦已有窗口
```

### 多实例预览窗

```ts
// 每次都新建预览窗
await windowApi.open({
  title: '视频预览',
  width: 800,
  height: 600,
  url: '/preview/123',   // URL 含业务参数，自然不同
  resizable: false,
  unique: false,
  showTitleBar: true,
  isMain: false,
})
```

**说明：**
- `unique=false` 时每次都新建
- URL 含业务参数（如 `/preview/123`），即使 `unique=true` 也不会冲突
- 窗口内关闭通过顶栏关闭按钮 → `Window.Close()`

---

## 前端目录结构参考

```
src/
├── components/common/WindowTitleBar.vue  ← 顶栏组件
├── composables/useWindowMeta.ts          ← 窗口元数据读取
├── layouts/
│   ├── BlankLayout.vue                   ← 全屏布局（登录/引导）
│   └── MainLayout.vue                    ← 主布局（侧边栏 + 内容区）
├── router/index.ts                        ← 路由 + 守卫
├── services/window.ts                    ← 窗口服务封装
└── ...
```

## 相关文件索引

**后端：**
- [services/window_service.go](file:///d:/project/tg-download/services/window_service.go)
- [main.go](file:///d:/project/tg-download/main.go)

**前端：**
- [src/services/window.ts](file:///d:/project/tg-download/frontend/src/services/window.ts)
- [src/router/index.ts](file:///d:/project/tg-download/frontend/src/router/index.ts)
- [src/composables/useWindowMeta.ts](file:///d:/project/tg-download/frontend/src/composables/useWindowMeta.ts)
- [src/components/common/WindowTitleBar.vue](file:///d:/project/tg-download/frontend/src/components/common/WindowTitleBar.vue)
- [src/layouts/BlankLayout.vue](file:///d:/project/tg-download/frontend/src/layouts/BlankLayout.vue)
- [src/layouts/MainLayout.vue](file:///d:/project/tg-download/frontend/src/layouts/MainLayout.vue)
