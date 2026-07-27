// 视频服务封装层
//
// 对 bindings/tg-download/services 的 VideoService 二次封装，
// 提供视频播放元数据查询。
// 注：videoservice.js 仅具名导出函数，
// VideoService 命名空间由 services/index.js 重新导出，故从此处加载。

// VideoPlayInfo 视频播放元数据
export interface VideoPlayInfo {
  recordId: number
  taskId: number
  title: string
  episodeNumber: number | null
  episodeRaw: string
  fileName: string
  streamUrl: string
  prevRecordId: number | null
  nextRecordId: number | null
  episodeList: EpisodeListItem[]
}

// EpisodeListItem 集数列表项
export interface EpisodeListItem {
  recordId: number
  episodeNumber: number | null
  episodeRaw: string
  fileName: string
  isCurrent: boolean
}

// bindings 文件生成后从此路径导入；生成前调用会报错，属预期行为
// 注：VideoService 绑定由后端生成（videoservice.ts 尚未生成），
// 故此处将 mod 标注为 any 以通过类型检查，绑定生成后无需调整即可工作。
let VideoServiceBinding: any = null

async function loadBinding() {
  if (!VideoServiceBinding) {
    const mod: any = await import('../../bindings/tg-download/services/index')
    VideoServiceBinding = mod.VideoService
  }
  return VideoServiceBinding
}

// getVideoPlayInfo 获取视频播放元数据
export async function getVideoPlayInfo(recordId: number): Promise<VideoPlayInfo> {
  const svc = await loadBinding()
  return svc.GetVideoPlayInfo(recordId)
}

// getEpisodeStream 根据分集的 TG 频道与消息 ID 返回可播放的流地址
export async function getEpisodeStream(channelId: number, messageId: number): Promise<string> {
  const svc = await loadBinding()
  return svc.GetEpisodeStream(channelId, messageId)
}
