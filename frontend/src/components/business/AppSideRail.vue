<script setup lang="ts">
import { ref, onUnmounted, type Component } from 'vue'
import { Download, Settings, Filter, User, Compass } from '@vicons/tabler'

interface NavItem {
  name: string
  path: string
  icon: Component
  label: string
}

// 顶部图标组（分类、发现、下载）
const topItems: NavItem[] = [
  { name: 'category', path: '/category', icon: Filter, label: '分类' },
  { name: 'discover', path: '/discover', icon: Compass, label: '发现' },
  { name: 'download', path: '/download', icon: Download, label: '下载' },
]

// 底部图标组（我的、设置）
const bottomItems: NavItem[] = [
  { name: 'me', path: '/me', icon: User, label: '我的' },
  { name: 'settings', path: '/settings', icon: Settings, label: '设置' },
]

// tooltip 延迟显示控制：悬停超过 500ms 显示，移出立即消失
const activeTooltip = ref<string | null>(null)
let tooltipTimer: ReturnType<typeof setTimeout> | null = null

function handleMouseEnter(name: string) {
  tooltipTimer = setTimeout(() => {
    activeTooltip.value = name
  }, 500)
}

function handleMouseLeave() {
  if (tooltipTimer) {
    clearTimeout(tooltipTimer)
    tooltipTimer = null
  }
  activeTooltip.value = null
}

// 组件卸载时清除定时器，防止内存泄漏
onUnmounted(() => {
  if (tooltipTimer) {
    clearTimeout(tooltipTimer)
    tooltipTimer = null
  }
})
</script>

<template>
  <nav class="side-rail">
    <div class="side-rail__top">
      <router-link
        v-for="item in topItems"
        :key="item.name"
        :to="item.path"
        class="side-rail__btn"
        @mouseenter="handleMouseEnter(item.name)"
        @mouseleave="handleMouseLeave"
      >
        <component :is="item.icon" class="side-rail__icon" />
        <span
          v-if="activeTooltip === item.name"
          class="side-rail__tooltip"
        >
          {{ item.label }}
        </span>
      </router-link>
    </div>
    <div class="side-rail__bottom">
      <router-link
        v-for="item in bottomItems"
        :key="item.name"
        :to="item.path"
        class="side-rail__btn"
        @mouseenter="handleMouseEnter(item.name)"
        @mouseleave="handleMouseLeave"
      >
        <component :is="item.icon" class="side-rail__icon" />
        <span
          v-if="activeTooltip === item.name"
          class="side-rail__tooltip"
        >
          {{ item.label }}
        </span>
      </router-link>
    </div>
  </nav>
</template>

<style scoped>
.side-rail {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 64px;
  height: 100%;
  background-color: var(--bg-elevated);
  border-right: 1px solid var(--border-subtle);
  padding: 8px 0;
  box-sizing: border-box;
}

.side-rail__top {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.side-rail__bottom {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-top: auto;
  gap: 4px;
}

.side-rail__btn {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  text-decoration: none;
  border-radius: var(--radius-md);
  transition: background-color var(--duration-normal) var(--ease-fluid),
    color var(--duration-normal) var(--ease-fluid);
}

.side-rail__btn:hover {
  background-color: var(--menu-hover-bg);
  color: var(--text-primary);
}

.side-rail__btn.router-link-active {
  background-color: var(--menu-active-bg);
  color: var(--accent-sakura);
}

/* 激活态左侧樱花粉强调条：3px 宽、24px 高、垂直居中 */
.side-rail__btn::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  width: 3px;
  height: 24px;
  background-color: var(--accent-sakura);
  border-radius: var(--radius-full);
  transform: translateY(-50%) scaleY(0);
  transform-origin: center;
  transition: transform 300ms var(--ease-out-expo);
}

.side-rail__btn.router-link-active::before {
  transform: translateY(-50%) scaleY(1);
}

.side-rail__icon {
  width: 20px;
  height: 20px;
}

/* tooltip：位于图标右侧 8px */
.side-rail__tooltip {
  position: absolute;
  left: calc(100% + 8px);
  top: 50%;
  transform: translateY(-50%);
  padding: 4px 8px;
  background-color: var(--bg-card-solid);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-family: var(--font-body);
  font-size: 12px;
  white-space: nowrap;
  pointer-events: none;
  z-index: 1000;
}

/* 减弱动效偏好：强调条瞬时显示、悬停过渡降级 */
@media (prefers-reduced-motion: reduce) {
  .side-rail__btn::before {
    transition: none;
  }

  .side-rail__btn {
    transition-duration: 0ms;
  }
}
</style>
