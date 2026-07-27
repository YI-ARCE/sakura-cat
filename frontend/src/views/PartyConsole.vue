<script setup lang="ts">
// 一起看控制台
//
// 纯展示窗口:不播放视频,所有播放控制由观看端(浏览器扫码加入)发起。
// 本窗口仅展示:
//   - 邀请面板(二维码 + URL + 在线人数 + 结束一起看按钮)
//   - 当前播放进度(只读,由房主 state_sync 驱动)
//   - 下载区块图(15×16 grid,迅雷式方块)
//   - 分集列表(只读,高亮当前集)
//   - 观看端列表(房主带星标)
//
// 通过 Wails Event 订阅后端推送:
//   party:download_blocks / party:playback_state / party:clients_changed / party:episode_changed
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useMessage } from 'naive-ui'
import QRCode from 'qrcode'
import { Spinner, StatusHint } from '../components/common/Ui'
import {
  partyApi,
  partyWindowApi,
  onPartyDownloadBlocks,
  onPartyPlaybackState,
  onPartyClientsChanged,
  onPartyEpisodeChanged,
  onPartyDurationReported,
  type PartyState,
  type PartyEpisode,
  type PartyDownloadBlocksEvent,
  type PartyPlaybackStateEvent,
  type PartyClientsChangedEvent,
  type PartyEpisodeChangedEvent,
  type PartyDurationReportedEvent,
  type PartyClientInfo,
} from '../services/party'
import { useWindowMeta } from '../composables/useWindowMeta'

const route = useRoute()
const message = useMessage()
const { setTitle } = useWindowMeta()

// ============ 状态 ============

const roomId = ref<string>(String(route.params.roomId || ''))
const loading = ref(true)
const error = ref('')
const partyState = ref<PartyState | null>(null)

// 二维码 DataURL
const qrDataUrl = ref('')

// 当前播放状态(由房主 state_sync 驱动)
const playback = ref({
  ve_id: 0,
  currentTime: 0,
  duration: 0,
  playing: false,
  rate: 1.0,
})

// 下载区块状态
const download = ref<{
  ve_id: number
  downloaded: number[] // 长度 240
  playing_block: number
  downloading_block: number
  speed: number
}>({
  ve_id: 0,
  downloaded: new Array(240).fill(0),
  playing_block: -1,
  downloading_block: -1,
  speed: 0,
})

// 观看端列表
const clients = ref<PartyClientInfo[]>([])
const hostName = ref('')

// 结束一起看中标记
const stopping = ref(false)

// ============ 计算属性 ============

const inviteUrl = computed(() => partyState.value?.inviteUrl || '')
const onlineCount = computed(() => clients.value.length)
const episodeList = computed(() => partyState.value?.episodeList || [])
const currentVeId = computed(() => playback.value.ve_id || partyState.value?.current_ve_id || 0)
const currentEpisode = computed(() =>
  episodeList.value.find((e) => e.ve_id === currentVeId.value),
)
const currentEpisodeLabel = computed(() => {
  const ep = currentEpisode.value
  if (!ep) return '—'
  return ep.ve_title ? `第 ${ep.ve_collect} 集 · ${ep.ve_title}` : `第 ${ep.ve_collect} 集`
})

// 进度条百分比
const progressPercent = computed(() => {
  if (playback.value.duration <= 0) return 0
  const p = (playback.value.currentTime / playback.value.duration) * 100
  return Math.max(0, Math.min(100, p))
})

// 时间显示 12:30 / 24:00
const timeDisplay = computed(() => {
  return `${formatTime(playback.value.currentTime)} / ${formatTime(playback.value.duration)}`
})

// 下载进度百分比
const downloadPercent = computed(() => {
  const done = download.value.downloaded.filter((v) => v === 1).length
  return Math.round((done / 240) * 100)
})

// 下载速度显示
const speedDisplay = computed(() => {
  const s = download.value.speed
  if (s <= 0) return ''
  if (s >= 1024 * 1024) return `${(s / 1024 / 1024).toFixed(2)} MB/s`
  if (s >= 1024) return `${(s / 1024).toFixed(1)} KB/s`
  return `${s} B/s`
})

// 区块图 240 块的状态(0=未下载 1=已下载 2=播放块 3=下载块)
const blockStates = computed(() => {
  const result = new Array(240).fill(0)
  for (let i = 0; i < 240; i++) {
    if (download.value.downloaded[i] === 1) result[i] = 1
  }
  if (download.value.playing_block >= 0 && download.value.playing_block < 240) {
    result[download.value.playing_block] = 2
  }
  if (download.value.downloading_block >= 0 && download.value.downloading_block < 240) {
    // 下载块优先级高于播放块(蓝色脉冲覆盖红色脉冲)
    result[download.value.downloading_block] = 3
  }
  return result
})

// ============ 工具函数 ============

function formatTime(sec: number): string {
  if (!sec || sec < 0 || !isFinite(sec)) return '00:00'
  const m = Math.floor(sec / 60)
  const s = Math.floor(sec % 60)
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

// ============ Wails Event 订阅 ============

let unsubBlocks: (() => void) | null = null
let unsubPlayback: (() => void) | null = null
let unsubClients: (() => void) | null = null
let unsubEpisode: (() => void) | null = null
let unsubDuration: (() => void) | null = null

function subscribeEvents() {
  unsubBlocks = onPartyDownloadBlocks((data) => {
    if (data.roomId !== roomId.value) return
    download.value = {
      ve_id: data.ve_id,
      downloaded: data.downloaded,
      playing_block: data.playing_block,
      downloading_block: data.downloading_block,
      speed: data.speed,
    }
  })
  unsubPlayback = onPartyPlaybackState((data) => {
    if (data.roomId !== roomId.value) return
    // 仅更新播放状态字段,duration 由独立的 party:duration_reported 事件更新
    playback.value = {
      ve_id: data.ve_id,
      currentTime: data.currentTime,
      duration: playback.value.duration,
      playing: data.playing,
      rate: data.rate,
    }
  })
  unsubDuration = onPartyDurationReported((data) => {
    if (data.roomId !== roomId.value) return
    // 仅更新 duration 字段,不影响 currentTime/playing/rate
    playback.value = {
      ...playback.value,
      ve_id: data.ve_id,
      duration: data.duration,
    }
  })
  unsubClients = onPartyClientsChanged((data) => {
    if (data.roomId !== roomId.value) return
    clients.value = data.clients
    hostName.value = data.hostName
  })
  unsubEpisode = onPartyEpisodeChanged((data) => {
    if (data.roomId !== roomId.value) return
    // 切集后重置播放进度(duration 等待新集 report_duration)
    playback.value = {
      ve_id: data.ve_id,
      currentTime: 0,
      duration: 0,
      playing: false,
      rate: 1.0,
    }
    if (partyState.value) {
      partyState.value.current_ve_id = data.ve_id
    }
  })
}

function unsubscribeEvents() {
  unsubBlocks?.()
  unsubPlayback?.()
  unsubClients?.()
  unsubEpisode?.()
  unsubDuration?.()
  unsubBlocks = unsubPlayback = unsubClients = unsubEpisode = unsubDuration = null
}

// ============ 初始化 ============

async function loadPartyState() {
  loading.value = true
  error.value = ''
  try {
    const state = await partyApi.getState(roomId.value)
    partyState.value = state
    playback.value.ve_id = state.current_ve_id
    // 初始化在线列表与房主名称(后续由 party:clients_changed 事件增量更新)
    clients.value = state.clients || []
    hostName.value = state.hostName || ''
    setTitle(`一起看 - ${state.roomId}`)
    // 生成二维码
    if (state.inviteUrl) {
      qrDataUrl.value = await QRCode.toDataURL(state.inviteUrl, {
        width: 192,
        margin: 1,
        color: { dark: '#1a1a1a', light: '#ffffff' },
      })
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载一起看状态失败'
  } finally {
    loading.value = false
  }
}

// ============ 结束一起看 ============

async function handleStopParty() {
  if (stopping.value) return
  stopping.value = true
  try {
    await partyApi.stop(roomId.value)
    message.success('一起看已结束')
    // 关闭窗口
    await partyWindowApi.closeConsole(roomId.value)
  } catch (e) {
    message.error(e instanceof Error ? e.message : '结束一起看失败')
  } finally {
    stopping.value = false
  }
}

// ============ 生命周期 ============

onMounted(async () => {
  await loadPartyState()
  if (!error.value) {
    subscribeEvents()
  }
})

onBeforeUnmount(() => {
  unsubscribeEvents()
})

// roomId 可能在路由参数变化时更新(理论上单例窗口不会,但兜底)
watch(
  () => route.params.roomId,
  (newId) => {
    if (newId && String(newId) !== roomId.value) {
      roomId.value = String(newId)
      unsubscribeEvents()
      void loadPartyState().then(() => {
        if (!error.value) subscribeEvents()
      })
    }
  },
)
</script>

<template>
  <div class="party-console">
    <!-- 加载态 -->
    <div v-if="loading" class="party-console__state">
      <Spinner :size="32" />
    </div>

    <!-- 错误态 -->
    <div v-else-if="error" class="party-console__state">
      <StatusHint variant="error" :title="error" />
    </div>

    <!-- 主体 -->
    <div v-else class="party-console__body">
      <!-- 邀请面板 -->
      <section class="invite-panel">
        <div class="invite-panel__qr">
          <img v-if="qrDataUrl" :src="qrDataUrl" alt="邀请二维码" class="invite-panel__qr-img" />
          <div v-else class="invite-panel__qr-placeholder">二维码生成中</div>
        </div>
        <div class="invite-panel__info">
          <div class="invite-panel__url" :title="inviteUrl">{{ inviteUrl }}</div>
          <div class="invite-panel__online">
            在线:<strong>{{ onlineCount }}</strong> 人
            <span v-if="hostName" class="invite-panel__host">· 房主 {{ hostName }}</span>
          </div>
        </div>
        <button
          type="button"
          class="invite-panel__stop"
          :disabled="stopping"
          @click="handleStopParty"
        >
          {{ stopping ? '结束中...' : '结束一起看' }}
        </button>
      </section>

      <!-- 当前播放进度(只读) -->
      <section class="playback-panel">
        <span class="playback-panel__label">{{ currentEpisodeLabel }}</span>
        <div class="playback-panel__track">
          <div class="playback-panel__track-fill" :style="{ width: `${progressPercent}%` }"></div>
        </div>
        <span class="playback-panel__time">{{ timeDisplay }}</span>
      </section>

      <!-- 下载区块图 -->
      <section class="download-panel">
        <div class="download-panel__header">
          <span>下载进度 {{ downloadPercent }}%</span>
          <span class="download-panel__speed">{{ speedDisplay }}</span>
        </div>
        <div class="download-panel__grid">
          <div
            v-for="(state, idx) in blockStates"
            :key="idx"
            class="block"
            :class="{
              'block--downloaded': state === 1,
              'block--playing': state === 2,
              'block--downloading': state === 3,
            }"
          ></div>
        </div>
        <div class="download-panel__legend">
          <span class="legend-item"><i class="legend legend--undownloaded"></i>未下载</span>
          <span class="legend-item"><i class="legend legend--downloaded"></i>已下载</span>
          <span class="legend-item"><i class="legend legend--playing"></i>播放位置</span>
          <span class="legend-item"><i class="legend legend--downloading"></i>下载位置</span>
        </div>
      </section>

      <!-- 底部:分集列表 + 观看端列表 -->
      <section class="bottom-panels">
        <!-- 分集列表(只读) -->
        <div class="episode-list">
          <div class="episode-list__header">分集({{ episodeList.length }})</div>
          <div class="episode-list__body">
            <button
              v-for="ep in episodeList"
              :key="ep.ve_id"
              type="button"
              class="episode-item"
              :class="{ 'episode-item--current': ep.ve_id === currentVeId }"
              :title="ep.ve_title || `第 ${ep.ve_collect} 集`"
              disabled
            >
              {{ ep.ve_collect }}
            </button>
          </div>
        </div>

        <!-- 观看端列表 -->
        <div class="clients-list">
          <div class="clients-list__header">观看端({{ onlineCount }})</div>
          <div class="clients-list__body">
            <div v-if="clients.length === 0" class="clients-list__empty">
              暂无观看端,扫码加入
            </div>
            <div
              v-for="c in clients"
              :key="c.name"
              class="client-item"
              :class="{ 'client-item--host': c.isHost }"
            >
              <span class="client-item__star">{{ c.isHost ? '★' : '·' }}</span>
              <span class="client-item__name">{{ c.name }}</span>
              <span class="client-item__role">{{ c.isHost ? '房主' : '跟随' }}</span>
            </div>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.party-console {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-base);
  overflow: hidden;
}

.party-console__state {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
  min-height: 50vh;
}

.party-console__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  height: 100%;
  overflow: hidden;
}

/* ===================== 邀请面板 ===================== */
.invite-panel {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  flex-shrink: 0;
}

.invite-panel__qr {
  width: 96px;
  height: 96px;
  flex-shrink: 0;
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
}

.invite-panel__qr-img {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: contain;
}

.invite-panel__qr-placeholder {
  font-size: 11px;
  color: var(--text-tertiary);
  text-align: center;
  padding: var(--space-1);
}

.invite-panel__info {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.invite-panel__url {
  font-size: 12px;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono, monospace);
}

.invite-panel__online {
  font-size: 13px;
  color: var(--text-primary);
}

.invite-panel__online strong {
  color: var(--accent-sakura);
  font-weight: 600;
}

.invite-panel__host {
  color: var(--text-tertiary);
  margin-left: var(--space-1);
}

.invite-panel__stop {
  flex-shrink: 0;
  border: none;
  border-radius: var(--radius-sm);
  padding: 6px 14px;
  background: var(--color-error, #ef4444);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
  transition: filter var(--duration-fast) var(--ease-fluid);
}

.invite-panel__stop:hover:not(:disabled) {
  filter: brightness(1.08);
}

.invite-panel__stop:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* ===================== 播放进度面板(只读) ===================== */
.playback-panel {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  flex-shrink: 0;
}

.playback-panel__label {
  flex-shrink: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.playback-panel__track {
  flex: 1;
  height: 4px;
  background: var(--border-subtle);
  border-radius: var(--radius-full);
  overflow: hidden;
  cursor: default;
}

.playback-panel__track-fill {
  height: 100%;
  background: var(--accent-sakura);
  border-radius: var(--radius-full);
  transition: width var(--duration-normal) var(--ease-fluid);
}

.playback-panel__time {
  flex-shrink: 0;
  font-size: 12px;
  color: var(--text-tertiary);
  font-family: var(--font-mono, monospace);
}

/* ===================== 下载区块图 ===================== */
.download-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  flex: 1;
  overflow: hidden;
  min-height: 0;
}

.download-panel__header {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: var(--text-secondary);
  flex-shrink: 0;
}

.download-panel__speed {
  color: var(--text-tertiary);
  font-family: var(--font-mono, monospace);
}

.download-panel__grid {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(16, 1fr);
  grid-template-rows: repeat(15, 1fr);
  gap: 2px;
  overflow: hidden;
  min-height: 0;
}

.block {
  background: var(--border-subtle);
  border-radius: 2px;
  transition: background-color var(--duration-fast) var(--ease-fluid);
}

.block--downloaded {
  background: var(--accent-sakura);
}

.block--playing {
  background: var(--color-error, #ef4444);
  animation: pulse-playing 1.2s ease-in-out infinite;
}

.block--downloading {
  background: var(--color-info, #3b82f6);
  animation: pulse-downloading 0.8s ease-in-out infinite;
}

@keyframes pulse-playing {
  0%, 100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.6;
    transform: scale(0.92);
  }
}

@keyframes pulse-downloading {
  0%, 100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.5;
    transform: scale(0.85);
  }
}

.download-panel__legend {
  display: flex;
  gap: var(--space-3);
  font-size: 11px;
  color: var(--text-tertiary);
  flex-shrink: 0;
  flex-wrap: wrap;
}

.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.legend {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 2px;
}

.legend--undownloaded {
  background: var(--border-subtle);
}

.legend--downloaded {
  background: var(--accent-sakura);
}

.legend--playing {
  background: var(--color-error, #ef4444);
}

.legend--downloading {
  background: var(--color-info, #3b82f6);
}

/* ===================== 底部面板 ===================== */
.bottom-panels {
  display: flex;
  gap: var(--space-3);
  height: 140px;
  flex-shrink: 0;
}

/* 分集列表(只读) */
.episode-list {
  flex: 1;
  display: flex;
  flex-direction: column;
  padding: var(--space-2);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  overflow: hidden;
  min-width: 0;
}

.episode-list__header,
.clients-list__header {
  font-size: 12px;
  color: var(--text-secondary);
  padding: 0 var(--space-1) var(--space-1);
  flex-shrink: 0;
}

.episode-list__body {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: var(--space-1);
  overflow-y: auto;
}

.episode-item {
  border: none;
  border-radius: var(--radius-xs);
  padding: 4px 0;
  background: var(--bg-base);
  color: var(--text-secondary);
  font-size: 12px;
  cursor: default;
  text-align: center;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    color var(--duration-fast) var(--ease-fluid);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.episode-item--current {
  background: var(--accent-sakura);
  color: #fff;
  font-weight: 600;
}

/* 观看端列表 */
.clients-list {
  flex: 0 0 200px;
  display: flex;
  flex-direction: column;
  padding: var(--space-2);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.clients-list__body {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.clients-list__empty {
  font-size: 12px;
  color: var(--text-tertiary);
  text-align: center;
  padding: var(--space-4) var(--space-2);
}

.client-item {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: 4px var(--space-1);
  font-size: 12px;
  color: var(--text-primary);
  border-radius: var(--radius-xs);
}

.client-item--host {
  background: color-mix(in srgb, var(--accent-sakura) 12%, transparent);
}

.client-item__star {
  color: var(--accent-sakura);
  width: 12px;
  flex-shrink: 0;
}

.client-item--host .client-item__star {
  font-weight: 700;
}

.client-item__name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.client-item__role {
  font-size: 10px;
  color: var(--text-tertiary);
  flex-shrink: 0;
}

.client-item--host .client-item__role {
  color: var(--accent-sakura);
}
</style>
