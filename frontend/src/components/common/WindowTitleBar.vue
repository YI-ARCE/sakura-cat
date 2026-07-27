<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { Window } from '@wailsio/runtime'
import { useWindowMeta } from '../../composables/useWindowMeta'

const { resizable, title } = useWindowMeta()

// 当前窗口对象（Window 即 wails runtime 注入的 thisWindow 实例，无需 new）
const win = Window

// 当前是否最大化（控制图标切换）
const isMaximised = ref<boolean>(false)

// 同步最大化状态
async function syncMaximiseState() {
  try {
    isMaximised.value = await win.IsMaximised()
  } catch {
    isMaximised.value = false
  }
}

// 最小化
function handleMinimise() {
  win.Minimise()
}

// 最大化/还原
function handleToggleMaximise() {
  if (!resizable.value) return
  win.ToggleMaximise().then(() => syncMaximiseState())
}

// 关闭当前窗口
function handleClose() {
  win.Close()
}

// 监听窗口 resize（捕捉最大化/还原切换）
function handleResize() {
  syncMaximiseState()
}

onMounted(() => {
  syncMaximiseState()
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
})
</script>

<template>
  <div class="title-bar">
    <!-- 左侧：应用图标 + 标题（可拖拽区域） -->
    <div class="title-bar__left">
      <img src="/logo.png" alt="logo" class="title-bar__logo" />
      <span class="title-bar__title">{{ title }}</span>
    </div>

    <!-- 中间：拖拽延伸区 -->
    <div class="title-bar__drag" />

    <!-- 右侧：窗口控制按钮 -->
    <div class="title-bar__controls">
      <button
        class="title-bar__btn title-bar__btn--min"
        title="最小化"
        @click="handleMinimise"
      >
        <svg viewBox="0 0 10 10" width="10" height="10">
          <rect x="1" y="4.5" width="8" height="1" />
        </svg>
      </button>
      <button
        class="title-bar__btn title-bar__btn--max"
        :class="{ 'is-disabled': !resizable }"
        :disabled="!resizable"
        :title="resizable ? (isMaximised ? '还原' : '最大化') : '不可调整大小'"
        @click="handleToggleMaximise"
      >
        <svg v-if="!isMaximised" viewBox="0 0 10 10" width="10" height="10">
          <rect x="1" y="1" width="8" height="8" fill="none" stroke="currentColor" stroke-width="1" />
        </svg>
        <svg v-else viewBox="0 0 10 10" width="10" height="10">
          <rect x="2.5" y="0.5" width="6" height="6" fill="none" stroke="currentColor" stroke-width="1" />
          <rect x="0.5" y="2.5" width="6" height="6" fill="none" stroke="currentColor" stroke-width="1" />
        </svg>
      </button>
      <button
        class="title-bar__btn title-bar__btn--close"
        title="关闭"
        @click="handleClose"
      >
        <svg viewBox="0 0 10 10" width="10" height="10">
          <path d="M1 1 L9 9 M9 1 L1 9" stroke="currentColor" stroke-width="1" />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
/* CSS 拖拽区域：app-region: drag 允许拖拽移动窗口 */
/* 按钮：app-region: no-drag 保证按钮可点击 */
.title-bar {
  display: flex;
  align-items: center;
  height: 32px;
  padding: 0 0 0 8px;
  background-color: var(--aside-bg);
  --wails-draggable: drag;
  user-select: none;
}

.title-bar__left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.title-bar__logo {
  width: 16px;
  height: 16px;
}

.title-bar__title {
  font-size: 12px;
  font-weight: 500;
  color: var(--text-color);
}

.title-bar__drag {
  flex: 1;
  height: 100%;
}

.title-bar__controls {
  display: flex;
  align-items: center;
  height: 100%;
  --wails-draggable: no-drag;
}

.title-bar__btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  height: 32px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-color);
  cursor: pointer;
  transition: background-color 0.15s;
}

.title-bar__btn:hover {
  background-color: rgba(255, 255, 255, 0.1);
}

.title-bar__btn--close:hover {
  background-color: #e81123;
  color: #fff;
}

.title-bar__btn.is-disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.title-bar__btn.is-disabled:hover {
  background-color: transparent;
}

.title-bar__btn svg {
  fill: currentColor;
}
</style>
