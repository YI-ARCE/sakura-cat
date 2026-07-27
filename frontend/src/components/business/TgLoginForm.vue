<script setup lang="ts">
// TG 账户登录表单组件
// 流程：手机号 -> 验证码 ->（可选）2FA 密码
// 代理设置通过 n-modal 弹窗展示
import { ref, onMounted } from 'vue'
import {
  tgLogin,
  submitCode,
  submitPassword,
  resendCode,
  getProxy,
  saveProxy,
  testProxy,
  ProxyType,
  type ProxyConfig,
  type LoginStep,
} from '../../services/settings'
import { useNaiveFeedback } from '../../composables/useNaiveFeedback'

const emit = defineEmits<{
  success: []
}>()

const feedback = useNaiveFeedback()

// 登录步骤：phone -> code -> password -> done
type Step = 'phone' | 'code' | 'password' | 'done'
const step = ref<Step>('phone')

// 表单字段
const phone = ref('')
const code = ref('')
const password = ref('')

// 状态
const submitting = ref(false)
const errorMsg = ref('')

// 代理设置弹窗
const showProxyModal = ref(false)
const proxyType = ref<ProxyType>(ProxyType.ProxyTypeSOCKS5)
const proxyAddress = ref('')
const proxyPort = ref(1080)
const proxyUsername = ref('')
const proxyPassword = ref('')
const proxyEnabled = ref(false)
const savingProxy = ref(false)
const testingProxy = ref(false)

const proxyTypeOptions = [
  { label: 'SOCKS5', value: ProxyType.ProxyTypeSOCKS5 },
  { label: 'HTTP', value: ProxyType.ProxyTypeHTTP },
  { label: 'HTTPS', value: ProxyType.ProxyTypeHTTPS },
]

function errMsg(e: unknown, fallback: string): string {
  if (e instanceof Error) return e.message || fallback
  return fallback
}

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

// 发送验证码
async function handleSendCode() {
  if (!phone.value.trim()) {
    errorMsg.value = '请输入手机号（含国家区号，如 +8613800138000）'
    return
  }
  submitting.value = true
  errorMsg.value = ''
  try {
    await tgLogin(phone.value.trim())
    step.value = 'code'
  } catch (e) {
    errorMsg.value = errMsg(e, '发送验证码失败，请检查代理设置或网络')
  } finally {
    submitting.value = false
  }
}

// 提交验证码
async function handleSubmitCode() {
  if (!code.value.trim()) {
    errorMsg.value = '请输入验证码'
    return
  }
  submitting.value = true
  errorMsg.value = ''
  try {
    const result = await submitCode(code.value.trim())
    if (result === 'logged_in' as LoginStep) {
      step.value = 'done'
      emit('success')
    } else if (result === 'wait_password' as LoginStep) {
      step.value = 'password'
    } else {
      errorMsg.value = '未知响应，请重试'
    }
  } catch (e) {
    errorMsg.value = errMsg(e, '验证码验证失败')
  } finally {
    submitting.value = false
  }
}

// 提交 2FA 密码
async function handleSubmitPassword() {
  if (!password.value) {
    errorMsg.value = '请输入两步验证密码'
    return
  }
  submitting.value = true
  errorMsg.value = ''
  try {
    await submitPassword(password.value)
    step.value = 'done'
    emit('success')
  } catch (e) {
    errorMsg.value = errMsg(e, '2FA 验证失败')
  } finally {
    submitting.value = false
  }
}

// 重新发送验证码
async function handleResend() {
  submitting.value = true
  errorMsg.value = ''
  try {
    await resendCode()
    feedback.success('验证码已重新发送')
  } catch (e) {
    feedback.error(errMsg(e, '重新发送失败'))
  } finally {
    submitting.value = false
  }
}

// 测试代理
async function handleTestProxy() {
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

// 保存代理
async function handleSaveProxy() {
  savingProxy.value = true
  try {
    await saveProxy(buildProxyConfig())
    feedback.success('代理已保存')
    showProxyModal.value = false
  } catch (e) {
    feedback.error(errMsg(e, '保存失败'))
  } finally {
    savingProxy.value = false
  }
}

onMounted(async () => {
  try {
    const p = await getProxy()
    proxyType.value = p.type || ProxyType.ProxyTypeSOCKS5
    proxyAddress.value = p.address
    proxyPort.value = p.port
    proxyUsername.value = p.username
    proxyPassword.value = p.password
    proxyEnabled.value = p.enabled
  } catch {
    // 代理加载失败不阻塞登录流程
  }
})
</script>

<template>
  <div class="tg-login-form">
    <h2 class="tg-login-form__title">登录 Telegram</h2>
    <p class="tg-login-form__desc">
      请登录您的 Telegram 账户
    </p>

    <!-- 步骤指示 -->
    <div class="step-indicator">
      <span class="step-indicator__item" :class="{ 'step-indicator__item--active': step === 'phone' }">1. 手机号</span>
      <span class="step-indicator__sep">›</span>
      <span class="step-indicator__item" :class="{ 'step-indicator__item--active': step === 'code' }">2. 验证码</span>
      <span class="step-indicator__sep">›</span>
      <span class="step-indicator__item" :class="{ 'step-indicator__item--active': step === 'password' }">3. 两步验证</span>
    </div>

    <!-- 步骤1：手机号 -->
    <div v-if="step === 'phone'" class="step-form">
      <label class="step-form__label">手机号</label>
      <n-input
        v-model:value="phone"
        type="password"
        show-password-on="click"
        placeholder="+8613800138000"
        @keyup.enter="handleSendCode"
      />
      <p class="step-form__hint">需包含国家区号，E.164 格式</p>
    </div>

    <!-- 步骤2：验证码 -->
    <div v-else-if="step === 'code'" class="step-form">
      <label class="step-form__label">验证码</label>
      <n-input
        v-model:value="code"
        placeholder="输入收到的验证码"
        @keyup.enter="handleSubmitCode"
      />
      <n-button
        text
        type="primary"
        :disabled="submitting"
        @click="handleResend"
      >
        重新发送验证码
      </n-button>
    </div>

    <!-- 步骤3：2FA密码 -->
    <div v-else-if="step === 'password'" class="step-form">
      <label class="step-form__label">两步验证密码</label>
      <n-input
        v-model:value="password"
        type="password"
        show-password-on="click"
        placeholder="输入两步验证密码"
        @keyup.enter="handleSubmitPassword"
      />
    </div>

    <!-- 错误提示 -->
    <n-alert
      v-if="errorMsg"
      type="error"
      :show-icon="true"
      style="margin-top: var(--space-3)"
    >
      {{ errorMsg }}
    </n-alert>

    <!-- 操作按钮 -->
    <div class="tg-login-form__actions">
      <n-button
        v-if="step === 'phone'"
        type="primary"
        block
        :loading="submitting"
        @click="handleSendCode"
      >
        发送验证码
      </n-button>
      <n-button
        v-else-if="step === 'code'"
        type="primary"
        block
        :loading="submitting"
        @click="handleSubmitCode"
      >
        提交验证码
      </n-button>
      <n-button
        v-else-if="step === 'password'"
        type="primary"
        block
        :loading="submitting"
        @click="handleSubmitPassword"
      >
        提交密码
      </n-button>
    </div>

    <!-- 代理设置小字链接 -->
    <div class="tg-login-form__proxy-link">
      <n-button text size="small" @click="showProxyModal = true">
        代理设置
      </n-button>
    </div>

    <!-- 代理设置弹窗 -->
    <n-modal
      v-model:show="showProxyModal"
      preset="card"
      title="代理设置"
      style="width: 480px"
    >
      <div class="proxy-body">
        <div class="proxy-row">
          <label class="proxy-label">类型</label>
          <n-select v-model:value="proxyType" :options="proxyTypeOptions" />
        </div>

        <div class="proxy-row">
          <label class="proxy-label">地址</label>
          <n-input v-model:value="proxyAddress" placeholder="127.0.0.1" />
        </div>

        <div class="proxy-row">
          <label class="proxy-label">端口</label>
          <n-input-number
            v-model:value="proxyPort"
            :min="0"
            :max="65535"
            placeholder="1080"
          />
        </div>

        <div class="proxy-row">
          <label class="proxy-label">用户名</label>
          <n-input v-model:value="proxyUsername" />
        </div>

        <div class="proxy-row">
          <label class="proxy-label">密码</label>
          <n-input
            v-model:value="proxyPassword"
            type="password"
            show-password-on="click"
          />
        </div>

        <div class="proxy-row">
          <label class="proxy-label">启用</label>
          <n-switch v-model:value="proxyEnabled" />
        </div>
      </div>

      <template #footer>
        <div class="proxy-actions">
          <n-button :loading="testingProxy" @click="handleTestProxy">
            测试连接
          </n-button>
          <n-button type="primary" :loading="savingProxy" @click="handleSaveProxy">
            保存
          </n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<style scoped>
.tg-login-form {
  width: 100%;
  max-width: 360px;
}

.tg-login-form__title {
  margin: 0 0 var(--space-2);
  font-family: var(--font-display);
  font-size: 22px;
  font-weight: 700;
  color: var(--text-primary);
}

.tg-login-form__desc {
  margin: 0 0 var(--space-5);
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.5;
}

/* 步骤指示 */
.step-indicator {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-5);
  font-family: var(--font-body);
  font-size: 13px;
}

.step-indicator__item {
  color: var(--text-tertiary);
}

.step-indicator__item--active {
  color: var(--accent-sakura);
  font-weight: 600;
}

.step-indicator__sep {
  color: var(--text-tertiary);
}

/* 步骤表单 */
.step-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.step-form__label {
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-secondary);
}

.step-form__hint {
  margin: 0;
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-tertiary);
}

/* 操作区 */
.tg-login-form__actions {
  margin-top: var(--space-5);
}

/* 代理设置链接 */
.tg-login-form__proxy-link {
  margin-top: var(--space-4);
  text-align: center;
}

/* 代理弹窗内容 */
.proxy-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.proxy-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.proxy-label {
  flex-shrink: 0;
  width: 56px;
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-secondary);
}

.proxy-row :deep(.n-input),
.proxy-row :deep(.n-input-number),
.proxy-row :deep(.n-base-selection) {
  flex: 1;
}

.proxy-actions {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: var(--space-3);
}
</style>
