import { onUnmounted, ref, watch, type Ref } from 'vue'

// 视频元素引用类型
type VideoEl = HTMLVideoElement | null

// 时间格式化工具：不足 1 小时显示 MM:SS，超过显示 HH:MM:SS；非法值返回 '00:00'
export function formatTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '00:00'
  const total = Math.floor(seconds)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => n.toString().padStart(2, '0')
  if (h > 0) {
    return `${pad(h)}:${pad(m)}:${pad(s)}`
  }
  return `${pad(m)}:${pad(s)}`
}

// useVideoPlayer 封装原生 <video> 元素的响应式状态与控制方法
// 用法：
//   const videoRef = ref<HTMLVideoElement | null>(null)
//   const { currentTime, duration, paused, play, pause } = useVideoPlayer(videoRef)
//   // 模板：<video ref="videoRef" />
//
// 特性：
//   - 通过 ref 接收 video 元素，自动绑定原生事件
//   - onUnmounted 时解绑事件监听
//   - 全屏状态由 fullscreenchange 事件维护
export function useVideoPlayer(videoRef: Ref<VideoEl>) {
  // -- 响应式状态 --
  const currentTime = ref(0)
  const duration = ref(0)
  const paused = ref(true)
  const volume = ref(1)
  const muted = ref(false)
  const playbackRate = ref(1)
  const buffered = ref(0)
  const waiting = ref(false)
  const error = ref<string | null>(null)
  const isFullscreen = ref(false)

  // -- 事件处理 --
  function onTimeUpdate() {
    const v = videoRef.value
    if (!v) return
    currentTime.value = v.currentTime
  }

  function onDurationChange() {
    const v = videoRef.value
    if (!v) return
    duration.value = v.duration
  }

  function onPlay() {
    paused.value = false
  }

  function onPause() {
    paused.value = true
  }

  function onVolumeChange() {
    const v = videoRef.value
    if (!v) return
    volume.value = v.volume
    muted.value = v.muted
  }

  function onRateChange() {
    const v = videoRef.value
    if (!v) return
    playbackRate.value = v.playbackRate
  }

  function onWaiting() {
    waiting.value = true
  }

  function onCanPlay() {
    waiting.value = false
  }

  function onEnded() {
    // 视频播放结束：waiting 复位（ended 不在状态列表，由组件模板 @ended 自行 emit）
    waiting.value = false
  }

  function onProgress() {
    const v = videoRef.value
    if (!v) return
    if (v.buffered.length > 0) {
      buffered.value = v.buffered.end(v.buffered.length - 1)
    }
  }

  function onErrorEvent() {
    const v = videoRef.value
    if (!v) return
    const mediaError = v.error
    let message = '视频加载失败'
    if (mediaError) {
      switch (mediaError.code) {
        case MediaError.MEDIA_ERR_ABORTED:
          message = '视频加载被中断'
          break
        case MediaError.MEDIA_ERR_NETWORK:
          message = '网络错误导致视频加载失败'
          break
        case MediaError.MEDIA_ERR_DECODE:
          message = '视频解码失败'
          break
        case MediaError.MEDIA_ERR_SRC_NOT_SUPPORTED:
          message = '视频格式不受支持'
          break
      }
    }
    error.value = message
  }

  function onLoadedMetadata() {
    const v = videoRef.value
    if (!v) return
    duration.value = v.duration
    volume.value = v.volume
    muted.value = v.muted
    playbackRate.value = v.playbackRate
  }

  function onFullscreenChange() {
    isFullscreen.value = document.fullscreenElement !== null
  }

  // -- 事件绑定 / 解绑 --
  function bindEvents(v: HTMLVideoElement) {
    v.addEventListener('timeupdate', onTimeUpdate)
    v.addEventListener('durationchange', onDurationChange)
    v.addEventListener('play', onPlay)
    v.addEventListener('pause', onPause)
    v.addEventListener('volumechange', onVolumeChange)
    v.addEventListener('ratechange', onRateChange)
    v.addEventListener('waiting', onWaiting)
    v.addEventListener('canplay', onCanPlay)
    v.addEventListener('ended', onEnded)
    v.addEventListener('progress', onProgress)
    v.addEventListener('error', onErrorEvent)
    v.addEventListener('loadedmetadata', onLoadedMetadata)
    document.addEventListener('fullscreenchange', onFullscreenChange)
  }

  function unbindEvents(v: HTMLVideoElement) {
    v.removeEventListener('timeupdate', onTimeUpdate)
    v.removeEventListener('durationchange', onDurationChange)
    v.removeEventListener('play', onPlay)
    v.removeEventListener('pause', onPause)
    v.removeEventListener('volumechange', onVolumeChange)
    v.removeEventListener('ratechange', onRateChange)
    v.removeEventListener('waiting', onWaiting)
    v.removeEventListener('canplay', onCanPlay)
    v.removeEventListener('ended', onEnded)
    v.removeEventListener('progress', onProgress)
    v.removeEventListener('error', onErrorEvent)
    v.removeEventListener('loadedmetadata', onLoadedMetadata)
    document.removeEventListener('fullscreenchange', onFullscreenChange)
  }

  // -- 控制方法 --
  async function play(): Promise<void> {
    const v = videoRef.value
    if (!v) return
    try {
      await v.play()
    } catch (e) {
      // 自动播放策略可能拒绝 play()，捕获后由 paused 状态保持一致
      paused.value = v.paused
    }
  }

  function pause(): void {
    const v = videoRef.value
    if (!v) return
    v.pause()
  }

  function togglePlay(): void {
    const v = videoRef.value
    if (!v) return
    if (v.paused) {
      void play()
    } else {
      pause()
    }
  }

  function seek(time: number): void {
    const v = videoRef.value
    if (!v) return
    const safe = Math.max(0, Math.min(time, v.duration || 0))
    v.currentTime = safe
    currentTime.value = safe
  }

  function setVolume(vol: number): void {
    const v = videoRef.value
    if (!v) return
    const safe = Math.max(0, Math.min(vol, 1))
    v.volume = safe
    if (safe > 0 && v.muted) {
      v.muted = false
    }
    volume.value = safe
  }

  function toggleMute(): void {
    const v = videoRef.value
    if (!v) return
    v.muted = !v.muted
    muted.value = v.muted
  }

  function setPlaybackRate(rate: number): void {
    const v = videoRef.value
    if (!v) return
    v.playbackRate = rate
    playbackRate.value = rate
  }

  async function enterFullscreen(el: HTMLElement): Promise<void> {
    if (!el) return
    try {
      if (el.requestFullscreen) {
        await el.requestFullscreen()
      }
    } catch (e) {
      // 全屏被拒绝时忽略
    }
  }

  async function exitFullscreen(): Promise<void> {
    try {
      if (document.exitFullscreen && document.fullscreenElement) {
        await document.exitFullscreen()
      }
    } catch (e) {
      // 忽略
    }
  }

  async function toggleFullscreen(el: HTMLElement): Promise<void> {
    if (document.fullscreenElement) {
      await exitFullscreen()
    } else {
      await enterFullscreen(el)
    }
  }

  // -- 错误状态重置（src 变化时由组件调用） --
  function resetError() {
    error.value = null
    waiting.value = false
  }

  // 通过 watch 自动在 videoRef 挂载时绑定事件
  let boundEl: HTMLVideoElement | null = null
  const stopWatch = watch(
    videoRef,
    (el, _old, onCleanup) => {
      if (boundEl) {
        unbindEvents(boundEl)
        boundEl = null
      }
      if (el) {
        bindEvents(el)
        boundEl = el
        // 同步初始状态
        currentTime.value = el.currentTime
        duration.value = el.duration
        paused.value = el.paused
        volume.value = el.volume
        muted.value = el.muted
        playbackRate.value = el.playbackRate
      }
      onCleanup(() => {
        if (boundEl) {
          unbindEvents(boundEl)
          boundEl = null
        }
      })
    },
    { immediate: true },
  )

  onUnmounted(() => {
    if (boundEl) {
      unbindEvents(boundEl)
      boundEl = null
    }
    stopWatch()
  })

  return {
    // 状态
    currentTime,
    duration,
    paused,
    volume,
    muted,
    playbackRate,
    buffered,
    waiting,
    error,
    isFullscreen,
    // 控制
    play,
    pause,
    togglePlay,
    seek,
    setVolume,
    toggleMute,
    setPlaybackRate,
    enterFullscreen,
    exitFullscreen,
    toggleFullscreen,
    resetError,
  }
}

export default useVideoPlayer
