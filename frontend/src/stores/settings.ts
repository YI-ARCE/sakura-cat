import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  getSettings,
  saveSettings,
  getProxy,
  saveProxy,
  type AppSettings,
  type ProxyConfig,
  type ProxyType,
} from '../services/settings'

// 应用设置 store：对接后端 SettingsService 与 ProxyService
// 字段对齐 AppSettings（camelCase）+ ProxyConfig
export const useSettingsStore = defineStore('settings', () => {
  // 应用设置（对齐 AppSettings）
  const downloadDir = ref('')
  const concurrency = ref(0)
  const speedLimit = ref(0)
  const namingTemplate = ref('')
  const theme = ref('dark')
  const autoDownload = ref(false)

  // 代理配置（对齐 ProxyConfig）
  const proxyEnabled = ref(false)
  const proxyHost = ref('')
  const proxyPort = ref(0)
  const proxyType = ref<ProxyType | ''>('')
  const proxyUsername = ref('')
  const proxyPassword = ref('')

  // load 从后端加载应用设置与代理配置
  async function load() {
    const s = await getSettings()
    downloadDir.value = s.download_dir
    concurrency.value = s.concurrency
    speedLimit.value = s.speed_limit
    namingTemplate.value = s.naming_template
    theme.value = s.theme || 'dark'
    autoDownload.value = s.auto_download

    const p = await getProxy()
    proxyEnabled.value = p.enabled
    proxyHost.value = p.address
    proxyPort.value = p.port
    proxyType.value = p.type
    proxyUsername.value = p.username
    proxyPassword.value = p.password
  }

  // save 保存应用设置与代理配置到后端
  async function save() {
    const cfg = {
      theme: theme.value,
      concurrency: concurrency.value,
      speed_limit: speedLimit.value,
      naming_template: namingTemplate.value,
      download_dir: downloadDir.value,
      language: 'zh-CN',
      auto_download: autoDownload.value,
    } as AppSettings
    await saveSettings(cfg)

    const proxy = {
      type: proxyType.value,
      address: proxyHost.value,
      port: proxyPort.value,
      username: proxyUsername.value,
      password: proxyPassword.value,
      enabled: proxyEnabled.value,
    } as ProxyConfig
    await saveProxy(proxy)
  }

  return {
    downloadDir,
    concurrency,
    speedLimit,
    namingTemplate,
    theme,
    autoDownload,
    proxyEnabled,
    proxyHost,
    proxyPort,
    proxyType,
    proxyUsername,
    proxyPassword,
    load,
    save,
  }
})
