<script setup lang="ts">
import { computed, type Component } from 'vue'
import Spinner from './Spinner.vue'

type Variant = 'primary' | 'secondary' | 'ghost'
type Size = 'sm' | 'md' | 'lg'

interface Props {
  /** 视觉变体 */
  variant?: Variant
  /** 尺寸 */
  size?: Size
  /** 禁用态 */
  disabled?: boolean
  /** 加载态（显示 Spinner 替换 icon） */
  loading?: boolean
  /** 尾部图标组件（trailing） */
  icon?: Component
  /** 原生 button type */
  type?: 'button' | 'submit' | 'reset'
}

const props = withDefaults(defineProps<Props>(), {
  variant: 'primary',
  size: 'md',
  disabled: false,
  loading: false,
  type: 'button',
})

// 加载态下按钮不可交互
const isInteractive = computed(() => !props.disabled && !props.loading)

// Spinner 尺寸随按钮尺寸缩放
const spinnerSize = computed(() => {
  switch (props.size) {
    case 'sm':
      return 14
    case 'lg':
      return 18
    case 'md':
    default:
      return 16
  }
})
</script>

<template>
  <button
    class="icon-btn"
    :class="[
      `icon-btn--${variant}`,
      `icon-btn--${size}`,
      { 'is-disabled': disabled, 'is-loading': loading },
    ]"
    :type="type"
    :disabled="!isInteractive"
    :aria-busy="loading"
  >
    <span class="icon-btn__label">
      <slot />
    </span>
    <!-- Button-in-Button：尾部图标嵌套独立圆形容器；loading 时复用 Spinner -->
    <span
      v-if="icon || loading"
      class="icon-btn__icon"
      aria-hidden="true"
    >
      <Spinner v-if="loading" :size="spinnerSize" />
      <component v-else :is="icon" class="icon-btn__icon-img" />
    </span>
  </button>
</template>

<style scoped>
.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  border: none;
  border-radius: var(--radius-full);
  font-family: var(--font-body);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
  user-select: none;
  transition: transform var(--duration-normal) var(--ease-fluid),
    background-color var(--duration-normal) var(--ease-fluid),
    color var(--duration-normal) var(--ease-fluid),
    border-color var(--duration-normal) var(--ease-fluid),
    opacity var(--duration-normal) var(--ease-fluid);
}

/* -- 尺寸 -- */
.icon-btn--sm {
  padding: var(--space-2) var(--space-4);
  font-size: 13px;
}
.icon-btn--md {
  padding: var(--space-3) var(--space-6);
  font-size: 14px;
}
.icon-btn--lg {
  padding: var(--space-4) var(--space-7);
  font-size: 15px;
}

/* -- 变体 -- */
/* primary：樱花粉主按钮 */
.icon-btn--primary {
  background-color: var(--accent-sakura);
  color: #f5f5f7; /* on-accent 文字：spec 指定白色，双模式保持一致 */
}
.icon-btn--primary:hover {
  background-color: var(--accent-sakura-hover);
}
.icon-btn--primary:active {
  background-color: var(--accent-sakura-pressed);
}

/* secondary：透明描边 */
.icon-btn--secondary {
  background-color: transparent;
  color: var(--text-primary);
  border: 1px solid var(--border-strong);
}
.icon-btn--secondary:hover {
  background-color: var(--bg-card);
}

/* ghost：透明文字按钮 */
.icon-btn--ghost {
  background-color: transparent;
  color: var(--text-secondary);
}
.icon-btn--ghost:hover {
  background-color: var(--bg-card);
  color: var(--text-primary);
}

/* -- 磁吸物理动效（纯 CSS） -- */
.icon-btn:hover {
  transform: scale(1.02);
}
.icon-btn:active {
  transform: scale(0.98);
}

/* -- Button-in-Button：尾部图标圆形容器 -- */
.icon-btn__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  flex-shrink: 0;
  transition: transform var(--duration-normal) var(--ease-fluid);
}

/* 容器尺寸 = 行高量级 */
.icon-btn--sm .icon-btn__icon {
  width: 20px;
  height: 20px;
  font-size: 14px;
}
.icon-btn--md .icon-btn__icon {
  width: 24px;
  height: 24px;
  font-size: 16px;
}
.icon-btn--lg .icon-btn__icon {
  width: 28px;
  height: 28px;
  font-size: 18px;
}

/* 图标组件根 svg 尺寸（@vicons 未内置宽高，按 1em 缩放） */
.icon-btn__icon-img {
  width: 1em;
  height: 1em;
  display: block;
}

/* primary 图标容器：白色半透明覆盖（spec 指定） */
.icon-btn--primary .icon-btn__icon {
  background-color: rgba(255, 255, 255, 0.12);
}
/* secondary / ghost 图标容器：淡卡片底色 */
.icon-btn--secondary .icon-btn__icon,
.icon-btn--ghost .icon-btn__icon {
  background-color: var(--bg-card);
}

/* hover 时图标容器位移 + 微放大（禁用/加载态除外） */
.icon-btn:not(.is-disabled):not(.is-loading):hover .icon-btn__icon {
  transform: translateX(2px) scale(1.05);
}

/* -- 禁用态：不响应 hover/active -- */
.icon-btn.is-disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.icon-btn.is-disabled:hover,
.icon-btn.is-disabled:active {
  transform: none;
}

/* loading：保持视觉，不可交互 */
.icon-btn.is-loading {
  cursor: progress;
}
.icon-btn.is-loading:hover,
.icon-btn.is-loading:active {
  transform: none;
}

/* 聚焦无障碍：樱花粉焦点环 */
.icon-btn:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus-sakura);
}

/* 无障碍：减少动效时移除缩放/位移，仅保留颜色过渡 */
@media (prefers-reduced-motion: reduce) {
  .icon-btn,
  .icon-btn__icon {
    transition: background-color var(--duration-fast) var(--ease-out-expo),
      color var(--duration-fast) var(--ease-out-expo),
      opacity var(--duration-fast) var(--ease-out-expo);
  }
  .icon-btn:hover,
  .icon-btn:active,
  .icon-btn:not(.is-disabled):not(.is-loading):hover .icon-btn__icon {
    transform: none;
  }
}
</style>
