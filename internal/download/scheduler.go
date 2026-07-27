// Package download 提供文件下载的任务调度、扫描与持久化能力。
// 本文件实现全局并发下载调度器，管理所有任务的下载并发、限速、重试与暂停/恢复。
package download

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tg-download/internal/telegram"
	"tg-download/internal/template"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"golang.org/x/time/rate"
)

// maxGlobalConcurrency 全局并发下载上限（最多同时下载 10 个文件）。
const maxGlobalConcurrency = 10

// maxRetries 单文件下载失败后的最大重试次数（不含首次下载）。
const maxRetries = 5

// rateBurstMin 限速器的最小 burst 大小，确保单次 WaitN 不会超过 burst。
// 默认下载分片为 512KB，这里取 1MB 留余量。
const rateBurstMin = 1024 * 1024

// downloadCallTimeout 单次下载尝试（含消息拉取与文件写入）的最大时长。
// 较大文件可能需要更长时间，此处设为 30 分钟。
const downloadCallTimeout = 30 * time.Minute

// Scheduler 全局下载调度器，管理所有任务的下载并发、限速与生命周期。
//
// 设计要点：
//   - 全局并发信号量 globalSem（容量 10）限制所有任务同时下载的文件数
//   - 任务级并发由 taskRunner.concurrency 控制（worker pool 大小）
//   - 全局限速器 limiter（可选）控制所有下载的总速率
//   - active map 跟踪正在运行的任务，paused map 标记暂停状态
type Scheduler struct {
	mu        sync.Mutex
	db        *sql.DB
	manager   *telegram.ClientManager
	storage   *StorageManager
	emitter   EventEmitter
	active    map[int64]*taskRunner // taskID -> runner
	globalSem chan struct{}          // 全局并发信号量（容量 10）
	paused    map[int64]bool         // taskID -> 是否暂停
	limiter   *rate.Limiter          // 全局限速器（可能为 nil，表示不限速）
}

// taskRunner 单个任务的运行器，负责调度该任务下所有 pending 记录的下载。
type taskRunner struct {
	task        Task
	scheduler   *Scheduler
	cancel      context.CancelFunc
	concurrency int // 任务级并发上限（worker 数）
	wg          sync.WaitGroup
}

// NewScheduler 创建一个新的全局下载调度器。
// 全局并发上限固定为 10，限速器在首次有任务指定 SpeedLimit > 0 时惰性创建。
func NewScheduler(db *sql.DB, manager *telegram.ClientManager, storage *StorageManager, emitter EventEmitter) *Scheduler {
	return &Scheduler{
		db:        db,
		manager:   manager,
		storage:   storage,
		emitter:   emitter,
		active:    make(map[int64]*taskRunner),
		globalSem: make(chan struct{}, maxGlobalConcurrency),
		paused:    make(map[int64]bool),
	}
}

// StartTask 启动指定任务的下载。
//
// 流程：
//  1. 从 SQLite 加载 Task 与所有 pending 状态的 DownloadRecords
//  2. 创建 taskRunner，在后台 goroutine 中执行下载
//  3. 单任务并发受 task.Config.Concurrency 约束，全局并发受 maxGlobalConcurrency 约束
//  4. 若 task.Config.SpeedLimit > 0，更新全局限速器
func (s *Scheduler) StartTask(ctx context.Context, taskID int64) error {
	s.mu.Lock()
	if _, exists := s.active[taskID]; exists {
		s.mu.Unlock()
		log.Printf("[scheduler] task #%d StartTask 失败：已在运行中", taskID)
		return fmt.Errorf("任务 #%d 已在运行中", taskID)
	}
	s.mu.Unlock()

	// 加载任务
	task, err := GetTask(s.db, taskID)
	if err != nil {
		log.Printf("[scheduler] task #%d StartTask 失败：加载任务失败: %v", taskID, err)
		return fmt.Errorf("加载任务失败: %w", err)
	}

	// 加载 pending 记录
	records, err := s.listPendingRecords(taskID)
	if err != nil {
		log.Printf("[scheduler] task #%d StartTask 失败：加载待下载记录失败: %v", taskID, err)
		return fmt.Errorf("加载待下载记录失败: %w", err)
	}
	if len(records) == 0 {
		// 没有 pending 记录，直接标记完成
		log.Printf("[scheduler] task #%d StartTask：无 pending 记录，直接完成", taskID)
		_ = UpdateTaskStatus(s.db, taskID, StatusCompleted)
		return nil
	}

	log.Printf("[scheduler] task #%d StartTask：启动下载 (pending=%d, concurrency=%d, speed_limit=%d, channel=%s)",
		taskID, len(records), task.Config.Concurrency, task.Config.SpeedLimit, task.Config.DialogName)

	// 更新全局限速器（若配置了 SpeedLimit）
	s.updateLimiter(task.Config.SpeedLimit)

	// 更新任务状态为 downloading
	if err := UpdateTaskStatus(s.db, taskID, StatusDownloading); err != nil {
		return fmt.Errorf("更新任务状态失败: %w", err)
	}
	task.Status = StatusDownloading

	// 创建任务级信号量
	concurrency := task.Config.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > maxGlobalConcurrency {
		concurrency = maxGlobalConcurrency
	}

	// 创建可取消的下载上下文。
	// 注意：必须基于 context.Background() 派生，不能复用调用方传入的 ctx。
	// 原因：调用方 ctx（如 CreateTaskFromMessagesSync 的 WithTimeout ctx）在函数返回后会被 cancel，
	// 若下载 ctx 作为其子 ctx 会被级联取消，导致下载 goroutine 立即退出、任务被误判为 paused。
	// 调用方 ctx 仅用于控制 StartTask 本身的执行（如加载任务、加载记录），不参与下载生命周期。
	subCtx, cancel := context.WithCancel(context.Background())

	runner := &taskRunner{
		task:        task,
		scheduler:   s,
		cancel:      cancel,
		concurrency: concurrency,
	}

	s.mu.Lock()
	s.active[taskID] = runner
	s.paused[taskID] = false
	s.mu.Unlock()

	// 推送任务状态变更事件
	s.emit(EventTaskStatus, task)

	// 启动下载 goroutine
	log.Printf("[scheduler] task #%d StartTask：启动 run goroutine (records=%d)", taskID, len(records))
	go runner.run(subCtx, records)

	return nil
}

// PauseTask 暂停指定任务。
// 取消任务上下文，正在下载的文件会被中止；pending 但未开始的记录保持 pending。
func (s *Scheduler) PauseTask(taskID int64) error {
	s.mu.Lock()
	runner, exists := s.active[taskID]
	s.paused[taskID] = true
	s.mu.Unlock()

	if exists && runner != nil {
		runner.cancel()
	}

	// 主动把所有 downloading 状态的记录批量改为 pending，保证 DB 一致。
	// 此前依赖 worker 异步在 downloadWithRetry 中改 status，存在时序竞争：
	// 若 ResumeTask 在 worker 改完前触发，listPendingRecords(status='pending')
	// 查不到卡在 downloading 的记录，导致恢复顺序错乱或并发 worker 取不到足够任务。
	// downloadFile 暂停分支已持久化 downloaded_bytes/local_path，此处仅改 status，断点信息不受影响。
	if n, err := ResetDownloadingToPending(s.db, taskID); err != nil {
		log.Printf("[scheduler] task #%d PauseTask 批量重置 downloading 记录失败: %v", taskID, err)
	} else if n > 0 {
		log.Printf("[scheduler] task #%d PauseTask 主动重置 %d 条 downloading 记录为 pending", taskID, n)
	}

	// 更新任务状态为 paused
	if err := UpdateTaskStatus(s.db, taskID, StatusPaused); err != nil {
		return fmt.Errorf("更新任务状态失败: %w", err)
	}

	// 推送状态变更事件
	if task, err := GetTask(s.db, taskID); err == nil {
		s.emit(EventTaskStatus, task)
	}
	return nil
}

// ResumeTask 恢复指定任务的下载。
// 重新加载所有 pending 记录并启动新的 taskRunner。
func (s *Scheduler) ResumeTask(ctx context.Context, taskID int64) error {
	s.mu.Lock()
	if _, exists := s.active[taskID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("任务 #%d 已在运行中", taskID)
	}
	s.paused[taskID] = false
	s.mu.Unlock()

	return s.StartTask(ctx, taskID)
}

// DeleteTask 删除指定任务。
// 取消正在运行的 taskRunner，并删除任务及其所有下载记录。
func (s *Scheduler) DeleteTask(taskID int64) error {
	// 取消正在运行的 taskRunner
	s.mu.Lock()
	runner, exists := s.active[taskID]
	delete(s.active, taskID)
	delete(s.paused, taskID)
	s.mu.Unlock()

	if exists && runner != nil {
		runner.cancel()
	}

	// 删除任务及关联记录
	if err := DeleteTask(s.db, taskID); err != nil {
		return fmt.Errorf("删除任务失败: %w", err)
	}
	return nil
}

// RetryFailed 重试指定任务的所有失败记录。
// 将 failed 记录改为 pending，然后启动任务。
func (s *Scheduler) RetryFailed(ctx context.Context, taskID int64) error {
	// 将 failed 记录改为 pending
	if err := s.resetFailedRecords(taskID); err != nil {
		return fmt.Errorf("重置失败记录失败: %w", err)
	}
	return s.StartTask(ctx, taskID)
}

// StopAll 停止所有活跃任务的下载，并将所有进行中（downloading）的任务状态改为 paused。
// 用于退出登录等场景：停止所有下载但不删除任务记录，便于重新登录后恢复。
// 已暂停/已完成/失败的任务不受影响。
func (s *Scheduler) StopAll() error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	// 取消所有活跃任务的 runner，并标记为暂停态（避免 finalizeTask 覆盖状态）
	s.mu.Lock()
	taskIDs := make([]int64, 0, len(s.active))
	for taskID, runner := range s.active {
		taskIDs = append(taskIDs, taskID)
		if runner != nil {
			runner.cancel()
		}
		s.paused[taskID] = true
	}
	s.mu.Unlock()

	// 将数据库中所有 downloading 状态的任务批量改为 paused
	// （覆盖 active 中的任务以及可能存在的孤儿 downloading 任务）
	now := time.Now().Format(time.RFC3339)
	if _, err := s.db.Exec(
		`UPDATE tasks SET status = ?, updated_at = ? WHERE status = ?`,
		string(StatusPaused), now, string(StatusDownloading),
	); err != nil {
		return fmt.Errorf("更新任务状态失败: %w", err)
	}

	// 推送每个被暂停任务的状态变更事件给前端
	for _, taskID := range taskIDs {
		if task, err := GetTask(s.db, taskID); err == nil {
			s.emit(EventTaskStatus, task)
		}
	}
	return nil
}

// run 在后台 goroutine 中执行，处理所有 pending 记录。
//
// 调度模型：有序 channel + 固定 worker pool
//   - 分片生成器：按 records 数组顺序提交到 taskCh（FIFO）
//   - worker pool：concurrency 个 worker 从 taskCh 取任务，保证顺序确定
//   - 全局并发仍由 globalSem 控制
//
// 这样恢复下载时，有进度的 records（由 listPendingRecords 排序在前的）会被优先取出，
// 不会出现"跳过有进度的、去下载没进度的"问题。
func (r *taskRunner) run(ctx context.Context, records []DownloadRecord) {
	log.Printf("[scheduler] task #%d run：开始执行 (records=%d, workers=%d)", r.task.ID, len(records), r.concurrency)
	defer func() {
		// 等待所有下载 goroutine 完成
		r.wg.Wait()
		log.Printf("[scheduler] task #%d run：所有下载 goroutine 完成，清理 active map", r.task.ID)

		// 任务结束后从 active map 移除（先删除，以便 ResumeTask 能创建新 runner）
		r.scheduler.mu.Lock()
		delete(r.scheduler.active, r.task.ID)
		r.scheduler.mu.Unlock()

		// 更新任务最终状态（若任务被暂停则跳过，由 PauseTask 已设置 paused 状态）
		r.finalizeTask(ctx)
	}()

	// 有序 channel：按 records 数组顺序提交任务
	taskCh := make(chan DownloadRecord, len(records))
	go func() {
		defer close(taskCh)
		for _, rec := range records {
			select {
			case taskCh <- rec:
			case <-ctx.Done():
				return
			}
		}
	}()

	// worker pool：固定数量 = concurrency，从有序 channel 取任务
	// worker 数本身限制任务级并发，不再需要 taskSem
	workerCount := r.concurrency
	if workerCount <= 0 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			for rec := range taskCh {
				if ctx.Err() != nil {
					return
				}
				// 获取全局并发槽位
				select {
				case r.scheduler.globalSem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				// 执行下载（含重试），全局槽位在匿名函数 defer 中释放
				// 传指针：downloadFile 内部对断点信息的修改需回传给 downloadWithRetry，避免重试时用过期数据
				func() {
					defer func() { <-r.scheduler.globalSem }()
					r.downloadWithRetry(ctx, &rec)
				}()
			}
		}()
	}
}

// finalizeTask 在所有下载完成后更新任务的最终状态。
func (r *taskRunner) finalizeTask(ctx context.Context) {
	// 重新加载任务以获取最新进度
	task, err := GetTask(r.scheduler.db, r.task.ID)
	if err != nil {
		// 任务可能已被删除，忽略
		log.Printf("[scheduler] task #%d finalizeTask 加载任务失败: %v", r.task.ID, err)
		return
	}

	// 防止与新 runner 竞争：run defer 中 delete(active) 在 finalizeTask 之前执行，
	// 若用户在旧 runner 退出期间快速点"继续"，ResumeTask 会创建新 runner 并写入 active。
	// 此时旧 finalizeTask 若继续执行，会把新 runner 正在下载的任务状态覆盖为 paused/completed/failed。
	// 因此先检查 active 是否已有新 runner，有则跳过；同时读取 paused 标志。
	r.scheduler.mu.Lock()
	_, hasNewRunner := r.scheduler.active[r.task.ID]
	paused := r.scheduler.paused[r.task.ID]
	r.scheduler.mu.Unlock()

	if hasNewRunner {
		log.Printf("[scheduler] task #%d finalizeTask: 检测到新 runner 已启动，跳过状态更新避免覆盖", r.task.ID)
		return
	}

	if paused {
		log.Printf("[scheduler] task #%d finalizeTask: 任务已暂停，跳过状态更新", r.task.ID)
		return
	}

	// 检查是否有 pending 记录（可能因取消未完成）
	pending, _ := r.scheduler.listPendingRecords(r.task.ID)
	if len(pending) > 0 {
		// 有未完成的记录，标记为 paused（用户可恢复）
		_ = UpdateTaskStatus(r.scheduler.db, r.task.ID, StatusPaused)
		task.Status = StatusPaused
		log.Printf("[scheduler] task #%d finalizeTask: %d 条 pending 未完成，标记为 paused", r.task.ID, len(pending))
	} else if task.FailedFiles > 0 {
		// 有失败记录
		_ = UpdateTaskStatus(r.scheduler.db, r.task.ID, StatusFailed)
		task.Status = StatusFailed
		log.Printf("[scheduler] task #%d finalizeTask: %d 条失败，标记为 failed", r.task.ID, task.FailedFiles)
	} else {
		// 全部完成
		_ = UpdateTaskStatus(r.scheduler.db, r.task.ID, StatusCompleted)
		task.Status = StatusCompleted
		log.Printf("[scheduler] task #%d finalizeTask: 全部完成 (total=%d)", r.task.ID, task.TotalFiles)
	}

	r.scheduler.emit(EventTaskStatus, task)
}

// downloadWithRetry 执行单个文件的下载，失败时按指数退避重试。
//
// record 采用指针传递：downloadFile 内部对 record.DownloadedBytes / record.LocalPath 的修改
//（如断点重置、暂停持久化）需回传给本函数，否则重试时仍用过期的断点信息，
// 导致"首次下载超时 → 重试时 DownloadedBytes=0 → 走首次下载路径 → ResolveConflict 生成 _1 文件"。
func (r *taskRunner) downloadWithRetry(ctx context.Context, record *DownloadRecord) {
	// 标记为 downloading。
	// 使用 UpdateRecordStatusOnly 仅改状态，保留 local_path/downloaded_bytes（断点续传信息由 downloadFile 持久化）。
	_ = UpdateRecordStatusOnly(r.scheduler.db, record.ID, "downloading")
	record.Status = "downloading"
	r.scheduler.emit(EventFileStatus, *record)

	// 断点续传恢复：若记录有已下载字节数，推送一个初始 file:progress 事件，
	// 让前端 fileProgress 恢复到断点值，避免显示从 0 开始。
	if record.DownloadedBytes > 0 && record.FileSize > 0 {
		r.scheduler.emit(EventFileProgress, FileProgress{
			TaskID:     r.task.ID,
			RecordID:   record.ID,
			MessageID:  record.MessageID,
			Downloaded: record.DownloadedBytes,
			Total:      record.FileSize,
			Speed:      0,
			Done:       false,
		})
		log.Printf("[scheduler] task #%d 恢复下载 msg_id=%d 初始进度 %d/%d 字节",
			r.task.ID, record.MessageID, record.DownloadedBytes, record.FileSize)
	}

	log.Printf("[scheduler] task #%d 开始下载 msg_id=%d file=%q (attempt 0/%d)",
		r.task.ID, record.MessageID, record.FileName, maxRetries)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			// 被取消（暂停），仅更新状态为 pending，保留 local_path/downloaded_bytes（断点续传信息由 downloadFile 持久化）
			_ = UpdateRecordStatusOnly(r.scheduler.db, record.ID, "pending")
			record.Status = "pending"
			r.scheduler.emit(EventFileStatus, *record)
			log.Printf("[scheduler] task #%d 下载 msg_id=%d 被取消（保持 pending）", r.task.ID, record.MessageID)
			return
		}

		err := r.downloadFile(ctx, record)
		if err == nil {
			// 下载成功
			log.Printf("[scheduler] task #%d 下载成功 msg_id=%d (attempt=%d)", r.task.ID, record.MessageID, attempt)
			return
		}
		lastErr = err
		log.Printf("[scheduler] task #%d 下载失败 msg_id=%d (attempt=%d/%d): %v",
			r.task.ID, record.MessageID, attempt+1, maxRetries+1, err)

		// 如果是父上下文取消（暂停），不重试
		if ctx.Err() != nil {
			_ = UpdateRecordStatusOnly(r.scheduler.db, record.ID, "pending")
			record.Status = "pending"
			r.scheduler.emit(EventFileStatus, *record)
			return
		}

		// 未达到最大重试次数，等待退避后重试
		if attempt < maxRetries {
			backoff := retryBackoff(attempt)
			log.Printf("[scheduler] task #%d 等待 %v 后重试 msg_id=%d", r.task.ID, backoff, record.MessageID)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				_ = UpdateRecordStatusOnly(r.scheduler.db, record.ID, "pending")
				record.Status = "pending"
				r.scheduler.emit(EventFileStatus, *record)
				return
			}
		}
	}

	// 超过重试次数，标记为失败
	errMsg := lastErr.Error()
	_ = UpdateRecordStatus(r.scheduler.db, record.ID, "failed", "", errMsg)
	record.Status = "failed"
	record.Error = errMsg
	r.scheduler.emit(EventFileStatus, *record)
	log.Printf("[scheduler] task #%d 下载最终失败 msg_id=%d (重试 %d 次后放弃): %s",
		r.task.ID, record.MessageID, maxRetries+1, errMsg)

	// 更新任务失败计数
	task, _ := GetTask(r.scheduler.db, r.task.ID)
	if task.ID > 0 {
		_ = UpdateTaskProgress(r.scheduler.db, r.task.ID,
			task.TotalFiles, task.CompletedFiles, task.FailedFiles+1)
		task.FailedFiles++
		r.scheduler.emit(EventTaskProgress, task)
	}
}

// downloadFile 下载单个文件到本地。
//
// 流程：
//  1. 拉取消息以获取最新的 FileReference（文件引用可能过期）
//  2. 从消息媒体中构造 InputFileLocation
//  3. 应用命名模板生成文件名
//  4. 使用 StreamRangeToWriter 下载文件到本地（支持 offset 断点续传）
//  5. 每 500ms 推送一次 EventFileProgress 进度事件
//  6. 更新记录状态为 completed，推送事件
//
// 关键设计：
//   - record 采用指针传递，断点重置/暂停持久化对 downloadWithRetry 可见
//   - 暂停判断用 ctx.Err()（父 ctx），而非 dlCtx.Err()（子 ctx 含 30min 超时）
//     避免大文件超时被误判为暂停 → 重试时用过期 record → ResolveConflict 生成 _1 文件
//   - 重新下载（断点损坏/超时/失败重试）时直接 os.Create 覆盖原文件，不走 ResolveConflict
//     满足"重新下载不使用新文件，而是覆盖原文件内容"的需求
func (r *taskRunner) downloadFile(ctx context.Context, record *DownloadRecord) error {
	if r.scheduler.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	client := r.scheduler.manager.GetClient()
	if client == nil {
		return fmt.Errorf("客户端未启动")
	}

	// 带超时的下载上下文（仅控制本次下载尝试，不表达暂停语义）
	dlCtx, cancel := context.WithTimeout(ctx, downloadCallTimeout)
	defer cancel()

	// 拉取消息以获取最新的 FileReference
	msg, err := r.fetchMessageByID(dlCtx, record.MessageID)
	if err != nil {
		return fmt.Errorf("拉取消息 %d 失败: %w", record.MessageID, err)
	}

	// 构造文件下载位置
	location, err := buildFileLocation(msg)
	if err != nil {
		return fmt.Errorf("构造文件位置失败: %w", err)
	}

	// 获取任务目录：基于 SaveDirName，使每个任务的文件按主题分目录存放
	taskDir, err := r.scheduler.storage.GetTaskDir(r.task.Config.SaveDirName)
	if err != nil {
		return fmt.Errorf("获取任务目录失败: %w", err)
	}

	// 应用命名模板生成文件名
	finalName := r.renderFilename(*record)
	// 视频类型应用前缀
	if record.MediaType == "video" && r.task.Config.VideoPrefix != "" {
		finalName = template.ApplyVideoPrefix(finalName, r.task.Config.VideoPrefix)
	}
	// 确定性文件路径：同一 record 的文件名固定，重新下载时 os.Create 会覆盖原文件
	fullPath := filepath.Join(taskDir, finalName)

	// 断点续传可用性校验：若文件被删除或大小不足（用户手动删半成品、异常残留空文件），
	// 删除残留文件 + 重置断点，降级为全新下载，避免在空文件上 Seek 产生稀疏文件。
	if record.DownloadedBytes > 0 && record.LocalPath != "" {
		if info, statErr := os.Stat(record.LocalPath); statErr != nil || info.Size() < record.DownloadedBytes {
			log.Printf("[scheduler] task #%d 断点续传文件不可用 msg_id=%d path=%s (stat_err=%v size=%d downloaded=%d)，重置断点从头下载",
				r.task.ID, record.MessageID, record.LocalPath, statErr, func() int64 {
					if statErr != nil {
						return -1
					}
					return info.Size()
				}(), record.DownloadedBytes)
			// 先删磁盘残留文件，再重置 DB 断点，避免残留文件导致后续 os.Create 覆盖时混淆
			if record.LocalPath != fullPath {
				_ = os.Remove(record.LocalPath)
			}
			_ = ResetRecordBreakpoint(r.scheduler.db, record.ID)
			record.DownloadedBytes = 0
			record.LocalPath = ""
		} else {
			// 复用已持久化的文件路径（断点续传场景）
			fullPath = record.LocalPath
			log.Printf("[scheduler] task #%d 恢复下载 msg_id=%d 复用文件 %s 从字节 %d/%d 继续",
				r.task.ID, record.MessageID, fullPath, record.DownloadedBytes, record.FileSize)
		}
	}

	// 打开文件：断点续传用 O_WRONLY|O_CREATE + Seek，首次/重新下载用 os.Create（O_TRUNC 覆盖）
	var f *os.File
	if record.DownloadedBytes > 0 {
		f, err = os.OpenFile(filepath.Clean(fullPath), os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return fmt.Errorf("打开文件失败: %w", err)
		}
		if _, err = f.Seek(record.DownloadedBytes, 0); err != nil {
			_ = f.Close()
			return fmt.Errorf("定位文件写入位置失败: %w", err)
		}
	} else {
		// os.Create = O_RDWR|O_CREATE|O_TRUNC：若文件已存在则截断为 0，覆盖原内容
		f, err = os.Create(filepath.Clean(fullPath))
		if err != nil {
			return fmt.Errorf("创建文件失败: %w", err)
		}
	}
	defer func() { _ = f.Close() }()

	s := r.scheduler
	s.mu.Lock()
	limiter := s.limiter
	s.mu.Unlock()

	// 构造进度 writer
	//   - 首次下载：startOffset=0, length=0（拉到末尾）
	//   - 断点续传：startOffset=record.DownloadedBytes, length=FileSize-DownloadedBytes
	pw := &progressWriter{
		ctx:       dlCtx,
		base:      f,
		limiter:   limiter,
		taskID:    r.task.ID,
		recordID:  record.ID,
		messageID: record.MessageID,
		total:     record.FileSize,
		emitFn:    s.emit,
	}
	// 初始化已下载字节数为恢复点，保证前端进度连续
	pw.mu.Lock()
	pw.downloaded = record.DownloadedBytes
	pw.lastBytes = record.DownloadedBytes
	pw.mu.Unlock()
	pw.start()

	// 计算本次拉取的起始 offset 与 length
	var startOffset, length int64
	if record.DownloadedBytes > 0 {
		startOffset = record.DownloadedBytes
		if record.FileSize > record.DownloadedBytes {
			length = record.FileSize - record.DownloadedBytes
		}
	} else {
		// 首次下载：length=0 表示拉到末尾
		length = 0
	}

	_, streamErr := StreamRangeToWriter(dlCtx, client.API(), location, startOffset, length, pw)

	// 暂停判断：用父 ctx 而非 dlCtx。
	// dlCtx 含 30min 超时，大文件超时后 dlCtx.Err()!=nil 但 ctx.Err()==nil，
	// 此时不应走暂停保留逻辑，而应走失败重试（重试时 os.Create 覆盖原文件）。
	isPaused := ctx.Err() != nil

	if streamErr != nil {
		pw.stop(false)
		if isPaused {
			// 暂停：保留文件与断点，便于恢复
			r.persistBreakpoint(pw, fullPath, record, "下载暂停")
		} else {
			// 非暂停失败（超时/网络错误）：删除文件 + 重置断点，重试时从头下载并覆盖原文件
			_ = os.Remove(fullPath)
			_ = ResetRecordBreakpoint(s.db, record.ID)
			record.DownloadedBytes = 0
			record.LocalPath = ""
		}
		return fmt.Errorf("下载文件失败: %w", streamErr)
	}

	// StreamRangeToWriter 返回 nil 但 ctx 已取消（取消发生在最后一块 EOF 之后）
	// 此时不能走完整性校验删文件，必须走暂停保留逻辑
	if isPaused {
		pw.stop(false)
		r.persistBreakpoint(pw, fullPath, record, "下载取消(返回nil)")
		return fmt.Errorf("下载被取消")
	}

	// 完整性校验：StreamRangeToWriter 返回 nil 不代表写入完整，
	// 内部把"短读"（len(data)<limit）当作 EOF 提前返回成功。
	pw.mu.Lock()
	actualDownloaded := pw.downloaded
	pw.mu.Unlock()
	if record.FileSize > 0 && actualDownloaded < record.FileSize {
		pw.stop(false)
		_ = os.Remove(fullPath)
		_ = ResetRecordBreakpoint(s.db, record.ID)
		record.DownloadedBytes = 0
		record.LocalPath = ""
		log.Printf("[scheduler] task #%d 下载不完整 msg_id=%d 实际 %d/%d 字节，删除文件并重置断点",
			r.task.ID, record.MessageID, actualDownloaded, record.FileSize)
		return fmt.Errorf("下载不完整: 已写入 %d/%d 字节", actualDownloaded, record.FileSize)
	}

	// 下载成功
	pw.stop(true)
	_ = UpdateRecordStatus(s.db, record.ID, "completed", fullPath, "")
	_ = UpdateRecordDownloadedBytes(s.db, record.ID, 0)
	record.Status = "completed"
	record.LocalPath = fullPath
	record.DownloadedBytes = 0
	s.emit(EventFileStatus, *record)

	// 更新任务完成计数
	task, _ := GetTask(s.db, r.task.ID)
	if task.ID > 0 {
		_ = UpdateTaskProgress(s.db, r.task.ID,
			task.TotalFiles, task.CompletedFiles+1, task.FailedFiles)
		task.CompletedFiles++
		s.emit(EventTaskProgress, task)
	}

	return nil
}

// persistBreakpoint 暂停时持久化断点信息（合并原 downloadFile 中重复的两段逻辑）。
//   - 已下载 > 0：保留文件，写回 downloaded_bytes 与 local_path，便于恢复时 Seek 续传
//   - 已下载 = 0：删除 0 字节空文件，清除断点，下次从头下载
func (r *taskRunner) persistBreakpoint(pw *progressWriter, fullPath string, record *DownloadRecord, scenario string) {
	pw.mu.Lock()
	currentDownloaded := pw.downloaded
	pw.mu.Unlock()
	if currentDownloaded > 0 {
		// 合并为单条 UPDATE，避免两次写入间的锁冲突窗口
		if _, err := r.scheduler.db.Exec(
			`UPDATE download_records SET downloaded_bytes = ?, local_path = ? WHERE id = ?`,
			currentDownloaded, fullPath, record.ID,
		); err != nil {
			log.Printf("[scheduler] task #%d %s msg_id=%d 持久化断点失败: %v",
				r.task.ID, scenario, record.MessageID, err)
		}
		record.LocalPath = fullPath
		log.Printf("[scheduler] task #%d %s msg_id=%d 已下载 %d/%d 字节（保留文件）",
			r.task.ID, scenario, record.MessageID, currentDownloaded, record.FileSize)
	} else {
		_ = os.Remove(fullPath)
		_ = UpdateRecordDownloadedBytes(r.scheduler.db, record.ID, 0)
		log.Printf("[scheduler] task #%d %s msg_id=%d 无数据（删除空文件）",
			r.task.ID, scenario, record.MessageID)
	}
}

// progressWriter 包装底层 io.Writer，追踪下载字节数并定期推送进度事件。
// 同时支持可选的限速（当 limiter 非 nil 时）。
//
// 进度推送策略：
//   - start() 启动一个 ticker 协程，每 progressInterval（500ms）推送一次进度
//   - stop(ok) 停止 ticker 并推送最终事件（Done=true，Downloaded=Total if ok）
//   - Write 方法在写入数据时累加字节数，并按限速器（如有）等待
type progressWriter struct {
	ctx       context.Context
	base      io.Writer
	limiter   *rate.Limiter
	taskID    int64
	recordID  int64
	messageID int64
	total     int64

	mu         sync.Mutex
	downloaded int64
	lastTick   time.Time
	lastBytes  int64
	ticker     *time.Ticker
	done       chan struct{}

	emitFn func(eventName string, data interface{})
}

// progressInterval 是进度推送间隔（500ms）。
const progressInterval = 500 * time.Millisecond

func (pw *progressWriter) Write(p []byte) (int, error) {
	// 限速（若启用）
	if pw.limiter != nil {
		if err := pw.limiter.WaitN(pw.ctx, len(p)); err != nil {
			return 0, err
		}
	}
	n, err := pw.base.Write(p)
	if n > 0 {
		pw.mu.Lock()
		pw.downloaded += int64(n)
		pw.mu.Unlock()
	}
	return n, err
}

// start 启动 ticker 协程，定期推送进度事件。
func (pw *progressWriter) start() {
	pw.mu.Lock()
	pw.lastTick = time.Now()
	pw.mu.Unlock()
	pw.ticker = time.NewTicker(progressInterval)
	pw.done = make(chan struct{})
	go pw.tickLoop()
}

// tickLoop 是 ticker 协程的主循环，每 500ms 推送一次进度。
func (pw *progressWriter) tickLoop() {
	for {
		select {
		case <-pw.done:
			return
		case <-pw.ctx.Done():
			return
		case <-pw.ticker.C:
			pw.emit(false)
		}
	}
}

// emit 推送一次进度事件。计算瞬时速度基于上次推送以来的字节差 / 时间差。
func (pw *progressWriter) emit(done bool) {
	pw.mu.Lock()
	downloaded := pw.downloaded
	now := time.Now()
	elapsed := now.Sub(pw.lastTick)
	bytesDiff := downloaded - pw.lastBytes
	// 更新基准值
	pw.lastTick = now
	pw.lastBytes = downloaded
	pw.mu.Unlock()

	// 计算瞬时速度 bytes/s
	var speed int64
	if elapsed > 0 {
		speed = int64(float64(bytesDiff) / elapsed.Seconds())
		if speed < 0 {
			speed = 0
		}
	}

	// 下载完成时，已下载 = 总大小（避免最后一帧误差）
	if done && pw.total > 0 {
		downloaded = pw.total
	}

	pw.emitFn(EventFileProgress, FileProgress{
		TaskID:     pw.taskID,
		RecordID:   pw.recordID,
		MessageID:  pw.messageID,
		Downloaded: downloaded,
		Total:      pw.total,
		Speed:      speed,
		Done:       done,
	})
}

// stop 停止 ticker 并推送最终进度事件。
// ok=true 表示下载成功（Downloaded=Total），ok=false 表示失败/取消。
func (pw *progressWriter) stop(ok bool) {
	if pw.ticker != nil {
		pw.ticker.Stop()
	}
	select {
	case <-pw.done:
		// 已关闭
	default:
		close(pw.done)
	}
	pw.emit(ok)
}

// fetchMessageByID 拉取指定消息 ID 的最新内容（含有效的 FileReference）。
// 频道使用 ChannelsGetMessages，其他对话使用 MessagesGetMessages。
//
// 注意：access_hash 必须从 dialog_infos 表实时查询，**不能**使用 task.Config.DialogAccessHash。
// 原因：task.Config 经过 Wails JS↔Go 序列化时，int64 在 JS Number 中精度丢失
// （JS Number 只有 53 位精度，超过 2^53 的整数末尾几位会变成 0）。
// dialog_infos 表的 access_hash 由 Go 直接从 Telegram API 写入 SQLite，精度无损。
func (r *taskRunner) fetchMessageByID(ctx context.Context, messageID int64) (*tg.Message, error) {
	api := r.scheduler.manager.GetClient().API()
	id := int(messageID)
	inputMsg := []tg.InputMessageClass{&tg.InputMessageID{ID: id}}

	var resp tg.MessagesMessagesClass
	var err error

	// 实时查询对话信息以获取精度无损的 access_hash
	dialog, derr := telegram.LoadDialogByPeerID(r.scheduler.db, r.task.Config.DialogPeerID)
	if derr != nil {
		log.Printf("[scheduler] task #%d fetchMessageByID 查询对话失败 peer_id=%d: %v",
			r.task.ID, r.task.Config.DialogPeerID, derr)
		// 查询失败时回退到 task.Config 中的值（可能精度丢失，但聊胜于无）
		dialog.AccessHash = r.task.Config.DialogAccessHash
		dialog.Type = ""
	}

	if derr == nil && dialog.Type == telegram.DialogTypeChannel {
		// 频道使用 ChannelsGetMessages（用 dialog 表中的精度无损 access_hash）
		resp, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  r.task.Config.DialogPeerID,
				AccessHash: dialog.AccessHash,
			},
			ID: inputMsg,
		})
	} else {
		// 其他对话使用 MessagesGetMessages
		resp, err = api.MessagesGetMessages(ctx, inputMsg)
	}

	if err != nil {
		log.Printf("[scheduler] task #%d fetchMessageByID API 调用失败 msg_id=%d peer_id=%d access_hash=%d (config_hash=%d): %v",
			r.task.ID, messageID, r.task.Config.DialogPeerID, dialog.AccessHash, r.task.Config.DialogAccessHash, err)
		return nil, fmt.Errorf("API 调用失败: %w", err)
	}

	messages, err := extractMessages(resp)
	if err != nil {
		log.Printf("[scheduler] task #%d fetchMessageByID 解析响应失败 msg_id=%d: %v", r.task.ID, messageID, err)
		return nil, err
	}
	if len(messages) == 0 {
		log.Printf("[scheduler] task #%d fetchMessageByID 消息不存在 msg_id=%d", r.task.ID, messageID)
		return nil, fmt.Errorf("消息 %d 不存在", messageID)
	}

	msg, ok := messages[0].(*tg.Message)
	if !ok || msg == nil {
		log.Printf("[scheduler] task #%d fetchMessageByID 消息类型异常 msg_id=%d type=%T", r.task.ID, messageID, messages[0])
		return nil, fmt.Errorf("消息 %d 类型异常", messageID)
	}
	return msg, nil
}

// renderFilename 根据命名模板渲染最终文件名。
// 复用 RenderFilenameForRecord，确保下载阶段与前端展示的渲染逻辑完全一致
// （包括扩展名自动追加、视频前缀等行为）。前缀由调用方按需应用。
func (r *taskRunner) renderFilename(record DownloadRecord) string {
	return RenderFilenameForRecord(record, r.task.Config, r.task.VideoName)
}

// updateLimiter 更新全局限速器。
// 若 SpeedLimit > 0，创建或更新限速器；若 SpeedLimit = 0，保持现有配置不变。
func (s *Scheduler) updateLimiter(speedLimit int64) {
	if speedLimit <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	burst := int(speedLimit)
	if burst < rateBurstMin {
		burst = rateBurstMin
	}

	if s.limiter == nil {
		s.limiter = rate.NewLimiter(rate.Limit(speedLimit), burst)
	} else {
		s.limiter.SetLimit(rate.Limit(speedLimit))
		s.limiter.SetBurst(burst)
	}
}

// listPendingRecords 列出指定任务下所有 pending 状态的下载记录。
// 字段顺序需与 scanRecords 期望的 15 列保持一致（含 sort_order, episode_number, episode_raw, downloaded_bytes）。
//
// 排序规则：按 id ASC 排序。
// 选集下载场景下，BatchCreateRecords 在单事务内按数组顺序插入，SQLite 自增 id 单调递增，
// 因此 id 升序就是用户提交顺序（ep1 → ep2 → ep3 → ep4）。
// 暂停后恢复时按同一顺序喂入 worker pool，保证"从1开始顺延"的需求，
// 不再按 downloaded_bytes DESC 排序——那会破坏提交顺序。
func (s *Scheduler) listPendingRecords(taskID int64) ([]DownloadRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw, downloaded_bytes
		 FROM download_records WHERE task_id = ? AND status = ?
		 ORDER BY id ASC`,
		taskID, "pending",
	)
	if err != nil {
		return nil, fmt.Errorf("查询 pending 记录失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanRecords(rows)
}

// resetFailedRecords 将指定任务下所有 failed 记录重置为 pending。
func (s *Scheduler) resetFailedRecords(taskID int64) error {
	_, err := s.db.Exec(
		`UPDATE download_records SET status = ?, error = '' WHERE task_id = ? AND status = ?`,
		"pending", taskID, "failed",
	)
	if err != nil {
		return fmt.Errorf("重置 failed 记录失败: %w", err)
	}
	return nil
}

// emit 安全推送事件。
func (s *Scheduler) emit(eventName string, data interface{}) {
	if s.emitter == nil {
		return
	}
	_ = s.emitter.Emit(eventName, data)
}

// Emit 公开的事件推送方法，供 Service 层在任务状态变更时推送事件给前端。
// 若未注入 emitter 则静默忽略。
func (s *Scheduler) Emit(eventName string, data interface{}) {
	s.emit(eventName, data)
}

// retryBackoff 计算第 attempt 次重试的退避时长（指数退避，上限 30s）。
// attempt 从 0 开始：1s, 2s, 4s, 8s, 16s, 30s, 30s...
func retryBackoff(attempt int) time.Duration {
	if attempt >= 5 {
		return 30 * time.Second
	}
	return time.Duration(1<<uint(attempt)) * time.Second
}

// buildFileLocation 从消息媒体中构造下载位置（tg.InputFileLocationClass）。
// 支持 Document 与 Photo 两种媒体类型。
func buildFileLocation(msg *tg.Message) (tg.InputFileLocationClass, error) {
	media, ok := msg.GetMedia()
	if !ok || media == nil {
		return nil, fmt.Errorf("消息 %d 无媒体附件", msg.ID)
	}

	switch m := media.(type) {
	case *tg.MessageMediaDocument:
		if m.Document == nil {
			return nil, fmt.Errorf("消息 %d 的 Document 为空", msg.ID)
		}
		doc, isDoc := m.Document.(*tg.Document)
		if !isDoc {
			return nil, fmt.Errorf("消息 %d 的 Document 类型异常", msg.ID)
		}
		return &tg.InputDocumentFileLocation{
			ID:           doc.ID,
			AccessHash:   doc.AccessHash,
			FileReference: doc.FileReference,
			ThumbSize:    "", // 下载原文件，不下载缩略图
		}, nil

	case *tg.MessageMediaPhoto:
		if m.Photo == nil {
			return nil, fmt.Errorf("消息 %d 的 Photo 为空", msg.ID)
		}
		photo, isPhoto := m.Photo.(*tg.Photo)
		if !isPhoto {
			return nil, fmt.Errorf("消息 %d 的 Photo 类型异常", msg.ID)
		}
		// 选择最大尺寸的 PhotoSize 类型作为 ThumbSize
		thumbSize, _, _ := pickLargestPhotoSize(photo.Sizes)
		return &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     thumbSize,
		}, nil

	default:
		return nil, fmt.Errorf("消息 %d 的媒体类型 %T 不支持下载", msg.ID, media)
	}
}

// rateLimitedWriter 包装 io.Writer，在每次写入前调用限速器等待。
// 用于在下载流中实现字节级限速。
type rateLimitedWriter struct {
	w       io.Writer
	limiter *rate.Limiter
	ctx     context.Context
}

// Write 实现 io.Writer 接口。先等待限速器分配令牌，再写入数据。
func (r *rateLimitedWriter) Write(p []byte) (int, error) {
	if r.limiter != nil {
		if err := r.limiter.WaitN(r.ctx, len(p)); err != nil {
			return 0, fmt.Errorf("限速等待失败: %w", err)
		}
	}
	return r.w.Write(p)
}

// DownloadSingleFile 下载单个文件到本地，供监听服务（Listener）在新消息匹配后调用。
//
// 与 taskRunner.downloadFile 的区别：
//   - 不依赖 Task（监听下载不创建任务，record.TaskID 可为 0）
//   - 不更新任务进度计数（无 Task 可更新）
//   - 仍受全局并发信号量（globalSem）约束
//   - 仍受全局限速器（limiter）约束
//   - 仍推送 EventFileStatus 事件给前端
//
// 参数：
//   - ctx: 下载上下文
//   - record: 下载记录（需包含 MessageID、FileID、FileName、FileSize、MediaType）
//   - cfg: 任务配置（需包含 DialogPeerID、DialogAccessHash、DialogName、VideoPrefix、NamingTemplate）
//
// 该方法会阻塞直到下载完成或失败，调用方通常在独立 goroutine 中调用。
func (s *Scheduler) DownloadSingleFile(ctx context.Context, record DownloadRecord, cfg TaskConfig) error {
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	client := s.manager.GetClient()
	if client == nil {
		return fmt.Errorf("客户端未启动")
	}

	// 获取全局并发槽位
	select {
	case s.globalSem <- struct{}{}:
		defer func() { <-s.globalSem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// 带超时的下载上下文
	dlCtx, cancel := context.WithTimeout(ctx, downloadCallTimeout)
	defer cancel()

	// 拉取消息以获取最新的 FileReference
	msg, err := fetchMessageByPeer(dlCtx, s.db, client.API(), cfg.DialogPeerID, cfg.DialogAccessHash, record.MessageID)
	if err != nil {
		_ = UpdateRecordStatus(s.db, record.ID, "failed", "", fmt.Sprintf("拉取消息失败: %v", err))
		record.Status = "failed"
		record.Error = fmt.Sprintf("拉取消息失败: %v", err)
		s.emit(EventFileStatus, record)
		return fmt.Errorf("拉取消息 %d 失败: %w", record.MessageID, err)
	}

	// 构造文件下载位置
	location, err := buildFileLocation(msg)
	if err != nil {
		_ = UpdateRecordStatus(s.db, record.ID, "failed", "", fmt.Sprintf("构造文件位置失败: %v", err))
		record.Status = "failed"
		record.Error = fmt.Sprintf("构造文件位置失败: %v", err)
		s.emit(EventFileStatus, record)
		return fmt.Errorf("构造文件位置失败: %w", err)
	}

	// 获取任务目录：基于 SaveDirName（用户填写的保存目录名）
	taskDir, err := s.storage.GetTaskDir(cfg.SaveDirName)
	if err != nil {
		return fmt.Errorf("获取任务目录失败: %w", err)
	}

	// 渲染文件名（单文件下载场景无 videoName，走模板逻辑）
	finalName := RenderFilenameForRecord(record, cfg, "")
	// 视频类型应用前缀
	if record.MediaType == "video" && cfg.VideoPrefix != "" {
		finalName = template.ApplyVideoPrefix(finalName, cfg.VideoPrefix)
	}
	// 解决重名冲突
	finalName, err = template.ResolveConflict(taskDir, finalName)
	if err != nil {
		return fmt.Errorf("解决文件名冲突失败: %w", err)
	}
	fullPath := filepath.Join(taskDir, finalName)

	// 标记为 downloading
	_ = UpdateRecordStatus(s.db, record.ID, "downloading", "", "")
	record.Status = "downloading"
	s.emit(EventFileStatus, record)

	// 创建下载器并下载
	dl := downloader.NewDownloader()
	builder := dl.Download(client.API(), location)

	// 根据是否限速选择下载方式
	s.mu.Lock()
	limiter := s.limiter
	s.mu.Unlock()

	if limiter != nil {
		// 限速模式
		f, err := os.Create(filepath.Clean(fullPath))
		if err != nil {
			_ = UpdateRecordStatus(s.db, record.ID, "failed", "", fmt.Sprintf("创建文件失败: %v", err))
			record.Status = "failed"
			record.Error = fmt.Sprintf("创建文件失败: %v", err)
			s.emit(EventFileStatus, record)
			return fmt.Errorf("创建文件失败: %w", err)
		}
		defer func() { _ = f.Close() }()

		limitedWriter := &rateLimitedWriter{
			w:       f,
			limiter: limiter,
			ctx:     dlCtx,
		}
		if _, err := builder.Stream(dlCtx, limitedWriter); err != nil {
			_ = os.Remove(fullPath)
			_ = UpdateRecordStatus(s.db, record.ID, "failed", "", fmt.Sprintf("下载失败: %v", err))
			record.Status = "failed"
			record.Error = fmt.Sprintf("下载失败: %v", err)
			s.emit(EventFileStatus, record)
			return fmt.Errorf("下载文件失败: %w", err)
		}
	} else {
		// 不限速模式
		if _, err := builder.ToPath(dlCtx, fullPath); err != nil {
			_ = os.Remove(fullPath)
			_ = UpdateRecordStatus(s.db, record.ID, "failed", "", fmt.Sprintf("下载失败: %v", err))
			record.Status = "failed"
			record.Error = fmt.Sprintf("下载失败: %v", err)
			s.emit(EventFileStatus, record)
			return fmt.Errorf("下载文件失败: %w", err)
		}
	}

	// 下载成功，更新记录
	_ = UpdateRecordStatus(s.db, record.ID, "completed", fullPath, "")
	record.Status = "completed"
	record.LocalPath = fullPath
	s.emit(EventFileStatus, record)
	return nil
}

// fetchMessageByPeer 拉取指定对话中指定 ID 的消息。
// 频道使用 ChannelsGetMessages，其他对话使用 MessagesGetMessages。
//
// 注意：accessHash 参数来自 TaskConfig，可能因 Wails JS↔Go 序列化精度丢失。
// 因此函数内部会优先从 dialog_infos 表查询精度无损的 access_hash，仅在查询失败时回退。
func fetchMessageByPeer(ctx context.Context, db *sql.DB, api *tg.Client, peerID, accessHash, messageID int64) (*tg.Message, error) {
	id := int(messageID)
	inputMsg := []tg.InputMessageClass{&tg.InputMessageID{ID: id}}

	var resp tg.MessagesMessagesClass
	var err error

	// 查找对话类型与精度无损的 access_hash
	dialog, derr := telegram.LoadDialogByPeerID(db, peerID)
	effectiveHash := accessHash
	if derr == nil {
		// 优先用 dialog 表中的值（精度无损）
		effectiveHash = dialog.AccessHash
	} else {
		log.Printf("[scheduler] fetchMessageByPeer 查询对话失败 peer_id=%d，回退到传入的 access_hash: %v", peerID, derr)
	}

	if derr == nil && dialog.Type == telegram.DialogTypeChannel {
		// 频道使用 ChannelsGetMessages
		resp, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  peerID,
				AccessHash: effectiveHash,
			},
			ID: inputMsg,
		})
	} else {
		// 其他对话使用 MessagesGetMessages
		resp, err = api.MessagesGetMessages(ctx, inputMsg)
	}

	if err != nil {
		log.Printf("[scheduler] fetchMessageByPeer API 调用失败 msg_id=%d peer_id=%d effective_hash=%d (passed_hash=%d): %v",
			messageID, peerID, effectiveHash, accessHash, err)
		return nil, fmt.Errorf("API 调用失败: %w", err)
	}

	messages, err := extractMessages(resp)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("消息 %d 不存在", messageID)
	}

	msg, ok := messages[0].(*tg.Message)
	if !ok || msg == nil {
		return nil, fmt.Errorf("消息 %d 类型异常", messageID)
	}
	return msg, nil
}

// RenderFilenameForRecord 根据命名模板渲染最终文件名（不依赖 Task）。
// 用于在下载前展示渲染后的文件名（如前端文件列表的"下载名称"列）。
//
// 命名规则：
//   - 选集下载场景（videoName 非空）：固定格式 "{VideoName}_第{Episode:02d}集{ext}"
//     VideoName 中的空格替换为下划线，避免文件名含空格不便管理
//   - 扫描下载场景（videoName 为空）：走用户配置的命名模板
//
// 渲染流程（模板场景）：
//  1. 提取源文件扩展名（如 .mp4），从 Filename 变量中去除扩展名
//  2. 应用命名模板（模板变量 {filename} 已不含扩展名）
//  3. 若渲染结果未以源扩展名结尾（不区分大小写），自动追加源扩展名
//
// 注意：未做冲突解决（同名文件加 _1 _2 后缀），也未应用视频前缀，
// 调用方按需应用前缀（参考 taskRunner.downloadFile / DownloadSingleFile）。
func RenderFilenameForRecord(record DownloadRecord, cfg TaskConfig, videoName string) string {
	filename := record.FileName
	if filename == "" {
		filename = strconv.FormatInt(record.MessageID, 10)
	}
	// 提取源文件扩展名（含点，如 .mp4）；无扩展名时为空字符串
	ext := filepath.Ext(filename)

	// 选集下载场景：固定命名规则 {VideoName}_第{Episode:02d}集{ext}
	if videoName != "" {
		// 空格替换为下划线
		safeName := strings.ReplaceAll(videoName, " ", "_")
		// 集数优先用 EpisodeNumber，回退 SortOrder
		ep := 0
		if record.EpisodeNumber != nil && *record.EpisodeNumber > 0 {
			ep = *record.EpisodeNumber
		} else if record.SortOrder > 0 {
			ep = record.SortOrder
		}
		rendered := fmt.Sprintf("%s_第%02d集", safeName, ep)
		// 追加源扩展名（若未以源扩展名结尾）
		if ext != "" && !strings.EqualFold(filepath.Ext(rendered), ext) {
			rendered += ext
		}
		return rendered
	}

	// 扫描下载场景：走用户配置的命名模板
	vars := template.NamingVars{
		Date:      "",
		Channel:   cfg.DialogName,
		MessageID: record.MessageID,
		Filename:  strings.TrimSuffix(filename, ext),
		MediaType: record.MediaType,
		// Episode 取记录的 sort_order（1-based）。
		// 未配置集数正则的任务 sort_order 由扫描时初始化（按 message_id），
		// 渲染出的是基于消息 ID 的非连续数字，意义不大；模板未使用 {episode} 时不影响。
		Episode: record.SortOrder,
	}
	rendered := template.RenderTemplate(cfg.NamingTemplate, vars)
	// 渲染结果若未以源扩展名结尾（不区分大小写），自动追加源扩展名
	// 例如模板 "{channel}_{message_id}" 渲染为 "channel_12345"，源文件是 .mp4 → 追加为 "channel_12345.mp4"
	// 若模板已显式包含扩展名（如 "{filename}.mp4" 渲染为 "video.mp4"），则不重复追加
	if ext != "" && !strings.EqualFold(filepath.Ext(rendered), ext) {
		rendered += ext
	}
	return rendered
}

// RenderRecordName 根据任务配置与记录信息，按命名模板渲染最终文件名（含视频前缀）。
// 用于在下载前展示完整的下载文件名（前端展示用）。
// 注意：未做冲突解决（同名文件加 _1 _2 后缀），仅渲染模板 + 前缀结果。
func RenderRecordName(record DownloadRecord, cfg TaskConfig, videoName string) string {
	rendered := RenderFilenameForRecord(record, cfg, videoName)
	if record.MediaType == "video" && cfg.VideoPrefix != "" {
		rendered = template.ApplyVideoPrefix(rendered, cfg.VideoPrefix)
	}
	return rendered
}
