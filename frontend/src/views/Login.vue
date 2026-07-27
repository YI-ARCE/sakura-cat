<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Window } from '@wailsio/runtime'
import LoginLayout from '../layouts/LoginLayout.vue'
import LoginVisualPanel from '../components/business/LoginVisualPanel.vue'
import TgLoginForm from '../components/business/TgLoginForm.vue'
import { useAuthStore } from '../stores/auth'
import { windowApi } from '../services/window'
import { useNaiveFeedback } from '../composables/useNaiveFeedback'
import { restoreSession, logout } from '../services/settings'
import { refreshDialogs, syncSourceList } from '../services/repo'

const auth = useAuthStore()
const feedback = useNaiveFeedback()

const jumpError = ref('')

const formLoading = ref(true)
const formLoadingMsg = ref('正在检查登录状态...')

async function openMainWindow(msg = '正在进入主窗口...') {
  formLoading.value = true
  formLoadingMsg.value = msg
  auth.setAuthed(true)
  try {
    await windowApi.openMainWindow({
      title: '樱花猫',
      width: 1280,
      height: 800,
      resizable: true,
      showTitleBar: true,
    })
    await Window.Close()
  } catch (err) {
    formLoading.value = false
    auth.setAuthed(false)
    jumpError.value =
      err instanceof Error ? err.message : '打开主窗口失败，请重试'
    console.error('[Login] 跳转主窗失败:', err)
  }
}

function delay(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

async function openMainWindowWithProgress() {
  formLoading.value = true
  formLoadingMsg.value = 'TG 账户验证通过'
  await delay(700)
  formLoadingMsg.value = '正在同步频道列表...'
  try {
    await refreshDialogs()
  } catch (e) {
    console.error('[Login] 刷新频道列表失败:', e)
  }
  try {
    const r = await syncSourceList()
    if (r.total > 0) {
      console.log(`[Login] 清单同步完成：共 ${r.total} 条，写入 ${r.upserted}，新订阅 ${r.subscribed}，失败 ${r.failed}`)
    }
  } catch (e) {
    console.error('[Login] 同步视频源清单失败:', e)
  }
  formLoadingMsg.value = '正在进入主窗口...'
  await delay(600)
  await openMainWindow('正在进入主窗口...')
}

async function resetLoginState() {
  const ok = await feedback.confirm({ title: '确定重置登录状态？', content: '将清空 TG 登录信息', danger: true })
  if (!ok) return
  try {
    try {
      await logout()
    } catch (e) {
      console.error('[Login] 清除 TG 会话失败:', e)
    }
    auth.reset()
    await delay(200)
  } finally {
    window.location.reload()
  }
}

async function handleTgSuccess() {
  formLoading.value = true
  formLoadingMsg.value = '登录成功'
  await delay(500)
  formLoadingMsg.value = '正在同步频道列表...'
  try {
    await refreshDialogs()
  } catch (e) {
    console.error('[Login] 刷新频道列表失败:', e)
  }
  try {
    const r = await syncSourceList()
    if (r.total > 0) {
      console.log(`[Login] 清单同步完成：共 ${r.total} 条，写入 ${r.upserted}，新订阅 ${r.subscribed}，失败 ${r.failed}`)
    }
  } catch (e) {
    console.error('[Login] 同步视频源清单失败:', e)
  }
  await openMainWindow('正在进入主窗口...')
}

onMounted(async () => {
  formLoadingMsg.value = '正在检查登录状态...'
  try {
    const authed = await restoreSession()
    if (authed) {
      await openMainWindowWithProgress()
      return
    }
    formLoading.value = false
  } catch (e) {
    console.error('[Login] TG 状态检测失败:', e)
    formLoading.value = false
  }
})
</script>

<template>
  <div class="login-view">
    <div class="drag"></div>
    <LoginLayout>
      <template #visual>
        <LoginVisualPanel />
      </template>
      <template #form>
        <div class="login-form-stage">
          <div class="login-form-stage__panel">
            <TgLoginForm @success="handleTgSuccess" />
          </div>
          <div
            v-if="formLoading"
            class="login-form-stage__loading"
          >
            <n-spin :size="32" />
            <span class="login-form-stage__loading-text">{{ formLoadingMsg }}</span>
          </div>
        </div>
      </template>
    </LoginLayout>
    <n-alert
      v-if="jumpError"
      type="error"
      :title="jumpError"
      class="login-view__error"
    />
    <a
      class="login-view__reset"
      @click="resetLoginState"
    >重置登录状态</a>
  </div>
</template>

<style scoped>
.login-view {
  position: relative;
  width: 100%;
  height: 100%;
}

.drag{
  position: absolute;
  z-index: 99;
  width: 100%;
  height: 30px;
  --wails-draggable: drag;
  top: 0;
}

.login-form-stage {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.login-form-stage__panel {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.login-form-stage__loading {
  position: absolute;
  inset: 0;
  z-index: 10;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  background: var(--bg-base);
}

.login-form-stage__loading-text {
  font-family: var(--font-body);
  font-size: 14px;
  color: var(--text-secondary);
}

.login-view__error {
  position: absolute;
  left: 50%;
  bottom: var(--space-6);
  transform: translateX(-50%);
  max-width: calc(100% - var(--space-8) * 2);
  pointer-events: none;
  z-index: 20;
}

.login-view__reset {
  position: absolute;
  right: var(--space-5);
  bottom: var(--space-4);
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-tertiary);
  cursor: pointer;
  z-index: 20;
  transition: color var(--duration-normal) var(--ease-fluid);
}

.login-view__reset:hover {
  color: var(--accent-sakura);
}
</style>
