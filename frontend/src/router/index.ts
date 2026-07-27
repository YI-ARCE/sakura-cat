import { createRouter, createWebHashHistory, RouteRecordRaw } from 'vue-router'

// 路由 meta 约定：
//   showTitleBar:  boolean  是否显示前端自定义顶栏（默认 true）
//   resizable:     boolean  是否允许调整大小（控制最大化按钮，默认 true）
//   title:         string   顶栏标题（同步到系统任务栏）
//
// 登录流程由 Go 端控制（登录窗 → 扫码成功 → 开主窗 → 关登录窗），
// 前端路由不做鉴权，只负责页面跳转。
// 后端 OpenWindow 时拼 URL query（?titlebar=xx&resizable=xx），
// 全局守卫解析 query 覆盖 meta，实现"同一路由不同窗口实例不同行为"。

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: '/category',
  },
  {
    // Bangumi 发现：左侧 bangumi 筛选 + 右侧频道消息检索
    path: '/discover',
    name: 'Discover',
    component: () => import('../views/Discover.vue'),
    meta: { showTitleBar: true, resizable: true, title: '发现 - 樱花猫' },
  },
  {
    // 频道收录页打开的播放页：通过 localStorage 传递 payload
    path: '/repo-player',
    name: 'RepoPlayer',
    component: () => import('../views/RepoPlayer.vue'),
    meta: { showTitleBar: true, resizable: true, title: '频道播放 - 樱花猫' },
  },
  {
    path: '/category',
    name: 'Category',
    component: () => import('../views/Category.vue'),
    meta: { showTitleBar: true, resizable: true, title: '分类 - 樱花猫' },
  },
  {
    path: '/download',
    name: 'Download',
    component: () => import('../views/Download.vue'),
    meta: { showTitleBar: true, resizable: true, title: '下载 - 樱花猫' },
  },
  {
    path: '/settings',
    name: 'Settings',
    component: () => import('../views/Settings.vue'),
    meta: { showTitleBar: true, resizable: true, title: '设置 - 樱花猫' },
  },
  {
    path: '/me',
    name: 'Me',
    component: () => import('../views/Me.vue'),
    meta: { showTitleBar: true, resizable: true, title: '我的 - 樱花猫' },
  },
  {
    // 预览窗：独立窗口，走 PreviewLayout（noLayout: false 但 App.vue 按 /preview 前缀特殊处理）
    // title 不在此设置：由 Go 创建窗口时传入（剧名 - 樱花猫），Preview.vue 加载后 syncWindowTitle 动态覆盖
    path: '/preview/:videoId',
    name: 'Preview',
    component: () => import('../views/Preview.vue'),
    meta: {
      noLayout: false,
      showTitleBar: true,
      resizable: true,
      title: '',
    },
  },
  {
    // 一起看控制台:独立窗口,纯展示(邀请二维码/下载区块图/在线观看端),
    // 不播放视频,所有控制由观看端(浏览器扫码加入)发起。
    // title 由 Go 创建窗口时传入(一起看 - 樱花猫)。
    path: '/party-console/:roomId',
    name: 'PartyConsole',
    component: () => import('../views/PartyConsole.vue'),
    meta: {
      noLayout: false,
      showTitleBar: true,
      resizable: true,
      title: '一起看 - 樱花猫',
    },
  },
  {
    path: '/login',
    name: 'Login',
    component: () => import('../views/Login.vue'),
    // noLayout: true 跳过 MainLayout 的侧栏/内边距，使用 BlankLayout 全屏承载 LoginLayout
    meta: {
      noLayout: true,
      showTitleBar: false,
      resizable: false,
      title: '登录',
    },
  },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

// 全局前置守卫：解析 URL query 覆盖 route.meta
// 后端创建窗口时拼入 ?titlebar=&resizable=，前端首次加载时解析
router.beforeEach((to) => {
  if (to.query.titlebar !== undefined) {
    to.meta.showTitleBar = to.query.titlebar === 'true'
  }
  if (to.query.resizable !== undefined) {
    to.meta.resizable = to.query.resizable === 'true'
  }
})

export default router
