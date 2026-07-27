import { onScopeDispose, ref, watch } from 'vue'

// 入场动画配置
interface RevealOptions {
  delay?: number             // 延迟 ms，默认 0
  duration?: number          // 时长 ms，默认 800（对应 --duration-entrance）
  initialTransform?: string  // 初始 transform，默认 'translateY(24px)'（由 CSS keyframe 决定，当前未单独应用）
  threshold?: number         // IntersectionObserver threshold，默认 0.15
  once?: boolean             // 是否只触发一次，默认 true
}

// useScrollReveal 基于 IntersectionObserver 实现元素入场动画
//
// 用法：
//   const { target, isVisible } = useScrollReveal({ delay: 120 })
//   // 模板：<div ref="target" class="reveal" :class="{ 'is-visible': isVisible }">
//
// 特性：
//   - 元素进入视口时 isVisible 置 true，配合 .reveal / .is-visible 工具类触发动画
//   - 自动检测 prefers-reduced-motion，开启时直接 isVisible = true（瞬时显示，跳过动画）
//   - onScopeDispose 自动断开 observer，无需手动清理
//   - 不使用 scroll 事件监听（硬禁项）
export function useScrollReveal(options: RevealOptions = {}) {
  const {
    delay = 0,
    duration = 800,
    threshold = 0.15,
    once = true,
  } = options

  // target 绑定到需要入场的元素；isVisible 标记是否进入视口
  const target = ref<HTMLElement | null>(null)
  const isVisible = ref(false)

  // 尊重 prefers-reduced-motion：直接显示，跳过动画
  const prefersReducedMotion =
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches

  if (prefersReducedMotion) {
    isVisible.value = true
  }

  let observer: IntersectionObserver | null = null

  // 仅在支持 IntersectionObserver 且未启用 reduced-motion 时观察
  if (!prefersReducedMotion && typeof IntersectionObserver !== 'undefined') {
    observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            isVisible.value = true
            // once 模式下触发一次后即停止观察
            if (once && observer) {
              observer.disconnect()
            }
          } else if (!once) {
            // 非 once 模式：离开视口时复位，便于反复触发
            isVisible.value = false
          }
        }
      },
      { threshold },
    )

    // 元素挂载后启动观察，并写入 delay / duration CSS 变量供工具类使用
    watch(target, (el, _old, onCleanup) => {
      if (!el || !observer) return
      el.style.setProperty('--reveal-delay', `${delay}ms`)
      el.style.setProperty('--reveal-duration', `${duration}ms`)
      observer.observe(el)
      onCleanup(() => observer?.unobserve(el))
    })
  }

  // 作用域销毁时断开 observer，避免泄漏
  onScopeDispose(() => {
    observer?.disconnect()
    observer = null
  })

  return { target, isVisible }
}
