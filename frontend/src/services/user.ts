// 用户信息类型定义
//
// 本地模式下无服务器用户体系，UserInfo 仅作类型保留，
// 供 stores/auth.ts 的缓存结构使用。

// UserInfo 用户信息
export interface UserInfo {
  username: string // 用户名
  nickname: string // 昵称
  avatar: string // 头像相对路径（前端拼 CDN 前缀）
  first: number // 首次登录标识
  status: string // 账号状态文案（如 "无聊"）
  ip_origin: string // IP 归属
  online_day: number // 在线天数
  city: string // 城市
  vu_level?: number // 用户等级
  vu_level_next?: number // 距离下一级所需天数
  vu_look_count?: number // 观看次数
  total_hour?: number // 累计观看小时
}
