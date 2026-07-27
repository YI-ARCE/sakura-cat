<script setup lang="ts">
// 加载旋转图标 -- 纯 SVG + CSS 实现，无外部依赖
interface Props {
  /** 图标尺寸（px） */
  size?: number
}

withDefaults(defineProps<Props>(), {
  size: 24,
})
</script>

<template>
  <svg
    class="spinner"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    role="img"
    aria-label="加载中"
  >
    <!-- 背景轨道 -->
    <circle
      class="spinner__track"
      cx="12"
      cy="12"
      r="10"
      fill="none"
      stroke-width="2.5"
    />
    <!-- 旋转加载头（描边弧） -->
    <circle
      class="spinner__head"
      cx="12"
      cy="12"
      r="10"
      fill="none"
      stroke-width="2.5"
      stroke-linecap="round"
    />
  </svg>
</template>

<style scoped>
/* 容器：旋转动画（机械循环，是唯一允许 linear 的场景） */
.spinner {
  display: inline-block;
  vertical-align: middle;
  transform-origin: center;
  animation: spinner-spin var(--duration-entrance) linear infinite;
}

/* 背景轨道：淡边框色 */
.spinner__track {
  stroke: var(--border-subtle);
}

/* 加载头：樱花粉弧形，dasharray + dashoffset 定义弧长与起点 */
.spinner__head {
  stroke: var(--accent-sakura);
  /* 圆周 ≈ 62.83；绘制约 25% 弧（16 单位），其余留白 */
  stroke-dasharray: 16 47;
  stroke-dashoffset: 0;
}

@keyframes spinner-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

/* 无障碍：减少动效时减缓旋转至 2s，降低前庭刺激 */
@media (prefers-reduced-motion: reduce) {
  .spinner {
    animation-duration: 2s;
  }
}
</style>
