// 设置服务封装层
//
// 对 bindings/tg-download/services 的 SettingsService / ProxyService / AuthService / ApiService 二次封装，
// 提供应用设置、代理配置、登录状态与 API 服务地址的读写。

import { AppSettings } from '../../bindings/tg-download/internal/settings/models'
import { ProxyConfig, ProxyType, TestResult, LoginStep } from '../../bindings/tg-download/internal/telegram/models'

export { AppSettings, ProxyConfig, ProxyType, TestResult, LoginStep }

// bindings 文件生成后从此路径导入；生成前调用会报错，属预期行为
let mod: any = null

async function load() {
  if (!mod) {
    mod = await import('../../bindings/tg-download/services/index')
  }
  return mod
}

// ---- SettingsService ----

// getSettings 返回当前应用设置，缺失项以默认值填充
export async function getSettings(): Promise<AppSettings> {
  const m = await load()
  return m.SettingsService.GetSettings()
}

// saveSettings 保存应用设置到数据库（UPSERT）
export async function saveSettings(cfg: AppSettings): Promise<void> {
  const m = await load()
  return m.SettingsService.SaveSettings(cfg)
}

// getDownloadDir 仅返回下载目录
export async function getDownloadDir(): Promise<string> {
  const m = await load()
  return m.SettingsService.GetDownloadDir()
}

// setDownloadDir 仅设置下载目录
export async function setDownloadDir(dir: string): Promise<void> {
  const m = await load()
  return m.SettingsService.SetDownloadDir(dir)
}

// ---- ProxyService ----

// getProxy 读取当前代理配置
export async function getProxy(): Promise<ProxyConfig> {
  const m = await load()
  return m.ProxyService.GetProxy()
}

// saveProxy 保存代理配置并触发客户端重连（若客户端已启动）
export async function saveProxy(cfg: ProxyConfig): Promise<void> {
  const m = await load()
  return m.ProxyService.SaveProxy(cfg)
}

// testProxy 测试给定代理配置能否成功连接到 Telegram DC
export async function testProxy(cfg: ProxyConfig): Promise<TestResult> {
  const m = await load()
  return m.ProxyService.TestProxy(cfg)
}

// ---- AuthService ----

// getLoginStatus 返回当前登录状态：[是否已登录, 当前登录步骤]
export async function getLoginStatus(): Promise<[boolean, LoginStep]> {
  const m = await load()
  return m.AuthService.GetLoginStatus()
}

// logout 登出当前账号并清理本地会话
export async function logout(): Promise<void> {
  const m = await load()
  return m.AuthService.Logout()
}

// tgLogin 发起 TG 登录：发送验证码到指定手机号。
// apiID/apiHash 传 0/空串，后端回退到内置默认凭据。
export async function tgLogin(phone: string): Promise<void> {
  const m = await load()
  return m.AuthService.Login(0, '', phone)
}

// submitCode 提交验证码，返回当前登录步骤（logged_in 或 wait_password）
export async function submitCode(code: string): Promise<LoginStep> {
  const m = await load()
  return m.AuthService.SubmitCode(code)
}

// submitPassword 提交 2FA 密码
export async function submitPassword(password: string): Promise<void> {
  const m = await load()
  return m.AuthService.SubmitPassword(password)
}

// resendCode 重新发送验证码
export async function resendCode(): Promise<void> {
  const m = await load()
  return m.AuthService.ResendCode()
}

// restoreSession 启动时尝试恢复已保存的 TG 会话。
// fallbackAPIID/fallbackAPIHash 传 0/空串，后端回退到内置默认凭据。
// 返回 true 表示已登录，false 表示需要走登录流程。
export async function restoreSession(): Promise<boolean> {
  const m = await load()
  return m.AuthService.RestoreSession(0, '')
}

// ---- ApiService（API 服务地址） ----

// getBaseURL 返回当前 API 服务地址
export async function getBaseURL(): Promise<string> {
  const m = await load()
  return m.ApiService.GetBaseURL()
}

// setBaseURL 设置 API 服务地址
export async function setBaseURL(baseURL: string): Promise<void> {
  const m = await load()
  return m.ApiService.SetBaseURL(baseURL)
}

// testAPIConnection 测试 API 服务连通性
export async function testAPIConnection(): Promise<string> {
  const m = await load()
  return m.ApiService.TestAPIConnection()
}
