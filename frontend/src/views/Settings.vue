<script setup lang="ts">
// 设置页：下载 / 代理 / 账户 三组分组式表单
// 各分组独立加载与保存，复用 Yoru Sakura 设计 token
import { ref, onMounted } from 'vue'
import { Spinner, StatusHint } from '../components/common/Ui'
import { useNaiveFeedback } from '../composables/useNaiveFeedback'
import { useAuthStore } from '../stores/auth'
import { windowApi } from '../services/window'
import {
  getSettings,
  saveSettings,
  getProxy,
  saveProxy,
  testProxy,
  getLoginStatus,
  logout,
  ProxyType,
  type AppSettings,
  type ProxyConfig,
  type LoginStep,
} from '../services/settings'
import { syncSourceList } from '../services/repo'

// ---- 初始加载 ----
const loading = ref(true)

// ---- 下载设置 ----
const downloadDir = ref('')
const concurrency = ref(3)
const speedLimitMB = ref(0)
const namingTemplate = ref('')
const settingsError = ref('')
const savingSettings = ref(false)

// ---- 视频源清单 ----
const sourceListURL = ref('')
const syncingSourceList = ref(false)

// ---- 代理设置 ----
const proxyType = ref<ProxyType>(ProxyType.ProxyTypeSOCKS5)
const proxyAddress = ref('')
const proxyPort = ref(0)
const proxyUsername = ref('')
const proxyPassword = ref('')
const proxyEnabled = ref(false)
const proxyError = ref('')
const savingProxy = ref(false)
const testingProxy = ref(false)

const proxyTypeOptions = [
  { label: 'SOCKS5', value: ProxyType.ProxyTypeSOCKS5 },
  { label: 'HTTP', value: ProxyType.ProxyTypeHTTP },
  { label: 'HTTPS', value: ProxyType.ProxyTypeHTTPS },
]

// ---- 账户 ----
const isLoggedIn = ref(false)
const loginStep = ref<LoginStep>('' as LoginStep)
const accountError = ref('')
const loggingOut = ref(false)

// MB/s ↔ bytes/s 转换
const BYTES_PER_MB = 1024 * 1024

const feedback = useNaiveFeedback()
const authStore = useAuthStore()

// 错误信息提取
function errMsg(e: unknown, fallback: string): string {
  if (e instanceof Error) return e.message || fallback
  return fallback
}

// 构建 AppSettings 对象
function buildAppSettings(): AppSettings {
  return {
    theme: 'dark',
    concurrency: concurrency.value,
    speed_limit: Math.round(speedLimitMB.value * BYTES_PER_MB),
    naming_template: namingTemplate.value,
    download_dir: downloadDir.value,
    language: 'zh-CN',
    auto_download: false,
    source_list_url: sourceListURL.value,
  } as AppSettings
}

// 构建 ProxyConfig 对象
function buildProxyConfig(): ProxyConfig {
  return {
    type: proxyType.value || ProxyType.ProxyTypeSOCKS5,
    address: proxyAddress.value,
    port: proxyPort.value,
    username: proxyUsername.value,
    password: proxyPassword.value,
    enabled: proxyEnabled.value,
  } as ProxyConfig
}

// 保存下载设置
async function saveDownloadSettings() {
  savingSettings.value = true
  try {
    await saveSettings(buildAppSettings())
    feedback.success('保存成功')
  } catch (e) {
    feedback.error(errMsg(e, '保存失败'))
  } finally {
    savingSettings.value = false
  }
}

// 手动同步视频源清单
async function doSyncSourceList() {
  if (!sourceListURL.value.trim()) {
    feedback.error('请先填写清单 URL 并保存')
    return
  }
  syncingSourceList.value = true
  try {
    const r = await syncSourceList()
    if (r.total === 0) {
      feedback.info('清单为空或未配置 URL')
    } else {
      feedback.success(`同步完成：共 ${r.total} 条，写入 ${r.upserted}，新订阅 ${r.subscribed}${r.failed > 0 ? `，失败 ${r.failed}` : ''}`)
    }
  } catch (e) {
    feedback.error(errMsg(e, '同步失败'))
  } finally {
    syncingSourceList.value = false
  }
}

// 测试代理连接
async function testProxyConnection() {
  testingProxy.value = true
  try {
    const result = await testProxy(buildProxyConfig())
    if (result.success) {
      feedback.success(`连接成功 ${result.latency_ms}ms`)
    } else {
      feedback.error(result.message || '连接失败')
    }
  } catch (e) {
    feedback.error(errMsg(e, '测试失败'))
  } finally {
    testingProxy.value = false
  }
}

// 保存代理配置
async function saveProxyConfig() {
  savingProxy.value = true
  try {
    await saveProxy(buildProxyConfig())
    feedback.success('保存成功')
  } catch (e) {
    feedback.error(errMsg(e, '保存失败'))
  } finally {
    savingProxy.value = false
  }
}

// 退出登录（二次确认）
// 清除 TG 会话 + 微信 token + 本地缓存，然后开登录窗、关主窗（不退出应用）
// 注意：串行执行避免 sqlite 锁竞争
async function doLogout() {
  const ok = await feedback.confirm({ title: '确定退出登录？', danger: true })
  if (!ok) return
  loggingOut.value = true
  try {
    try { await logout() } catch (e) { console.error('[Settings] 清除 TG 会话失败:', e) }
    authStore.reset()
    isLoggedIn.value = false
    loginStep.value = '' as LoginStep
    feedback.success('已退出登录')
    await windowApi.openLoginWindow()
  } catch (e) {
    feedback.error(errMsg(e, '退出失败'))
  } finally {
    loggingOut.value = false
  }
}

onMounted(async () => {
  loading.value = true
  const results = await Promise.allSettled([
    getSettings(),
    getProxy(),
    getLoginStatus(),
  ])

  const r0 = results[0]
  if (r0.status === 'fulfilled') {
    const s = r0.value
    downloadDir.value = s.download_dir
    concurrency.value = s.concurrency
    speedLimitMB.value = s.speed_limit / BYTES_PER_MB
    namingTemplate.value = s.naming_template
    sourceListURL.value = s.source_list_url || ''
  } else {
    settingsError.value = errMsg(r0.reason, '加载设置失败')
  }

  const r1 = results[1]
  if (r1.status === 'fulfilled') {
    const p = r1.value
    proxyType.value = p.type || ProxyType.ProxyTypeSOCKS5
    proxyAddress.value = p.address
    proxyPort.value = p.port
    proxyUsername.value = p.username
    proxyPassword.value = p.password
    proxyEnabled.value = p.enabled
  } else {
    proxyError.value = errMsg(r1.reason, '加载代理失败')
  }

  const r2 = results[2]
  if (r2.status === 'fulfilled') {
    isLoggedIn.value = r2.value[0]
    loginStep.value = r2.value[1]
  } else {
    accountError.value = errMsg(r2.reason, '加载登录状态失败')
  }

  loading.value = false
})
</script>

<template>
  <div class="settings">
    <div class="settings__content">
      <h1 class="settings__title">设置</h1>

      <!-- 下载分组 -->
      <section class="settings__group">
        <h2 class="settings__group-title">下载</h2>

        <div v-if="loading" class="settings__loading">
          <Spinner :size="24" />
        </div>
        <div v-else-if="settingsError" class="settings__state">
          <StatusHint variant="error" :title="settingsError" />
        </div>
        <template v-else>
          <div class="settings__row">
            <label class="settings__label" for="downloadDir">下载目录</label>
            <div class="settings__input-group">
              <n-input
                id="downloadDir"
                v-model:value="downloadDir"
                placeholder="选择下载目录"
              />
              <n-button>选择</n-button>
            </div>
          </div>

          <div class="settings__row">
            <label class="settings__label" for="concurrency">并发数</label>
            <div class="settings__range-group">
              <n-slider
                id="concurrency"
                v-model:value="concurrency"
                :min="1"
                :max="10"
                :step="1"
              />
              <span class="settings__range-value">{{ concurrency }}</span>
            </div>
          </div>

          <div class="settings__row">
            <label class="settings__label" for="speedLimit">限速</label>
            <div class="settings__input-group">
              <n-input-number
                id="speedLimit"
                v-model:value="speedLimitMB"
                :min="0"
                placeholder="0"
              />
              <span class="settings__unit">MB/s（0=不限）</span>
            </div>
          </div>

          <div class="settings__row">
            <label class="settings__label" for="namingTemplate">命名模板</label>
            <n-input
              id="namingTemplate"
              v-model:value="namingTemplate"
              placeholder="{date}_{filename}"
            />
          </div>

          <div class="settings__actions">
            <n-button
              type="primary"
              :loading="savingSettings"
              @click="saveDownloadSettings"
            >
              保存
            </n-button>
          </div>
        </template>
      </section>

      <!-- 代理分组 -->
      <section class="settings__group">
        <h2 class="settings__group-title">代理</h2>

        <div v-if="loading" class="settings__loading">
          <Spinner :size="24" />
        </div>
        <div v-else-if="proxyError" class="settings__state">
          <StatusHint variant="error" :title="proxyError" />
        </div>
        <template v-else>
          <div class="settings__row">
            <label class="settings__label" for="proxyType">类型</label>
            <n-select
              id="proxyType"
              v-model:value="proxyType"
              :options="proxyTypeOptions"
            />
          </div>

          <div class="settings__row">
            <label class="settings__label" for="proxyAddress">地址</label>
            <n-input
              id="proxyAddress"
              v-model:value="proxyAddress"
              placeholder="127.0.0.1"
            />
          </div>

          <div class="settings__row">
            <label class="settings__label" for="proxyPort">端口</label>
            <n-input-number
              id="proxyPort"
              v-model:value="proxyPort"
              :min="0"
              :max="65535"
              placeholder="1080"
            />
          </div>

          <div class="settings__row">
            <label class="settings__label" for="proxyUsername">用户名</label>
            <n-input
              id="proxyUsername"
              v-model:value="proxyUsername"
            />
          </div>

          <div class="settings__row">
            <label class="settings__label" for="proxyPassword">密码</label>
            <n-input
              id="proxyPassword"
              v-model:value="proxyPassword"
              type="password"
              show-password-on="click"
            />
          </div>

          <div class="settings__row">
            <label class="settings__label">启用</label>
            <n-switch v-model:value="proxyEnabled" />
          </div>

          <div class="settings__actions">
            <n-button
              :loading="testingProxy"
              @click="testProxyConnection"
            >
              测试连接
            </n-button>
            <n-button
              type="primary"
              :loading="savingProxy"
              @click="saveProxyConfig"
            >
              保存
            </n-button>
          </div>
        </template>
      </section>

      <!-- 视频源清单分组 -->
      <section class="settings__group">
        <h2 class="settings__group-title">视频源清单</h2>

        <div v-if="loading" class="settings__loading">
          <Spinner :size="24" />
        </div>
        <div v-else-if="settingsError" class="settings__state">
          <StatusHint variant="error" :title="settingsError" />
        </div>
        <template v-else>
          <div class="settings__row">
            <label class="settings__label settings__label--auto" for="sourceListURL">清单 URL</label>
            <n-input
              id="sourceListURL"
              v-model:value="sourceListURL"
              placeholder="https://raw.githubusercontent.com/{owner}/{repo}/{branch}/sources.json"
            />
          </div>

          <div class="settings__actions">
            <n-button
              :loading="syncingSourceList"
              @click="doSyncSourceList"
            >
              立即同步
            </n-button>
            <n-button
              type="primary"
              :loading="savingSettings"
              @click="saveDownloadSettings"
            >
              保存
            </n-button>
          </div>
        </template>
      </section>

      <!-- 账户分组 -->
      <section class="settings__group">
        <h2 class="settings__group-title">账户</h2>

        <div v-if="loading" class="settings__loading">
          <Spinner :size="24" />
        </div>
        <div v-else-if="accountError" class="settings__state">
          <StatusHint variant="error" :title="accountError" />
        </div>
        <template v-else>
          <div class="settings__row">
            <label class="settings__label">登录状态</label>
            <div class="settings__account">
              <template v-if="isLoggedIn">
                <span class="settings__badge settings__badge--success">已登录</span>
                <n-button
                  :loading="loggingOut"
                  @click="doLogout"
                >
                  退出登录
                </n-button>
              </template>
              <span v-else class="settings__account-hint">未登录，请前往登录窗</span>
            </div>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>

<style scoped>
/* ===================== 容器 ===================== */
.settings {
  min-height: 100%;
  background: var(--bg-base);
}

.settings__content {
  max-width: 720px;
  margin: 0 auto;
  padding: var(--space-6);
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
}

.settings__title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 24px;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: 0.02em;
}

/* ===================== 分组卡片 ===================== */
.settings__group {
  background: var(--bg-card-solid);
  border-radius: var(--radius-lg);
  padding: var(--space-5);
}

.settings__group-title {
  margin: 0 0 var(--space-4);
  font-family: var(--font-body);
  font-size: 16px;
  font-weight: 600;
  color: var(--text-primary);
}

.settings__loading,
.settings__state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4) 0;
}

/* ===================== 表单行 ===================== */
.settings__row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.settings__row:last-of-type {
  margin-bottom: 0;
}

.settings__label {
  flex-shrink: 0;
  width: 120px;
  font-family: var(--font-body);
  font-size: 14px;
  color: var(--text-secondary);
}

/* 清单 URL 的 label 不固定宽度，按内容自适应 */
.settings__label--auto {
  width: auto;
}

/* input + 按钮组合 */
.settings__input-group {
  flex: 1;
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.settings__unit {
  flex-shrink: 0;
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-tertiary);
  white-space: nowrap;
}

/* range 滑块组 */
.settings__range-group {
  flex: 1;
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.settings__range-value {
  flex-shrink: 0;
  min-width: 24px;
  text-align: center;
  font-family: var(--font-mono);
  font-size: 14px;
  color: var(--text-primary);
}

/* Naive UI 表单控件在行内撑满剩余空间 */
.settings__row :deep(.n-input),
.settings__row :deep(.n-input-number),
.settings__row :deep(.n-select),
.settings__row :deep(.n-slider) {
  flex: 1;
  min-width: 0;
}

/* ===================== 操作区 ===================== */
.settings__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

/* ===================== 账户 ===================== */
.settings__account {
  flex: 1;
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.settings__badge {
  display: inline-flex;
  align-items: center;
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
  font-family: var(--font-body);
  font-size: 13px;
  font-weight: 600;
}

.settings__badge--success {
  background: color-mix(in srgb, var(--color-success) 16%, transparent);
  color: var(--color-success);
}

.settings__account-hint {
  font-family: var(--font-body);
  font-size: 14px;
  color: var(--text-secondary);
}
</style>
