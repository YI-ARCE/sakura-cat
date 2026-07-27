// 一起看(Party)服务封装层
//
// 对 bindings/tg-download/services 的 PartyService 与 WindowService.OpenPartyConsoleWindow 二次封装,
// 统一 import 入口与 Wails Event 订阅工具。
// 注:Wails Event 推送由 Go 侧 PartyService.pushEvent 触发,前端通过 Events.On 监听。

import { Events } from '@wailsio/runtime'

// ============ 类型定义 ============

// 一起看分集信息(与 Go 侧 PartyEpisode 对齐)
export interface PartyEpisode {
  ve_id: number
  ve_collect: number
  ve_title: string
  vs_channel_id: number
  ve_message_id: number
}

// StartParty 返回值
export interface PartyStartResult {
  roomId: string
  wsUrl: string
  streamUrl: string
  inviteUrl: string
  lanIp: string
  ve_id: number
  episodeList: PartyEpisode[]
}

// GetPartyState 返回值(供 PartyConsole 初始化)
export interface PartyState {
  roomId: string
  vr_id: number
  current_ve_id: number
  lanIp: string
  wsUrl: string
  streamUrl: string
  inviteUrl: string
  episodeList: PartyEpisode[]
  // 当前在线客户端列表(初始化展示用,后续由 party:clients_changed 事件增量更新)
  clients: PartyClientInfo[]
  // 当前房主名称
  hostName: string
}

// Wails Event: party:download_blocks payload
export interface PartyDownloadBlocksEvent {
  roomId: string
  ve_id: number
  downloaded: number[] // 长度 240,1=已下载,0=未下载
  playing_block: number
  downloading_block: number
  speed: number // 字节/秒
}

// Wails Event: party:playback_state payload
export interface PartyPlaybackStateEvent {
  roomId: string
  ve_id: number
  currentTime: number
  playing: boolean
  rate: number
  duration?: number // 仅 report_duration 时携带
}

// Wails Event: party:clients_changed payload
export interface PartyClientInfo {
  name: string
  isHost: boolean
}
export interface PartyClientsChangedEvent {
  roomId: string
  clients: PartyClientInfo[]
  hostName: string
}

// Wails Event: party:duration_reported payload
export interface PartyDurationReportedEvent {
  roomId: string
  ve_id: number
  duration: number
}

// Wails Event: party:episode_changed payload
export interface PartyEpisodeChangedEvent {
  roomId: string
  ve_id: number
}

// ============ Wails Binding 加载 ============

let PartyServiceBinding: any = null
let WindowServiceBinding: any = null

async function loadPartyBinding() {
  if (!PartyServiceBinding) {
    const mod = await import('../../bindings/tg-download/services/index')
    PartyServiceBinding = mod.PartyService
  }
  return PartyServiceBinding
}

async function loadWindowBinding() {
  if (!WindowServiceBinding) {
    const mod = await import('../../bindings/tg-download/services/index')
    WindowServiceBinding = mod.WindowService
  }
  return WindowServiceBinding
}

// ============ API 封装 ============

export const partyApi = {
  /** 开启一起看,返回 roomId/wsUrl/streamUrl/inviteUrl 等 */
  async start(vrId: number): Promise<PartyStartResult> {
    const svc = await loadPartyBinding()
    return svc.StartParty(vrId)
  },

  /** 结束一起看:广播 host_leaving + 关 servers + 取消下载 + 删缓存目录 */
  async stop(roomId: string): Promise<void> {
    const svc = await loadPartyBinding()
    return svc.StopParty(roomId)
  },

  /** 获取一起看房间状态(供 PartyConsole 初始化) */
  async getState(roomId: string): Promise<PartyState> {
    const svc = await loadPartyBinding()
    return svc.GetPartyState(roomId)
  },

  /** 查询视频的分集列表 */
  async getEpisodeList(vrId: number): Promise<PartyEpisode[]> {
    const svc = await loadPartyBinding()
    return svc.GetEpisodeList(vrId)
  },

  /** 切换一起看房间的当前分集(由 WS handler 调用,前端一般不直接调) */
  async switchEpisode(roomId: string, veId: number): Promise<string> {
    const svc = await loadPartyBinding()
    return svc.SwitchEpisode(roomId, veId)
  },
}

export const partyWindowApi = {
  /** 打开 PartyConsole 窗口(800×600,带顶栏,可调整大小) */
  async openConsole(roomId: string): Promise<{ alreadyOpen: boolean }> {
    const svc = await loadWindowBinding()
    return svc.OpenPartyConsoleWindow(roomId)
  },

  /** 关闭 PartyConsole 窗口(按 URL 匹配单例) */
  async closeConsole(roomId: string): Promise<void> {
    const svc = await loadWindowBinding()
    return svc.CloseWindow(`/party-console/${roomId}`)
  },
}

// ============ Wails Event 订阅工具 ============

// 每个订阅返回取消订阅函数,便于 onUnmounted 注销

export function onPartyDownloadBlocks(callback: (data: PartyDownloadBlocksEvent) => void): () => void {
  return Events.On('party:download_blocks', (ev: { data: any }) => callback(ev.data))
}

export function onPartyPlaybackState(callback: (data: PartyPlaybackStateEvent) => void): () => void {
  return Events.On('party:playback_state', (ev: { data: any }) => callback(ev.data))
}

export function onPartyClientsChanged(callback: (data: PartyClientsChangedEvent) => void): () => void {
  return Events.On('party:clients_changed', (ev: { data: any }) => callback(ev.data))
}

export function onPartyEpisodeChanged(callback: (data: PartyEpisodeChangedEvent) => void): () => void {
  return Events.On('party:episode_changed', (ev: { data: any }) => callback(ev.data))
}

export function onPartyDurationReported(callback: (data: PartyDurationReportedEvent) => void): () => void {
  return Events.On('party:duration_reported', (ev: { data: any }) => callback(ev.data))
}
