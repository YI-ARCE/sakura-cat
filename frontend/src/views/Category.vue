<script setup lang="ts">
// 分类检索页
// 顶部 sticky 筛选栏（分类药丸 + 年份/季度/排序下拉 + 搜索框） + 视频网格 + 分页
// 支持顶栏「管理」按钮进入多选模式，批量删除视频源
import { ref, computed, onMounted } from 'vue'
import { Search } from '@vicons/tabler'
import { useDialog, useMessage } from 'naive-ui'
import { VideoCard, EpisodeDownloadPicker } from '../components/business'
import { Spinner, StatusHint } from '../components/common/Ui'
import {
  searchVideos,
  deleteVideoRepo,
  type SearchRequest,
  type VideoSearchItem,
} from '../services/repo'
import { openPreviewWindow } from '../services/window'
import { partyApi, partyWindowApi } from '../services/party'
import { useEpisodeDownloadPicker } from '../composables/useEpisodeDownloadPicker'

// 选集下载弹窗（复用 composable，加载分集 → 打开弹窗）
const { state: picker, openPicker } = useEpisodeDownloadPicker()

const dialog = useDialog()
const message = useMessage()

const PAGE_SIZE = 20

// 筛选项
const category = ref('')
const year = ref('')
const season = ref('')
const sort = ref('latest')
const keyword = ref('')

// 结果状态
const list = ref<VideoSearchItem[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const error = ref('')

// 多选模式状态
const selectMode = ref(false)
const selectedIds = ref<Set<number>>(new Set())
const deleting = ref(false)

// 下拉/药丸选项
const categoryOptions = [
  { label: '全部', value: '' },
  { label: '动漫', value: 'anime' },
  { label: '电影', value: 'movie' },
  { label: '剧集', value: 'tv' },
  { label: 'OVA', value: 'ova' },
  { label: '特别篇', value: 'special' },
]
const yearOptions = [
  { label: '全部', value: '' },
  { label: '2025', value: '2025' },
  { label: '2024', value: '2024' },
  { label: '2023', value: '2023' },
  { label: '2022', value: '2022' },
  { label: '更早', value: 'older' },
]
const seasonOptions = [
  { label: '全部', value: '' },
  { label: '冬', value: 'winter' },
  { label: '春', value: 'spring' },
  { label: '夏', value: 'summer' },
  { label: '秋', value: 'autumn' },
]
const sortOptions = [
  { label: '最新', value: 'latest' },
  { label: '最热', value: 'hot' },
]

// 根元素引用（用于切页滚动到顶部）
const rootRef = ref<HTMLElement | null>(null)

// 并发请求令牌，避免快速切换筛选导致旧响应覆盖新结果
let reqToken = 0

const totalPages = computed(() =>
  Math.max(1, Math.ceil(total.value / PAGE_SIZE)),
)

// 组装检索请求
function buildRequest(): SearchRequest {
  const req: SearchRequest = {
    sort: sort.value,
    page: page.value,
    page_size: PAGE_SIZE,
  }
  if (category.value) req.category = category.value
  // SearchRequest.year 为 number，"更早"无法用单一年份表达，暂不下发（需后端支持区间查询）
  if (year.value && year.value !== 'older') req.year = Number(year.value)
  if (season.value) req.season = season.value
  const kw = keyword.value.trim()
  if (kw) req.keyword = kw
  return req
}

async function doSearch() {
  loading.value = true
  error.value = ''
  const token = ++reqToken
  try {
    const res = await searchVideos(buildRequest())
    if (token !== reqToken) return
    list.value = res.list || []
    total.value = res.total || 0
  } catch (e) {
    if (token !== reqToken) return
    error.value = e instanceof Error ? e.message : '检索失败'
    list.value = []
    total.value = 0
  } finally {
    if (token === reqToken) loading.value = false
  }
}

// 切换任意筛选项：重置页码并重新检索
function onFilterChange() {
  page.value = 1
  doSearch()
}

// 选中分类药丸：设置分类并触发筛选
function selectCategory(value: string) {
  category.value = value
  onFilterChange()
}

// 搜索框回车
function onSearchEnter() {
  onFilterChange()
}

function goToPage(p: number) {
  if (p < 1 || p > totalPages.value || p === page.value) return
  page.value = p
  doSearch()
  scrollToTop()
}

// ===================== 多选模式 =====================

const selectedCount = computed(() => selectedIds.value.size)
const allSelected = computed(
  () => list.value.length > 0 && list.value.every((it) => selectedIds.value.has(it.vr_id)),
)

function enterSelectMode() {
  selectMode.value = true
  selectedIds.value = new Set()
}

function exitSelectMode() {
  selectMode.value = false
  selectedIds.value = new Set()
}

function toggleSelect(id: number, checked: boolean) {
  const next = new Set(selectedIds.value)
  if (checked) next.add(id)
  else next.delete(id)
  selectedIds.value = next
}

function toggleSelectAll() {
  if (allSelected.value) {
    // 取消本页全选
    const next = new Set(selectedIds.value)
    for (const it of list.value) next.delete(it.vr_id)
    selectedIds.value = next
  } else {
    // 选中本页全部
    const next = new Set(selectedIds.value)
    for (const it of list.value) next.add(it.vr_id)
    selectedIds.value = next
  }
}

function invertSelection() {
  const next = new Set(selectedIds.value)
  for (const it of list.value) {
    if (next.has(it.vr_id)) next.delete(it.vr_id)
    else next.add(it.vr_id)
  }
  selectedIds.value = next
}

function confirmDelete() {
  if (selectedIds.value.size === 0) return
  const ids = Array.from(selectedIds.value)
  dialog.warning({
    title: '删除视频源',
    content: `确定删除选中的 ${ids.length} 部视频源吗？将一并删除其分集、标签关联、播放历史、追番、弹幕与评论，操作不可撤销。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      deleting.value = true
      try {
        await deleteVideoRepo(ids)
        message.success(`已删除 ${ids.length} 部视频源`)
        exitSelectMode()
        page.value = 1
        await doSearch()
      } catch (e) {
        message.error(e instanceof Error ? e.message : '删除失败')
      } finally {
        deleting.value = false
      }
    },
  })
}

// 滚动到最近的滚动容器顶部（主区域滚动容器）
function scrollToTop() {
  const el = rootRef.value
  if (!el) return
  let node: HTMLElement | null = el.parentElement
  while (node) {
    const style = getComputedStyle(node)
    if (/(auto|scroll)/.test(style.overflowY)) {
      node.scrollTo({ top: 0, behavior: 'smooth' })
      return
    }
    node = node.parentElement
  }
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

// VideoCard 点击 → 打开预览窗
function handleCardClick(item: VideoSearchItem) {
  openPreviewWindow(item.vr_id, {
    title: item.vr_name,
    cover: item.vr_cover,
    category: item.vr_category,
    year: item.vr_year ? String(item.vr_year) : '',
    season: item.vr_season,
  })
}

// ===================== 一起看 =====================

// 一起看开启中标记(防止重复点击)
const partyStarting = ref(false)

// VideoCard 分享图标点击 → 弹出"开启一起看"确认弹窗
function handlePartyClick(item: VideoSearchItem) {
  const episodeLabel =
    item.vr_episode_count && item.vr_episode_count > 0
      ? `${item.vr_episode_count} 集`
      : '连载中'
  dialog.warning({
    title: '开启一起看',
    content: `确认开启「${item.vr_name}」的一起看吗?\n当前 ${episodeLabel},开启后将新开控制台窗口,你可以扫码邀请朋友加入同步观看。`,
    positiveText: '开启一起看',
    negativeText: '取消',
    onPositiveClick: async () => {
      if (partyStarting.value) return
      partyStarting.value = true
      try {
        const result = await partyApi.start(item.vr_id)
        message.success(`一起看已开启,房间号 ${result.roomId}`)
        // 新开 PartyConsole 窗口
        await partyWindowApi.openConsole(result.roomId)
      } catch (e) {
        message.error(e instanceof Error ? e.message : '开启一起看失败')
      } finally {
        partyStarting.value = false
      }
    },
  })
}

onMounted(() => {
  void doSearch()
})
</script>

<template>
  <div ref="rootRef" class="category">
    <!-- 筛选栏（sticky 毛玻璃） -->
    <div class="filter-bar">
      <!-- 第一行：分类药丸 -->
      <div class="filter-bar__row filter-bar__row--pills">
        <button
          v-for="opt in categoryOptions"
          :key="opt.value || 'all'"
          type="button"
          class="pill"
          :class="{ 'pill--active': category === opt.value }"
          @click="selectCategory(opt.value)"
        >
          {{ opt.label }}
        </button>
      </div>

      <!-- 第二行：年份 / 季度 / 排序 / 搜索框 / 管理·取消 -->
      <div class="filter-bar__row filter-bar__row--controls">
        <n-select
          v-model:value="year"
          :options="yearOptions"
          class="filter-select"
          aria-label="年份"
          @update:value="onFilterChange"
        />
        <n-select
          v-model:value="season"
          :options="seasonOptions"
          class="filter-select"
          aria-label="季度"
          @update:value="onFilterChange"
        />
        <n-select
          v-model:value="sort"
          :options="sortOptions"
          class="filter-select filter-select--sort"
          aria-label="排序"
          @update:value="onFilterChange"
        />
        <n-input
          v-model:value="keyword"
          class="search-input"
          placeholder="搜索视频名称"
          clearable
          @keyup.enter="onSearchEnter"
        >
          <template #prefix>
            <n-icon :component="Search" />
          </template>
        </n-input>
        <button
          v-if="!selectMode"
          type="button"
          class="pill pill--action"
          @click="enterSelectMode"
        >
          管理
        </button>
        <button
          v-else
          type="button"
          class="pill pill--action"
          @click="exitSelectMode"
        >
          取消
        </button>
      </div>

      <!-- 多选模式批量操作条 -->
      <div v-if="selectMode" class="filter-bar__row filter-bar__row--bulk">
        <span class="bulk-count">已选 {{ selectedCount }} 部</span>
        <button type="button" class="pill" @click="toggleSelectAll">
          {{ allSelected ? '取消本页全选' : '本页全选' }}
        </button>
        <button type="button" class="pill" @click="invertSelection">反选</button>
        <button
          type="button"
          class="pill pill--danger"
          :disabled="selectedCount === 0 || deleting"
          @click="confirmDelete"
        >
          删除
        </button>
      </div>
    </div>

    <!-- 网格区 -->
    <div class="grid-wrap">
      <!-- 加载覆盖层 -->
      <div v-if="loading" class="grid-overlay">
        <Spinner :size="32" />
      </div>

      <!-- 错误态 -->
      <div v-if="error" class="grid-state">
        <StatusHint variant="error" :title="error" />
        <n-button size="small" @click="doSearch">重试</n-button>
      </div>

      <!-- 空结果 -->
      <div v-else-if="list.length === 0 && !loading" class="grid-state">
        <StatusHint variant="info" title="未找到匹配视频" />
      </div>

      <!-- 视频网格 -->
      <div v-if="list.length > 0" class="video-grid">
        <VideoCard
          v-for="item in list"
          :key="item.vr_id"
          :video-id="item.vr_id"
          :title="item.vr_name"
          :cover="item.vr_cover"
          :episode-count="item.vr_episode_count"
          :category="item.vr_category"
          :year="item.vr_year"
          :season="item.vr_season"
          :view-count="item.vr_view_count"
          :select-mode="selectMode"
          :selected="selectedIds.has(item.vr_id)"
          @click="handleCardClick(item)"
          @download="() => openPicker(item.vr_id, item.vr_name, item.vr_cover)"
          @party="() => handlePartyClick(item)"
          @update:selected="(v: boolean) => toggleSelect(item.vr_id, v)"
        />
      </div>
    </div>

    <!-- 分页 -->
    <div
      v-if="total > PAGE_SIZE && !error && list.length > 0"
      class="pager"
    >
      <n-pagination
        :page="page"
        :page-count="totalPages"
        :page-slot="5"
        @update:page="goToPage"
      />
    </div>

    <!-- 选集下载弹窗 -->
    <EpisodeDownloadPicker
      v-model:show="picker.show"
      :video-id="picker.videoId"
      :video-name="picker.videoName"
      :video-cover="picker.videoCover"
      :channel-id="picker.channelId"
      :episodes="picker.episodes"
    />
  </div>
</template>

<style scoped>
.category {
  display: flex;
  flex-direction: column;
  background: var(--bg-base);
}

/* ===================== 筛选栏 ===================== */
.filter-bar {
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--bg-base);
  backdrop-filter: var(--glass-blur);
  -webkit-backdrop-filter: var(--glass-blur);
  padding: var(--space-4) var(--space-6);
  border-bottom: 1px solid var(--border-subtle);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.filter-bar__row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.filter-bar__row--pills {
  flex-wrap: wrap;
}

.filter-bar__row--controls {
  flex-wrap: wrap;
}

/* 多选模式批量操作条 */
.filter-bar__row--bulk {
  flex-wrap: wrap;
  padding-top: var(--space-2);
  border-top: 1px dashed var(--border-subtle);
}

.bulk-count {
  font-size: 13px;
  color: var(--text-secondary);
  margin-right: var(--space-1);
}

/* 分类药丸 */
.pill {
  border: none;
  border-radius: var(--radius-full);
  padding: 4px 12px;
  background: var(--bg-card);
  color: var(--text-secondary);
  font-family: var(--font-body);
  font-size: 13px;
  line-height: 1.5;
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    color var(--duration-fast) var(--ease-fluid);
}

.pill:hover {
  color: var(--text-primary);
}

.pill--active {
  background: var(--accent-sakura);
  color: #fff;
}

.pill--active:hover {
  color: #fff;
}

/* 顶栏管理/取消按钮：与搜索框同行末尾 */
.pill--action {
  flex-shrink: 0;
  margin-left: var(--space-2);
}

/* 批量删除按钮：危险色 */
.pill--danger {
  background: var(--accent-sakura);
  color: #fff;
  margin-left: auto;
}

.pill--danger:hover {
  color: #fff;
  filter: brightness(1.08);
}

.pill--danger:disabled {
  background: var(--bg-card);
  color: var(--text-tertiary);
  cursor: not-allowed;
  filter: none;
}

/* ===================== 筛选下拉（Naive UI n-select） ===================== */
.filter-select {
  width: 104px;
  flex-shrink: 0;
}

.filter-select--sort {
  width: 88px;
}

/* ===================== 搜索输入框（Naive UI n-input） ===================== */
.search-input {
  flex: 1;
  min-width: 180px;
  margin-left: auto;
}

/* ===================== 网格区 ===================== */
.grid-wrap {
  position: relative;
  min-height: 50vh;
}

.video-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: var(--space-5);
  padding: var(--space-6);
}

.grid-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--bg-base) 65%, transparent);
  z-index: 5;
}

.grid-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-16) var(--space-6);
  min-height: 40vh;
}

/* ===================== 分页（Naive UI n-pagination） ===================== */
.pager {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: var(--space-6);
}
</style>
