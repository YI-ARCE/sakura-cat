// 下载任务服务封装层
//
// 对 bindings/tg-download/services 的 DownloadService 二次封装，
// 提供下载任务生命周期管理方法与 Wails 事件订阅。
// 事件名与 Go 端 internal/download/scanner.go 约定一致。

import { Events } from '@wailsio/runtime'

import type {
  Task,
  DownloadRecord,
  CreateTaskFromMessagesRequest,
  EpisodeDownloadInput,
  EpisodeDownloadStatus,
} from '../../bindings/tg-download/internal/download/models'

export type { Task, DownloadRecord, CreateTaskFromMessagesRequest, EpisodeDownloadInput, EpisodeDownloadStatus }

// TaskProgressEvent 任务进度事件载荷
export interface TaskProgressEvent {
  task_id: number
  completed_files: number
  total_files: number
  failed_files: number
}

// TaskStatusEvent 任务状态变更事件载荷
export interface TaskStatusEvent {
  task_id: number
  status: string
}

// FileProgressEvent 单文件下载进度事件载荷
export interface FileProgressEvent {
  task_id: number
  record_id: number
  message_id: number
  downloaded: number
  total: number
  speed: number
  done: boolean
}

// FileStatusEvent 单文件状态变更事件载荷
export interface FileStatusEvent {
  id: number
  task_id: number
  message_id: number
  file_name: string
  status: string
  local_path: string
  error: string
}

// 事件名（与 Go 端 internal/download/scanner.go 约定一致）
const TASK_PROGRESS_EVENT = 'task:progress'
const TASK_STATUS_EVENT = 'task:status'
const FILE_PROGRESS_EVENT = 'file:progress'
const FILE_STATUS_EVENT = 'file:status'

// bindings 文件生成后从此路径导入；生成前调用会报错，属预期行为
let DownloadServiceBinding: any = null

async function loadBinding() {
  if (!DownloadServiceBinding) {
    const mod: any = await import('../../bindings/tg-download/services/index')
    DownloadServiceBinding = mod.DownloadService
  }
  return DownloadServiceBinding
}

// listTasks 列出所有下载任务（按创建时间倒序）
export async function listTasks(): Promise<Task[]> {
  const svc = await loadBinding()
  return svc.ListTasks()
}

// getTask 获取单个下载任务
export async function getTask(id: number): Promise<Task> {
  const svc = await loadBinding()
  return svc.GetTask(id)
}

// listTaskRecords 列出指定任务下的所有下载记录
export async function listTaskRecords(taskId: number): Promise<DownloadRecord[]> {
  const svc = await loadBinding()
  return svc.ListTaskRecords(taskId)
}

// pauseTask 暂停指定任务
export async function pauseTask(taskId: number): Promise<void> {
  const svc = await loadBinding()
  return svc.PauseTask(taskId)
}

// resumeTask 恢复指定任务
export async function resumeTask(taskId: number): Promise<void> {
  const svc = await loadBinding()
  return svc.ResumeTask(taskId)
}

// deleteTask 删除指定任务及其所有下载记录
export async function deleteTask(taskId: number): Promise<void> {
  const svc = await loadBinding()
  return svc.DeleteTask(taskId)
}

// retryFailed 重试指定任务的所有失败记录
export async function retryFailed(taskId: number): Promise<void> {
  const svc = await loadBinding()
  return svc.RetryFailed(taskId)
}

// confirmStart 在任务处于待确认状态时由用户确认后开始下载
export async function confirmStart(taskId: number): Promise<void> {
  const svc = await loadBinding()
  return svc.ConfirmStart(taskId)
}

// ============ 选集下载场景（基于消息 ID 列表直接创建任务）============

// createTaskFromMessagesSync 基于消息 ID 列表同步创建下载任务并启动下载。
// 阻塞直到任务创建完成（含消息拉取与下载启动），返回任务 ID。
// 用于选集弹窗点击"开始下载"后调用。
export async function createTaskFromMessagesSync(req: CreateTaskFromMessagesRequest): Promise<number> {
  const svc = await loadBinding()
  return svc.CreateTaskFromMessagesSync(req)
}

// getEpisodeDownloadStatus 查询指定频道下多个消息 ID 的下载状态。
// 用于选集弹窗展示每集是否已下载（"仅未下载"筛选）。
// 返回与传入 messageIDs 等长的状态列表，顺序一致。
export async function getEpisodeDownloadStatus(
  channelId: number,
  messageIds: number[]
): Promise<EpisodeDownloadStatus[]> {
  const svc = await loadBinding()
  return svc.GetEpisodeDownloadStatus(channelId, messageIds)
}

// openTaskDir 打开指定任务的下载目录（在系统文件管理器中打开）
export async function openTaskDir(taskId: number): Promise<void> {
  const svc = await loadBinding()
  return svc.OpenTaskDir(taskId)
}

// onTaskProgress 订阅任务进度事件，返回取消订阅函数（便于 onUnmounted 注销）
export function onTaskProgress(callback: (data: TaskProgressEvent) => void): () => void {
  return Events.On(TASK_PROGRESS_EVENT, (ev: { data: any }) => callback(ev.data))
}

// onTaskStatus 订阅任务状态变更事件，返回取消订阅函数（便于 onUnmounted 注销）
export function onTaskStatus(callback: (data: TaskStatusEvent) => void): () => void {
  return Events.On(TASK_STATUS_EVENT, (ev: { data: any }) => callback(ev.data))
}

// onFileProgress 订阅单文件下载进度事件，返回取消订阅函数
export function onFileProgress(callback: (data: FileProgressEvent) => void): () => void {
  return Events.On(FILE_PROGRESS_EVENT, (ev: { data: any }) => callback(ev.data))
}

// onFileStatus 订阅单文件状态变更事件，返回取消订阅函数
export function onFileStatus(callback: (data: FileStatusEvent) => void): () => void {
  return Events.On(FILE_STATUS_EVENT, (ev: { data: any }) => callback(ev.data))
}
