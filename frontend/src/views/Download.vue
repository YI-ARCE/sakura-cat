<script setup lang="ts">
// 下载任务页（卡片式布局）
//
// - 卡片：封面 + 视频标题 + 集数统计 + 状态药丸 + 进度条 + 操作按钮
// - 不展示频道名（@XXX），改用视频源标题 vr_name
// - 展开分集明细（max-height 200px 滚动），默认收起
// - 批量操作：全部暂停 / 全部继续 / 全部删除
// - 订阅 task:progress / task:status / file:progress / file:status 事件实时更新
import { ref, reactive, onMounted, onUnmounted } from 'vue'

import { Spinner, StatusHint, BaseImage } from '../components/common/Ui'
import { useNaiveFeedback } from '../composables/useNaiveFeedback'
import {
  listTasks,
  pauseTask,
  resumeTask,
  deleteTask,
  retryFailed,
  confirmStart,
  listTaskRecords,
  openTaskDir,
  onTaskProgress,
  onTaskStatus,
  onFileProgress,
  onFileStatus,
  type Task,
  type DownloadRecord,
} from '../services/download'
import { TaskStatus } from '../../bindings/tg-download/internal/download/models'

const feedback = useNaiveFeedback()

// 任务列表与加载状态
const tasks = ref<Task[]>([])
const loading = ref(false)
const loadError = ref('')
// 正在执行单卡操作的 task id（禁用对应按钮）
const busyId = ref<number | null>(null)
// 批量操作进行中
const batchBusy = ref(false)

// 展开的任务 id 集合
const expandedSet = ref<Set<number>>(new Set())
// 任务分集记录缓存：taskId → records（展开时按需加载）
const recordsMap = reactive<Record<number, DownloadRecord[]>>({})
// 文件实时进度：record_id → { downloaded, total, speed, lastSpeed }
// lastSpeed 用于 speed=0 时保持上一次非零速度，避免暂停后恢复时速度闪烁
const fileProgress = reactive<Record<number, { downloaded: number; total: number; speed: number; lastSpeed: number }>>({})
// 文件实时状态：record_id → status（覆盖 record.status）
const fileStatus = reactive<Record<number, string>>({})

// 事件订阅取消函数
let unsubProgress: (() => void) | null = null
let unsubStatus: (() => void) | null = null
let unsubFileProgress: (() => void) | null = null
let unsubFileStatus: (() => void) | null = null

function errMsg(e: unknown, fallback: string): string {
  if (e instanceof Error) return e.message || fallback
  return fallback
}

// 加载任务列表
async function loadTasks() {
  loading.value = true
  loadError.value = ''
  try {
    tasks.value = await listTasks()
    // 预加载活跃任务（downloading/paused）的 records，用于计算字节进度与总速度
    await preloadActiveRecords()
  } catch (e) {
    loadError.value = errMsg(e, '加载任务列表失败')
    tasks.value = []
  } finally {
    loading.value = false
  }
}

// 预加载活跃任务的 records（用于字节进度计算，不阻塞渲染）
async function preloadActiveRecords() {
  const need = tasks.value.filter(
    (t) =>
      (t.status === TaskStatus.StatusDownloading || t.status === TaskStatus.StatusPaused) &&
      !recordsMap[t.id]
  )
  await Promise.all(need.map((t) => loadRecords(t.id)))
}

// 任务标题：优先 vr_name，回退 dialog_name / save_dir_name / 任务#id
function taskName(t: Task): string {
  if (t.vr_name) return t.vr_name
  return t.config?.dialog_name || t.config?.save_dir_name || `任务 #${t.id}`
}

// 状态中文文案
function statusText(status: TaskStatus): string {
  switch (status) {
    case TaskStatus.StatusScanning:
      return '扫描中'
    case TaskStatus.StatusAwaitingSort:
      return '待排序'
    case TaskStatus.StatusPending:
      return '待开始'
    case TaskStatus.StatusDownloading:
      return '下载中'
    case TaskStatus.StatusPaused:
      return '已暂停'
    case TaskStatus.StatusCompleted:
      return '已完成'
    case TaskStatus.StatusFailed:
      return '失败'
    default:
      return '未知'
  }
}

function statusColorVar(status: TaskStatus): string {
  switch (status) {
    case TaskStatus.StatusScanning:
    case TaskStatus.StatusDownloading:
      return 'var(--accent-cyan)'
    case TaskStatus.StatusPaused:
      return 'var(--text-secondary)'
    case TaskStatus.StatusCompleted:
      return 'var(--color-success)'
    case TaskStatus.StatusFailed:
      return 'var(--color-error)'
    case TaskStatus.StatusAwaitingSort:
    case TaskStatus.StatusPending:
    default:
      return 'var(--color-warning)'
  }
}

// 任务级进度百分比（按字节计算，回退集数进度）
function pct(t: Task): number {
  if (t.status === TaskStatus.StatusCompleted) return 100
  const total = taskTotalBytes(t)
  if (total > 0) {
    const downloaded = taskDownloadedBytes(t)
    return Math.min(100, Math.round((downloaded / total) * 100))
  }
  // records 还没加载到或 file_size 全为 0，回退集数进度
  if (!t.total_files || t.total_files === 0) return 0
  return Math.min(100, Math.round((t.completed_files / t.total_files) * 100))
}

// ============ 总进度条：字节进度与总速度 ============

// 字节格式化（智能选 KB/MB/GB）
function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${bytes} B`
}

// 速度格式化
function formatSpeed(speed: number): string {
  if (speed <= 0) return ''
  if (speed >= 1024 * 1024) return `${(speed / 1024 / 1024).toFixed(1)} MB/s`
  if (speed >= 1024) return `${(speed / 1024).toFixed(0)} KB/s`
  return `${speed} B/s`
}

// 任务总字节（所有 records 的 file_size 之和）
function taskTotalBytes(t: Task): number {
  const records = recordsMap[t.id]
  if (!records || !records.length) return 0
  return records.reduce((sum, r) => sum + (r.file_size || 0), 0)
}

// 任务已下载字节（已完成集按 file_size，其他状态优先用实时 fileProgress，回退持久化 downloaded_bytes）
function taskDownloadedBytes(t: Task): number {
  const records = recordsMap[t.id]
  if (!records || !records.length) return 0
  let sum = 0
  for (const r of records) {
    const fileSize = r.file_size || 0
    if (recordStatus(r) === 'completed') {
      sum += fileSize
      continue
    }
    // downloading/pending/paused/failed：优先用实时进度，回退持久化值
    // 暂停时 fileProgress 不会被清除（仅 completed/failed 清除），保留最后的实时值
    const p = fileProgress[r.id]
    const downloaded = p ? p.downloaded : (r.downloaded_bytes || 0)
    // 防御性：单集已下载不超过该集总大小（避免断点续传初始值或最后一帧误差导致 >100%）
    sum += fileSize > 0 ? Math.min(downloaded, fileSize) : downloaded
  }
  return sum
}

// 任务总速度（所有 downloading 状态 record 的 speed 之和）
function taskTotalSpeed(t: Task): number {
  const records = recordsMap[t.id]
  if (!records || !records.length) return 0
  let sum = 0
  for (const r of records) {
    if (recordStatus(r) !== 'downloading') continue
    const p = fileProgress[r.id]
    if (p && p.speed > 0) sum += p.speed
  }
  return sum
}

function formatDate(date: string): string {
  if (!date) return ''
  const d = new Date(date)
  if (isNaN(d.getTime())) return date
  return d.toLocaleString('zh-CN')
}

function isBusy(t: Task): boolean {
  return busyId.value === t.id || batchBusy.value
}

// 单卡操作封装
async function runAction(task: Task, fn: (id: number) => Promise<void>) {
  if (busyId.value !== null || batchBusy.value) return
  busyId.value = task.id
  try {
    await fn(task.id)
    await loadTasks()
  } catch (e) {
    feedback.error(errMsg(e, '操作失败'))
  } finally {
    busyId.value = null
  }
}

function handlePause(t: Task) {
  runAction(t, pauseTask)
}
function handleResume(t: Task) {
  runAction(t, resumeTask)
}
function handleStart(t: Task) {
  runAction(t, confirmStart)
}
function handleRetry(t: Task) {
  runAction(t, retryFailed)
}

// 打开下载目录
async function handleOpenDir(t: Task) {
  if (busyId.value !== null) return
  busyId.value = t.id
  try {
    await openTaskDir(t.id)
  } catch (e) {
    feedback.error(errMsg(e, '打开目录失败'))
  } finally {
    busyId.value = null
  }
}

// 删除（二次确认）
async function handleDelete(t: Task) {
  if (busyId.value !== null || batchBusy.value) return
  const name = taskName(t)
  const ok = await feedback.confirm({
    title: `确定删除任务“${name}”吗？`,
    content: '此操作不可撤销',
    danger: true,
    positiveText: '删除',
  })
  if (!ok) return
  busyId.value = t.id
  try {
    await deleteTask(t.id)
    await loadTasks()
  } catch (e) {
    feedback.error(errMsg(e, '删除失败'))
  } finally {
    busyId.value = null
  }
}

// 切换展开分集明细
async function toggleExpand(t: Task) {
  if (expandedSet.value.has(t.id)) {
    const s = new Set(expandedSet.value)
    s.delete(t.id)
    expandedSet.value = s
    return
  }
  const s = new Set(expandedSet.value)
  s.add(t.id)
  expandedSet.value = s
  // 首次展开加载分集记录
  if (!recordsMap[t.id]) {
    await loadRecords(t.id)
  }
}

async function loadRecords(taskId: number) {
  try {
    const list = await listTaskRecords(taskId)
    recordsMap[taskId] = list || []
  } catch (e) {
    feedback.error(errMsg(e, '加载分集记录失败'))
    recordsMap[taskId] = []
  }
}

// ============ 分集明细渲染辅助 ============

// 单条记录的实时状态
// 优先用 fileStatus（来自 file:status 事件）
// 若未收到 file:status 事件，用 fileProgress 推断：
//   - 有进度数据且 downloaded >= total → completed
//   - 有进度数据（downloaded > 0）→ downloading
//   - 无进度数据 → 回退 record.status（数据库快照）
// 这解决了 file:status 事件与 loadRecords 时序竞争导致状态显示"等待中"的问题
function recordStatus(r: DownloadRecord): string {
  const fs = fileStatus[r.id]
  if (fs) return fs
  const p = fileProgress[r.id]
  if (p) {
    if (p.total > 0 && p.downloaded >= p.total) return 'completed'
    return 'downloading'
  }
  return r.status
}

function recordStatusText(r: DownloadRecord): string {
  switch (recordStatus(r)) {
    case 'completed':
      return '已完成'
    case 'downloading':
      return '下载中'
    case 'pending':
      return '等待中'
    case 'failed':
      return '失败'
    default:
      return '未知'
  }
}

function recordStatusClass(r: DownloadRecord): string {
  return `is-${recordStatus(r) || 'unknown'}`
}

// 单条记录进度百分比
function recordPct(r: DownloadRecord): number {
  const p = fileProgress[r.id]
  if (!p || !p.total || p.total === 0) {
    return recordStatus(r) === 'completed' ? 100 : 0
  }
  return Math.min(100, Math.round((p.downloaded / p.total) * 100))
}

// 速度格式化
function recordSpeed(r: DownloadRecord): string {
  // 非 downloading 状态不显示速度（暂停/等待/失败/完成都不显示）
  if (recordStatus(r) !== 'downloading') return ''
  const p = fileProgress[r.id]
  if (!p || !p.speed || p.speed <= 0) return ''
  return formatSpeed(p.speed)
}

// ============ 批量操作 ============

async function batchPause() {
  if (batchBusy.value) return
  const targets = tasks.value.filter((t) => t.status === TaskStatus.StatusDownloading)
  if (!targets.length) {
    feedback.warning('没有可暂停的任务')
    return
  }
  batchBusy.value = true
  try {
    await Promise.allSettled(targets.map((t) => pauseTask(t.id)))
    await loadTasks()
  } finally {
    batchBusy.value = false
  }
}

async function batchResume() {
  if (batchBusy.value) return
  const targets = tasks.value.filter((t) => t.status === TaskStatus.StatusPaused)
  if (!targets.length) {
    feedback.warning('没有可恢复的任务')
    return
  }
  batchBusy.value = true
  try {
    await Promise.allSettled(targets.map((t) => resumeTask(t.id)))
    await loadTasks()
  } finally {
    batchBusy.value = false
  }
}

async function batchDelete() {
  if (batchBusy.value || !tasks.value.length) return
  const ok = await feedback.confirm({
    title: `确定删除全部 ${tasks.value.length} 个任务吗？`,
    content: '此操作不可撤销，将同时删除所有分集记录',
    danger: true,
    positiveText: '全部删除',
  })
  if (!ok) return
  batchBusy.value = true
  try {
    await Promise.allSettled(tasks.value.map((t) => deleteTask(t.id)))
    // 清理本地缓存
    for (const k of Object.keys(recordsMap)) {
      delete recordsMap[Number(k)]
    }
    expandedSet.value = new Set()
    await loadTasks()
  } finally {
    batchBusy.value = false
  }
}

onMounted(() => {
  loadTasks()
  // 任务级进度（后端推送整个 Task 对象，字段是 id 不是 task_id）
  unsubProgress = onTaskProgress((data: any) => {
    const t = tasks.value.find((x) => x.id === data.id)
    if (t) {
      t.completed_files = data.completed_files
      t.total_files = data.total_files
      t.failed_files = data.failed_files
    }
  })
  // 任务级状态（后端推送整个 Task 对象，字段是 id 不是 task_id）
  unsubStatus = onTaskStatus((data: any) => {
    const t = tasks.value.find((x) => x.id === data.id)
    if (t) {
      t.status = data.status as TaskStatus
      // 状态变为 downloading 时预加载 records（用于字节进度计算）
      if (t.status === TaskStatus.StatusDownloading && !recordsMap[t.id]) {
        loadRecords(t.id)
      }
    }
  })
  // 单文件进度
  unsubFileProgress = onFileProgress((data) => {
    const prev = fileProgress[data.record_id]
    // speed=0 时保持上一次非零速度，避免暂停后恢复时速度闪烁
    const effectiveSpeed = data.speed > 0 ? data.speed : (prev?.lastSpeed ?? 0)
    const lastSpeed = data.speed > 0 ? data.speed : (prev?.lastSpeed ?? 0)
    fileProgress[data.record_id] = {
      downloaded: data.downloaded,
      total: data.total,
      speed: effectiveSpeed,
      lastSpeed,
    }
  })
  // 单文件状态
  unsubFileStatus = onFileStatus((data) => {
    fileStatus[data.id] = data.status
    // 完成后清除进度缓存
    if (data.status === 'completed' || data.status === 'failed') {
      delete fileProgress[data.id]
    }
  })
})

onUnmounted(() => {
  unsubProgress?.()
  unsubStatus?.()
  unsubFileProgress?.()
  unsubFileStatus?.()
})
</script>

<template>
  <div class="download">
    <!-- 顶部工具栏：标题 + 批量操作 -->
    <header class="download__header">
      <h1 class="download__title">下载任务</h1>
      <div v-if="tasks.length" class="download__batch">
        <n-button size="small" :disabled="batchBusy" @click="batchPause">
          全部暂停
        </n-button>
        <n-button size="small" :disabled="batchBusy" @click="batchResume">
          全部继续
        </n-button>
        <n-button
          size="small"
          type="error"
          :disabled="batchBusy"
          @click="batchDelete"
        >
          全部删除
        </n-button>
      </div>
    </header>

    <div class="download__body">
      <!-- 加载态 -->
      <div v-if="loading" class="download__state">
        <Spinner :size="28" />
      </div>

      <!-- 错误态 -->
      <div v-else-if="loadError" class="download__state download__state--col">
        <StatusHint variant="error" :title="loadError" />
        <n-button size="small" @click="loadTasks">重试</n-button>
      </div>

      <!-- 空列表 -->
      <div v-else-if="tasks.length === 0" class="download__state">
        <StatusHint
          variant="info"
          title="暂无下载任务"
          description="在首页或分类页点击视频卡片右上角下载图标创建任务"
        />
      </div>

      <!-- 任务卡片列表 -->
      <ul v-else class="task-list">
        <li v-for="t in tasks" :key="t.id" class="task-card">
          <!-- 主行：封面 + 信息 + 状态 + 操作 -->
          <div class="task-card__main">
            <BaseImage
              :src="t.vr_cover"
              :alt="taskName(t)"
              class="task-card__cover"
            />
            <div class="task-card__info">
              <div class="task-card__name" :title="taskName(t)">
                {{ taskName(t) }}
              </div>
              <div class="task-card__stats">
                共 {{ t.total_files }} 集 · 完成 {{ t.completed_files }} · 失败
                {{ t.failed_files }}
              </div>
              <div class="task-card__progress">
                <div class="progress">
                  <div
                    class="progress__fill"
                    :style="{ width: pct(t) + '%' }"
                  ></div>
                </div>
                <span class="task-card__pct">
                  <template v-if="t.status === TaskStatus.StatusCompleted">100%</template>
                  <template v-else>
                    {{ pct(t) }}%<span
                      v-if="taskTotalBytes(t) > 0"
                      class="task-card__bytes"
                      > · {{ formatBytes(taskDownloadedBytes(t)) }}/{{ formatBytes(taskTotalBytes(t)) }}</span
                    ><span
                      v-if="taskTotalSpeed(t) > 0"
                      class="task-card__speed"
                      > · {{ formatSpeed(taskTotalSpeed(t)) }}</span
                    >
                  </template>
                </span>
              </div>
            </div>
            <span
              class="task-card__pill"
              :style="{ '--pill-color': statusColorVar(t.status) }"
              >{{ statusText(t.status) }}</span
            >
            <div class="task-card__actions">
              <n-button
                v-if="t.status === TaskStatus.StatusDownloading"
                size="small"
                :disabled="isBusy(t)"
                @click="handlePause(t)"
              >
                暂停
              </n-button>
              <n-button
                v-else-if="t.status === TaskStatus.StatusPaused"
                size="small"
                :disabled="isBusy(t)"
                @click="handleResume(t)"
              >
                继续
              </n-button>
              <n-button
                v-else-if="
                  t.status === TaskStatus.StatusPending ||
                  t.status === TaskStatus.StatusAwaitingSort
                "
                size="small"
                type="primary"
                :disabled="isBusy(t)"
                @click="handleStart(t)"
              >
                开始
              </n-button>
              <n-button
                v-else-if="t.status === TaskStatus.StatusFailed"
                size="small"
                :disabled="isBusy(t)"
                @click="handleRetry(t)"
              >
                重试
              </n-button>
              <n-button
                size="small"
                :disabled="isBusy(t)"
                @click="handleOpenDir(t)"
              >
                目录
              </n-button>
              <n-button
                size="small"
                :disabled="isBusy(t)"
                @click="toggleExpand(t)"
              >
                {{ expandedSet.has(t.id) ? '收起' : '明细' }}
              </n-button>
              <n-button
                size="small"
                type="error"
                :disabled="isBusy(t)"
                @click="handleDelete(t)"
              >
                删除
              </n-button>
            </div>
          </div>

          <!-- 分集明细（展开时） -->
          <div v-if="expandedSet.has(t.id)" class="task-card__detail">
            <div v-if="!recordsMap[t.id]" class="detail-loading">
              <Spinner :size="20" />
            </div>
            <div v-else-if="!recordsMap[t.id].length" class="detail-empty">
              暂无分集记录
            </div>
            <div v-else class="detail-list">
              <div
                v-for="r in recordsMap[t.id]"
                :key="r.id"
                class="detail-item"
              >
                <span
                  class="detail-item__ep"
                  :title="r.rendered_name || r.file_name"
                >
                  {{ r.episode_number ? `第${r.episode_number}集` : '未编号' }}
                </span>
                <div class="detail-item__bar">
                  <div class="progress progress--sm">
                    <div
                      class="progress__fill"
                      :style="{ width: recordPct(r) + '%' }"
                    ></div>
                  </div>
                  <span class="detail-item__pct">{{ recordPct(r) }}%</span>
                </div>
                <span
                  v-if="recordSpeed(r)"
                  class="detail-item__speed"
                >{{ recordSpeed(r) }}</span>
                <span
                  class="detail-item__status"
                  :class="recordStatusClass(r)"
                  >{{ recordStatusText(r) }}</span
                >
              </div>
            </div>
          </div>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.download {
  display: flex;
  flex-direction: column;
  min-height: 100%;
  padding: var(--space-6);
  background: var(--bg-base);
}

/* ===================== 顶部工具栏 ===================== */
.download__header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding-bottom: var(--space-4);
  border-bottom: 1px solid var(--border-subtle);
}

.download__title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 20px;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: 0.01em;
}

.download__batch {
  display: flex;
  gap: var(--space-2);
}

/* ===================== 主体区 ===================== */
.download__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.download__state {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-8) var(--space-4);
}

.download__state--col {
  flex-direction: column;
  gap: var(--space-3);
}

/* ===================== 任务卡片 ===================== */
.task-list {
  list-style: none;
  margin: 0;
  padding: var(--space-4) 0 0;
}

.task-card {
  padding: var(--space-4) var(--space-5);
  background: var(--bg-card-solid);
  border-radius: var(--radius-md);
  margin-bottom: var(--space-3);
}

.task-card__main {
  display: flex;
  align-items: center;
  gap: var(--space-4);
}

.task-card__cover {
  flex-shrink: 0;
  width: 48px;
  height: 64px;
  border-radius: var(--radius-sm);
  object-fit: cover;
  background: var(--bg-card);
}

.task-card__info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.task-card__name {
  font-family: var(--font-body);
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.task-card__stats {
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--text-secondary);
}

.task-card__progress {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.progress {
  flex: 1;
  min-width: 0;
  height: 6px;
  border-radius: var(--radius-full);
  background: var(--bg-card);
  overflow: hidden;
}

.progress__fill {
  height: 100%;
  border-radius: var(--radius-full);
  background: var(--accent-sakura);
  transition: width var(--duration-normal) var(--ease-fluid);
}

.progress--sm {
  height: 4px;
}

.task-card__pct {
  flex-shrink: 0;
  text-align: right;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-secondary);
  white-space: nowrap;
}

.task-card__bytes {
  color: var(--text-tertiary);
}

.task-card__speed {
  color: var(--accent-cyan);
}

/* 状态药丸 */
.task-card__pill {
  flex-shrink: 0;
  padding: 2px 10px;
  border-radius: var(--radius-full);
  font-family: var(--font-body);
  font-size: 12px;
  font-weight: 600;
  color: var(--pill-color);
  background: color-mix(in srgb, var(--pill-color) 15%, transparent);
}

/* 操作按钮组 */
.task-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-wrap: wrap;
  justify-content: flex-end;
}

/* ===================== 分集明细 ===================== */
.task-card__detail {
  margin-top: var(--space-3);
  padding-top: var(--space-3);
  border-top: 1px dashed var(--border-subtle);
}

.detail-loading,
.detail-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4) 0;
  color: var(--text-tertiary);
  font-size: 12px;
}

.detail-list {
  max-height: 200px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding-right: var(--space-1);
}

.detail-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  font-size: 12px;
}

.detail-item__ep {
  flex: 0 0 auto;
  min-width: 56px;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.detail-item__bar {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.detail-item__pct {
  flex-shrink: 0;
  min-width: 32px;
  text-align: right;
  font-family: var(--font-mono);
  color: var(--text-secondary);
}

.detail-item__speed {
  flex-shrink: 0;
  font-family: var(--font-mono);
  color: var(--accent-cyan);
}

.detail-item__status {
  flex-shrink: 0;
  padding: 1px 8px;
  border-radius: var(--radius-full);
  font-weight: 600;
}

.detail-item__status.is-completed {
  color: var(--color-success);
  background: color-mix(in srgb, var(--color-success) 15%, transparent);
}

.detail-item__status.is-downloading {
  color: var(--accent-cyan);
  background: color-mix(in srgb, var(--accent-cyan) 15%, transparent);
}

.detail-item__status.is-pending {
  color: var(--color-warning);
  background: color-mix(in srgb, var(--color-warning) 15%, transparent);
}

.detail-item__status.is-failed {
  color: var(--color-error);
  background: color-mix(in srgb, var(--color-error) 15%, transparent);
}

.detail-item__status.is-unknown {
  color: var(--text-tertiary);
  background: var(--bg-card-solid);
}
</style>
