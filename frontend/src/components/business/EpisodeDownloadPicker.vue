<script setup lang="ts">
// 选集下载弹窗组件（可复用）
//
// 职责：接收视频源信息与分集列表，打开时查询每集本地下载状态，
// 已下载/下载中/已失效的分集禁用勾选，提交时调用 createTaskFromMessagesSync
// 创建下载任务并 emit success。
// 复用场景：首页/分类页视频卡片下载入口、视频详情页下载按钮等。
import { ref, computed, watch } from 'vue'

import { BaseImage } from '../common/Ui'
import { useNaiveFeedback } from '../../composables/useNaiveFeedback'
import {
  createTaskFromMessagesSync,
  getEpisodeDownloadStatus,
  type EpisodeDownloadStatus,
} from '../../services/download'
import type { EpisodeItem } from '../../services/repo'

interface Props {
  /** 弹窗显隐（v-model:show） */
  show: boolean
  /** 视频 ID（video_repo.vr_id） */
  videoId: number
  /** 视频标题 */
  videoName: string
  /** 视频封面相对路径 */
  videoCover: string
  /** 频道 ID（TG peer_id，对应 vs_channel_id） */
  channelId: number
  /** 分集列表（来自 video_episodes 查询） */
  episodes: EpisodeItem[]
  /** 视频文件名前缀（可选） */
  videoPrefix?: string
}

const props = withDefaults(defineProps<Props>(), {
  videoPrefix: '',
})

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'success', taskId: number): void
}>()

const feedback = useNaiveFeedback()

// 加载与提交状态
const loading = ref(false)
const submitting = ref(false)

// message_id → status 映射（来自后端 EpisodeDownloadStatus）
const statusMap = ref<Map<number, string>>(new Map())

// 选中的 message_id 列表
const selectedIds = ref<number[]>([])

// 按 ve_collect 排序后的分集（ve_collect=0 排最后，保持稳定）
const sortedEpisodes = computed(() => {
  const arr = [...props.episodes]
  arr.sort((a, b) => {
    const ca = a.ve_collect || 0
    const cb = b.ve_collect || 0
    if (ca === 0 && cb !== 0) return 1
    if (cb === 0 && ca !== 0) return -1
    return ca - cb
  })
  return arr
})

// 富化后的分集项（带 disabled / 状态文案 / 状态 class）
interface EnrichedEpisode {
  ve_id: number
  ve_message_id: number
  ve_collect: number
  ve_title: string
  ve_status: number
  disabled: boolean
  statusLabel: string
  statusClass: string
}

const enrichedEpisodes = computed<EnrichedEpisode[]>(() =>
  sortedEpisodes.value.map((e) => {
    const s = statusMap.value.get(e.ve_message_id) || ''
    let disabled = false
    let label = ''
    let cls = 'none'
    if (e.ve_status === 2) {
      disabled = true
      label = '已失效'
      cls = 'invalid'
    } else if (s === 'completed') {
      disabled = true
      label = '已下载'
      cls = 'completed'
    } else if (s === 'downloading') {
      disabled = true
      label = '下载中'
      cls = 'downloading'
    } else if (s === 'pending') {
      label = '待下载'
      cls = 'pending'
    } else if (s === 'failed') {
      label = '失败'
      cls = 'failed'
    }
    return {
      ve_id: e.ve_id,
      ve_message_id: e.ve_message_id,
      ve_collect: e.ve_collect,
      ve_title: e.ve_title,
      ve_status: e.ve_status,
      disabled,
      statusLabel: label,
      statusClass: cls,
    }
  }),
)

const selectableCount = computed(
  () => enrichedEpisodes.value.filter((e) => !e.disabled).length,
)

const selectedCount = computed(() => selectedIds.value.length)

// 加载分集下载状态
async function loadStatus() {
  if (!props.episodes.length || !props.channelId) return
  loading.value = true
  try {
    const ids = props.episodes.map((e) => e.ve_message_id)
    const list: EpisodeDownloadStatus[] = await getEpisodeDownloadStatus(
      props.channelId,
      ids,
    )
    const m = new Map<number, string>()
    list.forEach((item) => {
      if (item && item.message_id) m.set(item.message_id, item.status || '')
    })
    statusMap.value = m
  } catch (e) {
    feedback.error(e instanceof Error ? e.message : '查询下载状态失败')
    statusMap.value = new Map()
  } finally {
    loading.value = false
  }
}

function reset() {
  selectedIds.value = []
  statusMap.value = new Map()
}

// 打开时加载状态
watch(
  () => props.show,
  (v) => {
    if (v) {
      reset()
      loadStatus()
    }
  },
)

// 勾选/取消勾选
function toggle(msgId: number) {
  const idx = selectedIds.value.indexOf(msgId)
  if (idx >= 0) {
    selectedIds.value.splice(idx, 1)
  } else {
    selectedIds.value.push(msgId)
  }
}

function isSelected(msgId: number): boolean {
  return selectedIds.value.includes(msgId)
}

// 全选（仅可选分集）
function selectAll() {
  selectedIds.value = enrichedEpisodes.value
    .filter((e) => !e.disabled)
    .map((e) => e.ve_message_id)
}

// 反选（仅可选范围内反转）
function invertSelection() {
  const set = new Set(selectedIds.value)
  selectedIds.value = enrichedEpisodes.value
    .filter((e) => !e.disabled && !set.has(e.ve_message_id))
    .map((e) => e.ve_message_id)
}

// 仅未下载（等价于全选可选分集；语义入口，保留独立按钮便于用户快速回到"全部未下载"态）
function selectUndownloaded() {
  selectAll()
}

function close() {
  emit('update:show', false)
}

// 提交下载
async function handleSubmit() {
  if (!selectedIds.value.length || submitting.value) return
  submitting.value = true
  try {
    const selectedSet = new Set(selectedIds.value)
    // 按 enrichedEpisodes 顺序构建输入（保持集数顺序）
    const episodes = enrichedEpisodes.value
      .filter((e) => selectedSet.has(e.ve_message_id))
      .map((e) => ({
        message_id: e.ve_message_id,
        episode_number: e.ve_collect || 0,
        title: e.ve_title || '',
      }))
    const taskId = await createTaskFromMessagesSync({
      vr_id: props.videoId,
      vr_name: props.videoName,
      vr_cover: props.videoCover,
      channel_id: props.channelId,
      episodes,
      video_prefix: props.videoPrefix,
    })
    feedback.success(`已创建下载任务 #${taskId}`)
    emit('success', taskId)
    emit('update:show', false)
  } catch (e) {
    feedback.error(e instanceof Error ? e.message : '创建下载任务失败')
  } finally {
    submitting.value = false
  }
}

// 集数展示文案
function episodeLabel(e: EnrichedEpisode): string {
  return e.ve_collect > 0 ? `第 ${e.ve_collect} 集` : '未编号'
}
</script>

<template>
  <n-modal
    :show="show"
    :mask-closable="false"
    :close-on-esc="!submitting"
    style="width: 640px; max-width: 92vw"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <div class="picker">
      <!-- 头部 -->
      <header class="picker__header">
        <BaseImage
          :src="videoCover"
          :alt="videoName"
          class="picker__cover"
        />
        <div class="picker__headinfo">
          <h3 class="picker__title" :title="videoName">
            {{ videoName || '选择分集下载' }}
          </h3>
          <p class="picker__subtitle">
            共 {{ episodes.length }} 集 · 可选 {{ selectableCount }} 集
          </p>
        </div>
        <button
          class="picker__close"
          type="button"
          aria-label="关闭"
          :disabled="submitting"
          @click="close"
        >
          ×
        </button>
      </header>

      <!-- 工具栏 -->
      <div class="picker__toolbar">
        <n-button
          size="small"
          :disabled="loading || submitting"
          @click="selectAll"
        >
          全选
        </n-button>
        <n-button
          size="small"
          :disabled="loading || submitting"
          @click="invertSelection"
        >
          反选
        </n-button>
        <n-button
          size="small"
          :disabled="loading || submitting"
          @click="selectUndownloaded"
        >
          仅未下载
        </n-button>
        <span class="picker__spacer" />
        <span v-if="loading" class="picker__loading">查询状态中…</span>
      </div>

      <!-- 分集列表 -->
      <div class="picker__list">
        <n-spin :show="loading">
          <div v-if="!episodes.length" class="picker__empty">暂无分集数据</div>
          <div v-else class="picker__grid">
            <label
              v-for="e in enrichedEpisodes"
              :key="e.ve_id"
              class="ep-item"
              :class="{
                'is-disabled': e.disabled,
                'is-selected': isSelected(e.ve_message_id),
              }"
            >
              <input
                type="checkbox"
                class="ep-item__checkbox"
                :checked="isSelected(e.ve_message_id)"
                :disabled="e.disabled || submitting"
                @change="toggle(e.ve_message_id)"
              />
              <span class="ep-item__check" aria-hidden="true">
                <svg viewBox="0 0 16 16" class="ep-item__check-icon">
                  <path
                    d="M3.5 8.5l3 3 6-6.5"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  />
                </svg>
              </span>
              <span class="ep-item__num">{{ episodeLabel(e) }}</span>
              <span
                v-if="e.statusLabel"
                class="ep-item__status"
                :class="`is-${e.statusClass}`"
              >
                {{ e.statusLabel }}
              </span>
            </label>
          </div>
        </n-spin>
      </div>

      <!-- 底部 -->
      <footer class="picker__footer">
        <span class="picker__count">已选 {{ selectedCount }} 集</span>
        <div class="picker__actions">
          <n-button :disabled="submitting" @click="close">取消</n-button>
          <n-button
            type="primary"
            :loading="submitting"
            :disabled="!selectedCount"
            @click="handleSubmit"
          >
            开始下载
          </n-button>
        </div>
      </footer>
    </div>
  </n-modal>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  max-height: 80vh;
  background: var(--bg-card-solid);
  border-radius: var(--radius-lg);
  overflow: hidden;
  box-shadow: var(--shadow-card);
}

/* ===================== 头部 ===================== */
.picker__header {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}

.picker__cover {
  flex-shrink: 0;
  width: 80px;
  height: 120px;
  border-radius: var(--radius-sm);
  object-fit: cover;
  background: var(--bg-card);
}

.picker__headinfo {
  flex: 1;
  min-width: 0;
}

.picker__title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 16px;
  font-weight: 700;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.picker__subtitle {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--text-tertiary);
}

.picker__close {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--text-secondary);
  font-size: 26px;
  line-height: 1;
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    color var(--duration-fast) var(--ease-fluid);
}

.picker__close:hover:not(:disabled) {
  background: var(--bg-card);
  color: var(--text-primary);
}

.picker__close:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* ===================== 工具栏 ===================== */
.picker__toolbar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}

.picker__spacer {
  flex: 1;
}

.picker__loading {
  font-size: 12px;
  color: var(--text-tertiary);
}

/* ===================== 分集列表 ===================== */
.picker__list {
  flex: 1;
  min-height: 120px;
  overflow-y: auto;
  padding: var(--space-4) var(--space-5);
}

.picker__empty {
  text-align: center;
  color: var(--text-tertiary);
  font-size: 13px;
  padding: var(--space-8) 0;
}

.picker__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: var(--space-2);
}

.ep-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  cursor: pointer;
  user-select: none;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    border-color var(--duration-fast) var(--ease-fluid);
  border: 1px solid var(--border-subtle);
}

.ep-item:hover:not(.is-disabled) {
  background: color-mix(in srgb, var(--accent-sakura) 10%, var(--bg-card));
  border-color: color-mix(in srgb, var(--accent-sakura) 30%, transparent);
}

.ep-item.is-selected {
  background: color-mix(in srgb, var(--accent-sakura) 16%, var(--bg-card));
  border-color: color-mix(in srgb, var(--accent-sakura) 45%, transparent);
}

.ep-item.is-disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* 原生 checkbox 视觉隐藏,保留可访问性与键盘交互 */
.ep-item__checkbox {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
  cursor: inherit;
}

/* 自定义勾选盒 */
.ep-item__check {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
  border-radius: var(--radius-xs);
  border: 1px solid var(--border-strong);
  background: transparent;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  transition: background-color var(--duration-fast) var(--ease-fluid),
    border-color var(--duration-fast) var(--ease-fluid);
}

.ep-item__check-icon {
  width: 12px;
  height: 12px;
  display: block;
  opacity: 0;
  transform: scale(0.6);
  transition: opacity var(--duration-fast) var(--ease-fluid),
    transform var(--duration-fast) var(--ease-fluid);
}

/* 选中态:樱花粉填充 + 白勾显现 */
.ep-item.is-selected .ep-item__check {
  background: var(--accent-sakura);
  border-color: var(--accent-sakura);
}

.ep-item.is-selected .ep-item__check-icon {
  opacity: 1;
  transform: scale(1);
}

/* 禁用态:灰底 + 隐藏勾 */
.ep-item.is-disabled .ep-item__check {
  background: var(--bg-card-solid);
  border-color: var(--border-subtle);
}

/* 键盘聚焦轮廓 */
.ep-item__checkbox:focus-visible + .ep-item__check {
  box-shadow: var(--shadow-focus-sakura);
}

.ep-item__num {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ep-item.is-disabled .ep-item__num {
  color: var(--text-tertiary);
}

.ep-item__status {
  flex-shrink: 0;
  font-size: 11px;
  padding: 1px 6px;
  border-radius: var(--radius-full);
  font-weight: 600;
}

.ep-item__status.is-completed {
  color: var(--color-success);
  background: color-mix(in srgb, var(--color-success) 15%, transparent);
}

.ep-item__status.is-downloading {
  color: var(--accent-cyan);
  background: color-mix(in srgb, var(--accent-cyan) 15%, transparent);
}

.ep-item__status.is-pending {
  color: var(--color-warning);
  background: color-mix(in srgb, var(--color-warning) 15%, transparent);
}

.ep-item__status.is-failed {
  color: var(--color-error);
  background: color-mix(in srgb, var(--color-error) 15%, transparent);
}

.ep-item__status.is-invalid {
  color: var(--text-tertiary);
  background: var(--bg-card-solid);
}

/* ===================== 底部 ===================== */
.picker__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-5);
  border-top: 1px solid var(--border-subtle);
}

.picker__count {
  font-size: 13px;
  color: var(--text-secondary);
}

.picker__actions {
  display: flex;
  gap: var(--space-2);
}
</style>
