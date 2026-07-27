// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 DownloadService，负责向 Wails 绑定暴露下载任务的创建、查询、
// 暂停、恢复、删除与重试等方法。
package services

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"tg-download/internal/download"
	"tg-download/internal/settings"
)

// scanTimeout 是单次扫描阶段的超时时间。
// 频道消息较多时扫描可能耗时较长，此处设为 30 分钟。
const scanTimeout = 30 * time.Minute

// DownloadService 是 Wails 绑定的下载任务服务。
// 它组合了 Scanner（扫描阶段）与 Scheduler（下载阶段），
// 向前端提供完整的下载任务生命周期管理方法。
type DownloadService struct {
	db        *sql.DB
	scanner   *download.Scanner
	scheduler *download.Scheduler
}

// NewDownloadService 创建一个新的 DownloadService 实例。
// db、scanner、scheduler 均需已初始化。
func NewDownloadService(db *sql.DB, scanner *download.Scanner, scheduler *download.Scheduler) *DownloadService {
	return &DownloadService{
		db:        db,
		scanner:   scanner,
		scheduler: scheduler,
	}
}

// CreateTask 创建下载任务并立即在后台启动扫描阶段。
//
// 流程：
//  1. 在 SQLite 中创建任务（status=scanning）
//  2. 在后台 goroutine 中执行扫描（Scanner.Scan）
//  3. 扫描完成后，若 AutoDownload=true 则自动启动下载（Scheduler.StartTask）
//  4. 返回 taskID 供前端跟踪
func (s *DownloadService) CreateTask(cfg download.TaskConfig) (int64, error) {
	if s.db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	if s.scanner == nil {
		return 0, fmt.Errorf("扫描器未初始化")
	}
	if s.scheduler == nil {
		return 0, fmt.Errorf("调度器未初始化")
	}

	// 校验 SaveDirName：必填，作为下载子目录名
	if strings.TrimSpace(cfg.SaveDirName) == "" {
		return 0, fmt.Errorf("保存目录名不能为空")
	}

	// 创建任务（status 初始为 scanning）
	taskID, err := download.CreateTask(s.db, cfg)
	if err != nil {
		return 0, fmt.Errorf("创建任务失败: %w", err)
	}

	// 在后台 goroutine 中启动扫描
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()

		// 加载完整任务（含 ID 与初始状态）
		task, err := download.GetTask(s.db, taskID)
		if err != nil {
			// 加载失败，标记任务为 failed
			log.Printf("[download_service] task #%d 加载任务失败: %v", taskID, err)
			_ = download.UpdateTaskStatus(s.db, taskID, download.StatusFailed)
			return
		}

		// 执行扫描
		if err := s.scanner.Scan(ctx, task); err != nil {
			// 扫描失败，标记任务为 failed。
			// 详细错误信息已通过 EventScanProgress 事件推送（scanner 内部 emitScanError / defer）
			log.Printf("[download_service] task #%d 扫描失败: %v", taskID, err)
			_ = download.UpdateTaskStatus(s.db, taskID, download.StatusFailed)
			// 推送状态变更事件
			if t, e := download.GetTask(s.db, taskID); e == nil {
				t.Status = download.StatusFailed
				s.scheduler.Emit(download.EventTaskStatus, t)
			}
			return
		}

		// 扫描成功，重新加载任务获取最新状态（仅记录日志，不再自动启动下载）
		// 扫描完成后任务状态统一为 StatusAwaitingSort，由用户在前端确认排序后手动开始下载
		task, err = download.GetTask(s.db, taskID)
		if err != nil {
			log.Printf("[download_service] task #%d 扫描后加载任务失败: %v", taskID, err)
			return
		}
		log.Printf("[download_service] task #%d 扫描完成，状态=%s（等待用户手动开始下载）", taskID, task.Status)
	}()

	return taskID, nil
}

// ListTasks 列出所有下载任务（按 created_at 倒序）。
func (s *DownloadService) ListTasks() ([]download.Task, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	tasks, err := download.ListTasks(s.db)
	if err != nil {
		return nil, fmt.Errorf("列出任务失败: %w", err)
	}
	return tasks, nil
}

// GetTask 获取单个下载任务。
func (s *DownloadService) GetTask(id int64) (download.Task, error) {
	if s.db == nil {
		return download.Task{}, fmt.Errorf("数据库未初始化")
	}
	task, err := download.GetTask(s.db, id)
	if err != nil {
		return download.Task{}, fmt.Errorf("获取任务失败: %w", err)
	}
	return task, nil
}

// ListTaskRecords 列出指定任务下的所有下载记录。
// 同时根据任务命名模板填充每条记录的 RenderedName 字段，
// 便于前端展示渲染后的下载文件名（含集数、视频前缀等）。
func (s *DownloadService) ListTaskRecords(taskID int64) ([]download.DownloadRecord, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	records, err := download.ListRecordsByTask(s.db, taskID)
	if err != nil {
		return nil, fmt.Errorf("列出下载记录失败: %w", err)
	}
	// 加载任务配置，按模板渲染每条记录的最终文件名
	task, err := download.GetTask(s.db, taskID)
	if err == nil && task.ID > 0 {
		for i := range records {
			records[i].RenderedName = download.RenderRecordName(records[i], task.Config, task.VideoName)
		}
	}
	return records, nil
}

// PauseTask 暂停指定任务。
func (s *DownloadService) PauseTask(taskID int64) error {
	if s.scheduler == nil {
		return fmt.Errorf("调度器未初始化")
	}
	if err := s.scheduler.PauseTask(taskID); err != nil {
		return fmt.Errorf("暂停任务失败: %w", err)
	}
	return nil
}

// ResumeTask 恢复指定任务。
func (s *DownloadService) ResumeTask(taskID int64) error {
	if s.scheduler == nil {
		return fmt.Errorf("调度器未初始化")
	}
	if err := s.scheduler.ResumeTask(context.Background(), taskID); err != nil {
		return fmt.Errorf("恢复任务失败: %w", err)
	}
	return nil
}

// DeleteTask 删除指定任务及其所有下载记录。
func (s *DownloadService) DeleteTask(taskID int64) error {
	if s.scheduler == nil {
		return fmt.Errorf("调度器未初始化")
	}
	if err := s.scheduler.DeleteTask(taskID); err != nil {
		return fmt.Errorf("删除任务失败: %w", err)
	}
	return nil
}

// DeleteRecord 删除指定的下载记录。
// 用于用户手动移除匹配到的不需要的文件。
// 注意：若记录正在下载中，本方法不主动取消下载 goroutine，仅从数据库删除；
// 若需安全删除，建议在任务非 downloading 状态下操作。
func (s *DownloadService) DeleteRecord(recordID int64) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := download.DeleteRecord(s.db, recordID); err != nil {
		return fmt.Errorf("删除记录失败: %w", err)
	}
	return nil
}

// RetryFailed 重试指定任务的所有失败记录。
func (s *DownloadService) RetryFailed(taskID int64) error {
	if s.scheduler == nil {
		return fmt.Errorf("调度器未初始化")
	}
	if err := s.scheduler.RetryFailed(context.Background(), taskID); err != nil {
		return fmt.Errorf("重试失败记录失败: %w", err)
	}
	return nil
}

// ConfirmStart 在任务处于"待确认"状态（扫描完成但未自动下载）时，
// 由用户确认后开始下载。
func (s *DownloadService) ConfirmStart(taskID int64) error {
	if s.scheduler == nil {
		return fmt.Errorf("调度器未初始化")
	}
	// 校验任务状态
	task, err := download.GetTask(s.db, taskID)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	if task.Status != download.StatusPending {
		return fmt.Errorf("任务 #%d 当前状态为 %s，无法确认开始（仅 pending 状态可确认）", taskID, task.Status)
	}
	// 启动下载
	if err := s.scheduler.StartTask(context.Background(), taskID); err != nil {
		return fmt.Errorf("启动下载失败: %w", err)
	}
	return nil
}

// PreviewMatch 测试匹配：在创建任务前预览匹配效果。
// 按给定配置扫描频道历史消息，匹配文件后通过事件推送（scan:preview:matched / scan:preview:progress），
// 不创建任务、不创建下载记录、不更新扫描位置。
//
// 用途：在频道配置抽屉中点击"测试匹配"按钮调用，弹窗实时显示匹配进度与命中文件，
// 便于用户调整配置（媒体类型、关键词、匹配范围）后再创建任务。
//
// 返回匹配到的文件总数。
func (s *DownloadService) PreviewMatch(cfg download.TaskConfig) (int, error) {
	if s.scanner == nil {
		return 0, fmt.Errorf("扫描器未初始化")
	}
	// 后台执行扫描，避免阻塞 Wails 调用（事件通过 emitter 推送到前端）
	matched, err := s.scanner.PreviewMatch(context.Background(), cfg)
	if err != nil {
		return matched, fmt.Errorf("测试匹配失败: %w", err)
	}
	return matched, nil
}

// FetchRecentMessages 拉取频道最新的若干条消息（默认 100 条），返回完整内容供前端展示。
//
// 用于"测试匹配"弹窗的本地筛选模式：前端一次性拉取消息列表，
// 用户复制文本作为关键词后在前端本地实时筛选，无需反复请求后端。
//
// 参数：
//   - peerID: 频道/群组/用户 ID
//   - limit: 拉取条数（≤100，0 表示用默认值 100）
func (s *DownloadService) FetchRecentMessages(peerID int64, limit int) ([]download.RecentMessage, error) {
	if s.scanner == nil {
		return nil, fmt.Errorf("扫描器未初始化")
	}
	messages, err := s.scanner.FetchRecentMessages(context.Background(), peerID, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取最新消息失败: %w", err)
	}
	return messages, nil
}

// SortBy 定义排序键（与 download.SortBy 一致，重导出便于 Wails 绑定）。
type SortBy = download.SortBy

// 排序键常量重导出
const (
	SortByEpisode    = download.SortByEpisode
	SortByMessageID  = download.SortByMessageID
	SortByDate       = download.SortByDate
)

// SortTaskRecords 对指定任务的下载记录按指定键排序，并持久化 sort_order。
// 用于"待排序"状态（StatusAwaitingSort）下的自动排序。
//
// 参数：
//   - taskID: 任务 ID
//   - sortBy: 排序键（episode/message_id/date）
//   - ascending: true 升序，false 降序
//
// 注意：按 episode 排序时无法提取集数（episode_number=NULL）的记录始终排在最后，
// 升降序不影响这部分记录的相对顺序（按 message_id ASC 兜底）。
func (s *DownloadService) SortTaskRecords(taskID int64, sortBy SortBy, ascending bool) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	records, err := download.ListRecordsByTask(s.db, taskID)
	if err != nil {
		return fmt.Errorf("加载任务记录失败: %w", err)
	}
	// 排序
	download.SortRecordsBy(records, sortBy, ascending)

	// 取有序 ID 列表，写回 sort_order
	orderedIDs := make([]int64, 0, len(records))
	for _, r := range records {
		orderedIDs = append(orderedIDs, r.ID)
	}
	if err := download.UpdateRecordsSortOrder(s.db, taskID, orderedIDs); err != nil {
		return fmt.Errorf("更新排序失败: %w", err)
	}
	return nil
}

// ReorderTaskRecords 接收用户在前端拖拽后的有序记录 ID 列表，写回 sort_order。
// 用于"待排序"状态下的手动排序。
func (s *DownloadService) ReorderTaskRecords(taskID int64, orderedIDs []int64) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := download.UpdateRecordsSortOrder(s.db, taskID, orderedIDs); err != nil {
		return fmt.Errorf("更新排序失败: %w", err)
	}
	return nil
}

// StartDownloadAfterSort 在用户确认排序后启动下载。
// 流程：
//  1. 校验任务状态为 StatusAwaitingSort（非此状态拒绝）
//  2. 更新任务状态为 StatusDownloading
//  3. 调用 Scheduler.StartTask 开始下载
func (s *DownloadService) StartDownloadAfterSort(taskID int64) error {
	if s.scheduler == nil {
		return fmt.Errorf("调度器未初始化")
	}
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	task, err := download.GetTask(s.db, taskID)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	if task.Status != download.StatusAwaitingSort {
		return fmt.Errorf("任务 #%d 当前状态为 %s，无法确认开始（仅 awaiting_sort 状态可确认）", taskID, task.Status)
	}
	// 状态切换为 downloading
	if err := download.UpdateTaskStatus(s.db, taskID, download.StatusDownloading); err != nil {
		return fmt.Errorf("更新任务状态失败: %w", err)
	}
	// 启动下载
	if err := s.scheduler.StartTask(context.Background(), taskID); err != nil {
		return fmt.Errorf("启动下载失败: %w", err)
	}
	return nil
}

// ============ 选集下载场景（基于消息 ID 列表直接创建任务）============

// CreateTaskFromMessages 基于消息 ID 列表直接创建下载任务并启动下载。
// 用于首页/分类页视频卡片上的下载入口：用户在选集弹窗选择分集后调用此方法。
// 跳过扫描阶段，直接拉取消息元数据 → 创建下载记录 → 启动下载。
//
// 返回新任务 ID。任务创建与下载启动在后台 goroutine 中执行，
// 前端通过 task:status 事件跟踪状态变更。
func (s *DownloadService) CreateTaskFromMessages(req download.CreateTaskFromMessagesRequest) (int64, error) {
	if s.db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	if s.scanner == nil {
		return 0, fmt.Errorf("扫描器未初始化")
	}
	if s.scheduler == nil {
		return 0, fmt.Errorf("调度器未初始化")
	}
	if len(req.Episodes) == 0 {
		return 0, fmt.Errorf("未选择任何分集")
	}

	// 后台执行，避免阻塞 Wails 调用（拉取消息元数据可能耗时）
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	go func() {
		defer cancel()
		taskID, err := s.scanner.CreateTaskFromMessages(ctx, req, s.scheduler)
		if err != nil {
			log.Printf("[download_service] CreateTaskFromMessages 失败: %v (taskID=%d)", err, taskID)
		}
	}()

	// 由于后台执行，这里返回 0 表示任务 ID 将通过事件推送。
	// 实际上 CreateTaskFromMessages 内部先创建任务（同步），再后台拉取消息。
	// 为保持 API 简单，前端应通过 task:status 事件感知新任务。
	// 此处返回 0，前端不应依赖返回值；改进方案见下文。
	return 0, nil
}

// CreateTaskFromMessagesSync 是 CreateTaskFromMessages 的同步版本，
// 阻塞直到任务创建完成（含消息拉取与下载启动），返回任务 ID。
// 前端若需立即拿到 task_id 用于跳转或操作，调用此方法。
func (s *DownloadService) CreateTaskFromMessagesSync(req download.CreateTaskFromMessagesRequest) (int64, error) {
	if s.db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	if s.scanner == nil {
		return 0, fmt.Errorf("扫描器未初始化")
	}
	if s.scheduler == nil {
		return 0, fmt.Errorf("调度器未初始化")
	}
	if len(req.Episodes) == 0 {
		return 0, fmt.Errorf("未选择任何分集")
	}

	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()
	return s.scanner.CreateTaskFromMessages(ctx, req, s.scheduler)
}

// GetEpisodeDownloadStatus 查询指定频道下多个消息 ID 的下载状态。
// 用于选集弹窗展示每集是否已下载（"仅未下载"筛选）。
// 返回与传入 messageIDs 等长的状态列表，顺序一致。
func (s *DownloadService) GetEpisodeDownloadStatus(channelID int64, messageIDs []int64) ([]download.EpisodeDownloadStatus, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	statusMap, err := download.ListRecordStatusByChannelAndMessages(s.db, channelID, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("查询分集下载状态失败: %w", err)
	}
	// 按传入顺序构造返回
	result := make([]download.EpisodeDownloadStatus, 0, len(messageIDs))
	for _, msgID := range messageIDs {
		status := ""
		if v, ok := statusMap[msgID]; ok {
			status = v
		}
		result = append(result, download.EpisodeDownloadStatus{
			MessageID: msgID,
			Status:    status,
		})
	}
	return result, nil
}

// OpenTaskDir 打开指定任务的下载目录（在系统文件管理器中打开）。
// 任务目录由 SaveDirName 决定，位于下载根目录下。
func (s *DownloadService) OpenTaskDir(taskID int64) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	task, err := download.GetTask(s.db, taskID)
	if err != nil {
		return fmt.Errorf("获取任务失败: %w", err)
	}
	// 读取下载根目录
	downloadDir, err := settings.GetSetting(s.db, "download_dir")
	if err != nil {
		return fmt.Errorf("读取下载目录失败: %w", err)
	}
	if downloadDir == "" {
		return fmt.Errorf("下载目录未配置")
	}
	// 拼接任务目录（与 storage.GetTaskDir 一致，但这里只读不创建）
	taskDir := filepath.Join(downloadDir, sanitizeDirNameForService(task.Config.SaveDirName))
	if _, err := os.Stat(taskDir); err != nil {
		return fmt.Errorf("任务目录不存在: %w", err)
	}
	// 调用系统文件管理器打开
	return openInFileExplorer(taskDir)
}

// sanitizeDirNameForService 净化目录名为 Windows 合法名。
// 复用 download 包的 sanitizeDirName 逻辑（通过包级函数访问）。
func sanitizeDirNameForService(name string) string {
	// 这里直接复用 download 包未导出的 sanitizeDirName 会循环依赖，
	// 简单实现：替换非法字符
	replacer := strings.NewReplacer(
		"<", "_", ">", "_", ":", "_", `"`, "_",
		"/", "_", `\`, "_", "|", "_", "?", "_", "*", "_",
	)
	s := replacer.Replace(name)
	s = strings.Trim(s, " .")
	if s == "" {
		s = "unnamed"
	}
	return s
}

// openInFileExplorer 在系统文件管理器中打开指定目录。
// Windows 使用 explorer.exe，其他平台使用 open/open 命令。
func openInFileExplorer(dir string) error {
	switch runtime.GOOS {
	case "windows":
		// Windows 使用 explorer.exe 打开目录
		// 注意：路径中的反斜杠无需转义，exec.Command 会处理
		return execCommand("explorer.exe", dir)
	case "darwin":
		return execCommand("open", dir)
	default:
		// Linux 等
		return execCommand("xdg-open", dir)
	}
}

// execCommand 执行系统命令打开目录（非阻塞，不等待返回）。
func execCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// 不需要捕获输出，仅启动
	return cmd.Start()
}
