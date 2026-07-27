<script setup lang="ts">
import { computed, type Component } from 'vue'
import { InfoCircle, CircleCheck, AlertCircle } from '@vicons/tabler'

type Variant = 'info' | 'success' | 'error'
type Size = 'sm' | 'md'

interface Props {
  /** 状态类型 */
  variant?: Variant
  /** 标题（必填） */
  title: string
  /** 副文案（可选） */
  description?: string
  /** 尺寸：sm=横向布局，md=纵向布局（图标在上文案在下） */
  size?: Size
}

const props = withDefaults(defineProps<Props>(), {
  variant: 'info',
  size: 'md',
})

// 根据 variant 选择图标组件
const iconComp = computed<Component>(() => {
  switch (props.variant) {
    case 'success':
      return CircleCheck
    case 'error':
      return AlertCircle
    case 'info':
    default:
      return InfoCircle
  }
})
</script>

<template>
  <div
    class="status-hint"
    :class="[`status-hint--${variant}`, `status-hint--${size}`]"
    role="status"
  >
    <div class="status-hint__icon">
      <!-- class 落到图标组件根 svg 上，便于 scoped 样式命中尺寸 -->
      <component :is="iconComp" class="status-hint__icon-img" />
    </div>
    <div class="status-hint__content">
      <p class="status-hint__title">{{ title }}</p>
      <p v-if="description" class="status-hint__desc">{{ description }}</p>
    </div>
  </div>
</template>

<style scoped>
.status-hint {
  display: flex;
  gap: var(--space-3);
  transition: opacity var(--duration-normal) var(--ease-out-expo);
}

/* md：图标在上，文案在下 */
.status-hint--md {
  flex-direction: column;
  align-items: center;
  text-align: center;
}

/* sm：横向布局 */
.status-hint--sm {
  flex-direction: row;
  align-items: flex-start;
  text-align: left;
}

/* 图标容器：淡色背景圆 */
.status-hint__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: var(--radius-full);
  font-size: 20px;
  flex-shrink: 0;
  transition: background-color var(--duration-normal) var(--ease-out-expo),
    color var(--duration-normal) var(--ease-out-expo);
}

.status-hint--sm .status-hint__icon {
  width: 28px;
  height: 28px;
  font-size: 16px;
}

/* 图标组件根 svg 尺寸（@vicons 未内置宽高，按 1em 缩放） */
.status-hint__icon-img {
  width: 1em;
  height: 1em;
  display: block;
}

.status-hint__content {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.status-hint__title {
  margin: 0;
  font-family: var(--font-body);
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--text-primary);
}

.status-hint--md .status-hint__title {
  font-size: 15px;
}

.status-hint__desc {
  margin: 0;
  font-family: var(--font-body);
  font-size: 13px;
  line-height: 1.5;
  color: var(--text-secondary);
}

/* variant 颜色映射 -- 图标色 + 由变量派生的淡色背景（避免硬编码） */
.status-hint--info .status-hint__icon {
  color: var(--accent-cyan);
  background: color-mix(in srgb, var(--accent-cyan) 14%, transparent);
}

.status-hint--success .status-hint__icon {
  color: var(--accent-sakura);
  background: color-mix(in srgb, var(--accent-sakura) 14%, transparent);
}

.status-hint--error .status-hint__icon {
  color: var(--color-error);
  background: color-mix(in srgb, var(--color-error) 14%, transparent);
}

/* 无障碍：减少动效时移除过渡 */
@media (prefers-reduced-motion: reduce) {
  .status-hint,
  .status-hint__icon {
    transition: none;
  }
}
</style>
