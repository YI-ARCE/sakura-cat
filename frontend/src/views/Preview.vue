<script setup lang="ts">
import { ref, computed, reactive, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { type InputInst } from 'naive-ui'
import {
  MessageCircle,
  Send,
  MoodSmile,
} from '@vicons/tabler'
import { VideoPlayer } from '../components/business'
import { Spinner, StatusHint } from '../components/common/Ui'
import {
  listEpisodes,
  listDiscuss,
  postDiscuss,
  getVideoDetail,
  listDanmaku,
  postDanmaku,
  getHistory,
  reportHistory,
  followVideo,
  incrementViewCount,
  type EpisodeItem,
  type DiscussItem,
  type DiscussPostRequest,
  type VideoDetail,
  type ListDiscussRequest,
  type DanmakuItem,
  type DanmakuPostRequest,
} from '../services/repo'
import { getEpisodeStream } from '../services/video'
import { useWindowMeta } from '../composables/useWindowMeta'

const route = useRoute()

// 调用 useWindowMeta 注册标题 watcher
// 实际窗口标题在 syncWindowTitle() 中通过 setTitle() 触发同步
const { setTitle } = useWindowMeta()

// 响应式状态
const videoId = ref(0)

// 可选元数据（来自 route.query，可能为空）
const videoMeta = reactive({
  title: '',
  cover: '',
  category: '',
  year: '',
  season: '',
})

const episodes = ref<EpisodeItem[]>([])
const currentEpisode = ref<EpisodeItem | null>(null)
const streamUrl = ref('')
const streamError = ref<string | null>(null)
const streamLoading = ref(false)

// 弹幕
const danmakuList = ref<DanmakuItem[]>([])
const danmakuEnabled = ref(true)

// 视频简介详情（来自 detail 接口）
const videoDetail = ref<VideoDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref<string | null>(null)

const comments = ref<DiscussItem[]>([])
const repliesMap = ref<Record<number, DiscussItem[]>>({})
const expandedIds = ref<Set<number>>(new Set())
const loadingRepliesId = ref<number | null>(null)

// 评论分页
const COMMENT_PAGE_SIZE = 20
const commentSort = ref<'latest' | 'hot'>('latest')
const commentLoading = ref(false)
const commentHasMore = ref(false)
const commentCursor = ref<{ vrd_id: number } | null>(null)

// 回复分页（按评论ID记录各自的游标和加载状态）
const REPLY_PAGE_SIZE = 20
const replyCursors = ref<Record<number, { vrd_id: number } | null>>({})
const replyHasMore = ref<Record<number, boolean>>({})
const replyLoadingMore = ref<Record<number, boolean>>({})

const loading = ref(false)
const error = ref<string | null>(null)

const commentText = ref('')
// 表情面板是否展开
const showEmojiPanel = ref(false)
// 表情面板根元素引用（用于点击外部关闭）
const emojiPanelRef = ref<HTMLElement | null>(null)
const replyTarget = ref<{
  parentId: number          // 顶级评论ID（parent_id 始终指向顶级，保证两级）
} | null>(null)
const posting = ref(false)
const postingError = ref<string | null>(null)

// 右侧边栏当前 Tab：'detail'（详情/分集） | 'comment'（评论）
const activeTab = ref<'detail' | 'comment'>('detail')

// 分类/季度英文值 → 中文标签映射
const CATEGORY_LABELS: Record<string, string> = {
  anime: '动漫',
  movie: '电影',
  tv: '剧集',
  ova: 'OVA',
  special: '特别篇',
}
const SEASON_LABELS: Record<string, string> = {
  winter: '冬',
  spring: '春',
  summer: '夏',
  autumn: '秋',
}
const categoryLabel = computed(() => CATEGORY_LABELS[videoMeta.category] || videoMeta.category)
const seasonLabel = computed(() => SEASON_LABELS[videoMeta.season] || videoMeta.season)

// 输入框与列表 DOM 引用
const commentInputRef = ref<InputInst | null>(null)
const episodeListRef = ref<HTMLElement | null>(null)

const emoji = [
  '🤣', '🙃', '😅', '😇', '🥰', '😚', '😋', '🤪', '🤭', '🤭', '🫣', '🤔', '🤨', '😑', '😏',
  '😪', '😔', '🤮', '☹️', '😨', '😞', '😫', '😤', '😣', '😒', '🙄', '🤐', '😮', '🥱', '💔',
  '💗', '🤡', '👻', '💫', '💩', '🌿', '🍁', '🌵', '🌴', '🪴', '🍀', '🌹', '🌺', '🌸', '💐',
  '🌻', '🌷',
]

// 计算：VideoPlayer 顶部标题
const playerTitle = computed(() => {
  const t = videoMeta.title || ''
  const ep = currentEpisode.value
  if (ep && ep.ve_collect > 0) return `${t} - 第${ep.ve_collect}集`
  return t
})

// 当前集在列表中的索引
const currentEpisodeIndex = computed(() => {
  if (!currentEpisode.value) return -1
  return episodes.value.findIndex((e) => e.ve_id === currentEpisode.value!.ve_id)
})

const hasPrev = computed(() => currentEpisodeIndex.value > 0)
const hasNext = computed(
  () => currentEpisodeIndex.value >= 0 && currentEpisodeIndex.value < episodes.value.length - 1
)

// 输入框 placeholder：顶级评论 vs 回复
const inputPlaceholder = computed(() => {
  if (replyTarget.value) return `回复：`
  return '发表评论...'
})

// 评论数（用于 Tab 标签显示）
const commentCount = computed(() => comments.value.length)

// 视频状态文案：1=完结, 2=停更, 3=连载中
const statusText = computed(() => {
  const s = videoDetail.value?.vr_status
  if (s === 1) return '完结'
  if (s === 2) return '停更'
  if (s === 3) return '连载中'
  return ''
})

// 播放量格式化：万以上显示 x.x万
function formatViewCount(n: number): string {
  if (n >= 10000) return (n / 10000).toFixed(1).replace(/\.0$/, '') + '万'
  return String(n)
}

// grid 正方形格子内显示文案：有集号显示纯数字，否则显示标题前两字
function episodeDisplayText(ep: EpisodeItem): string {
  if (ep.ve_collect > 0) return String(ep.ve_collect)
  if (ep.ve_title) return ep.ve_title.slice(0, 2)
  return '?'
}

// 按显示文案长度返回字号档位 class（数字位数越多字号越小）
function episodeLenClass(ep: EpisodeItem): string {
  const len = episodeDisplayText(ep).length
  if (len <= 1) return 'is-len1'
  if (len === 2) return 'is-len2'
  if (len === 3) return 'is-len3'
  return 'is-len4'
}

// 相对时间格式化（秒级时间戳）
function formatRelativeTime(ts: number): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  if (isNaN(d.getTime())) return ''
  const diff = Date.now() - d.getTime()
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return '刚刚'
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}小时前`
  const day = Math.floor(hr / 24)
  if (day < 30) return `${day}天前`
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${dd}`
}

// 读取 route.query 单值
function readQuery(key: string): string {
  const v = route.query[key]
  return Array.isArray(v) ? (v[0] ?? '') : (v ?? '')
}

// 同步窗口标题：通过 setTitle 更新 module 级 ref，触发 Window.SetTitle + 顶栏 UI
function syncWindowTitle() {
  const t = videoMeta.title || '樱花猫'
  const ep = currentEpisode.value
  const title =
    ep && ep.ve_collect > 0 ? `${t} - 第${ep.ve_collect}集 - 樱花猫` : `${t} - 樱花猫`
  setTitle(title)
}

// 切换分集：拉取流地址 + 评论 + 续播进度，重置回复状态
async function selectEpisode(episode: EpisodeItem) {
  // 切换前：上报旧分集最后一次进度
  flushHistory()

  currentEpisode.value = episode
  viewCountReported.value = false // 切集重置，允许新集再次上报播放量
  viewAccumulatedMs.value = 0
  viewLastTickAt.value = 0
  repliesMap.value = {}
  expandedIds.value = new Set()
  replyTarget.value = null
  commentText.value = ''
  postingError.value = null
  replyCursors.value = {}
  replyHasMore.value = {}
  replyLoadingMore.value = {}

  // 播放器渲染前必须完成：流地址 + 续播进度
  historyReady.value = false
  streamError.value = null
  streamLoading.value = true
  streamUrl.value = ''
  pendingResumeTime.value = 0

  // 流地址
  try {
    streamUrl.value = await getEpisodeStream(episode.vs_channel_id, episode.ve_message_id)
  } catch (e) {
    streamError.value = e instanceof Error ? e.message : String(e)
  } finally {
    streamLoading.value = false
  }

  // 续播进度（await 完成，确保自动播放前 seek 就位）
  try {
    const p = await getHistory(episode.ve_id)
    pendingResumeTime.value = p.vuh_history_time ?? 0
  } catch {
    pendingResumeTime.value = 0
  } finally {
    historyReady.value = true
  }

  // 评论第一页
  await loadComments(true)

  // 弹幕（不阻塞播放）
  listDanmaku(episode.ve_id)
    .then((list) => {
      danmakuList.value = list
    })
    .catch(() => {
      danmakuList.value = []
    })

  syncWindowTitle()

  // 自动滚动至当前集
  await nextTick()
  episodeListRef.value?.querySelector('[data-current]')?.scrollIntoView({ block: 'nearest' })
}

// 加载顶级评论：firstPage=true 重置列表，否则追加下一页
async function loadComments(firstPage: boolean) {
  if (!currentEpisode.value) return
  if (commentLoading.value) return
  commentLoading.value = true
  try {
    const req: ListDiscussRequest = {
      ve_id: currentEpisode.value.ve_id,
      parent_id: 0,
      sort: commentSort.value,
      cursor_vrd_id: firstPage ? 0 : (commentCursor.value?.vrd_id ?? 0),
      limit: COMMENT_PAGE_SIZE,
    }
    const list = await listDiscuss(req)
    if (firstPage) {
      comments.value = list
    } else {
      comments.value = [...comments.value, ...list]
    }
    // 根据 length < limit 判断是否到底
    commentHasMore.value = list.length >= COMMENT_PAGE_SIZE
    // 更新游标为最后一项
    if (list.length > 0) {
      const last = list[list.length - 1]
      commentCursor.value = { vrd_id: last.vrd_id }
    } else {
      commentCursor.value = null
    }
    // 用后端返回的预览回复初始化 repliesMap，默认展开
    for (const c of list) {
      const preview = c.replies ?? []
      repliesMap.value[c.vrd_id] = preview
      if (preview.length > 0) {
        expandedIds.value.add(c.vrd_id)
        replyCursors.value[c.vrd_id] = { vrd_id: preview[preview.length - 1].vrd_id }
        replyHasMore.value[c.vrd_id] = c.reply_count > preview.length
      } else {
        replyHasMore.value[c.vrd_id] = false
      }
    }
  } catch {
    if (firstPage) comments.value = []
    commentHasMore.value = false
  } finally {
    commentLoading.value = false
  }
}

// 切换排序：重置列表
async function changeCommentSort(sort: 'latest' | 'hot') {
  if (commentSort.value === sort) return
  commentSort.value = sort
  await loadComments(true)
}

// 顶级评论触底加载
function onCommentListScroll(e: Event) {
  const el = e.target as HTMLElement
  if (!commentHasMore.value || commentLoading.value) return
  if (el.scrollHeight - el.scrollTop - el.clientHeight < 50) {
    void loadComments(false)
  }
}

// 发送弹幕：VideoPlayer 触发，调接口 + 乐观追加到列表
async function handleSendDanmaku(content: string, time: number, mode: number, color: string) {
  if (!currentEpisode.value) return
  const req: DanmakuPostRequest = {
    ve_id: currentEpisode.value.ve_id,
    vd_content: content,
    vd_time: time,
    vd_mode: mode,
    vd_color: color,
  }
  try {
    const item = await postDanmaku(req)
    danmakuList.value = [...danmakuList.value, item]
  } catch {
    // 静默失败，弹幕不强制保存
  }
}

// 发表评论 / 回复
async function submitComment() {
  const text = commentText.value.trim()
  if (!text || !currentEpisode.value || posting.value) return
  posting.value = true
  postingError.value = null
  const target = replyTarget.value
  const req: DiscussPostRequest = {
    ve_id: currentEpisode.value.ve_id,
    vrd_content: text,
    parent_id: target ? target.parentId : 0,
  }
  try {
    const item = await postDiscuss(req)
    if (target) {
      const pid = target.parentId
      const list = repliesMap.value[pid] ?? []
      repliesMap.value[pid] = [...list, item]
      expandedIds.value.add(pid)
      // 更新顶级评论的 reply_count
      const parent = comments.value.find((c) => c.vrd_id === pid)
      if (parent) parent.reply_count += 1
      // 首次回复（原本无预览）时初始化游标
      if (list.length === 0) {
        replyCursors.value[pid] = { vrd_id: item.vrd_id }
      }
      // 重新计算是否还有更多未加载
      replyHasMore.value[pid] = (parent?.reply_count ?? 0) > repliesMap.value[pid].length
    } else {
      comments.value = [item, ...comments.value]
    }
    commentText.value = ''
    replyTarget.value = null
  } catch (e) {
    postingError.value = e instanceof Error ? e.message : String(e)
  } finally {
    posting.value = false
  }
}

// 视频追番/取消追番（toggle，乐观更新 + 失败回滚）
async function toggleFollow() {
  if (!videoDetail.value) return
  const prev = videoDetail.value.is_followed
  // 乐观更新
  videoDetail.value.is_followed = !prev
  try {
    const res = await followVideo(videoId.value)
    videoDetail.value.is_followed = res.is_followed
  } catch {
    // 回滚
    videoDetail.value.is_followed = prev
  }
}

// 展开 / 加载更多回复（预览已默认展开，此按钮用于加载剩余回复）
function toggleReplies(comment: DiscussItem) {
  const id = comment.vrd_id
  if (replyLoadingMore.value[id] || !replyHasMore.value[id]) return
  void loadReplies(comment, false)
}

// 加载回复：firstPage=true 初始化，否则追加下一页
async function loadReplies(comment: DiscussItem, firstPage: boolean) {
  if (!currentEpisode.value) return
  const id = comment.vrd_id
  if (firstPage) {
    loadingRepliesId.value = id
  } else {
    replyLoadingMore.value[id] = true
  }
  try {
    const cursor = replyCursors.value[id]
    const req: ListDiscussRequest = {
      ve_id: currentEpisode.value.ve_id,
      parent_id: id,
      sort: 'latest',
      cursor_vrd_id: firstPage ? 0 : (cursor?.vrd_id ?? 0),
      limit: REPLY_PAGE_SIZE,
    }
    const list = await listDiscuss(req)
    if (firstPage) {
      repliesMap.value[id] = list
    } else {
      // 合并去重并按 vrd_id 升序（避免发新回复后追加与加载更多产生重复/乱序）
      const merged = [...(repliesMap.value[id] ?? []), ...list]
      const seen = new Set<number>()
      repliesMap.value[id] = merged
        .filter(r => {
          if (seen.has(r.vrd_id)) return false
          seen.add(r.vrd_id)
          return true
        })
        .sort((a, b) => a.vrd_id - b.vrd_id)
    }
    replyHasMore.value[id] = list.length >= REPLY_PAGE_SIZE
    if (list.length > 0) {
      const last = list[list.length - 1]
      replyCursors.value[id] = { vrd_id: last.vrd_id }
    } else {
      replyCursors.value[id] = null
    }
  } catch {
    if (firstPage) repliesMap.value[id] = []
    replyHasMore.value[id] = false
  } finally {
    if (firstPage) {
      loadingRepliesId.value = null
    } else {
      replyLoadingMore.value[id] = false
    }
  }
}

// 回复触底加载
function onReplyListScroll(comment: DiscussItem, e: Event) {
  const id = comment.vrd_id
  if (!replyHasMore.value[id] || replyLoadingMore.value[id]) return
  const el = e.target as HTMLElement
  if (el.scrollHeight - el.scrollTop - el.clientHeight < 30) {
    void loadReplies(comment, false)
  }
}

// 设置回复目标
// comment: 顶级评论（parent_id 取它）
// replyItem: 可选，被回复的回复项；为 null 表示回复楼主
function setReplyTarget(comment: DiscussItem, replyItem?: DiscussItem | null) {
  replyTarget.value = {
    parentId: comment.vrd_id,
  }
  nextTick(() => commentInputRef.value?.focus())
}

// 取消回复，回到顶级评论
function clearReplyTarget() {
  replyTarget.value = null
}

// 插入 emoji 到评论输入框当前光标位置
function insertEmoji(e: string) {
  const inst = commentInputRef.value
  const el = inst?.textareaElRef
  if (!el) {
    // 兜底：直接追加到末尾
    commentText.value += e
    return
  }
  const start = el.selectionStart ?? commentText.value.length
  const end = el.selectionEnd ?? commentText.value.length
  commentText.value =
    commentText.value.slice(0, start) + e + commentText.value.slice(end)
  // 恢复光标到插入后位置
  nextTick(() => {
    const pos = start + e.length
    el.focus()
    el.setSelectionRange(pos, pos)
  })
}

// 切换表情面板开合
function toggleEmojiPanel() {
  showEmojiPanel.value = !showEmojiPanel.value
}

// 点击外部关闭表情面板
function onEmojiPanelDocumentClick(e: MouseEvent) {
  if (!showEmojiPanel.value) return
  const target = e.target as Node
  if (emojiPanelRef.value && !emojiPanelRef.value.contains(target)) {
    showEmojiPanel.value = false
  }
}

// 上一集 / 下一集
function handlePrev() {
  if (!hasPrev.value) return
  void selectEpisode(episodes.value[currentEpisodeIndex.value - 1])
}

function handleNext() {
  if (!hasNext.value) return
  void selectEpisode(episodes.value[currentEpisodeIndex.value + 1])
}

// 播放结束：自动切下一集
function handleEnded() {
  // 看完：上报 0（下次从头播）
  reportProgress(0)
  if (hasNext.value) handleNext()
}

// ============ 播放历史（续播 + 节流上报）============
const HISTORY_REPORT_INTERVAL = 10 // 上报间隔(秒)
const HISTORY_WATCHED_THRESHOLD = 0.9 // 进度≥90%视为已看完

const pendingResumeTime = ref(0) // 待 seek 的续播时间（ready 时消费）
const historyReady = ref(false) // 续播进度是否就绪（未就绪时不渲染播放器）
const lastReportTime = ref(0) // 上次上报的秒数
const lastReportAt = ref(0) // 上次上报的 Date.now()
const playerCurrentTime = ref(0) // 播放器当前时间（timeupdate 同步）
const viewCountReported = ref(false) // 本集播放量是否已上报（累计观看≥60s 触发，切集重置）
const viewAccumulatedMs = ref(0) // 本集累计观看毫秒数（仅播放中累加，暂停不计）
const viewLastTickAt = ref(0) // 上次 timeupdate 时间戳（0=未计时）
const playerDuration = ref(0) // 播放器总时长（ready 时同步）

// 播放器就绪：记录时长/当前位置，重置累计计时（新视频开始重新计时）
function handlePlayerReady() {
  const v = document.querySelector<HTMLVideoElement>('.player video')
  if (!v) return
  playerDuration.value = v.duration || 0
  playerCurrentTime.value = v.currentTime
  lastReportTime.value = Math.floor(v.currentTime)
  viewLastTickAt.value = 0
}

// 节流上报：距离上次上报 ≥10s 才发；同时累计观看时长，达到 60s 触发播放量上报
function handleTimeUpdate(t: number) {
  playerCurrentTime.value = t
  const now = Date.now()
  const sec = Math.floor(t)
  if (sec - lastReportTime.value >= HISTORY_REPORT_INTERVAL && now - lastReportAt.value > 5000) {
    reportProgress(sec)
  }
  // 累计真实观看时长（暂停期间 viewLastTickAt=0 不累加）
  if (viewLastTickAt.value > 0) {
    viewAccumulatedMs.value += now - viewLastTickAt.value
  }
  viewLastTickAt.value = now
  if (!viewCountReported.value && viewAccumulatedMs.value >= 60000 && videoId.value) {
    viewCountReported.value = true
    void incrementViewCount(videoId.value).catch(() => {})
  }
}

// 暂停时立即上报；停止累计观看时长（暂停期间不计入）
function handlePause() {
  viewLastTickAt.value = 0
  reportProgress(Math.floor(playerCurrentTime.value))
}

// 上报进度（90%判断：看完传0）
function reportProgress(sec: number) {
  if (!currentEpisode.value || !videoId.value) return
  const dur = playerDuration.value
  // 已知时长且进度≥90%：视为已看完
  const isFinished = dur > 0 && sec / dur >= HISTORY_WATCHED_THRESHOLD ? 1 : 0
  // 续播进度：看完传 0（保持原逻辑），未看完传当前秒数
  const historyTime = isFinished === 1 ? 0 : Math.max(0, sec)
  // 实际观看时长：看完传视频总时长，未看完传当前秒数
  const watchDuration = isFinished === 1 ? Math.floor(dur) : Math.max(0, sec)
  // 防抖：与上次相同则不发
  if (historyTime === lastReportTime.value && historyTime !== 0) return
  lastReportTime.value = historyTime
  lastReportAt.value = Date.now()
  void reportHistory({
    vr_id: videoId.value,
    ve_id: currentEpisode.value.ve_id,
    vuh_history_time: historyTime,
    vuh_duration: watchDuration,
    finished: isFinished,
  }).catch(() => {
    // 上报失败静默，不打断播放
  })
}

// 切换集/卸载前 flush 最后一次进度
function flushHistory() {
  if (!currentEpisode.value || playerCurrentTime.value <= 0) return
  reportProgress(Math.floor(playerCurrentTime.value))
}

onBeforeUnmount(() => {
  flushHistory()
  document.removeEventListener('click', onEmojiPanelDocumentClick)
})

onMounted(async () => {
  // 读取 videoId（路由参数为 string，转为 number）
  const rawId = route.params.videoId
  const idStr = Array.isArray(rawId) ? (rawId[0] ?? '') : (rawId ?? '')
  const id = Number(idStr)
  videoId.value = id

  // 表情面板点击外部关闭
  document.addEventListener('click', onEmojiPanelDocumentClick)

  // 读取可选元数据
  videoMeta.title = readQuery('title')
  videoMeta.cover = readQuery('cover')
  videoMeta.category = readQuery('category')
  videoMeta.year = readQuery('year')
  videoMeta.season = readQuery('season')

  if (!id) {
    error.value = '无效的视频 ID'
    syncWindowTitle()
    return
  }

  loading.value = true
  error.value = null
  detailLoading.value = true
  detailError.value = null
  try {
    episodes.value = await listEpisodes(id)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }

  // 并行加载视频简介（不阻塞分集选择）
  getVideoDetail(id)
    .then((d) => {
      videoDetail.value = d
    })
    .catch((e) => {
      detailError.value = e instanceof Error ? e.message : String(e)
    })
    .finally(() => {
      detailLoading.value = false
    })

  syncWindowTitle()

  // 默认选择第一集
  if (episodes.value.length > 0) {
    await selectEpisode(episodes.value[0])
  }
})
</script>

<template>
  <div class="preview">
    <!-- 左侧播放区 -->
    <div class="player-area">
      <div class="player">
        <VideoPlayer
          v-if="streamUrl && !streamError && historyReady"
          :src="streamUrl"
          :title="playerTitle"
          :poster="videoMeta.cover"
          :has-prev="hasPrev"
          :has-next="hasNext"
          :danmaku="danmakuList"
          :resume-time="pendingResumeTime"
          v-model:danmaku-enabled="danmakuEnabled"
          auto-play
          @ready="handlePlayerReady"
          @timeupdate="handleTimeUpdate"
          @pause="handlePause"
          @ended="handleEnded"
          @prev="handlePrev"
          @next="handleNext"
          @send-danmaku="handleSendDanmaku"
        />
        <div v-else-if="loading || streamLoading || !historyReady" class="player__state">
          <Spinner :size="48" />
        </div>
        <div v-else-if="error" class="player__state">
          <StatusHint variant="error" title="分集加载失败" :description="error" />
        </div>
        <div v-else-if="streamError" class="player__state">
          <StatusHint variant="error" title="视频流加载失败" :description="streamError" />
        </div>
        <div v-else-if="episodes.length === 0" class="player__state">
          <StatusHint variant="info" title="暂无分集可播放" />
        </div>
      </div>
    </div>

    <!-- 右侧边栏 -->
    <aside class="sidebar">
      <!-- Tab 头 -->
      <div class="sidebar__tabs">
        <button
          type="button"
          class="sidebar__tab"
          :class="{ 'is-active': activeTab === 'detail' }"
          @click="activeTab = 'detail'"
        >
          详情
        </button>
        <button
          type="button"
          class="sidebar__tab"
          :class="{ 'is-active': activeTab === 'comment' }"
          @click="activeTab = 'comment'"
        >
          评论<span v-if="commentCount > 0" class="sidebar__tab-count">({{ commentCount }})</span>
        </button>
      </div>

      <!-- 详情面板：视频信息 + 分集列表 -->
      <div v-show="activeTab === 'detail'" class="sidebar-panel">
        <div class="info-panel">
          <h2 class="info-panel__title">{{ videoMeta.title || '未命名视频' }}</h2>
          <div
            v-if="videoMeta.category || videoMeta.year || videoMeta.season"
            class="info-panel__tags"
          >
            <span v-if="videoMeta.category" class="info-panel__tag">{{ categoryLabel }}</span>
            <span v-if="videoMeta.year" class="info-panel__tag">{{ videoMeta.year }}</span>
            <span v-if="videoMeta.season" class="info-panel__tag">{{ seasonLabel }}</span>
          </div>

          <!-- 操作按钮行：追番 -->
          <div class="info-panel__actions">
            <button
              type="button"
              class="action-btn"
              :class="{ 'is-active': videoDetail?.is_followed }"
              :disabled="!videoDetail"
              @click="toggleFollow"
            >
              <svg class="action-btn__icon" viewBox="0 0 24 24" width="16" height="16">
                <path
                  :fill="videoDetail?.is_followed ? 'var(--accent-sakura)' : 'none'"
                  :stroke="videoDetail?.is_followed ? 'var(--accent-sakura)' : 'currentColor'"
                  stroke-width="2"
                  d="M19 21l-7-5-7 5V5a2 2 0 012-2h10a2 2 0 012 2v16z"
                />
              </svg>
              <span>{{ videoDetail?.is_followed ? '已追番' : '追番' }}</span>
            </button>
          </div>

          <!-- 统计行：播放 + 状态 -->
          <div v-if="videoDetail || detailLoading" class="info-panel__stats">
            <span v-if="detailLoading" class="info-panel__stat">加载中...</span>
            <template v-else-if="videoDetail">
              <span class="info-panel__stat">播放 {{ formatViewCount(videoDetail.vr_view_count) }}</span>
              <span v-if="statusText" class="info-panel__stat info-panel__stat--status">{{ statusText }}</span>
            </template>
          </div>

          <!-- 描述 -->
          <p
            v-if="videoDetail && videoDetail.vr_desc"
            class="info-panel__desc"
          >{{ videoDetail.vr_desc }}</p>
          <p
            v-else-if="detailError"
            class="info-panel__desc info-panel__desc--error"
          >简介加载失败</p>
        </div>

        <div ref="episodeListRef" class="episode-list">
          <div v-if="loading" class="episode-list__state">
            <Spinner :size="28" />
          </div>
          <div v-else-if="error" class="episode-list__state">
            <StatusHint variant="error" size="sm" title="加载失败" :description="error" />
          </div>
          <div v-else-if="episodes.length === 0" class="episode-list__empty">暂无分集</div>
          <template v-else>
            <div
              v-for="ep in episodes"
              :key="ep.ve_id"
              class="episode-list__item"
              :class="[
                episodeLenClass(ep),
                {
                  'is-current': currentEpisode?.ve_id === ep.ve_id,
                  'is-invalid': ep.ve_status === 2,
                },
              ]"
              :data-current="
                currentEpisode?.ve_id === ep.ve_id ? '' : undefined
              "
              @click="selectEpisode(ep)"
            >
              <span class="episode-list__item-label">{{ episodeDisplayText(ep) }}</span>
              <span v-if="ep.ve_status === 2" class="episode-list__item-status">失效</span>
            </div>
          </template>
        </div>
      </div>

      <!-- 评论面板：评论列表 + 输入框 -->
      <div v-show="activeTab === 'comment'" class="sidebar-panel sidebar-panel--comment">
        <!-- 排序切换 -->
        <div class="comment-toolbar">
          <button
            type="button"
            class="comment-toolbar__btn"
            :class="{ 'is-active': commentSort === 'latest' }"
            @click="changeCommentSort('latest')"
          >最新</button>
          <button
            type="button"
            class="comment-toolbar__btn"
            :class="{ 'is-active': commentSort === 'hot' }"
            @click="changeCommentSort('hot')"
          >热门</button>
        </div>

        <div class="comment-list" @scroll="onCommentListScroll">
          <div v-if="comments.length === 0 && commentLoading" class="comment-list__empty">
            <Spinner :size="24" />
          </div>
          <div v-else-if="comments.length === 0" class="comment-list__empty">暂无评论</div>
          <div v-for="c in comments" :key="c.vrd_id" class="comment">
            <div class="comment__body">
              <div class="comment__head">
                <span class="comment__time">{{ formatRelativeTime(c.create_time) }}</span>
              </div>
              <p class="comment__content">{{ c.vrd_content }}</p>
              <div class="comment__actions">
                <button
                  v-if="replyHasMore[c.vrd_id]"
                  type="button"
                  class="comment__action comment__action--btn"
                  :disabled="replyLoadingMore[c.vrd_id]"
                  @click="toggleReplies(c)"
                >
                  <MessageCircle class="comment__action-icon" />
                  <span>展开剩余{{ c.reply_count - (repliesMap[c.vrd_id]?.length ?? 0) }}条回复</span>
                </button>
                <button
                  type="button"
                  class="comment__action comment__action--btn"
                  @click="setReplyTarget(c)"
                >
                  回复
                </button>
              </div>

              <!-- 回复列表 -->
              <div v-if="expandedIds.has(c.vrd_id)" class="comment__replies">
                <div v-if="loadingRepliesId === c.vrd_id" class="comment__replies-loading">
                  <Spinner :size="18" />
                </div>
                <template v-else>
                  <div class="comment__replies-scroll" @scroll="onReplyListScroll(c, $event)">
                    <div
                      v-for="r in repliesMap[c.vrd_id] || []"
                      :key="r.vrd_id"
                      class="reply"
                    >
                      <div class="reply__body">
                        <div class="reply__head">
                          <span class="reply__time">{{ formatRelativeTime(r.create_time) }}</span>
                        </div>
                        <p class="reply__content">{{ r.vrd_content }}</p>
                        <div class="reply__actions">
                          <button
                            type="button"
                            class="comment__action comment__action--btn"
                            @click="setReplyTarget(c, r)"
                          >
                            回复
                          </button>
                        </div>
                      </div>
                    </div>
                    <div
                      v-if="(repliesMap[c.vrd_id] || []).length === 0"
                      class="comment__replies-empty"
                    >
                      暂无回复
                    </div>
                    <div v-if="replyLoadingMore[c.vrd_id]" class="comment__replies-loading">
                      <Spinner :size="14" />
                    </div>
                  </div>
                </template>
              </div>
            </div>
          </div>
          <!-- 顶级评论触底加载提示 -->
          <div v-if="commentLoading && comments.length > 0" class="comment-list__more">
            <Spinner :size="18" />
          </div>
        </div>

        <!-- 评论输入区 -->
        <div class="comment-input">
          <!-- 表情面板（悬浮在输入框上方，左对齐） -->
          <div
            v-if="showEmojiPanel"
            ref="emojiPanelRef"
            class="comment-input__emoji-panel"
          >
            <div class="comment-input__emoji-grid">
              <button
                v-for="(e, i) in emoji"
                :key="i"
                type="button"
                class="comment-input__emoji-item"
                @click="insertEmoji(e)"
              >
                {{ e }}
              </button>
            </div>
          </div>
          <div v-if="replyTarget" class="comment-input__banner">
            <span class="comment-input__banner-text">回复评论</span>
            <button
              type="button"
              class="comment-input__cancel"
              @click="clearReplyTarget()"
            >
              取消
            </button>
          </div>
          <n-input
            ref="commentInputRef"
            v-model:value="commentText"
            type="textarea"
            :placeholder="inputPlaceholder"
            :autosize="{ minRows: 2, maxRows: 6 }"
            :disabled="posting"
          />
          <div class="comment-input__bar">
            <div class="comment-input__bar-left">
              <button
                type="button"
                class="comment-input__emoji-btn"
                :class="{ 'is-active': showEmojiPanel }"
                :disabled="posting"
                aria-label="表情"
                @click.stop="toggleEmojiPanel"
              >
                <MoodSmile />
              </button>
              <span v-if="postingError" class="comment-input__error">{{ postingError }}</span>
            </div>
            <n-button
              type="primary"
              :loading="posting"
              :disabled="!commentText.trim()"
              @click="submitComment"
            >
              <template #icon>
                <Send />
              </template>
              {{ posting ? '发送中' : '发送' }}
            </n-button>
          </div>
        </div>
      </div>
    </aside>
  </div>
</template>

<style scoped>
/* -- 根容器：水平 flex，左侧播放区 + 右侧边栏，禁止页面级滚动 -- */
.preview {
  display: flex;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: var(--bg-elevated);
}

/* -- 左侧播放区：垂直 flex -- */
.player-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

/* VideoPlayer 容器：填满剩余空间，黑底 */
.player {
  position: relative;
  flex: 1;
  overflow: hidden;
  background: #000;
  min-height: 0;
  border-radius: 10px;

}

/* 加载 / 错误态：绝对定位居中 */
.player__state {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* -- 右侧边栏：固定 360px -- */
.sidebar {
  width: 360px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-elevated);
}

/* -- Tab 头 -- */
.sidebar__tabs {
  flex-shrink: 0;
  display: flex;
  border-bottom: 1px solid var(--border-subtle);
}

.sidebar__tab {
  flex: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: var(--space-3) var(--space-2);
  border: none;
  background: transparent;
  font-family: var(--font-display);
  font-size: 14px;
  font-weight: 600;
  color: var(--text-secondary);
  cursor: pointer;
  user-select: none;
  position: relative;
  transition: color var(--duration-fast) var(--ease-fluid);
}

.sidebar__tab:hover {
  color: var(--text-primary);
}

.sidebar__tab.is-active {
  color: var(--accent-sakura);
}

/* 当前 Tab 底部樱花粉强调条 */
.sidebar__tab.is-active::after {
  content: '';
  position: absolute;
  left: 20%;
  right: 20%;
  bottom: -1px;
  height: 2px;
  background: var(--accent-sakura);
  border-radius: var(--radius-full);
}

.sidebar__tab-count {
  font-family: var(--font-body);
  font-size: 12px;
  font-weight: 500;
}

/* -- 面板：占满剩余空间 -- */
.sidebar-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}

/* -- 视频信息面板 -- */
.info-panel {
  flex-shrink: 0;
  padding: var(--space-4);
  border-bottom: 1px solid var(--border-subtle);
}

.info-panel__title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 18px;
  font-weight: 700;
  line-height: 1.3;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.info-panel__tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.info-panel__tag {
  display: inline-flex;
  align-items: center;
  padding: 2px var(--space-2);
  font-family: var(--font-body);
  font-size: 11px;
  color: var(--text-secondary);
  background: var(--menu-hover-bg);
  border-radius: var(--radius-sm);
}

.info-panel__stats {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.info-panel__stat {
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-secondary);
}

.info-panel__stat--status {
  color: var(--accent-sakura);
}

/* 操作按钮行：追番 / 点赞 */
.info-panel__actions {
  display: flex;
  gap: var(--space-2);
  margin: var(--space-3) 0;
}

.action-btn {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-3);
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-secondary);
  background: var(--menu-hover-bg);
  border: 1px solid transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-fluid);
}

.action-btn:hover:not(:disabled) {
  background: var(--bg-card);
  border-color: var(--accent-sakura);
  color: var(--accent-sakura);
}

.action-btn.is-active {
  color: var(--accent-sakura);
  border-color: var(--accent-sakura);
  background: rgba(255, 183, 197, 0.1);
}

.action-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.action-btn__icon {
  flex-shrink: 0;
}

.info-panel__desc {
  margin: var(--space-2) 0 0;
  font-family: var(--font-body);
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.info-panel__desc--error {
  color: var(--color-error);
}

/* -- 分集列表：grid 正方形，可滚动 -- */
.episode-list {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  padding: var(--space-2);
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-2);
  align-content: start;
}

.episode-list__state,
.episode-list__empty {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-6) var(--space-4);
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-secondary);
}

.episode-list__item {
  position: relative;
  aspect-ratio: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-sm);
  background: var(--menu-hover-bg);
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid);
}

.episode-list__item:hover {
  background: color-mix(in srgb, var(--accent-sakura) 18%, transparent);
}

.episode-list__item.is-current {
  background: var(--accent-sakura);
}

.episode-list__item.is-current:hover {
  background: var(--accent-sakura-hover);
}

.episode-list__item-label {
  font-family: var(--font-display);
  font-weight: 600;
  color: var(--text-primary);
  line-height: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 100%;
  padding: 0 2px;
}

/* 字号自适应：位数越多越小 */
.episode-list__item.is-len1 .episode-list__item-label {
  font-size: 18px;
}

.episode-list__item.is-len2 .episode-list__item-label {
  font-size: 16px;
}

.episode-list__item.is-len3 .episode-list__item-label {
  font-size: 13px;
}

.episode-list__item.is-len4 .episode-list__item-label {
  font-size: 11px;
}

.episode-list__item.is-current .episode-list__item-label {
  color: #fff;
}

.episode-list__item.is-invalid {
  opacity: 0.5;
}

.episode-list__item-status {
  position: absolute;
  right: 2px;
  bottom: 2px;
  padding: 1px 3px;
  font-family: var(--font-body);
  font-size: 9px;
  line-height: 1;
  color: var(--color-error);
  background: color-mix(in srgb, var(--color-error) 14%, transparent);
  border-radius: var(--radius-sm);
}

/* -- 评论列表：可滚动 -- */
.comment-toolbar {
  flex-shrink: 0;
  display: flex;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--border-subtle);
}

.comment-toolbar__btn {
  padding: 2px var(--space-2);
  border: none;
  background: transparent;
  font-family: var(--font-body);
  font-size: 12px;
  font-weight: 500;
  color: var(--text-secondary);
  cursor: pointer;
  border-radius: var(--radius-sm);
  transition: all var(--duration-fast) var(--ease-fluid);
}

.comment-toolbar__btn:hover {
  color: var(--text-primary);
}

.comment-toolbar__btn.is-active {
  color: var(--accent-sakura);
  background: var(--menu-active-bg);
}

.comment-list {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  padding: var(--space-2);
}

.comment-list__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-8) var(--space-4);
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-secondary);
}

.comment-list__more {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-3);
}

.comment {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-2);
  border-radius: var(--radius-sm);
  transition: background-color var(--duration-fast) var(--ease-fluid);
}

.comment:hover {
  background: var(--menu-hover-bg);
}

.comment__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.comment__head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.comment__time {
  flex-shrink: 0;
  font-family: var(--font-body);
  font-size: 11px;
  color: var(--text-tertiary);
}

.comment__content {
  margin: 0;
  font-family: var(--font-body);
  font-size: 13px;
  line-height: 1.5;
  color: var(--text-primary);
  word-break: break-word;
}

.comment__actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-1);
}

.comment__action {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-secondary);
}

.comment__action--btn {
  padding: 0;
  border: none;
  background: transparent;
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-fluid);
}

.comment__action--btn:hover {
  color: var(--accent-sakura);
}

.comment__action-icon {
  width: 14px;
  height: 14px;
  display: block;
}

/* 回复列表 */
.comment__replies {
  margin-top: var(--space-2);
  padding-left: var(--space-3);
  border-left: 2px solid var(--border-subtle);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.comment__replies-scroll {
  max-height: 300px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.comment__replies-loading,
.comment__replies-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-2);
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-tertiary);
}

.reply {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
}

.reply__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.reply__head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.reply__time {
  flex-shrink: 0;
  margin-left: auto;
  font-family: var(--font-body);
  font-size: 11px;
  color: var(--text-tertiary);
}

.reply__actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-top: 2px;
}

.reply__content {
  margin: 0;
  font-family: var(--font-body);
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-primary);
  word-break: break-word;
}

/* -- 评论输入区 -- */
.comment-input {
  position: relative;
  flex-shrink: 0;
  padding: var(--space-3);
  border-top: 1px solid var(--border-subtle);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.comment-input__banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-1) var(--space-2);
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--accent-sakura);
  background: var(--menu-active-bg);
  border-radius: var(--radius-sm);
}

.comment-input__banner-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.comment-input__cancel {
  flex-shrink: 0;
  padding: 0;
  border: none;
  background: transparent;
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-secondary);
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-fluid);
}

.comment-input__cancel:hover {
  color: var(--text-primary);
}

.comment-input__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.comment-input__bar-left {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
}

.comment-input__error {
  flex: 1;
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--color-error);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 表情按钮 */
.comment-input__emoji-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-fluid), background var(--duration-fast) var(--ease-fluid);
}

.comment-input__emoji-btn:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--menu-hover-bg);
}

.comment-input__emoji-btn.is-active {
  color: var(--accent-sakura);
  background: var(--menu-active-bg);
}

.comment-input__emoji-btn:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.comment-input__emoji-btn svg {
  width: 18px;
  height: 18px;
}

/* 表情面板容器：悬浮于输入框上方，左对齐 */
.comment-input__emoji-panel {
  position: absolute;
  left: var(--space-3);
  bottom: calc(100% + 4px);
  z-index: 10;
  padding: 6px;
  width: 280px;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.3);
}

/* 表情网格面板 */
.comment-input__emoji-grid {
  display: grid;
  grid-template-columns: repeat(10, 1fr);
  gap: 2px;
  max-height: 200px;
  overflow-x: hidden;
  overflow-y: auto;
}

.comment-input__emoji-item {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  min-width: 0;
  aspect-ratio: 1;
  padding: 0;
  border: none;
  background: transparent;
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
  border-radius: var(--radius-sm);
  transition: background var(--duration-fast) var(--ease-fluid);
}

.comment-input__emoji-item:hover {
  background: var(--menu-hover-bg);
}

/* 无障碍：减少动效偏好 */
@media (prefers-reduced-motion: reduce) {
  .episode-list__item,
  .comment,
  .comment__action--btn,
  .comment-input__cancel {
    transition: none;
  }
}
</style>
