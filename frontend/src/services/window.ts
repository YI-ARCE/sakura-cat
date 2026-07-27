// 窗口服务封装层
//
// 对 bindings/tg-download/services 的二次封装，
// 统一 import 入口，便于后续扩展错误处理/事件订阅等。
// 注：windowservice.js 仅具名导出函数（OpenWindow/CloseWindow/SetWindowTitle），
// WindowService 命名空间由 services/index.js 重新导出，故从此处加载。

export interface WindowOptions {
  /** 窗口标题 */
  title: string
  /** 初始宽度（px） */
  width: number
  /** 初始高度（px） */
  height: number
  /** 路由路径（如 "/download"），同时作为单例窗口的 key */
  url: string
  /** 是否允许拖拽调整大小（false 时禁用最大化按钮） */
  resizable: boolean
  /** 是否唯一窗口：true 时同 URL 已存在则聚焦而非新建 */
  unique: boolean
  /** 是否显示前端自定义顶栏 */
  showTitleBar: boolean
  /** 是否主窗口：关闭时退出整个应用 */
  isMain: boolean
}

export interface WindowResult {
  /** true 表示单例窗口已存在，本次仅聚焦未新建 */
  alreadyOpen: boolean
}

// 主窗选项：传给 Go 侧 OpenMainWindow，由 Go 侧决定 URL（/ 或 /login）
export interface MainWindowOptions {
  title: string
  width: number
  height: number
  resizable: boolean
  showTitleBar: boolean
}

// bindings 文件生成后从此路径导入；生成前调用会报错，属预期行为
let WindowServiceBinding: any = null

async function loadBinding() {
  if (!WindowServiceBinding) {
    const mod = await import('../../bindings/tg-download/services/index')
    WindowServiceBinding = mod.WindowService
  }
  return WindowServiceBinding
}

export const windowApi = {
  /** 创建或聚焦窗口 */
  async open(opts: WindowOptions): Promise<WindowResult> {
    const svc = await loadBinding()
    return svc.OpenWindow(opts)
  },

  /** 关闭指定 URL 的单例窗口 */
  async close(url: string): Promise<void> {
    const svc = await loadBinding()
    return svc.CloseWindow(url)
  },

  /** 同步更新窗口的系统任务栏标题 */
  async setTitle(url: string, title: string): Promise<void> {
    const svc = await loadBinding()
    return svc.SetWindowTitle(url, title)
  },

  /** 打开主窗：Go 侧内部检测 TG 登录态后选择 URL（/ 或 /login） */
  async openMainWindow(opts: MainWindowOptions): Promise<WindowResult> {
    const svc = await loadBinding()
    return svc.OpenMainWindow(opts)
  },

  /** 退出登录：开登录窗 + 关主窗（不退出应用） */
  async openLoginWindow(): Promise<WindowResult> {
    const svc = await loadBinding()
    return svc.OpenLoginWindow()
  },
}

// VideoMeta 打开预览窗时携带的视频元数据（通过 URL query 传递给预览窗）
export interface VideoMeta {
  title: string
  cover: string
  category?: string
  year?: string
  season?: string
}

// openPreviewWindow 打开视频预览独立窗口
// 路由 /preview/:videoId，meta 通过 URL query 传递（title/cover/category/year/season）
export async function openPreviewWindow(videoId: number, meta: VideoMeta): Promise<WindowResult> {
  const params = new URLSearchParams()
  params.set('title', meta.title)
  params.set('cover', meta.cover)
  if (meta.category) params.set('category', meta.category)
  if (meta.year) params.set('year', meta.year)
  if (meta.season) params.set('season', meta.season)
  const url = `/preview/${videoId}?${params.toString()}`
  return windowApi.open({
    title: meta.title ? `${meta.title} - 樱花猫` : '樱花猫',
    width: 1280,
    height: 800,
    url,
    resizable: true,
    unique: true,
    showTitleBar: true,
    isMain: false,
  })
}
