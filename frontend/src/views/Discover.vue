<script setup lang="ts">
import { ref, onMounted, computed, h } from 'vue'
import {
  Search,
  Upload,
  Filter,
  Trash,
  CircleCheck,
  Plus,
  Edit,
  PlayerPlay,
  Star,
} from '@vicons/tabler'
import { useDialog, useMessage, NTag } from 'naive-ui'
import { Browser } from '@wailsio/runtime'
import { windowApi } from '../services/window'
import Spinner from '../components/common/Ui/Spinner.vue'
import StatusHint from '../components/common/Ui/StatusHint.vue'
import IconButton from '../components/common/Ui/IconButton.vue'
import BaseImage from '../components/common/Ui/BaseImage.vue'
import {
  listLocalDialogs,
  refreshDialogs,
  searchChannelMessages,
  searchVideos,
  getBaseInfo,
  createVideoRepo,
  uploadEpisodes,
  type LocalDialog,
  type RecentMessage,
  type VideoSearchItem,
  type BaseInfo,
  type CreateVideoRepoRequest,
  type EpisodeUploadItem,
} from '../services/repo'
import {
  browseSubjects,
  getSubject,
} from '../services/bangumi'
import type { BangumiSubject, BangumiBrowseRequest } from '../../bindings/tg-download/internal/api/models.js'
import { BangumiSubjectType } from '../../bindings/tg-download/internal/api/models.js'

const dialog = useDialog()
const message = useMessage()

// ===================== 频道（左侧下拉） =====================
const dialogs = ref<LocalDialog[]>([])
const selectedDialog = ref<LocalDialog | null>(null)
const loadingDialogs = ref(false)
let dialogSearchTimer: ReturnType<typeof setTimeout> | null = null

// ===================== bangumi 筛选 =====================
// 列表默认空，通过筛选弹窗输入条件后拉取
const subjects = ref<BangumiSubject[]>([])
const selectedSubject = ref<BangumiSubject | null>(null)
const loadingSubjects = ref(false)
const subjectsError = ref('')
// 筛选弹窗
const showFilterModal = ref(false)
const filterType = ref<number>(BangumiSubjectType.BangumiSubjectAnime) // 默认动画
const filterCat = ref<string>('') // 动画下：1=TV 2=OVA 3=Movie 5=WEB
const filterYear = ref<number | null>(null)
const filterMonth = ref<number | null>(null)
const filterSort = ref<string>('date') // date / rank
// bangumi access token：填入后存 localStorage，下次打开回填
// 不存后端，仅前端缓存，每次请求时透传给后端临时使用
const BANGUMI_TOKEN_KEY = 'bangumi-token'
const bangumiToken = ref<string>(
  localStorage.getItem(BANGUMI_TOKEN_KEY) || ''
)
// bangumi User-Agent：必填，格式 用户名/应用名，同 token 一样前端缓存回填
const BANGUMI_UA_KEY = 'bangumi-user-agent'
const bangumiUserAgent = ref<string>(
  localStorage.getItem(BANGUMI_UA_KEY) || ''
)
// 分页
const subjectsTotal = ref(0)
const subjectsOffset = ref(0)
const subjectsLimit = 30
const loadingMoreSubjects = ref(false)
// 左侧本地筛选关键字：实时过滤已拉取的 subjects（不调接口）
const subjectSearchKeyword = ref('')

// 计算属性：按本地关键字过滤后的条目列表
const filteredSubjects = computed(() => {
  const kw = subjectSearchKeyword.value.trim().toLowerCase()
  if (!kw) return subjects.value
  return subjects.value.filter((s) => {
    return (
      (s.name_cn || '').toLowerCase().includes(kw) ||
      (s.name || '').toLowerCase().includes(kw)
    )
  })
})

// bangumi 类型选项
const TYPE_OPTIONS = [
  { label: '动画', value: BangumiSubjectType.BangumiSubjectAnime },
  { label: '三次元', value: BangumiSubjectType.BangumiSubjectReal },
  { label: '书籍', value: BangumiSubjectType.BangumiSubjectBook },
  { label: '游戏', value: BangumiSubjectType.BangumiSubjectGame },
  { label: '音乐', value: BangumiSubjectType.BangumiSubjectMusic },
]
// 动画分类（随 type 变化）
const ANIME_CAT_OPTIONS = [
  { label: 'TV', value: '1' },
  { label: 'OVA', value: '2' },
  { label: 'Movie', value: '3' },
  { label: 'WEB', value: '5' },
]

// ===================== 右侧消息检索（复用 Repo 逻辑） =====================
const messageKeyword = ref('')
const messages = ref<RecentMessage[]>([])
const loadingMessages = ref(false)
const loadedCount = ref(0)
const loadingDone = ref(false)
let cancelSearch = false
const checkedIds = ref<Set<number>>(new Set())
const messageError = ref('')

// 二次筛选（与 Repo 一致）
const showMsgFilterModal = ref(false)
const filterKeywords = ref('')
const filterMatchMode = ref<'all' | 'any'>('all')
const appliedFilter = ref<{ keywords: string[]; mode: 'all' | 'any' } | null>(null)

// ===================== 上传分集弹窗 =====================
const showUploadModal = ref(false)
const videoList = ref<VideoSearchItem[]>([])
const loadingVideos = ref(false)
const selectedVideoId = ref<number | null>(null)
const uploadingEpisodes = ref(false)
let videoSearchTimer: ReturnType<typeof setTimeout> | null = null

// ===================== 新建视频源弹窗 =====================
const showCreateModal = ref(false)
const creating = ref(false)
const baseInfo = ref<BaseInfo | null>(null)
const loadingBaseInfo = ref(false)
const form = ref<CreateVideoRepoRequest>({
  vr_name: '',
  vr_category: '',
  vr_desc: '',
  vr_cover: '',
  vr_episode_count: 0,
  vr_year: new Date().getFullYear(),
  vr_season: '',
  vr_status: 3,
  vr_language: undefined,
  vr_bgm_id: 0,
  tags: [],
})

const CATEGORY_OPTIONS = [
  { label: '动漫', value: 'anime' },
  { label: '电影', value: 'movie' },
  { label: '电视剧', value: 'tv' },
  { label: 'OVA', value: 'ova' },
  { label: '特别篇', value: 'special' },
]
const SEASON_OPTIONS = [
  { label: '冬', value: 'winter' },
  { label: '春', value: 'spring' },
  { label: '夏', value: 'summer' },
  { label: '秋', value: 'autumn' },
]

// 一键播放 payload 在 localStorage 的 key
const REPO_PLAYER_PAYLOAD_KEY = 'repo-player-payload'

// ===================== 计算属性 =====================
const canSearchMessages = computed(
  () => !!selectedDialog.value && !!messageKeyword.value.trim() && !loadingMessages.value
)

const filterActive = computed(() => !!appliedFilter.value)

const checkedCount = computed(() => checkedIds.value.size)

const canBatchDelete = computed(() => checkedCount.value > 0 && !loadingMessages.value)

const canUploadEpisodes = computed(
  () => messages.value.length > 0
    && !loadingMessages.value
    && messages.value.some((m) => m.ve_collect && m.ve_collect > 0)
)

const canPlay = computed(
  () => messages.value.length > 0 && !loadingMessages.value
)

const hasMoreSubjects = computed(
  () => subjectsOffset.value + subjects.value.length < subjectsTotal.value
)

// ===================== 工具函数 =====================
function errMsg(e: unknown, fallback: string): string {
  if (e instanceof Error) return e.message || fallback
  return fallback
}

// 打开 bangumi access token 获取页（系统默认浏览器）
function openAccessTokenPage() {
  Browser.OpenURL('https://next.bgm.tv/demo/access-token')
}

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatDate(date: string): string {
  if (!date) return ''
  const d = new Date(date)
  if (isNaN(d.getTime())) return date
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function messageTitle(msg: RecentMessage): string {
  if (msg.file_name) return msg.file_name
  return msg.message_text.slice(0, 50) || '(无标题)'
}

// 按月份推季度（项目自定义口径：1-3 春 / 4-6 夏 / 7-9 秋 / 10-12 冬）
function monthToSeason(month: number): string {
  if (month >= 1 && month <= 3) return 'spring'
  if (month >= 4 && month <= 6) return 'summer'
  if (month >= 7 && month <= 9) return 'autumn'
  return 'winter'
}

// bangumi type + platform -> 项目 category 映射
function mapCategory(subject: BangumiSubject): string {
  if (subject.type === BangumiSubjectType.BangumiSubjectAnime) return 'anime'
  if (subject.type === BangumiSubjectType.BangumiSubjectReal) {
    const p = (subject.platform || '').toLowerCase()
    if (p.includes('电影') || p.includes('movie')) return 'movie'
    if (p.includes('tv') || p.includes('剧')) return 'tv'
    return ''
  }
  return ''
}

// ===================== 频道下拉 =====================
async function loadDialogs(keyword?: string) {
  loadingDialogs.value = true
  try {
    if (!keyword) {
      try {
        await refreshDialogs()
      } catch (e) {
        console.error('[Discover] 刷新频道列表失败:', e)
      }
    }
    const list = await listLocalDialogs(keyword)
    dialogs.value = [...list].sort((a, b) => {
      if (a.is_repo_uploaded === b.is_repo_uploaded) return 0
      return a.is_repo_uploaded ? -1 : 1
    })
  } catch (e) {
    dialogs.value = []
  } finally {
    loadingDialogs.value = false
  }
}

function onDialogSearch(query: string) {
  if (dialogSearchTimer) clearTimeout(dialogSearchTimer)
  dialogSearchTimer = setTimeout(() => {
    loadDialogs(query)
  }, 300)
}

// 频道下拉项渲染：名称 + 已收录标志（仅 is_repo_uploaded 为 true 时显示）
// 布局：左侧频道名称（超出省略号），右侧固定「已收录」标签
function renderDialogLabel(option: { label: string; value: number }) {
  const d = dialogs.value.find((x) => x.peer_id === option.value)
  if (d?.is_repo_uploaded) {
    return h(
      'span',
      {
        style:
          'display: flex; align-items: center; gap: 8px; width: 100%; min-width: 0;',
      },
      [
        h(
          'span',
          {
            style:
              'flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;',
          },
          option.label,
        ),
        h(
          NTag,
          { size: 'small', type: 'success', bordered: false, style: 'flex: 0 0 auto;' },
          { default: () => '已收录' },
        ),
      ],
    )
  }
  return option.label
}

function onSelectDialog(value: number) {
  const d = dialogs.value.find((x) => x.peer_id === value)
  if (!d) return
  selectedDialog.value = d
  // 切换频道时重置右侧消息
  messageKeyword.value = ''
  messages.value = []
  messageError.value = ''
  loadedCount.value = 0
  loadingDone.value = false
  checkedIds.value = new Set()
}

// ===================== bangumi 筛选弹窗 =====================
function openFilterModal() {
  showFilterModal.value = true
}

function applyBangumiFilter() {
  if (!filterType.value) {
    message.warning('请选择条目类型')
    return
  }
  if (!bangumiToken.value.trim()) {
    message.warning('请填写 Access Token')
    return
  }
  if (!bangumiUserAgent.value.trim()) {
    message.warning('请填写 User-Agent')
    return
  }
  // token / UA 写入 localStorage（空值也写，表示清除）
  localStorage.setItem(BANGUMI_TOKEN_KEY, bangumiToken.value.trim())
  localStorage.setItem(BANGUMI_UA_KEY, bangumiUserAgent.value.trim())
  showFilterModal.value = false
  subjectsOffset.value = 0
  fetchSubjects(true)
}

function clearBangumiFilter() {
  filterCat.value = ''
  filterYear.value = null
  filterMonth.value = null
  filterSort.value = 'date'
  showFilterModal.value = false
  subjectsOffset.value = 0
  fetchSubjects(true)
}

// 拉取 bangumi 条目
async function fetchSubjects(reset = false) {
  if (loadingSubjects.value && reset) return
  loadingSubjects.value = true
  if (reset) {
    subjectsError.value = ''
    subjects.value = []
  }
  try {
    const result = await browseSubjects({
      Type: filterType.value,
      Cat: filterCat.value || '',
      Sort: filterSort.value,
      Year: filterYear.value || 0,
      Month: filterMonth.value || 0,
      Limit: subjectsLimit,
      Offset: subjectsOffset.value,
    } as BangumiBrowseRequest, bangumiToken.value.trim(), bangumiUserAgent.value.trim())
    if (reset) {
      subjects.value = result.data || []
    } else {
      subjects.value = [...subjects.value, ...(result.data || [])]
    }
    subjectsTotal.value = result.total || 0
  } catch (e) {
    subjectsError.value = errMsg(e, '拉取 bangumi 条目失败')
    if (reset) subjects.value = []
  } finally {
    loadingSubjects.value = false
    loadingMoreSubjects.value = false
  }
}

// 加载更多
function loadMoreSubjects() {
  if (!hasMoreSubjects.value || loadingMoreSubjects.value) return
  loadingMoreSubjects.value = true
  subjectsOffset.value += subjectsLimit
  fetchSubjects(false)
}

// 选中 bangumi 条目 -> 拉详情 + 联动右侧搜索
async function selectSubject(subject: BangumiSubject) {
  selectedSubject.value = subject
  // 先用列表数据填充搜索框（即时反馈），再异步拉详情补全元数据卡片
  const name = subject.name_cn || subject.name
  messageKeyword.value = name
  if (selectedDialog.value) {
    searchMessages()
  }
  // 异步拉详情
  try {
    const detail = await getSubject(subject.id, bangumiToken.value.trim(), bangumiUserAgent.value.trim())
    selectedSubject.value = detail
  } catch (e) {
    // 详情失败不影响列表选择，保留列表项数据
    console.error('[Discover] 拉取条目详情失败:', e)
  }
}

// ===================== 右侧消息检索（复用 Repo 逻辑） =====================
async function searchMessages() {
  if (!selectedDialog.value || !messageKeyword.value.trim()) return

  loadingMessages.value = true
  loadingDone.value = false
  cancelSearch = false
  messageError.value = ''
  messages.value = []
  loadedCount.value = 0
  checkedIds.value = new Set()

  const peerId = selectedDialog.value.peer_id
  const keyword = messageKeyword.value.trim()
  let offsetId = 0

  try {
    while (true) {
      if (cancelSearch) break
      const result = await searchChannelMessages(peerId, keyword, offsetId, 100)
      if (cancelSearch) break
      const batch = appliedFilter.value
        ? result.list.filter(matchFilter)
        : result.list
      if (batch.length > 0) {
        messages.value = [...messages.value, ...batch]
      }
      loadedCount.value += result.list.length
      if (result.next_offset_id === 0 || result.list.length === 0) {
        break
      }
      offsetId = result.next_offset_id
    }
  } catch (e) {
    if (!cancelSearch) {
      messageError.value = errMsg(e, '搜索消息失败')
    }
  } finally {
    loadingMessages.value = false
    loadingDone.value = !cancelSearch
    if (!cancelSearch && messages.value.length > 0) {
      sortMessagesByTitle()
      renumberEpisodes(1)
    }
  }
}

function sortMessagesByTitle() {
  messages.value = [...messages.value].sort((a, b) => {
    const ta = messageTitle(a).toLowerCase()
    const tb = messageTitle(b).toLowerCase()
    return ta.localeCompare(tb, 'zh-CN')
  })
}

function renumberEpisodes(start: number) {
  messages.value = messages.value.map((m, i) => {
    const collect = start + i
    const userCustomized = m.ve_title && m.ve_collect != null && m.ve_title !== defaultTitle(m.ve_collect)
    return {
      ...m,
      ve_collect: collect,
      ve_title: userCustomized ? m.ve_title : `第${collect}集`,
    }
  })
}

function defaultTitle(collect: number): string {
  return `第${collect}集`
}

// ===================== 拖动排序 =====================
const dragIndex = ref<number | null>(null)
const dragOverIndex = ref<number | null>(null)

function onDragStart(index: number) {
  dragIndex.value = index
}
function onDragOver(e: DragEvent, index: number) {
  e.preventDefault()
  if (dragIndex.value === null || dragIndex.value === index) {
    dragOverIndex.value = null
    return
  }
  dragOverIndex.value = index
}
function onDragLeave() {
  dragOverIndex.value = null
}
function onDrop() {
  if (dragIndex.value === null || dragOverIndex.value === null) {
    dragIndex.value = null
    dragOverIndex.value = null
    return
  }
  const from = dragIndex.value
  const to = dragOverIndex.value
  if (from === to) {
    dragIndex.value = null
    dragOverIndex.value = null
    return
  }
  const list = [...messages.value]
  const [moved] = list.splice(from, 1)
  list.splice(to, 0, moved)
  messages.value = list
  renumberEpisodes(1)
  dragIndex.value = null
  dragOverIndex.value = null
}
function onDragEnd() {
  dragIndex.value = null
  dragOverIndex.value = null
}

// ===================== 编辑分集标题 =====================
const showEditTitleModal = ref(false)
const editingMsg = ref<RecentMessage | null>(null)
const editingTitle = ref('')

function startEditTitle(msg: RecentMessage) {
  editingMsg.value = msg
  editingTitle.value = msg.ve_title || ''
  showEditTitleModal.value = true
}

function confirmEditTitle() {
  if (!editingMsg.value) return
  const title = editingTitle.value.trim()
  if (!title) {
    message.warning('标题不能为空')
    return
  }
  const idx = messages.value.findIndex((m) => m.message_id === editingMsg.value!.message_id)
  if (idx >= 0) {
    messages.value[idx] = { ...messages.value[idx], ve_title: title }
  }
  showEditTitleModal.value = false
  editingMsg.value = null
  editingTitle.value = ''
}

// ===================== 初始排序 =====================
const showRenumberModal = ref(false)
const renumberStart = ref(1)

function openRenumberModal() {
  if (messages.value.length === 0) {
    message.warning('当前没有可排序的消息')
    return
  }
  renumberStart.value = 1
  showRenumberModal.value = true
}

function confirmRenumber() {
  if (renumberStart.value < 0) {
    message.warning('起始集数不能为负数')
    return
  }
  renumberEpisodes(renumberStart.value)
  message.success(`已从第 ${renumberStart.value} 集开始重新编号`)
  showRenumberModal.value = false
}

function onSearchClick() {
  searchMessages()
}

function cancelSearchLoad() {
  cancelSearch = true
}

// ===================== 勾选 / 删除 =====================
function toggleCheck(msgId: number) {
  const next = new Set(checkedIds.value)
  if (next.has(msgId)) {
    next.delete(msgId)
  } else {
    next.add(msgId)
  }
  checkedIds.value = next
}

function deleteMessage(msgId: number) {
  dialog.warning({
    title: '删除消息',
    content: '确定要从列表中删除这条消息吗？此操作仅影响当前列表，不会删除 Telegram 原始消息。',
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: () => {
      messages.value = messages.value.filter((m) => m.message_id !== msgId)
      const next = new Set(checkedIds.value)
      next.delete(msgId)
      checkedIds.value = next
    },
  })
}

function batchDelete() {
  if (!canBatchDelete.value) return
  dialog.warning({
    title: '批量删除',
    content: `确定要删除选中的 ${checkedCount.value} 条消息吗？此操作仅影响当前列表。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: () => {
      messages.value = messages.value.filter((m) => !checkedIds.value.has(m.message_id))
      checkedIds.value = new Set()
    },
  })
}

// ===================== 二次筛选 =====================
function parseFilterKeywords(text: string): string[] {
  return [
    ...new Set(
      text
        .split(/[,，]/)
        .map((s) => s.trim())
        .filter(Boolean)
    ),
  ]
}

function applyMsgFilter() {
  const keywords = parseFilterKeywords(filterKeywords.value)
  if (keywords.length === 0) {
    appliedFilter.value = null
  } else {
    appliedFilter.value = { keywords, mode: filterMatchMode.value }
  }
  showMsgFilterModal.value = false
  // 对当前列表重新执行检索以应用筛选
  if (messageKeyword.value.trim() && selectedDialog.value) {
    searchMessages()
  }
}

function clearMsgFilter() {
  filterKeywords.value = ''
  filterMatchMode.value = 'all'
  appliedFilter.value = null
  showMsgFilterModal.value = false
  if (messageKeyword.value.trim() && selectedDialog.value) {
    searchMessages()
  }
}

function matchFilter(msg: RecentMessage): boolean {
  const f = appliedFilter.value
  if (!f) return true
  const haystack = (msg.file_name + ' ' + msg.message_text).toLowerCase()
  if (f.mode === 'all') {
    return f.keywords.every((kw) => haystack.includes(kw.toLowerCase()))
  }
  return f.keywords.some((kw) => haystack.includes(kw.toLowerCase()))
}

// ===================== 一键播放 =====================
async function openPlayer() {
  if (!canPlay.value || !selectedDialog.value) return
  const payload = {
    channelName: selectedDialog.value.name,
    channelId: selectedDialog.value.peer_id,
    messages: messages.value.map((m) => ({
      message_id: m.message_id,
      ve_collect: m.ve_collect,
      ve_title: m.ve_title,
      file_name: m.file_name,
    })),
  }
  try {
    localStorage.setItem(REPO_PLAYER_PAYLOAD_KEY, JSON.stringify(payload))
  } catch {
    message.error('写入播放数据失败')
    return
  }
  try {
    await windowApi.open({
      title: `${selectedDialog.value.name} - 樱花猫`,
      width: 1280,
      height: 800,
      url: '/repo-player',
      resizable: true,
      unique: true,
      showTitleBar: true,
      isMain: false,
    })
  } catch (e) {
    message.error(errMsg(e, '打开播放窗失败'))
  }
}

// ===================== 上传分集弹窗 =====================
async function loadVideoList(keyword?: string) {
  loadingVideos.value = true
  try {
    const result = await searchVideos({
      keyword: keyword || undefined,
      page: 1,
      page_size: 100,
    })
    videoList.value = result.list
  } catch (e) {
    message.error(errMsg(e, '加载视频列表失败'))
    videoList.value = []
  } finally {
    loadingVideos.value = false
  }
}

function onVideoSearch(query: string) {
  if (videoSearchTimer) clearTimeout(videoSearchTimer)
  videoSearchTimer = setTimeout(() => {
    loadVideoList(query)
  }, 300)
}

function openUploadModal() {
  if (!canUploadEpisodes.value) return
  selectedVideoId.value = null
  showUploadModal.value = true
  loadVideoList()
}

async function confirmUploadEpisodes() {
  if (!selectedVideoId.value || uploadingEpisodes.value) return
  if (!selectedDialog.value) return

  const channelId = selectedDialog.value.peer_id
  const episodes: EpisodeUploadItem[] = []
  for (const msg of messages.value) {
    if (!msg.ve_collect || msg.ve_collect <= 0) continue
    episodes.push({
      vs_channel_id: channelId,
      ve_message_id: msg.message_id,
      ve_title: msg.ve_title || `第${msg.ve_collect}集`,
      ve_collect: msg.ve_collect,
    })
  }
  if (episodes.length === 0) {
    message.warning('没有可上传的分集')
    return
  }

  uploadingEpisodes.value = true
  try {
    await uploadEpisodes({
      vr_id: selectedVideoId.value,
      episodes,
    })
    message.success(`成功上传 ${episodes.length} 条分集`)
    showUploadModal.value = false
  } catch (e) {
    message.error(errMsg(e, '上传分集失败'))
  } finally {
    uploadingEpisodes.value = false
  }
}

// ===================== 新建视频源弹窗（自动填充） =====================
async function openCreateModal() {
  showCreateModal.value = true
  // 重置表单后从 selectedSubject 自动填充
  form.value = {
    vr_name: '',
    vr_category: '',
    vr_desc: '',
    vr_cover: '',
    vr_episode_count: 0,
    vr_year: new Date().getFullYear(),
    vr_season: '',
    vr_status: 3,
    vr_language: undefined,
    vr_bgm_id: 0,
    tags: [],
  }
  const subject = selectedSubject.value
  if (subject) {
    // 标题：中文名优先
    form.value.vr_name = subject.name_cn || subject.name || ''
    // 简介
    form.value.vr_desc = subject.summary || ''
    // 集数：total_episodes 优先，缺失退 eps
    form.value.vr_episode_count = subject.total_episodes || subject.eps || 0
    // 年份 + 季度：从 date(YYYY-MM-DD) 解析
    if (subject.date) {
      const parts = subject.date.split('-')
      if (parts.length >= 1) {
        const y = parseInt(parts[0], 10)
        if (!isNaN(y)) form.value.vr_year = y
      }
      if (parts.length >= 2) {
        const m = parseInt(parts[1], 10)
        if (!isNaN(m) && m >= 1 && m <= 12) {
          form.value.vr_season = monthToSeason(m)
        }
      }
    }
    // 分类映射
    form.value.vr_category = mapCategory(subject)
    // 标签：与本地标签做名称匹配
    if (!baseInfo.value) {
      try {
        baseInfo.value = await getBaseInfo()
      } catch (e) {
        message.error(errMsg(e, '加载基本信息失败'))
      }
    }
    if (baseInfo.value && (subject.meta_tags || subject.tags)) {
      const tagNameToId = new Map(
        baseInfo.value.tags.map((t) => [t.vt_name, t.vt_id])
      )
      const bangumiTagNames: string[] = [
        ...(subject.meta_tags || []),
        ...(subject.tags || []).map((t) => t.name),
      ]
      const matched: number[] = []
      for (const name of bangumiTagNames) {
        const id = tagNameToId.get(name)
        if (id !== undefined && !matched.includes(id)) {
          matched.push(id)
        }
      }
      form.value.tags = matched
    }
    // 封面：使用 bangumi common 档位（常规尺寸，列表展示足够）
    form.value.vr_cover = subject.images?.common || ''
    // bangumi 条目 ID
    form.value.vr_bgm_id = subject.id || 0
  } else {
    // 无选中条目时，仍拉基本信息
    if (!baseInfo.value) {
      loadingBaseInfo.value = true
      try {
        baseInfo.value = await getBaseInfo()
      } catch (e) {
        message.error(errMsg(e, '加载基本信息失败'))
      } finally {
        loadingBaseInfo.value = false
      }
    }
  }
}

function triggerCoverUpload() {
  // 封面由 bangumi 自动填充，只读展示，无上传入口
}

async function confirmCreateVideo() {
  if (creating.value) return
  if (!form.value.vr_name.trim()) {
    message.warning('请输入视频标题')
    return
  }
  if (!form.value.vr_category) {
    message.warning('请选择分类')
    return
  }
  creating.value = true
  try {
    const result = await createVideoRepo(form.value)
    message.success('新建视频源成功')
    showCreateModal.value = false
    await loadVideoList()
    selectedVideoId.value = result.vr_id
  } catch (e) {
    message.error(errMsg(e, '新建视频源失败'))
  } finally {
    creating.value = false
  }
}

function videoLabel(item: VideoSearchItem): string {
  return `${item.vr_name}（${item.vr_category}）`
}

onMounted(() => {
  loadDialogs()
})
</script>

<template>
  <div class="discover">
    <!-- 左侧栏：频道下拉 + bangumi 筛选 + 条目列表 -->
    <aside class="discover__sidebar">
      <!-- 频道选择 -->
      <div class="discover__sidebar-channel">
        <n-select
          :value="selectedDialog?.peer_id"
          :options="dialogs.map((d) => ({ label: d.name, value: d.peer_id }))"
          :loading="loadingDialogs"
          placeholder="选择频道（可输入过滤）"
          filterable
          clearable
          :render-label="renderDialogLabel"
          :menu-props="{ class: 'dialog-select-menu' }"
          @search="onDialogSearch"
          @update:value="onSelectDialog"
        />
      </div>

      <!-- 本地筛选输入框 + 内嵌筛选按钮（调接口拉取） -->
      <div class="discover__sidebar-filter">
        <n-input
          v-model:value="subjectSearchKeyword"
          placeholder="筛选已加载条目"
          class="discover__sidebar-filter-input"
        >
          <template #prefix>
            <Search />
          </template>
        </n-input>
        <button
          class="discover__sidebar-filter-btn"
          :class="{ 'is-active': subjects.length > 0 }"
          title="筛选条件"
          @click="openFilterModal"
        >
          <component :is="Filter" />
        </button>
        <span v-if="subjectsTotal > 0" class="discover__sidebar-count">
          共 {{ subjectsTotal }} 条
        </span>
      </div>

      <!-- bangumi 条目列表 -->
      <div class="discover__sidebar-list">
        <div v-if="loadingSubjects && subjects.length === 0" class="discover__state">
          <Spinner :size="24" />
        </div>
        <div v-else-if="subjectsError" class="discover__state discover__state--col">
          <StatusHint variant="error" :title="subjectsError" />
        </div>
        <div v-else-if="subjects.length === 0" class="discover__state">
          <StatusHint variant="info" title="点击筛选按钮拉取条目" />
        </div>
        <div v-else-if="filteredSubjects.length === 0" class="discover__state">
          <StatusHint variant="info" title="无匹配条目" />
        </div>
        <ul v-else class="subject-list">
          <li
            v-for="s in filteredSubjects"
            :key="s.id"
            class="subject-item"
            :class="{ 'is-selected': selectedSubject?.id === s.id }"
            @click="selectSubject(s)"
          >
            <BaseImage
              v-if="s.images?.small || s.images?.grid"
              :src="s.images?.small || s.images?.grid"
              class="subject-item__cover"
              alt=""
            />
            <div class="subject-item__main">
              <p class="subject-item__name">{{ s.name_cn || s.name }}</p>
              <p class="subject-item__sub">{{ s.name }}</p>
              <div class="subject-item__meta">
                <span v-if="s.rating?.score" class="subject-item__score">
                  <component :is="Star" />
                  {{ s.rating.score.toFixed(1) }}
                </span>
                <span v-if="s.date" class="subject-item__date">{{ s.date }}</span>
              </div>
            </div>
          </li>
        </ul>

        <!-- 加载更多 -->
        <div v-if="hasMoreSubjects" class="discover__more">
          <n-button
            size="small"
            :loading="loadingMoreSubjects"
            @click="loadMoreSubjects"
          >
            加载更多
          </n-button>
        </div>
      </div>
    </aside>

    <!-- 右侧：元数据卡片 + 消息检索 -->
    <section class="discover__main">
      <!-- 未选中 bangumi 条目 -->
      <div v-if="!selectedSubject" class="discover__main-empty">
        <StatusHint variant="info" title="请从左侧选择一个 bangumi 条目" />
      </div>

      <template v-else>
        <!-- 顶部：bangumi 元数据卡片（取代 Repo 的频道头） -->
        <header class="discover__meta">
          <BaseImage
            v-if="selectedSubject.images?.large || selectedSubject.images?.common"
            :src="selectedSubject.images?.large || selectedSubject.images?.common"
            class="discover__meta-cover"
            alt=""
          />
          <div class="discover__meta-info">
            <h2 class="discover__meta-name">
              {{ selectedSubject.name_cn || selectedSubject.name }}
            </h2>
            <p class="discover__meta-original">{{ selectedSubject.name }}</p>
            <div class="discover__meta-tags">
              <span v-if="selectedSubject.rating?.score" class="discover__meta-stat">
                <component :is="Star" />
                {{ selectedSubject.rating.score.toFixed(1) }}
                <template v-if="selectedSubject.rating?.rank">
                  · 排名 #{{ selectedSubject.rating.rank }}
                </template>
              </span>
              <span v-if="selectedSubject.date" class="discover__meta-date">
                {{ selectedSubject.date }}
              </span>
              <span v-if="selectedSubject.total_episodes" class="discover__meta-eps">
                {{ selectedSubject.total_episodes }} 集
              </span>
            </div>
            <p v-if="selectedSubject.summary" class="discover__meta-summary">
              {{ selectedSubject.summary }}
            </p>
          </div>
        </header>

        <!-- 消息搜索区 -->
        <div class="discover__search-bar">
          <n-input
            v-model:value="messageKeyword"
            placeholder="输入关键字搜索频道消息"
            class="discover__search-bar-input"
            @keyup.enter="onSearchClick"
          >
            <template #prefix>
              <Search />
            </template>
          </n-input>
          <IconButton
            variant="primary"
            size="sm"
            :icon="Search"
            :disabled="!canSearchMessages"
            :loading="loadingMessages"
            @click="onSearchClick"
          >
            搜索
          </IconButton>
          <IconButton
            variant="secondary"
            size="sm"
            :icon="Filter"
            :class="{ 'is-filter-active': filterActive }"
            @click="showMsgFilterModal = true"
          >
            筛选
          </IconButton>
        </div>

        <!-- 消息列表 -->
        <div class="discover__messages">
          <div v-if="messageError" class="discover__state discover__state--col">
            <StatusHint variant="error" :title="messageError" />
          </div>
          <div
            v-else-if="!loadingMessages && messages.length === 0"
            class="discover__state"
          >
            <StatusHint
              variant="info"
              :title="filterActive ? '当前筛选条件下无匹配消息' : '未找到匹配消息'"
            />
          </div>
          <ul v-else class="message-list">
            <li
              v-for="(msg, index) in messages"
              :key="msg.message_id"
              class="msg-item"
              :class="{
                'is-checked': checkedIds.has(msg.message_id),
                'is-dragging': dragIndex === index,
                'is-drag-over': dragOverIndex === index,
              }"
              draggable="true"
              @dragstart="onDragStart(index)"
              @dragover="onDragOver($event, index)"
              @dragleave="onDragLeave"
              @drop="onDrop"
              @dragend="onDragEnd"
            >
              <div class="msg-item__handle" title="拖动排序"></div>
              <p class="msg-item__name">{{ messageTitle(msg) }}</p>
              <p v-if="msg.message_text" class="msg-item__desc" :title="msg.message_text">
                {{ msg.message_text }}
              </p>
              <div class="msg-item__meta">
                <span v-if="msg.ve_collect" class="msg-item__ep">
                  E{{ String(msg.ve_collect).padStart(2, '0') }}
                </span>
                <span v-if="msg.ve_title" class="msg-item__ve-title" :title="msg.ve_title">
                  {{ msg.ve_title }}
                </span>
                <span class="msg-item__date">{{ formatDate(msg.date) }}</span>
                <span
                  v-if="msg.has_media && msg.file_size > 0"
                  class="msg-item__dot"
                >
                  ·
                </span>
                <span
                  v-if="msg.has_media && msg.file_size > 0"
                  class="msg-item__size"
                >
                  {{ formatSize(msg.file_size) }}
                </span>
              </div>
              <div class="msg-item__actions">
                <button
                  class="msg-item__btn msg-item__edit"
                  title="编辑标题"
                  @click.stop="startEditTitle(msg)"
                >
                  <component :is="Edit" />
                </button>
                <button
                  class="msg-item__btn msg-item__check"
                  :class="{ 'is-active': checkedIds.has(msg.message_id) }"
                  :title="checkedIds.has(msg.message_id) ? '取消选中' : '选中'"
                  @click.stop="toggleCheck(msg.message_id)"
                >
                  <component :is="CircleCheck" />
                </button>
                <button
                  class="msg-item__btn msg-item__del"
                  title="删除"
                  @click.stop="deleteMessage(msg.message_id)"
                >
                  <component :is="Trash" />
                </button>
              </div>
            </li>
          </ul>
        </div>

        <!-- 底部进度条 + 批量操作 -->
        <div
          v-if="loadingMessages || loadingDone || messages.length > 0"
          class="discover__load-bar"
        >
          <div class="discover__load-bar-left">
            <Spinner v-if="loadingMessages" :size="14" />
            <span class="discover__load-bar-text">
              <template v-if="loadingMessages">
                正在加载... 已加载 {{ loadedCount }} 条
              </template>
              <template v-else-if="loadingDone">
                加载完成，共 {{ messages.length }} 条
              </template>
              <template v-else>
                共 {{ messages.length }} 条
              </template>
            </span>
            <button
              v-if="loadingMessages"
              class="discover__load-bar-cancel"
              @click="cancelSearchLoad"
            >
              取消
            </button>
          </div>
          <div class="discover__load-bar-right">
            <span v-if="checkedCount > 0" class="discover__load-bar-count">
              已选 {{ checkedCount }} 条
            </span>
            <n-button
              size="small"
              :disabled="messages.length === 0 || loadingMessages"
              @click="openRenumberModal"
            >
              初始排序
            </n-button>
            <n-button
              size="small"
              :disabled="!canBatchDelete"
              @click="batchDelete"
            >
              批量删除
            </n-button>
            <n-button
              size="small"
              type="primary"
              :disabled="!canUploadEpisodes"
              @click="openUploadModal"
            >
              上传分集
            </n-button>
            <n-button
              size="small"
              type="primary"
              :disabled="!canPlay"
              @click="openPlayer"
            >
              <template #icon>
                <PlayerPlay />
              </template>
              一键播放
            </n-button>
          </div>
        </div>
      </template>
    </section>

    <!-- bangumi 筛选弹窗 -->
    <n-modal
      v-model:show="showFilterModal"
      preset="card"
      title="Bangumi 筛选"
      style="width: 480px"
      :bordered="false"
    >
      <div class="filter-modal">
        <p class="filter-modal__hint">
          选择筛选条件后拉取 bangumi 条目列表
        </p>
        <div class="filter-modal__row">
          <label class="filter-modal__label">
            <span class="filter-modal__required">*</span>条目类型
          </label>
          <n-select
            v-model:value="filterType"
            :options="TYPE_OPTIONS"
            placeholder="选择类型"
          />
        </div>
        <div v-if="filterType === 2" class="filter-modal__row">
          <label class="filter-modal__label">动画分类</label>
          <n-select
            v-model:value="filterCat"
            :options="ANIME_CAT_OPTIONS"
            placeholder="不筛选"
            clearable
          />
        </div>
        <div class="filter-modal__row filter-modal__row--2col">
          <div class="filter-modal__col">
            <label class="filter-modal__label">年份</label>
            <n-input-number
              v-model:value="filterYear"
              :min="1900"
              :max="2100"
              placeholder="如 2025"
              style="width: 100%"
            />
          </div>
          <div class="filter-modal__col">
            <label class="filter-modal__label">月份</label>
            <n-input-number
              v-model:value="filterMonth"
              :min="1"
              :max="12"
              placeholder="1~12"
              style="width: 100%"
            />
          </div>
        </div>
        <div class="filter-modal__row">
          <label class="filter-modal__label">排序</label>
          <n-radio-group v-model:value="filterSort">
            <n-radio value="date">按日期</n-radio>
            <n-radio value="rank">按排名</n-radio>
          </n-radio-group>
        </div>
        <div class="filter-modal__row">
          <div class="filter-modal__label-row">
            <label class="filter-modal__label">
              <span class="filter-modal__required">*</span>Access Token
            </label>
            <button
              type="button"
              class="filter-modal__link"
              @click="openAccessTokenPage"
            >
              去获取
            </button>
          </div>
          <n-input
            v-model:value="bangumiToken"
            type="password"
            show-password-on="click"
            placeholder="必填，提高速率上限"
          />
        </div>
        <div class="filter-modal__row">
          <label class="filter-modal__label">
            <span class="filter-modal__required">*</span>User-Agent
          </label>
          <n-input
            v-model:value="bangumiUserAgent"
            type="password"
            show-password-on="click"
            placeholder="必填，格式 用户名/应用名，如 acgbot/MyApp"
          />
        </div>
      </div>
      <template #footer>
        <div class="filter-modal__footer">
          <n-button @click="clearBangumiFilter">重置</n-button>
          <n-button @click="showFilterModal = false">取消</n-button>
          <n-button type="primary" @click="applyBangumiFilter">应用</n-button>
        </div>
      </template>
    </n-modal>

    <!-- 消息二次筛选弹窗 -->
    <n-modal
      v-model:show="showMsgFilterModal"
      preset="card"
      title="消息筛选"
      style="width: 480px"
      :bordered="false"
    >
      <div class="filter-modal">
        <p class="filter-modal__hint">
          输入多个关键字（逗号分隔），对搜索结果做二次过滤
        </p>
        <n-input
          v-model:value="filterKeywords"
          type="textarea"
          :rows="3"
          placeholder="如：1080P,第一季,简体"
        />
        <div class="filter-modal__mode">
          <span class="filter-modal__mode-label">匹配方式</span>
          <n-radio-group v-model:value="filterMatchMode">
            <n-radio value="all">全匹配（同时包含所有关键字）</n-radio>
            <n-radio value="any">任意匹配（包含任一关键字）</n-radio>
          </n-radio-group>
        </div>
      </div>
      <template #footer>
        <div class="filter-modal__footer">
          <n-button v-if="filterActive" @click="clearMsgFilter">清除筛选</n-button>
          <n-button @click="showMsgFilterModal = false">取消</n-button>
          <n-button type="primary" @click="applyMsgFilter">应用</n-button>
        </div>
      </template>
    </n-modal>

    <!-- 上传分集弹窗 -->
    <n-modal
      v-model:show="showUploadModal"
      preset="card"
      title="上传分集"
      style="width: 520px"
      :bordered="false"
    >
      <div class="upload-modal">
        <p class="upload-modal__hint">
          将当前列表中已设置集数的消息上传为分集，请选择目标视频源
        </p>
        <div class="upload-modal__select-row">
          <div class="upload-modal__select-wrap">
            <n-select
              v-model:value="selectedVideoId"
              :options="videoList.map((v) => ({ label: videoLabel(v), value: v.vr_id }))"
              :loading="loadingVideos"
              placeholder="输入关键字搜索视频源"
              filterable
              remote
              clearable
              @search="onVideoSearch"
            />
          </div>
          <button class="upload-modal__new-btn" @click="openCreateModal">
            <component :is="Plus" />
            <span>新建</span>
          </button>
        </div>
      </div>
      <template #footer>
        <div class="filter-modal__footer">
          <n-button @click="showUploadModal = false">取消</n-button>
          <n-button
            type="primary"
            :loading="uploadingEpisodes"
            :disabled="!selectedVideoId"
            @click="confirmUploadEpisodes"
          >
            确认上传
          </n-button>
        </div>
      </template>
    </n-modal>

    <!-- 新建视频源弹窗（自动填充） -->
    <n-modal
      v-model:show="showCreateModal"
      preset="card"
      title="新建视频源"
      style="width: 560px"
      :bordered="false"
    >
      <div class="create-form">
        <div class="create-form__row">
          <label class="create-form__label">
            <span class="create-form__required">*</span>视频标题
          </label>
          <n-input
            v-model:value="form.vr_name"
            placeholder="请输入视频标题"
          />
        </div>

        <div class="create-form__row create-form__row--2col">
          <div class="create-form__col">
            <label class="create-form__label">
              <span class="create-form__required">*</span>分类
            </label>
            <n-select
              v-model:value="form.vr_category"
              :options="CATEGORY_OPTIONS"
              placeholder="选择分类"
            />
          </div>
          <div class="create-form__col">
            <label class="create-form__label">年份</label>
            <n-input-number
              v-model:value="form.vr_year"
              :min="1900"
              :max="2100"
              placeholder="年份"
              style="width: 100%"
            />
          </div>
        </div>

        <div class="create-form__row create-form__row--2col">
          <div class="create-form__col">
            <label class="create-form__label">季度</label>
            <n-select
              v-model:value="form.vr_season"
              :options="SEASON_OPTIONS"
              placeholder="选择季度"
              clearable
            />
          </div>
          <div class="create-form__col">
            <label class="create-form__label">集数</label>
            <n-input-number
              v-model:value="form.vr_episode_count"
              :min="0"
              placeholder="0 表示未知"
              style="width: 100%"
            />
          </div>
        </div>

        <div class="create-form__row">
          <label class="create-form__label">语种</label>
          <n-select
            v-model:value="form.vr_language"
            :options="(baseInfo?.languages || []).map((l) => ({ label: l.vl_name, value: l.vl_id }))"
            :loading="loadingBaseInfo"
            placeholder="选择语种"
            clearable
          />
        </div>

        <div class="create-form__row">
          <label class="create-form__label">标签</label>
          <n-select
            v-model:value="form.tags"
            :options="(baseInfo?.tags || []).map((t) => ({ label: t.vt_name, value: t.vt_id }))"
            :loading="loadingBaseInfo"
            placeholder="选择标签（可多选）"
            multiple
            filterable
          />
        </div>

        <div class="create-form__row">
          <label class="create-form__label">封面</label>
          <n-input
            :value="form.vr_cover"
            :placeholder="form.vr_cover ? '' : '由 bangumi 自动填充'"
            disabled
          />
        </div>

        <div class="create-form__row">
          <label class="create-form__label">简介</label>
          <n-input
            v-model:value="form.vr_desc"
            type="textarea"
            :rows="3"
            placeholder="请输入视频简介"
          />
        </div>
      </div>
      <template #footer>
        <div class="filter-modal__footer">
          <n-button @click="showCreateModal = false">取消</n-button>
          <n-button
            type="primary"
            :loading="creating"
            @click="confirmCreateVideo"
          >
            创建
          </n-button>
        </div>
      </template>
    </n-modal>

    <!-- 初始排序弹窗 -->
    <n-modal
      v-model:show="showRenumberModal"
      preset="card"
      title="初始排序"
      style="width: 400px"
      :bordered="false"
    >
      <div class="renumber-modal">
        <p class="renumber-modal__hint">
          输入起始集数，确认后将从列表第一条开始依次填充集数
        </p>
        <n-input-number
          v-model:value="renumberStart"
          :min="0"
          placeholder="起始集数"
          style="width: 100%"
        />
      </div>
      <template #footer>
        <div class="filter-modal__footer">
          <n-button @click="showRenumberModal = false">取消</n-button>
          <n-button type="primary" @click="confirmRenumber">确认</n-button>
        </div>
      </template>
    </n-modal>

    <!-- 编辑分集标题弹窗 -->
    <n-modal
      v-model:show="showEditTitleModal"
      preset="card"
      title="编辑分集标题"
      style="width: 400px"
      :bordered="false"
    >
      <div class="renumber-modal">
        <p class="renumber-modal__hint">
          修改分集标题，留空则使用默认的"第X集"
        </p>
        <n-input
          v-model:value="editingTitle"
          placeholder="请输入分集标题"
        />
      </div>
      <template #footer>
        <div class="filter-modal__footer">
          <n-button @click="showEditTitleModal = false">取消</n-button>
          <n-button type="primary" @click="confirmEditTitle">确认</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<style scoped>
.discover {
  display: flex;
  width: 100%;
  height: 100%;
  background: var(--bg-base);
  overflow: hidden;
}

/* ===================== 左侧栏 ===================== */
.discover__sidebar {
  display: flex;
  flex-direction: column;
  width: 340px;
  flex-shrink: 0;
  background: var(--bg-elevated);
  border-right: 1px solid var(--border-subtle);
  overflow: hidden;
}

.discover__sidebar-channel {
  padding: var(--space-4);
  border-bottom: 1px solid var(--border-subtle);
}

.discover__sidebar-filter {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--border-subtle);
}

.discover__sidebar-filter-input {
  flex: 1;
}

/* 内嵌筛选按钮：绝对定位到输入框右侧 */
.discover__sidebar-filter-btn {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-secondary);
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    color var(--duration-fast) var(--ease-fluid),
    border-color var(--duration-fast) var(--ease-fluid);
}

.discover__sidebar-filter-btn svg {
  width: 16px;
  height: 16px;
}

.discover__sidebar-filter-btn:hover {
  background: var(--bg-card-solid);
  border-color: var(--border-strong);
  color: var(--text-primary);
}

.discover__sidebar-filter-btn.is-active {
  border-color: var(--accent-sakura);
  color: var(--accent-sakura);
}

.discover__sidebar-count {
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-tertiary);
  flex-shrink: 0;
}

.discover__sidebar-list {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.discover__state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-8) var(--space-4);
}

.discover__state--col {
  flex-direction: column;
  gap: var(--space-3);
}

/* bangumi 条目列表项 */
.subject-list {
  list-style: none;
  margin: 0;
  padding: var(--space-2);
}

.subject-item {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid);
}

.subject-item:hover {
  background: var(--bg-card);
}

.subject-item.is-selected {
  background: color-mix(in srgb, var(--accent-sakura) 10%, transparent);
}

.subject-item__cover {
  width: 48px;
  height: 64px;
  flex-shrink: 0;
  object-fit: cover;
  border-radius: var(--radius-sm);
  background: var(--bg-card-solid);
}

.subject-item__main {
  flex: 1;
  min-width: 0;
}

.subject-item__name {
  margin: 0;
  font-family: var(--font-body);
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  line-height: 1.4;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.subject-item__sub {
  margin: 2px 0 0;
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.subject-item__meta {
  margin-top: 4px;
  display: flex;
  align-items: center;
  gap: 6px;
}

.subject-item__score {
  display: flex;
  align-items: center;
  gap: 2px;
  font-family: var(--font-mono);
  font-size: 11px;
  font-weight: 600;
  color: var(--accent-sakura);
}

.subject-item__score svg {
  width: 12px;
  height: 12px;
}

.subject-item__date {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-tertiary);
}

.discover__more {
  display: flex;
  justify-content: center;
  padding: var(--space-3);
}

/* ===================== 右侧主区 ===================== */
.discover__main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  height: 100%;
  overflow: hidden;
}

.discover__main-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* bangumi 元数据卡片头 */
.discover__meta {
  flex-shrink: 0;
  display: flex;
  gap: var(--space-5);
  padding: var(--space-5) var(--space-8);
  border-bottom: 1px solid var(--border-subtle);
}

.discover__meta-cover {
  width: 80px;
  height: 112px;
  flex-shrink: 0;
  object-fit: cover;
  border-radius: var(--radius-md);
  background: var(--bg-card-solid);
}

.discover__meta-info {
  flex: 1;
  min-width: 0;
}

.discover__meta-name {
  margin: 0;
  font-family: var(--font-display);
  font-size: 20px;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: 0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.discover__meta-original {
  margin: 2px 0 0;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.discover__meta-tags {
  margin-top: var(--space-2);
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.discover__meta-stat {
  display: flex;
  align-items: center;
  gap: 2px;
  font-family: var(--font-mono);
  font-size: 12px;
  font-weight: 600;
  color: var(--accent-sakura);
}

.discover__meta-stat svg {
  width: 14px;
  height: 14px;
}

.discover__meta-date,
.discover__meta-eps {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-secondary);
}

.discover__meta-summary {
  margin: var(--space-2) 0 0;
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-secondary);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

/* 消息搜索条 */
.discover__search-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-8);
  border-bottom: 1px solid var(--border-subtle);
}

.discover__search-bar-input {
  flex: 1;
}

.discover__search-bar .is-filter-active {
  border-color: var(--accent-sakura);
  color: var(--accent-sakura);
}

/* 消息列表区 */
.discover__messages {
  flex: 1;
  position: relative;
  overflow-y: auto;
  min-height: 0;
  padding: var(--space-4) var(--space-8);
}

.message-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

/* 消息项（80px） */
.msg-item {
  position: relative;
  height: 80px;
  display: flex;
  flex-direction: column;
  padding: 12px 16px 12px 28px;
  background: var(--bg-card);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  transition: background-color var(--duration-fast) var(--ease-fluid),
    border-color var(--duration-fast) var(--ease-fluid);
}

.msg-item:hover {
  background: var(--bg-card-solid);
  border-color: var(--border-strong);
}

.msg-item.is-checked {
  background: color-mix(in srgb, var(--accent-sakura) 8%, transparent);
  border-color: var(--accent-sakura);
}

.msg-item.is-dragging {
  opacity: 0.4;
}

.msg-item.is-drag-over {
  border-color: var(--accent-sakura);
  border-style: dashed;
}

.msg-item__handle {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 20px;
  cursor: grab;
}

.msg-item__handle:active {
  cursor: grabbing;
}

.msg-item__ep {
  font-family: var(--font-mono);
  font-size: 11px;
  font-weight: 600;
  color: var(--accent-sakura);
  background: color-mix(in srgb, var(--accent-sakura) 12%, transparent);
  padding: 1px 6px;
  border-radius: var(--radius-sm);
}

.msg-item__name {
  margin: 0;
  font-family: var(--font-body);
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 108px;
}

.msg-item__ve-title {
  font-family: var(--font-body);
  font-size: 11px;
  font-weight: 600;
  color: var(--text-secondary);
  max-width: 120px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.msg-item__desc {
  margin: 2px 0 0;
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-tertiary);
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 108px;
}

.msg-item__meta {
  margin-top: auto;
  display: flex;
  align-items: center;
  gap: 6px;
}

.msg-item__date {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-tertiary);
}

.msg-item__dot {
  font-size: 11px;
  color: var(--text-tertiary);
}

.msg-item__size {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--accent-sakura);
}

.msg-item__actions {
  position: absolute;
  right: 16px;
  top: 50%;
  transform: translateY(-50%);
  display: flex;
  align-items: center;
  gap: 4px;
}

.msg-item__btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: var(--radius-full);
  background: transparent;
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    color var(--duration-fast) var(--ease-fluid);
}

.msg-item__btn svg {
  width: 16px;
  height: 16px;
}

.msg-item__check {
  color: var(--text-tertiary);
}

.msg-item__check:hover {
  background: color-mix(in srgb, var(--accent-sakura) 12%, transparent);
  color: var(--accent-sakura);
}

.msg-item__check.is-active {
  color: var(--accent-sakura);
}

.msg-item__del {
  color: var(--text-tertiary);
}

.msg-item__del:hover {
  background: color-mix(in srgb, var(--color-error) 12%, transparent);
  color: var(--color-error);
}

/* 底部加载进度条 */
.discover__load-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  background: var(--bg-elevated);
  border-top: 1px solid var(--border-subtle);
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-tertiary);
}

.discover__load-bar-left {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.discover__load-bar-right {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.discover__load-bar-text {
  flex-shrink: 0;
}

.discover__load-bar-count {
  color: var(--accent-sakura);
  font-weight: 600;
}

.discover__load-bar-cancel {
  margin-left: var(--space-2);
  padding: 2px var(--space-3);
  border: none;
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--accent-sakura);
  font-family: var(--font-body);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid);
}

.discover__load-bar-cancel:hover {
  background: color-mix(in srgb, var(--accent-sakura) 12%, transparent);
}

/* ===================== 弹窗通用 ===================== */
.filter-modal {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.filter-modal__hint {
  margin: 0;
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-tertiary);
}

.filter-modal__row {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.filter-modal__row--2col {
  flex-direction: row;
  gap: var(--space-4);
}

.filter-modal__col {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.filter-modal__label {
  font-family: var(--font-body);
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
}

.filter-modal__label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.filter-modal__link {
  border: none;
  background: transparent;
  padding: 0;
  color: var(--accent-sakura);
  font-family: var(--font-body);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity var(--duration-fast) var(--ease-fluid);
}

.filter-modal__link:hover {
  opacity: 0.75;
}

.filter-modal__required {
  color: var(--color-error);
  margin-right: 2px;
}

.filter-modal__mode {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.filter-modal__mode-label {
  font-family: var(--font-body);
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
}

.filter-modal__footer {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
}

/* 上传分集弹窗 */
.upload-modal {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.upload-modal__hint {
  margin: 0;
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-tertiary);
}

.upload-modal__select-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.upload-modal__select-wrap {
  flex: 1;
}

.upload-modal__new-btn {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 12px;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--accent-sakura);
  font-family: var(--font-body);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid);
}

.upload-modal__new-btn svg {
  width: 14px;
  height: 14px;
}

.upload-modal__new-btn:hover {
  background: color-mix(in srgb, var(--accent-sakura) 12%, transparent);
}

/* 初始排序弹窗 */
.renumber-modal {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.renumber-modal__hint {
  margin: 0;
  font-family: var(--font-body);
  font-size: 13px;
  color: var(--text-tertiary);
}

/* 新建视频源表单 */
.create-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.create-form__row {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.create-form__row--2col {
  flex-direction: row;
  gap: var(--space-4);
}

.create-form__col {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.create-form__label {
  font-family: var(--font-body);
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
}

.create-form__required {
  color: var(--color-error);
  margin-right: 2px;
}

/* 滚动条样式 */
.discover__sidebar-list::-webkit-scrollbar,
.discover__messages::-webkit-scrollbar {
  width: 6px;
}

.discover__sidebar-list::-webkit-scrollbar-thumb,
.discover__messages::-webkit-scrollbar-thumb {
  background: var(--border-strong);
  border-radius: var(--radius-full);
}

.discover__sidebar-list::-webkit-scrollbar-thumb:hover,
.discover__messages::-webkit-scrollbar-thumb:hover {
  background: var(--accent-sakura);
}

.discover__sidebar-list::-webkit-scrollbar-track,
.discover__messages::-webkit-scrollbar-track {
  background: transparent;
}
</style>

<!-- 非 scoped：频道下拉菜单 teleport 到 body，需全局样式控制选项内容区撑满 -->
<style>
.dialog-select-menu .n-base-select-option {
  padding-right: 8px;
}

.dialog-select-menu .n-base-select-option__content {
  width: 100%;
}
</style>
