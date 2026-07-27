<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  PlayerPlay,
  PlayerPause,
  PlayerSkipBack,
  PlayerSkipForward,
  Volume,
  Volume3,
  Maximize,
  Minimize,
  AlertCircle,
  MessageCircle,
  MessageCircleOff,
  MessageDots,
  Send,
  X,
  Settings,
} from '@vicons/tabler'
// @ts-ignore - danmaku 库的 TypeScript 类型声明可能不完整
import Danmaku from 'danmaku'
import { Spinner } from '../common/Ui'
import { useVideoPlayer, formatTime } from '../../composables/useVideoPlayer'
import type { DanmakuItem } from '../../services/repo'

interface Props {
  /** 视频源 URL（必填） */
  src: string
  /** 封面图 URL */
  poster?: string
  /** 是否自动播放 */
  autoPlay?: boolean
  /** 是否循环播放 */
  loop?: boolean
  /** 视频标题（顶部标题区） */
  title?: string
  /** 是否存在上一集（控制上一集按钮可用态） */
  hasPrev?: boolean
  /** 是否存在下一集（控制下一集按钮可用态） */
  hasNext?: boolean
  /** 弹幕数据数组（来自父组件） */
  danmaku?: DanmakuItem[]
  /** 弹幕开关（v-model 双向绑定） */
  danmakuEnabled?: boolean
  /** 续播起始时间（秒），>0 时在就绪后 seek 并显示续播提醒 */
  resumeTime?: number
}

const props = withDefaults(defineProps<Props>(), {
  poster: '',
  autoPlay: false,
  loop: false,
  title: '',
  hasPrev: false,
  hasNext: false,
  danmaku: () => [],
  danmakuEnabled: true,
  resumeTime: 0,
})

const emit = defineEmits<{
  (e: 'ready'): void
  (e: 'play'): void
  (e: 'pause'): void
  (e: 'ended'): void
  (e: 'error', message: string): void
  (e: 'timeupdate', currentTime: number): void
  (e: 'ratechange', playbackRate: number): void
  (e: 'prev'): void
  (e: 'next'): void
  /** 用户发送弹幕时触发，父组件负责调接口 */
  (
    e: 'sendDanmaku',
    content: string,
    time: number,
    mode: number,
    color: string,
  ): void
  /** 弹幕开关变化（v-model:danmakuEnabled） */
  (e: 'update:danmakuEnabled', value: boolean): void
}>()

// 倍速档位
const RATES = [1, 1.25, 1.5, 2] as const

// -- 弹幕常量 --
// vd_mode 到 danmaku 库 mode 的映射：1=滚动(rtl), 2=顶部(top), 3=底部(bottom)
const DANMAKU_MODE_MAP: Record<number, 'rtl' | 'top' | 'bottom'> = {
  1: 'rtl',
  2: 'top',
  3: 'bottom',
}
// 弹幕模式标签（用于输入区按钮）
const DANMAKU_MODE_OPTIONS = [
  { value: 1, label: '滚动' },
  { value: 2, label: '顶部' },
  { value: 3, label: '底部' },
] as const
// 预设颜色
const DANMAKU_COLORS = [
  '#FFFFFF',
  '#FF6B6B',
  '#FFD93D',
  '#6BCB77',
  '#4D96FF',
  '#FF6B9D',
] as const
// 设置项 localStorage key 前缀
const LS_PREFIX = 'vp-danmaku-'

// -- DOM 引用 --
const containerRef = ref<HTMLElement | null>(null)
const videoRef = ref<HTMLVideoElement | null>(null)
const progressRef = ref<HTMLElement | null>(null)
const volumeRef = ref<HTMLElement | null>(null)
const danmakuContainerRef = ref<HTMLElement | null>(null)
const danmakuInputRef = ref<HTMLInputElement | null>(null)

// -- composable --
const {
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
  play,
  pause,
  togglePlay,
  seek,
  setVolume,
  toggleMute,
  setPlaybackRate,
  toggleFullscreen,
  resetError,
} = useVideoPlayer(videoRef)

// -- UI 状态 --
const controlsVisible = ref(true)
const isProgressHover = ref(false)
const isDragging = ref(false)
const dragTime = ref(0)
// 进度条悬停预览：hoverX 为鼠标在进度条的水平比例（0-1），hoverTime 为对应时间（秒）
const hoverX = ref(0)
const showHoverTime = ref(false)
const showRateMenu = ref(false)
const volumeHovered = ref(false)
const isDraggingVolume = ref(false)
// 续播提醒是否已淡出（初始隐藏，开播时显示，2秒后淡出）
const resumeHintShown = ref(true)
// 待执行的续播 seek 时间（loadedmetadata 记录，首次 play 时消费）
const pendingResumeSeek = ref(0)
// 续播提醒淡出定时器
let resumeHintTimer: ReturnType<typeof setTimeout> | null = null
// 是否已完成首次播放尝试（区分 autoplay 初始加载期）
// 非 autoplay：初始 true，立即显示中心播放按钮
// autoplay：初始 false，显式调用 play() 后——成功则 play 事件置 true，被阻止则 catch 置 true
const started = ref(!props.autoPlay)

// -- 弹幕 UI 状态 --
// 弹幕输入框是否展开
const showDanmakuInput = ref(false)
// 弹幕输入文字
const danmakuInputText = ref('')
// 当前选择的弹幕模式（1=滚动, 2=顶部, 3=底部）
const danmakuInputMode = ref(1)
// 当前选择的弹幕颜色
const danmakuInputColor = ref('#FFFFFF')
// 设置面板是否展开
const showSettingsPanel = ref(false)

// -- 弹幕设置项（localStorage 持久化） --
function loadSetting<T>(key: string, defaultVal: T): T {
  try {
    const raw = localStorage.getItem(LS_PREFIX + key)
    if (raw === null) return defaultVal
    return JSON.parse(raw) as T
  } catch {
    return defaultVal
  }
}

function saveSetting(key: string, val: unknown): void {
  try {
    localStorage.setItem(LS_PREFIX + key, JSON.stringify(val))
  } catch {
    // localStorage 不可用时静默忽略
  }
}

// 字号 12-36px，默认 20
const danmakuFontSize = ref(loadSetting<number>('font-size', 20))
// 透明度 0.3-1，默认 1
const danmakuOpacity = ref(loadSetting<number>('opacity', 1))
// 显示区域占比 0.1-1，默认 0.3
const danmakuDisplayArea = ref(loadSetting<number>('display-area', 0.3))
// 滚动速度 1-10，默认 5（映射到 danmaku 库的 speed 属性）
const danmakuSpeed = ref(loadSetting<number>('speed', 5))

// 弹幕实例
let danmakuInst: Danmaku | null = null

let hideTimer: ReturnType<typeof setTimeout> | null = null
let clickTimer: ReturnType<typeof setTimeout> | null = null
let temporaryShowTimer: ReturnType<typeof setTimeout> | null = null

// -- 计算属性 --
const playedPercent = computed(() => {
  const dur = duration.value
  if (!dur || !Number.isFinite(dur)) return 0
  const t = isDragging.value ? dragTime.value : currentTime.value
  return Math.min(100, Math.max(0, (t / dur) * 100))
})

const bufferedPercent = computed(() => {
  const dur = duration.value
  if (!dur || !Number.isFinite(dur)) return 0
  return Math.min(100, Math.max(0, (buffered.value / dur) * 100))
})

const volumePercent = computed(() => {
  const v = muted.value ? 0 : volume.value
  return Math.min(100, Math.max(0, v * 100))
})

const currentTimeText = computed(() => {
  const t = isDragging.value ? dragTime.value : currentTime.value
  return formatTime(t)
})

const durationText = computed(() => formatTime(duration.value))

const rateLabel = computed(() => `${playbackRate.value}x`)

// 鼠标悬停在进度条时预览的时间（基于 hoverX 比例计算）
const hoverTime = computed(() => {
  const dur = duration.value
  if (!dur || !Number.isFinite(dur)) return 0
  return Math.min(dur, Math.max(0, hoverX.value * dur))
})

// 悬停时间提示的水平位置（百分比，限制在 8%-92% 避免溢出两端）
const hoverTimeLeft = computed(() => {
  const pct = hoverX.value * 100
  return Math.min(92, Math.max(8, pct))
})

// 是否显示中心播放按钮（已启动 + 暂停 + 非错误 + 非缓冲）
// started=false 期间（autoplay 初始加载）不显示，避免加载过程中闪现播放按钮
const showCenterBtn = computed(
  () => started.value && paused.value && !error.value && !waiting.value,
)

// 弹幕容器内联样式（高度、透明度、字号）
const danmakuContainerStyle = computed(() => ({
  height: danmakuDisplayArea.value * 100 + '%',
  opacity: danmakuOpacity.value,
  fontSize: danmakuFontSize.value + 'px',
}))

// -- 控件栏自动隐藏 --
function clearHideTimer() {
  if (hideTimer) {
    clearTimeout(hideTimer)
    hideTimer = null
  }
}

function scheduleHide() {
  clearHideTimer()
  hideTimer = setTimeout(() => {
    // 仅在播放中且非交互态时隐藏
    // 弹幕输入框/设置面板展开时也不自动隐藏
    if (
      !paused.value &&
      !isDragging.value &&
      !isDraggingVolume.value &&
      !showRateMenu.value &&
      !showDanmakuInput.value &&
      !showSettingsPanel.value
    ) {
      controlsVisible.value = false
    }
  }, 3000)
}

function showControls() {
  controlsVisible.value = true
  clearHideTimer()
}

// 快进/快退等操作后临时显示 2 秒
function showControlsTemporary(ms = 2000) {
  showControls()
  if (temporaryShowTimer) clearTimeout(temporaryShowTimer)
  temporaryShowTimer = setTimeout(() => {
    if (!paused.value) {
      scheduleHide()
    }
  }, ms)
}

function handleMouseMove() {
  showControls()
  if (!paused.value) {
    scheduleHide()
  }
}

// -- 视频画面单击/双击 --
// 用 setTimeout 延迟 click 判断是否为双击
function handleVideoClick() {
  if (clickTimer) return
  clickTimer = setTimeout(() => {
    clickTimer = null
    togglePlay()
  }, 200)
}

function handleVideoDblClick() {
  if (clickTimer) {
    clearTimeout(clickTimer)
    clickTimer = null
  }
  if (containerRef.value) {
    void toggleFullscreen(containerRef.value)
  }
}

// -- 进度条交互 --
function getProgressRatio(clientX: number): number {
  const el = progressRef.value
  if (!el) return 0
  const rect = el.getBoundingClientRect()
  if (rect.width === 0) return 0
  return Math.min(1, Math.max(0, (clientX - rect.left) / rect.width))
}

function handleProgressMouseDown(e: MouseEvent) {
  if (!duration.value || !Number.isFinite(duration.value)) return
  e.preventDefault()
  const ratio = getProgressRatio(e.clientX)
  dragTime.value = ratio * duration.value
  hoverX.value = ratio
  isDragging.value = true
  showHoverTime.value = true
  showControls()
  window.addEventListener('mousemove', handleProgressMouseMove)
  window.addEventListener('mouseup', handleProgressMouseUp)
}

function handleProgressMouseMove(e: MouseEvent) {
  if (!duration.value) return
  const ratio = getProgressRatio(e.clientX)
  dragTime.value = ratio * duration.value
  hoverX.value = ratio
}

// 进度条悬停移动：更新预览时间和位置
function handleProgressHoverMove(e: MouseEvent) {
  if (!duration.value || !Number.isFinite(duration.value)) return
  hoverX.value = getProgressRatio(e.clientX)
}

// 进度条进入：显示时间预览
function onProgressEnter() {
  isProgressHover.value = true
  if (duration.value && Number.isFinite(duration.value)) {
    showHoverTime.value = true
  }
}

// 进度条离开：隐藏时间预览（拖拽中由 mouseup 处理）
function onProgressLeave() {
  isProgressHover.value = false
  if (!isDragging.value) {
    showHoverTime.value = false
  }
}

function handleProgressMouseUp() {
  if (isDragging.value) {
    seek(dragTime.value)
    isDragging.value = false
  }
  // 松开后若鼠标已离开进度条则隐藏 tooltip
  if (!isProgressHover.value) {
    showHoverTime.value = false
  }
  window.removeEventListener('mousemove', handleProgressMouseMove)
  window.removeEventListener('mouseup', handleProgressMouseUp)
}

// -- 音量滑块交互 --
function getVolumeRatio(clientX: number): number {
  const el = volumeRef.value
  if (!el) return 0
  const rect = el.getBoundingClientRect()
  if (rect.width === 0) return 0
  return Math.min(1, Math.max(0, (clientX - rect.left) / rect.width))
}

function handleVolumeMouseDown(e: MouseEvent) {
  e.preventDefault()
  isDraggingVolume.value = true
  const ratio = getVolumeRatio(e.clientX)
  setVolume(ratio)
  window.addEventListener('mousemove', handleVolumeMouseMove)
  window.addEventListener('mouseup', handleVolumeMouseUp)
}

function handleVolumeMouseMove(e: MouseEvent) {
  const ratio = getVolumeRatio(e.clientX)
  setVolume(ratio)
}

function handleVolumeMouseUp() {
  isDraggingVolume.value = false
  window.removeEventListener('mousemove', handleVolumeMouseMove)
  window.removeEventListener('mouseup', handleVolumeMouseUp)
}

// -- 倍速菜单 --
function toggleRateMenu() {
  showRateMenu.value = !showRateMenu.value
  if (showRateMenu.value) {
    showControls()
    clearHideTimer()
  }
}

function changeRate(rate: number) {
  setPlaybackRate(rate)
  showRateMenu.value = false
}

function cycleRateUp() {
  const cur = playbackRate.value
  const idx = RATES.findIndex((r) => r === cur)
  if (idx < 0 || idx >= RATES.length - 1) {
    setPlaybackRate(RATES[RATES.length - 1])
  } else {
    setPlaybackRate(RATES[idx + 1])
  }
}

function cycleRateDown() {
  const cur = playbackRate.value
  const idx = RATES.findIndex((r) => r === cur)
  if (idx <= 0) {
    setPlaybackRate(RATES[0])
  } else {
    setPlaybackRate(RATES[idx - 1])
  }
}

// -- 全屏 --
function handleFullscreenToggle() {
  if (containerRef.value) {
    void toggleFullscreen(containerRef.value)
  }
}

// -- 键盘快捷键 --
function handleKeyDown(e: KeyboardEvent) {
  // 弹幕输入框聚焦时不触发视频快捷键
  const target = e.target as HTMLElement | null
  if (
    target &&
    (target.tagName === 'INPUT' ||
      target.tagName === 'TEXTAREA' ||
      target.isContentEditable)
  ) {
    return
  }
  switch (e.key) {
    case ' ':
    case 'Spacebar':
      e.preventDefault()
      togglePlay()
      break
    case 'ArrowRight':
      e.preventDefault()
      seek(Math.min(currentTime.value + 10, duration.value || currentTime.value + 10))
      showControlsTemporary()
      break
    case 'ArrowLeft':
      e.preventDefault()
      seek(Math.max(0, currentTime.value - 10))
      showControlsTemporary()
      break
    case 'ArrowUp':
      e.preventDefault()
      setVolume(Math.min(1, volume.value + 0.1))
      showControlsTemporary()
      break
    case 'ArrowDown':
      e.preventDefault()
      setVolume(Math.max(0, volume.value - 0.1))
      showControlsTemporary()
      break
    case 'f':
    case 'F':
      e.preventDefault()
      handleFullscreenToggle()
      break
    case '>':
      e.preventDefault()
      cycleRateUp()
      showControlsTemporary()
      break
    case '<':
      e.preventDefault()
      cycleRateDown()
      showControlsTemporary()
      break
  }
}

// -- 倍速菜单/设置面板外部点击关闭 --
function handleDocumentClick() {
  if (showRateMenu.value) {
    showRateMenu.value = false
  }
  if (showSettingsPanel.value) {
    showSettingsPanel.value = false
  }
}

// -- 弹幕系统 --
// 将 DanmakuItem[] 转换为 danmaku 库所需的 Comment[] 格式
// 注意：danmaku 库的 time 单位是秒，vd_time 是毫秒，需要除以 1000
function transformComments(items: DanmakuItem[]): any[] {
  return items.map((item) => ({
    time: item.vd_time / 1000,
    text: item.vd_content,
    mode: DANMAKU_MODE_MAP[item.vd_mode] || 'rtl',
    style: {
      color: item.vd_color || '#ffffff',
    },
  }))
}

// 创建 danmaku 实例
function initDanmaku() {
  if (danmakuInst || !danmakuContainerRef.value || !videoRef.value) return
  danmakuInst = new Danmaku({
    container: danmakuContainerRef.value,
    media: videoRef.value,
    comments: transformComments(props.danmaku),
    engine: 'dom',
  })
  // 设置滚动速度（slider 值 * 30 映射到库的 speed 属性，5->150 接近默认 144）
  danmakuInst.speed = danmakuSpeed.value * 30
  // 根据初始开关状态决定显示/隐藏
  if (!props.danmakuEnabled) {
    danmakuInst.hide()
  }
}

// 销毁 danmaku 实例
function destroyDanmaku() {
  if (danmakuInst) {
    danmakuInst.destroy()
    danmakuInst = null
  }
}

// 重建 danmaku 实例（用于 comments 数据变化时）
// 采用 destroy + recreate 方式，确保内部 comments 数组和 position 状态完全重置
function rebuildDanmaku() {
  if (!danmakuContainerRef.value || !videoRef.value) return
  destroyDanmaku()
  initDanmaku()
}

// 弹幕容器 resize（窗口变化/全屏切换/显示区域变化时调用）
function handleDanmakuResize() {
  if (danmakuInst) {
    danmakuInst.resize()
  }
}

// 切换弹幕开关
function toggleDanmakuEnabled() {
  emit('update:danmakuEnabled', !props.danmakuEnabled)
}

// 切换弹幕输入框展开/收起
function toggleDanmakuInput() {
  showDanmakuInput.value = !showDanmakuInput.value
  if (showDanmakuInput.value) {
    showControls()
    clearHideTimer()
    // 自动聚焦输入框
    setTimeout(() => {
      danmakuInputRef.value?.focus()
    }, 50)
  }
}

// 关闭弹幕输入框
function closeDanmakuInput() {
  showDanmakuInput.value = false
  danmakuInputText.value = ''
}

// 发送弹幕
function handleSendDanmaku() {
  const content = danmakuInputText.value.trim()
  if (!content) return
  // 时间：currentTime(秒) * 1000 = 毫秒（与 DanmakuItem.vd_time 一致）
  const time = Math.floor(currentTime.value * 1000)
  const mode = danmakuInputMode.value
  const color = danmakuInputColor.value
  // 通知父组件调接口保存
  emit('sendDanmaku', content, time, mode, color)
  // 立即在本地显示这条弹幕（秒为单位）
  if (danmakuInst) {
    danmakuInst.emit({
      time: currentTime.value,
      text: content,
      mode: DANMAKU_MODE_MAP[mode] || 'rtl',
      style: { color },
    })
  }
  // 清空输入框但保持展开态
  danmakuInputText.value = ''
}

// 弹幕输入框回车提交
function handleDanmakuInputKeyDown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    e.preventDefault()
    handleSendDanmaku()
  }
}

// 切换设置面板展开/收起
function toggleSettingsPanel() {
  showSettingsPanel.value = !showSettingsPanel.value
  if (showSettingsPanel.value) {
    showControls()
    clearHideTimer()
  }
}

// -- 事件转发 --
// 模板直接监听 video 元素原生事件转发给父组件
function onVideoPlay() {
  started.value = true
  // 续播 seek 延迟到首次 play 后执行：避免在 loadedmetadata 时 seek
  // 延迟 play 事件、导致 300ms 兜底误触发中心按钮
  if (pendingResumeSeek.value > 0 && videoRef.value) {
    const t = pendingResumeSeek.value
    pendingResumeSeek.value = 0
    // 仅当 < 90% 时长才 seek（避免续到结尾反复触发 ended）
    if (duration.value > 0 && t < duration.value * 0.9) {
      videoRef.value.currentTime = t
      // 播放成功时显示续播提醒，2秒后淡出
      resumeHintShown.value = false
      if (resumeHintTimer) clearTimeout(resumeHintTimer)
      resumeHintTimer = setTimeout(() => {
        resumeHintShown.value = true
        resumeHintTimer = null
      }, 2000)
    }
  }
  emit('play')
  if (!isDragging.value && !showRateMenu.value) scheduleHide()
}

function onVideoPause() {
  started.value = true
  emit('pause')
  showControls()
  clearHideTimer()
}

function onVideoEnded() {
  emit('ended')
  showControls()
  clearHideTimer()
}

// autoplay 显式调用 play()，通过 promise 检测浏览器是否阻止自动播放
// 成功：play 事件触发，onVideoPlay 设置 started=true
// 失败（被阻止）：标记 started=true 显示中心播放按钮供用户手动点击
async function attemptAutoplay() {
  if (!videoRef.value) return
  try {
    await videoRef.value.play()
    // 成功后 play 事件由 onVideoPlay 处理
  } catch {
    // 自动播放被浏览器策略阻止
    started.value = true
  }
}

function onVideoLoadedMetadata() {
  // 续播位置暂存：seek 延迟到首次 play 事件后执行（见 onVideoPlay）
  pendingResumeSeek.value = props.resumeTime > 0 ? props.resumeTime : 0
  if (pendingResumeSeek.value > 0) showControls()
  emit('ready')
  // autoplay：显式调用 play()，通过 promise 检测是否被浏览器阻止
  if (props.autoPlay && !started.value) {
    void attemptAutoplay()
  }
}

// currentTime / playbackRate 通过 watch 转发，确保拿到 composable 更新后的值
watch(currentTime, (t) => {
  emit('timeupdate', t)
})

watch(playbackRate, (r) => {
  emit('ratechange', r)
})

watch(error, (err) => {
  if (err) emit('error', err)
})

// -- 暂停/播放状态联动控件栏 --
watch(paused, (isPaused) => {
  if (isPaused) {
    showControls()
    clearHideTimer()
  } else {
    scheduleHide()
  }
})

// -- src 变化重置状态 --
watch(
  () => props.src,
  () => {
    resetError()
    currentTime.value = 0
    started.value = !props.autoPlay
    resumeHintShown.value = true
    pendingResumeSeek.value = 0
    showHoverTime.value = false
    if (resumeHintTimer) {
      clearTimeout(resumeHintTimer)
      resumeHintTimer = null
    }
  },
)

// -- 弹幕相关 watchers --
// danmaku 数据变化时重建实例
watch(
  () => props.danmaku,
  () => {
    rebuildDanmaku()
  },
  { deep: false },
)

// 弹幕开关变化时 show/hide
watch(
  () => props.danmakuEnabled,
  (enabled) => {
    if (danmakuInst) {
      if (enabled) {
        danmakuInst.show()
      } else {
        danmakuInst.hide()
      }
    }
  },
)

// 设置项变化时持久化 + 实时更新
watch(danmakuFontSize, (v) => {
  saveSetting('font-size', v)
  // 字号通过容器 CSS 继承，无需重建实例
})

watch(danmakuOpacity, (v) => {
  saveSetting('opacity', v)
  // 透明度通过容器内联样式，无需操作实例
})

watch(danmakuDisplayArea, (v) => {
  saveSetting('display-area', v)
  // 显示区域变化后需要 resize 让库重新计算轨道
  setTimeout(() => handleDanmakuResize(), 0)
})

watch(danmakuSpeed, (v) => {
  saveSetting('speed', v)
  // speed 直接映射到库的 speed 属性
  if (danmakuInst) {
    danmakuInst.speed = v * 30
  }
})

// 全屏切换时 resize 弹幕
watch(isFullscreen, () => {
  setTimeout(() => handleDanmakuResize(), 100)
})

// 控件隐藏时通过 JS 直接设置 cursor，确保 WebView2 下生效
watch(controlsVisible, (visible) => {
  if (containerRef.value) {
    containerRef.value.style.cursor = visible ? '' : 'none'
  }
  if (videoRef.value) {
    videoRef.value.style.cursor = visible ? 'pointer' : 'none'
  }
})

// -- 挂载/卸载 --
document.addEventListener('click', handleDocumentClick)

onMounted(() => {
  initDanmaku()
  window.addEventListener('resize', handleDanmakuResize)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleDocumentClick)
  if (hideTimer) clearTimeout(hideTimer)
  if (clickTimer) clearTimeout(clickTimer)
  if (temporaryShowTimer) clearTimeout(temporaryShowTimer)
  window.removeEventListener('resize', handleDanmakuResize)
  window.removeEventListener('mousemove', handleProgressMouseMove)
  window.removeEventListener('mouseup', handleProgressMouseUp)
  window.removeEventListener('mousemove', handleVolumeMouseMove)
  window.removeEventListener('mouseup', handleVolumeMouseUp)
  destroyDanmaku()
})
</script>

<template>
  <div
    ref="containerRef"
    class="video-player"
    :class="{
      'is-fullscreen': isFullscreen,
      'is-error': !!error,
      'is-controls-hidden': !controlsVisible,
    }"
    tabindex="0"
    @mousemove="handleMouseMove"
    @keydown="handleKeyDown"
  >
    <video
      ref="videoRef"
      class="video-player__media"
      :src="src"
      :poster="poster || undefined"
      :loop="loop"
      :controls="false"
      playsinline
      preload="metadata"
      @click="handleVideoClick"
      @dblclick="handleVideoDblClick"
      @play="onVideoPlay"
      @pause="onVideoPause"
      @ended="onVideoEnded"
      @loadedmetadata="onVideoLoadedMetadata"
    />

    <!-- UI 覆盖层（弹幕/控件/加载态等，与 video 分离避免相互影响渲染） -->
    <div class="video-player__ui">
      <!-- 弹幕渲染层（覆盖在 video 上方，不遮挡控件栏） -->
      <div
        v-if="!error"
        ref="danmakuContainerRef"
        class="video-player__danmaku"
        :style="danmakuContainerStyle"
      />

      <!-- 顶部标题栏（可选） -->
      <div
        v-if="title && !error"
        class="video-player__title-bar"
        :class="{ 'is-hidden': !controlsVisible }"
      >
        <span class="video-player__title-text">{{ title }}</span>
      </div>

      <!-- 中心加载 Spinner：
           初始加载态（!started，autoplay 从挂载到首次 play 事件）或播放中缓冲（waiting && !paused） -->
      <div v-if="(!started || (waiting && !paused)) && !error" class="video-player__loading">
        <Spinner :size="48" />
      </div>

      <!-- 中心播放按钮（暂停 + 非错误 + 非加载中） -->
      <transition name="vp-center">
        <button
          v-if="showCenterBtn"
          class="video-player__center-btn"
          :class="{ 'is-fullscreen': isFullscreen }"
          type="button"
          aria-label="播放"
          @click.stop="togglePlay"
        >
          <PlayerPlay class="video-player__center-icon" />
        </button>
      </transition>

      <!-- 错误状态 -->
      <div v-if="error" class="video-player__error">
        <AlertCircle class="video-player__error-icon" />
        <p class="video-player__error-text">视频加载失败</p>
      </div>

      <!-- 底部控件栏（垂直布局：进度条置顶 + 操作栏） -->
      <div
        v-if="!error"
        class="video-player__controls"
        :class="{ 'is-hidden': !controlsVisible }"
      >
      <!-- 续播提醒（进度条上方，2秒后淡出，跟随控制栏 opacity 联动） -->
      <div
        v-if="props.resumeTime > 0"
        class="video-player__resume-hint"
        :class="{ 'is-faded': resumeHintShown }"
      >
        已为你续播至 {{ formatTime(props.resumeTime) }}
      </div>

      <!-- 顶部：进度条（全宽） -->
      <div
        ref="progressRef"
        class="video-player__progress"
        :class="{ 'is-hover': isProgressHover || isDragging }"
        @mousedown="handleProgressMouseDown"
        @mouseenter="onProgressEnter"
        @mouseleave="onProgressLeave"
        @mousemove="handleProgressHoverMove"
      >
        <div
          class="video-player__progress-buffered"
          :style="{ width: bufferedPercent + '%' }"
        />
        <div
          class="video-player__progress-played"
          :style="{ width: playedPercent + '%' }"
        />
        <div
          class="video-player__progress-handle"
          :style="{ left: playedPercent + '%' }"
        />
        <!-- 鼠标悬停位置时间预览 -->
        <div
          v-if="showHoverTime"
          class="video-player__progress-tooltip"
          :style="{ left: hoverTimeLeft + '%' }"
        >
          {{ formatTime(hoverTime) }}
        </div>
      </div>

      <!-- 下方：操作栏 -->
      <div class="video-player__controls-bar">
        <!-- 上一集 -->
        <button
          class="vp-btn"
          :class="{ 'is-disabled': !hasPrev }"
          type="button"
          :disabled="!hasPrev"
          aria-label="上一集"
          @click="emit('prev')"
        >
          <PlayerSkipBack class="vp-btn__icon" />
        </button>

        <!-- 播放/暂停 -->
        <button
          class="vp-btn"
          type="button"
          :aria-label="paused ? '播放' : '暂停'"
          @click="togglePlay"
        >
          <PlayerPause v-if="!paused" class="vp-btn__icon" />
          <PlayerPlay v-else class="vp-btn__icon" />
        </button>

        <!-- 下一集 -->
        <button
          class="vp-btn"
          :class="{ 'is-disabled': !hasNext }"
          type="button"
          :disabled="!hasNext"
          aria-label="下一集"
          @click="emit('next')"
        >
          <PlayerSkipForward class="vp-btn__icon" />
        </button>

        <!-- 当前时间 -->
        <span class="video-player__time">{{ currentTimeText }}</span>

        <!-- 弹幕输入区域（中部，flex:1 自适应） -->
        <div class="video-player__danmaku-input-area">
          <!-- 收起态：弹幕输入入口（MessageDots 与开关按钮的 MessageCircle 区分） -->
          <button
            v-if="!showDanmakuInput"
            class="vp-btn"
            type="button"
            aria-label="发送弹幕"
            title="发送弹幕"
            @click.stop="toggleDanmakuInput"
          >
            <MessageDots class="vp-btn__icon" />
          </button>

          <!-- 展开态：[类型] [颜色] [输入框] [发送] [关闭] -->
          <div v-else class="video-player__danmaku-input-row" @click.stop>
            <!-- 类型选择（三个小按钮） -->
            <div class="vp-dm-mode-group">
              <button
                v-for="opt in DANMAKU_MODE_OPTIONS"
                :key="opt.value"
                class="vp-dm-mode-btn"
                :class="{ 'is-active': danmakuInputMode === opt.value }"
                type="button"
                :aria-label="opt.label"
                @click="danmakuInputMode = opt.value"
              >
                {{ opt.label }}
              </button>
            </div>

            <!-- 颜色选择（6 个小圆点） -->
            <div class="vp-dm-color-group">
              <button
                v-for="color in DANMAKU_COLORS"
                :key="color"
                class="vp-dm-color-dot"
                :class="{ 'is-active': danmakuInputColor === color }"
                type="button"
                :style="{ background: color }"
                :aria-label="'颜色 ' + color"
                @click="danmakuInputColor = color"
              />
            </div>

            <!-- 文字输入框 -->
            <input
              ref="danmakuInputRef"
              v-model="danmakuInputText"
              class="vp-dm-input"
              type="text"
              placeholder="发个弹幕..."
              maxlength="100"
              @keydown="handleDanmakuInputKeyDown"
            />

            <!-- 发送按钮 -->
            <button
              class="vp-btn"
              type="button"
              aria-label="发送"
              @click="handleSendDanmaku"
            >
              <Send class="vp-btn__icon" />
            </button>

            <!-- 关闭按钮 -->
            <button
              class="vp-btn"
              type="button"
              aria-label="关闭弹幕输入"
              @click="closeDanmakuInput"
            >
              <X class="vp-btn__icon" />
            </button>
          </div>
        </div>

        <!-- 总时长 -->
        <span class="video-player__time">{{ durationText }}</span>

        <!-- 弹幕开关按钮：开启时 MessageCircle（主题色），关闭时 MessageCircleOff（灰色） -->
        <button
          class="vp-btn vp-btn--dm-toggle"
          :class="{ 'is-active': danmakuEnabled }"
          type="button"
          :aria-label="danmakuEnabled ? '关闭弹幕' : '开启弹幕'"
          :title="danmakuEnabled ? '关闭弹幕' : '开启弹幕'"
          @click="toggleDanmakuEnabled"
        >
          <MessageCircle v-if="danmakuEnabled" class="vp-btn__icon" />
          <MessageCircleOff v-else class="vp-btn__icon" />
        </button>

        <!-- 设置按钮 -->
        <div class="video-player__settings">
          <button
            class="vp-btn"
            type="button"
            aria-label="弹幕设置"
            @click.stop="toggleSettingsPanel"
          >
            <Settings class="vp-btn__icon" />
          </button>

          <!-- 设置浮动面板 -->
          <transition name="vp-settings-panel">
            <div
              v-if="showSettingsPanel"
              class="video-player__settings-panel"
              @click.stop
            >
              <!-- 字号 -->
              <label class="vp-settings-item">
                <span class="vp-settings-label">字号</span>
                <span class="vp-settings-value">{{ danmakuFontSize }}px</span>
                <input
                  v-model.number="danmakuFontSize"
                  class="vp-settings-slider"
                  type="range"
                  min="12"
                  max="36"
                  step="1"
                />
              </label>

              <!-- 透明度 -->
              <label class="vp-settings-item">
                <span class="vp-settings-label">透明度</span>
                <span class="vp-settings-value">
                  {{ Math.round(danmakuOpacity * 100) }}%
                </span>
                <input
                  v-model.number="danmakuOpacity"
                  class="vp-settings-slider"
                  type="range"
                  min="0.3"
                  max="1"
                  step="0.05"
                />
              </label>

              <!-- 显示区域占比 -->
              <label class="vp-settings-item">
                <span class="vp-settings-label">显示区域</span>
                <span class="vp-settings-value">
                  {{ Math.round(danmakuDisplayArea * 100) }}%
                </span>
                <input
                  v-model.number="danmakuDisplayArea"
                  class="vp-settings-slider"
                  type="range"
                  min="0.1"
                  max="1"
                  step="0.05"
                />
              </label>

              <!-- 滚动速度 -->
              <label class="vp-settings-item">
                <span class="vp-settings-label">滚动速度</span>
                <span class="vp-settings-value">{{ danmakuSpeed }}</span>
                <input
                  v-model.number="danmakuSpeed"
                  class="vp-settings-slider"
                  type="range"
                  min="1"
                  max="10"
                  step="1"
                />
              </label>
            </div>
          </transition>
        </div>

        <!-- 倍速 -->
        <div class="video-player__rate">
          <button
            class="vp-btn vp-btn--text"
            type="button"
            aria-label="倍速"
            @click.stop="toggleRateMenu"
          >
            {{ rateLabel }}
          </button>
          <transition name="vp-rate-menu">
            <ul
              v-if="showRateMenu"
              class="video-player__rate-menu"
              @click.stop
            >
              <li
                v-for="rate in RATES"
                :key="rate"
                class="video-player__rate-item"
                :class="{ 'is-active': rate === playbackRate }"
                @click="changeRate(rate)"
              >
                {{ rate }}x
              </li>
            </ul>
          </transition>
        </div>

        <!-- 音量 -->
        <div
          class="video-player__volume"
          @mouseenter="volumeHovered = true"
          @mouseleave="volumeHovered = false"
        >
          <button
            class="vp-btn"
            type="button"
            :aria-label="muted || volume === 0 ? '取消静音' : '静音'"
            @click="toggleMute"
          >
            <Volume3
              v-if="muted || volume === 0"
              class="vp-btn__icon"
            />
            <Volume v-else class="vp-btn__icon" />
          </button>
          <div
            ref="volumeRef"
            class="video-player__volume-slider"
            :class="{ 'is-expanded': volumeHovered || isDraggingVolume }"
            @mousedown="handleVolumeMouseDown"
          >
            <div
              class="video-player__volume-fill"
              :style="{ width: volumePercent + '%' }"
            />
            <div
              class="video-player__volume-handle"
              :style="{ left: volumePercent + '%' }"
            />
          </div>
        </div>

        <!-- 全屏 -->
        <button
          class="vp-btn"
          type="button"
          :aria-label="isFullscreen ? '退出全屏' : '全屏'"
          @click="handleFullscreenToggle"
        >
          <Minimize v-if="isFullscreen" class="vp-btn__icon" />
          <Maximize v-else class="vp-btn__icon" />
        </button>
      </div>
    </div>
    </div>
  </div>
</template>

<style scoped>
/* -- 容器 -- */
.video-player {
  position: relative;
  width: 100%;
  height: 100%;
  background: var(--bg-base);
  overflow: hidden;
  outline: none;
}

.video-player:focus-visible {
  box-shadow: inset 0 0 0 2px var(--accent-sakura);
}

/* 控件隐藏时同步隐藏鼠标光标（播放中且无交互 3 秒后触发）
   scoped 下 * 会被加上 [data-v-xxx] 限制，改用 :deep() 穿透 */
.video-player.is-controls-hidden,
.video-player.is-controls-hidden :deep(*) {
  cursor: none !important;
}

/* 全屏下填满屏幕 */
.video-player.is-fullscreen {
  background: #000;
}

/* -- 视频元素 -- */
.video-player__media {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000;
  cursor: pointer;
}

/* -- UI 覆盖层：绝对定位覆盖整个容器，与 video 渲染分离 -- */
/* pointer-events: none 让鼠标穿透到 video；需要交互的子元素单独开启 auto */
.video-player__ui {
  position: absolute;
  inset: 0;
  pointer-events: none;
}

/* 需要交互的 UI 子元素重新开启鼠标事件 */
.video-player__controls,
.video-player__center-btn {
  pointer-events: auto;
}

/* -- 弹幕渲染层 -- */
.video-player__danmaku {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  pointer-events: none;
  overflow: hidden;
  z-index: 1;
  transition: opacity var(--duration-fast) var(--ease-fluid),
    height var(--duration-fast) var(--ease-fluid);
}

/* -- 顶部标题栏 -- */
.video-player__title-bar {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  padding: var(--space-4) var(--space-6);
  background: linear-gradient(
    to bottom,
    rgba(10, 10, 18, 0.7) 0%,
    transparent 100%
  );
  pointer-events: none;
  opacity: 1;
  transition: opacity var(--duration-fast) var(--ease-fluid);
  z-index: 2;
}

.video-player__title-bar.is-hidden {
  opacity: 0;
}

.video-player__title-text {
  font-family: var(--font-display);
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  text-shadow: 0 1px 4px rgba(0, 0, 0, 0.6);
}

/* -- 中心加载 Spinner -- */
.video-player__loading {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  pointer-events: none;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 2;
}

/* -- 中心播放按钮 -- */
.video-player__center-btn {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 72px;
  height: 72px;
  border: none;
  border-radius: var(--radius-full);
  background: var(--accent-sakura);
  color: #fff;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 8px 32px var(--accent-sakura-glow);
  transition: transform var(--duration-normal) var(--ease-fluid),
    background-color var(--duration-fast) var(--ease-fluid);
  z-index: 3;
}

.video-player__center-btn:hover {
  background: var(--accent-sakura-hover);
  transform: translate(-50%, -50%) scale(1.05);
}

.video-player__center-btn:active {
  transform: translate(-50%, -50%) scale(0.95);
}

/* 全屏下中心按钮放大至 96x96px */
.video-player__center-btn.is-fullscreen {
  width: 96px;
  height: 96px;
}

.video-player__center-icon {
  width: 32px;
  height: 32px;
  display: block;
}

.video-player.is-fullscreen .video-player__center-icon {
  width: 40px;
  height: 40px;
}

/* 中心按钮淡入淡出 */
.vp-center-enter-active {
  transition: opacity var(--duration-fast) var(--ease-fluid);
}
.vp-center-leave-active {
  transition: opacity var(--duration-fast) var(--ease-fluid);
}
.vp-center-enter-from,
.vp-center-leave-to {
  opacity: 0;
}

/* -- 错误状态 -- */
.video-player__error {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  pointer-events: none;
  z-index: 2;
}

.video-player__error-icon {
  width: 48px;
  height: 48px;
  color: var(--color-error);
}

.video-player__error-text {
  margin: 0;
  font-family: var(--font-body);
  font-size: 14px;
  color: var(--text-secondary);
}

/* -- 底部控件栏（垂直布局） -- */
.video-player__controls {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  flex-direction: column;
  opacity: 1;
  transition: opacity var(--duration-fast) var(--ease-fluid); /* 淡入 200ms */
  z-index: 4;
}

.video-player__controls.is-hidden {
  opacity: 0;
  pointer-events: none;
  transition: opacity 300ms var(--ease-fluid); /* 淡出 300ms */
}

/* -- 续播提醒（进度条上方左对齐，2秒后淡出，跟随控制栏 opacity 联动） -- */
.video-player__resume-hint {
  align-self: flex-start;
  width: fit-content;
  margin: 0 0 8px 12px;
  padding: 5px 12px;
  background: var(--accent-sakura);
  border-radius: 4px;
  color: #fff;
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
  pointer-events: none;
  box-shadow: 0 2px 8px var(--accent-sakura-glow);
  opacity: 1;
  transition: opacity 300ms var(--ease-fluid);
}

.video-player__resume-hint.is-faded {
  opacity: 0;
}

/* -- 进度条（置顶，全宽） -- */
.video-player__progress {
  position: relative;
  width: 100%;
  height: 4px;
  background: rgba(255, 255, 255, 0.15);
  cursor: pointer;
  transition: height var(--duration-fast) var(--ease-fluid);
  z-index: 1; /* 提升堆叠层级，避免 handle 下方被操作栏背景覆盖 */
}

.video-player__progress.is-hover {
  height: 6px;
}

.video-player__progress-buffered {
  position: absolute;
  top: 0;
  left: 0;
  height: 100%;
  background: rgba(255, 255, 255, 0.2);
  border-radius: var(--radius-full);
  pointer-events: none;
}

.video-player__progress-played {
  position: absolute;
  top: 0;
  left: 0;
  height: 100%;
  background: var(--accent-sakura);
  border-radius: var(--radius-full);
  pointer-events: none;
}

.video-player__progress-handle {
  position: absolute;
  top: 50%;
  width: 12px;
  height: 12px;
  border-radius: var(--radius-full);
  background: var(--accent-sakura);
  transform: translate(-50%, -50%);
  opacity: 0;
  transition: opacity var(--duration-fast) var(--ease-fluid);
  pointer-events: none;
}

.video-player__progress.is-hover .video-player__progress-handle {
  opacity: 1;
}

/* 进度条悬停时间预览 tooltip */
.video-player__progress-tooltip {
  position: absolute;
  bottom: calc(100% + 8px);
  transform: translateX(-50%);
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  background: rgba(0, 0, 0, 0.85);
  color: #fff;
  font-size: 12px;
  line-height: 1.5;
  white-space: nowrap;
  pointer-events: none;
  user-select: none;
}

/* -- 操作栏（水平 flex） -- */
.video-player__controls-bar {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 48px;
  padding: 0 var(--space-4);
  background: rgba(10, 10, 18, 0.5);
}

/* -- 控件按钮（图标按钮） -- */
.vp-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
  user-select: none;
  transition: color var(--duration-fast) var(--ease-fluid);
}

.vp-btn:hover {
  color: var(--accent-sakura);
}

.vp-btn:focus-visible {
  outline: none;
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow-focus-sakura);
}

.vp-btn.is-disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.vp-btn.is-disabled:hover {
  color: var(--text-primary);
}

/* 弹幕开关按钮：开启时用主题色高亮，关闭时用次级文本色 */
.vp-btn--dm-toggle.is-active {
  color: var(--accent-sakura);
}

.vp-btn--dm-toggle.is-active:hover {
  color: var(--accent-sakura-hover);
}

/* 文字按钮（倍速） */
.vp-btn--text {
  width: auto;
  min-width: 32px;
  font-family: var(--font-mono);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

/* 图标尺寸 20px */
.vp-btn__icon {
  width: 20px;
  height: 20px;
  display: block;
}

/* -- 时间显示 -- */
.video-player__time {
  flex-shrink: 0;
  min-width: 44px;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1;
  color: var(--text-secondary);
  text-align: center;
  user-select: none;
}

/* -- 弹幕输入区域 -- */
.video-player__danmaku-input-area {
  flex: 1;
  display: flex;
  align-items: center;
  min-width: 0;
}

.video-player__danmaku-input-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
}

/* 弹幕模式按钮组 */
.vp-dm-mode-group {
  display: flex;
  flex-shrink: 0;
  gap: 2px;
  padding: 2px;
  background: rgba(255, 255, 255, 0.08);
  border-radius: var(--radius-sm);
}

.vp-dm-mode-btn {
  padding: 2px 6px;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  font-family: var(--font-body);
  font-size: 11px;
  line-height: 1.4;
  cursor: pointer;
  border-radius: var(--radius-sm);
  transition: color var(--duration-fast) var(--ease-fluid),
    background-color var(--duration-fast) var(--ease-fluid);
}

.vp-dm-mode-btn:hover {
  color: var(--text-primary);
}

.vp-dm-mode-btn.is-active {
  color: var(--accent-sakura);
  background: rgba(255, 255, 255, 0.12);
}

/* 颜色选择圆点 */
.vp-dm-color-group {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
}

.vp-dm-color-dot {
  width: 14px;
  height: 14px;
  padding: 0;
  border: 2px solid transparent;
  border-radius: var(--radius-full);
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-fluid),
    transform var(--duration-fast) var(--ease-fluid);
}

.vp-dm-color-dot:hover {
  transform: scale(1.15);
}

.vp-dm-color-dot.is-active {
  border-color: var(--accent-sakura);
}

/* 弹幕文字输入框 */
.vp-dm-input {
  flex: 1;
  min-width: 0;
  height: 28px;
  padding: 0 var(--space-2);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  background: rgba(0, 0, 0, 0.3);
  color: var(--text-primary);
  font-family: var(--font-body);
  font-size: 13px;
  outline: none;
  transition: border-color var(--duration-fast) var(--ease-fluid);
}

.vp-dm-input::placeholder {
  color: var(--text-tertiary);
}

.vp-dm-input:focus {
  border-color: var(--accent-sakura);
}

/* -- 设置按钮及面板 -- */
.video-player__settings {
  position: relative;
}

.video-player__settings-panel {
  position: absolute;
  bottom: calc(100% + var(--space-2));
  right: 0;
  width: 300px;
  padding: var(--space-3);
  background: var(--bg-card-solid);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  z-index: 999999;
}

/* 设置项 */
.vp-settings-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) 0;
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-secondary);
}

.vp-settings-label {
  flex-shrink: 0;
  min-width: 56px;
  color: var(--text-primary);
}

.vp-settings-value {
  flex-shrink: 0;
  min-width: 36px;
  font-family: var(--font-mono);
  text-align: right;
  color: var(--text-secondary);
}

.vp-settings-slider {
  flex: 1;
  min-width: 60px;
  height: 4px;
  accent-color: var(--accent-sakura);
  cursor: pointer;
}

/* 设置面板淡入淡出 */
.vp-settings-panel-enter-active {
  transition: opacity var(--duration-fast) var(--ease-fluid),
    transform var(--duration-fast) var(--ease-fluid);
}
.vp-settings-panel-leave-active {
  transition: opacity var(--duration-fast) var(--ease-fluid);
}
.vp-settings-panel-enter-from {
  opacity: 0;
  transform: translateY(4px);
}
.vp-settings-panel-leave-to {
  opacity: 0;
}

/* -- 倍速菜单 -- */
.video-player__rate {
  position: relative;
  flex-shrink: 0;
}

.video-player__rate-menu {
  position: absolute;
  bottom: calc(100% + var(--space-2));
  right: 0;
  margin: 0;
  padding: var(--space-1);
  list-style: none;
  background: var(--bg-card-solid);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  min-width: 64px;
}

.video-player__rate-item {
  padding: var(--space-2) var(--space-3);
  font-family: var(--font-mono);
  font-size: 12px;
  font-weight: 500;
  color: var(--text-secondary);
  text-align: center;
  cursor: pointer;
  border-radius: var(--radius-sm);
  transition: color var(--duration-fast) var(--ease-fluid),
    background-color var(--duration-fast) var(--ease-fluid);
}

.video-player__rate-item:hover {
  color: var(--text-primary);
  background: var(--bg-card);
}

.video-player__rate-item.is-active {
  color: var(--accent-sakura);
}

/* 倍速菜单淡入 */
.vp-rate-menu-enter-active {
  transition: opacity var(--duration-fast) var(--ease-fluid),
    transform var(--duration-fast) var(--ease-fluid);
}
.vp-rate-menu-leave-active {
  transition: opacity var(--duration-fast) var(--ease-fluid);
}
.vp-rate-menu-enter-from {
  opacity: 0;
  transform: translateY(4px);
}
.vp-rate-menu-leave-to {
  opacity: 0;
}

/* -- 音量控制 -- */
.video-player__volume {
  position: relative;
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

.video-player__volume-slider {
  position: relative;
  width: 0;
  height: 4px;
  margin-left: 0;
  background: rgba(255, 255, 255, 0.15);
  border-radius: var(--radius-full);
  overflow: hidden;
  cursor: pointer;
  transition: width 200ms var(--ease-fluid), margin-left 200ms var(--ease-fluid);
}

.video-player__volume-slider.is-expanded {
  width: 80px;
  margin-left: var(--space-2);
  transition: width 300ms var(--ease-fluid),
    margin-left 300ms var(--ease-fluid);
}

.video-player__volume-fill {
  position: absolute;
  top: 0;
  left: 0;
  height: 100%;
  background: var(--accent-sakura);
  border-radius: var(--radius-full);
  pointer-events: none;
}

.video-player__volume-handle {
  position: absolute;
  top: 50%;
  width: 10px;
  height: 10px;
  border-radius: var(--radius-full);
  background: var(--accent-sakura);
  transform: translate(-50%, -50%);
  pointer-events: none;
}

/* -- 无障碍：减少动效 -- */
@media (prefers-reduced-motion: reduce) {
  .video-player__controls,
  .video-player__controls.is-hidden,
  .video-player__title-bar,
  .video-player__center-btn,
  .video-player__danmaku,
  .video-player__progress,
  .video-player__volume-slider,
  .video-player__volume-slider.is-expanded,
  .video-player__progress-handle,
  .vp-center-enter-active,
  .vp-center-leave-active,
  .vp-rate-menu-enter-active,
  .vp-rate-menu-leave-active,
  .vp-settings-panel-enter-active,
  .vp-settings-panel-leave-active {
    transition: none;
  }
}
</style>
