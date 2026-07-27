// 选集下载弹窗复用逻辑
//
// 封装"根据 vr_id 加载分集列表 → 打开 EpisodeDownloadPicker"的完整状态与流程，
// 供首页/分类页/个人页等使用 VideoCard 的场景复用，避免重复实现。
//
// 用法：
//   const { state: picker, openPicker } = useEpisodeDownloadPicker()
//   function handleDownload(item) { openPicker(item.vr_id, item.vr_name, item.vr_cover) }
//   模板：<EpisodeDownloadPicker v-model:show="picker.show" :video-id="picker.videoId" ... />
import { reactive } from 'vue'

import { listEpisodes, type EpisodeItem } from '../services/repo'
import { useNaiveFeedback } from './useNaiveFeedback'

export interface EpisodeDownloadPickerState {
  show: boolean
  loading: boolean
  videoId: number
  videoName: string
  videoCover: string
  channelId: number
  episodes: EpisodeItem[]
}

export function useEpisodeDownloadPicker() {
  const feedback = useNaiveFeedback()

  const state = reactive<EpisodeDownloadPickerState>({
    show: false,
    loading: false,
    videoId: 0,
    videoName: '',
    videoCover: '',
    channelId: 0,
    episodes: [],
  })

  // 打开弹窗：先重置状态并立刻显示（loading 态），异步加载分集列表后填充
  async function openPicker(
    vrId: number,
    vrName: string,
    vrCover: string,
  ): Promise<void> {
    state.videoId = vrId
    state.videoName = vrName
    state.videoCover = vrCover
    state.episodes = []
    state.channelId = 0
    state.loading = true
    state.show = true
    try {
      const list = await listEpisodes(vrId)
      state.episodes = list || []
      // 同一视频所有分集的 vs_channel_id 一致，取首条即可
      if (state.episodes.length) {
        state.channelId = state.episodes[0].vs_channel_id
      }
    } catch (e) {
      feedback.error(e instanceof Error ? e.message : '加载分集列表失败')
    } finally {
      state.loading = false
    }
  }

  function closePicker(): void {
    state.show = false
  }

  return {
    state,
    openPicker,
    closePicker,
  }
}
