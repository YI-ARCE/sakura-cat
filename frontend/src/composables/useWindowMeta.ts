import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Window } from '@wailsio/runtime'

// useWindowMeta 提供窗口元数据的响应式读取与系统标题同步
//
// 从 route.meta 读取：
//   - showTitleBar: 是否显示顶栏（默认 true）
//   - resizable: 是否允许调整大小（默认 true）
//   - title: 顶栏标题（同步到系统任务栏）
//
// 注意：Vue Router 4 的 useRoute() 返回 shallowRef，直接修改 route.meta.title
// 不触发响应。因此：
//   - 静态标题（路由定义时声明）：watch route.meta.title，路由切换时引用变化触发
//   - 动态标题（运行时设置）：调用 setTitle() 直接更新 module 级 ref
//
// 路由切换时自动同步系统标题（Wails runtime Window.SetTitle）

// module 级响应式 ref：当前窗口标题
const titleRef = ref('')

export function useWindowMeta() {
  const route = useRoute()

  const showTitleBar = computed(() => route.meta.showTitleBar !== false)
  const resizable = computed(() => route.meta.resizable !== false)
  const title = titleRef

  // Window 即 wails runtime 注入的 thisWindow 实例，无需 new
  const win = Window

  // 监听 titleRef 变化，同步到系统标题
  watch(titleRef, (t) => {
    if (t) win.SetTitle(t)
  }, { immediate: true })

  // 监听 route.meta.title 引用变化（路由切换时触发），
  // 将路由声明的静态标题同步到 titleRef
  watch(() => route.meta.title as string, (t) => {
    if (t) titleRef.value = t
  }, { immediate: true })

  // 设置动态标题（用于 Preview 等运行时确定标题的页面）
  function setTitle(t: string) {
    titleRef.value = t
  }

  return { showTitleBar, resizable, title, setTitle }
}
