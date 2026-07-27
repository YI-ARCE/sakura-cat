<script setup lang="ts">
import { computed } from 'vue'

interface Props {
  /** 图片 URL（统一走后端 /api/bangumi/image 代理） */
  src?: string
  /** 替换文本 */
  alt?: string
  /** 宽度（数字按 px，字符串原样） */
  width?: number | string
  /** 高度（数字按 px，字符串原样） */
  height?: number | string
  /** object-fit 模式 */
  fit?: 'cover' | 'contain' | 'fill' | 'none' | 'scale-down'
  /** 加载策略：lazy / eager */
  loading?: 'lazy' | 'eager'
  /** 占位图（src 为空时使用） */
  placeholder?: string
  /** 自定义 class */
  class?: string
}

const props = withDefaults(defineProps<Props>(), {
  src: '',
  alt: '',
  fit: 'cover',
  loading: 'lazy',
  placeholder: '',
})

// 所有图片统一走后端代理（外部链接仅有 bangumi 域名）
const finalSrc = computed(() => {
  const s = props.src?.trim()
  if (!s) return props.placeholder
  return `/api/bangumi/image?url=${encodeURIComponent(s)}`
})

const styleObj = computed(() => {
  const st: Record<string, string> = {}
  if (props.width !== undefined) {
    st.width = typeof props.width === 'number' ? `${props.width}px` : props.width
  }
  if (props.height !== undefined) {
    st.height = typeof props.height === 'number' ? `${props.height}px` : props.height
  }
  st.objectFit = props.fit
  return st
})

// 加载失败兜底：显示占位图或透明 1x1
function onError(e: Event) {
  const img = e.target as HTMLImageElement
  if (props.placeholder) {
    img.src = props.placeholder
  }
}
</script>

<template>
  <img
    :src="finalSrc"
    :alt="alt"
    :loading="loading"
    :style="styleObj"
    :class="$props.class"
    @error="onError"
  />
</template>
