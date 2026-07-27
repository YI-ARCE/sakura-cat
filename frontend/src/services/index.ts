// services 层：对 bindings/tg-download/services 的二次封装
//
// 约定：
// - 每个业务域一个文件（auth.ts、settings.ts、download.ts 等）
// - 统一处理错误、loading 状态、事件订阅
// - views/components 不直接 import bindings，统一经此层调用
//
// 示例：
// import { AuthService } from '../../bindings/tg-download/services/authservice.js'
// export async function getLoginStatus(): Promise<boolean> {
//   const [authed] = await AuthService.GetLoginStatus()
//   return !!authed
// }

export {}
