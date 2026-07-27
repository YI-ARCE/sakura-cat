<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { VideoPlayer } from '../components/business'
import { Spinner, StatusHint } from '../components/common/Ui'
import { getEpisodeStream } from '../services/video'
import { useWindowMeta } from '../composables/useWindowMeta'

// 与 Repo.vue 约定的 localStorage key
const REPO_PLAYER_PAYLOAD_KEY = 'repo-player-payload'

// payload 结构：由 Repo.vue 写入
interface RepoPlayerPayloadMessage {
  message_id: number
  ve_collect: number
  ve_title: string
  file_name: string
}
interface RepoPlayerPayload {
  channelName: string
  channelId: number
  messages: RepoPlayerPayloadMessage[]
}

// 内部分集结构：把 payload 转为可播放形态
interface PlayableEpisode {
  ve_collect: number
  ve_title: string
  vs_channel_id: number
  ve_message_id: number
}

// 调用 useWindowMeta 注册标题 watcher
const { setTitle } = useWindowMeta()

const channelName = ref('')
const episodes = ref<PlayableEpisode[]>([])
const currentEpisode = ref<PlayableEpisode | null>(null)
const streamUrl = ref('')
const streamError = ref<string | null>(null)
const streamLoading = ref(false)
const error = ref<string | null>(null)

const episodeListRef = ref<HTMLElement | null>(null)

// 计算：VideoPlayer 顶部标题
const playerTitle = computed(() => {
  const t = channelName.value || '频道播放'
  const ep = currentEpisode.value
  if (ep && ep.ve_collect > 0) return `${t} - 第${ep.ve_collect}集`
  return t
})

// 当前集在列表中的索引
const currentEpisodeIndex = computed(() => {
  if (!currentEpisode.value) return -1
  return episodes.value.findIndex(
    (e) =>
      e.vs_channel_id === currentEpisode.value!.vs_channel_id &&
      e.ve_message_id === currentEpisode.value!.ve_message_id
  )
})

const hasPrev = computed(() => currentEpisodeIndex.value > 0)
const hasNext = computed(
  () =>
    currentEpisodeIndex.value >= 0 &&
    currentEpisodeIndex.value < episodes.value.length - 1
)

// grid 正方形格子内显示文案：有集号显示纯数字，否则显示标题前两字
function episodeDisplayText(ep: PlayableEpisode): string {
  if (ep.ve_collect > 0) return String(ep.ve_collect)
  if (ep.ve_title) return ep.ve_title.slice(0, 2)
  return '?'
}

// 按显示文案长度返回字号档位 class
function episodeLenClass(ep: PlayableEpisode): string {
  const len = episodeDisplayText(ep).length
  if (len <= 1) return 'is-len1'
  if (len === 2) return 'is-len2'
  if (len === 3) return 'is-len3'
  return 'is-len4'
}

// 同步窗口标题
function syncWindowTitle() {
  const t = channelName.value || '樱花猫'
  const ep = currentEpisode.value
  const title =
    ep && ep.ve_collect > 0
      ? `${t} - 第${ep.ve_collect}集 - 樱花猫`
      : `${t} - 樱花猫`
  setTitle(title)
}

// 切换分集：拉取流地址
async function selectEpisode(episode: PlayableEpisode) {
  currentEpisode.value = episode
  streamError.value = null
  streamLoading.value = true
  streamUrl.value = ''
  try {
    streamUrl.value = await getEpisodeStream(
      episode.vs_channel_id,
      episode.ve_message_id
    )
  } catch (e) {
    streamError.value = e instanceof Error ? e.message : String(e)
  } finally {
    streamLoading.value = false
  }
  syncWindowTitle()
  // 自动滚动至当前集
  await nextTick()
  episodeListRef.value
    ?.querySelector('[data-current]')
    ?.scrollIntoView({ block: 'nearest' })
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
  if (hasNext.value) handleNext()
}

onMounted(() => {
  let payload: RepoPlayerPayload | null = null
  try {
    const raw = localStorage.getItem(REPO_PLAYER_PAYLOAD_KEY)
    if (raw) payload = JSON.parse(raw) as RepoPlayerPayload
  } catch {
    error.value = '播放数据解析失败'
    return
  }
  if (!payload) {
    error.value = '未找到播放数据'
    return
  }
  channelName.value = payload.channelName || ''
  episodes.value = (payload.messages || [])
    .filter((m) => m.message_id > 0)
    .map((m) => ({
      ve_collect: m.ve_collect || 0,
      ve_title: m.ve_title || '',
      vs_channel_id: payload!.channelId,
      ve_message_id: m.message_id,
    }))
  if (episodes.value.length === 0) {
    error.value = '没有可播放的分集'
    return
  }
  syncWindowTitle()
  // 默认选第一集
  void selectEpisode(episodes.value[0])
})

onBeforeUnmount(() => {
  // 卸载前清除 localStorage 中的 payload，避免脏数据
  try {
    localStorage.removeItem(REPO_PLAYER_PAYLOAD_KEY)
  } catch {
    // 静默
  }
})
</script>

<template>
  <div class="repo-player">
    <!-- 左侧播放区 -->
    <div class="player-area">
      <div class="player">
        <VideoPlayer
          v-if="streamUrl && !streamError"
          :src="streamUrl"
          :title="playerTitle"
          :has-prev="hasPrev"
          :has-next="hasNext"
          auto-play
          @prev="handlePrev"
          @next="handleNext"
          @ended="handleEnded"
        />
        <div v-else-if="streamLoading" class="player__state">
          <Spinner :size="48" />
        </div>
        <div v-else-if="error" class="player__state">
          <StatusHint variant="error" title="播放失败" :description="error" />
        </div>
        <div v-else-if="streamError" class="player__state">
          <StatusHint
            variant="error"
            title="视频流加载失败"
            :description="streamError"
          />
        </div>
        <div v-else-if="episodes.length === 0" class="player__state">
          <StatusHint variant="info" title="暂无分集可播放" />
        </div>
      </div>
    </div>

    <!-- 右侧边栏 -->
    <aside class="sidebar">
      <div class="sidebar__header">
        <h2 class="sidebar__title">{{ channelName || '频道播放' }}</h2>
      </div>
      <div ref="episodeListRef" class="episode-list">
        <div v-if="error" class="episode-list__state">
          <StatusHint variant="error" size="sm" title="加载失败" :description="error" />
        </div>
        <div v-else-if="episodes.length === 0" class="episode-list__empty">
          暂无分集
        </div>
        <template v-else>
          <div
            v-for="ep in episodes"
            :key="ep.ve_message_id"
            class="episode-list__item"
            :class="[
              episodeLenClass(ep),
              { 'is-current': currentEpisode?.ve_message_id === ep.ve_message_id },
            ]"
            :data-current="
              currentEpisode?.ve_message_id === ep.ve_message_id ? '' : undefined
            "
            @click="selectEpisode(ep)"
          >
            <span class="episode-list__item-label">{{ episodeDisplayText(ep) }}</span>
          </div>
        </template>
      </div>
    </aside>
  </div>
</template>

<style scoped>
.repo-player {
  display: flex;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: var(--bg-elevated);
}

/* 左侧播放区 */
.player-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

.player {
  position: relative;
  flex: 1;
  overflow: hidden;
  background: #000;
  min-height: 0;
  border-radius: 10px;
}

.player__state {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* 右侧边栏 */
.sidebar {
  width: 360px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-elevated);
}

.sidebar__header {
  flex-shrink: 0;
  padding: var(--space-4);
  border-bottom: 1px solid var(--border-subtle);
}

.sidebar__title {
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

/* 分集列表 grid */
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

/* 滚动条 */
.episode-list::-webkit-scrollbar {
  width: 6px;
}

.episode-list::-webkit-scrollbar-thumb {
  background: var(--border-strong);
  border-radius: var(--radius-full);
}

.episode-list::-webkit-scrollbar-thumb:hover {
  background: var(--accent-sakura);
}

.episode-list::-webkit-scrollbar-track {
  background: transparent;
}

@media (prefers-reduced-motion: reduce) {
  .episode-list__item {
    transition: none;
  }
}
</style>
