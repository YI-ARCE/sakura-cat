// Package download 提供文件下载的任务调度、扫描与持久化能力。
// 本文件实现任务与下载记录在 SQLite 中的持久化操作。
package download

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// CreateTask 在数据库中创建一个新的下载任务，初始状态为 "scanning"。
// config 字段序列化为 JSON 字符串保存。返回新任务的自增 ID。
func CreateTask(db *sql.DB, cfg TaskConfig) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return 0, fmt.Errorf("序列化任务 config 失败: %w", err)
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(
		`INSERT INTO tasks (dialog_id, status, scan_offset, total_files, completed_files, failed_files, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.DialogPeerID, string(StatusScanning), 0, 0, 0, 0, string(configJSON), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("插入任务失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取任务自增 ID 失败: %w", err)
	}
	return id, nil
}

// CreateTaskWithVideo 创建带视频源信息的下载任务（选集下载场景）。
// 与 CreateTask 的区别：同时写入 vr_id/vr_name/vr_cover，便于下载页直接展示。
// 初始 status 由调用方通过 initialStatus 指定（选集下载场景通常为 StatusDownloading 或 StatusPending）。
func CreateTaskWithVideo(db *sql.DB, cfg TaskConfig, vrID int64, vrName, vrCover string, initialStatus TaskStatus) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return 0, fmt.Errorf("序列化任务 config 失败: %w", err)
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(
		`INSERT INTO tasks (dialog_id, status, scan_offset, total_files, completed_files, failed_files, config, created_at, updated_at, vr_id, vr_name, vr_cover)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.DialogPeerID, string(initialStatus), 0, 0, 0, 0, string(configJSON), now, now, vrID, vrName, vrCover,
	)
	if err != nil {
		return 0, fmt.Errorf("插入任务失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取任务自增 ID 失败: %w", err)
	}
	return id, nil
}

// GetTask 按 ID 查询单个任务，包含 config 字段反序列化。
func GetTask(db *sql.DB, id int64) (Task, error) {
	if db == nil {
		return Task{}, fmt.Errorf("数据库未初始化")
	}
	var (
		t                    Task
		configJSON           string
		dialogID             int64
		scanOffset           int64
		totalFiles           int
		completedFiles       int
		failedFiles          int
		status               string
		createdAt, updatedAt string
	)
	err := db.QueryRow(
		`SELECT id, dialog_id, status, scan_offset, total_files, completed_files, failed_files, config, created_at, updated_at, vr_id, vr_name, vr_cover
		 FROM tasks WHERE id = ?`,
		id,
	).Scan(&t.ID, &dialogID, &status, &scanOffset, &totalFiles, &completedFiles, &failedFiles, &configJSON, &createdAt, &updatedAt, &t.VideoID, &t.VideoName, &t.VideoCover)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("任务 #%d 不存在", id)
		}
		return Task{}, fmt.Errorf("查询任务 #%d 失败: %w", id, err)
	}
	t.DialogID = dialogID
	t.Status = TaskStatus(status)
	t.ScanOffset = scanOffset
	t.TotalFiles = totalFiles
	t.CompletedFiles = completedFiles
	t.FailedFiles = failedFiles
	t.CreatedAt = createdAt
	t.UpdatedAt = updatedAt
	if err := json.Unmarshal([]byte(configJSON), &t.Config); err != nil {
		return Task{}, fmt.Errorf("解析任务 #%d 的 config JSON 失败: %w", id, err)
	}
	return t, nil
}

// ListTasks 列出所有任务，按 created_at 倒序排列。
func ListTasks(db *sql.DB) ([]Task, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT id, dialog_id, status, scan_offset, total_files, completed_files, failed_files, config, created_at, updated_at, vr_id, vr_name, vr_cover
		 FROM tasks ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("查询任务列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tasks []Task
	for rows.Next() {
		var (
			t                    Task
			configJSON           string
			dialogID             int64
			scanOffset           int64
			totalFiles           int
			completedFiles       int
			failedFiles          int
			status               string
			createdAt, updatedAt string
		)
		if err := rows.Scan(&t.ID, &dialogID, &status, &scanOffset, &totalFiles, &completedFiles, &failedFiles, &configJSON, &createdAt, &updatedAt, &t.VideoID, &t.VideoName, &t.VideoCover); err != nil {
			return nil, fmt.Errorf("扫描任务行失败: %w", err)
		}
		t.DialogID = dialogID
		t.Status = TaskStatus(status)
		t.ScanOffset = scanOffset
		t.TotalFiles = totalFiles
		t.CompletedFiles = completedFiles
		t.FailedFiles = failedFiles
		t.CreatedAt = createdAt
		t.UpdatedAt = updatedAt
		if err := json.Unmarshal([]byte(configJSON), &t.Config); err != nil {
			return nil, fmt.Errorf("解析任务 #%d 的 config JSON 失败: %w", t.ID, err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务行失败: %w", err)
	}
	return tasks, nil
}

// UpdateTaskStatus 更新指定任务的状态，并同步 updated_at。
func UpdateTaskStatus(db *sql.DB, id int64, status TaskStatus) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(
		`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), now, id,
	)
	if err != nil {
		return fmt.Errorf("更新任务 #%d 状态失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取任务 #%d 状态更新影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("任务 #%d 不存在", id)
	}
	return nil
}

// UpdateTaskProgress 更新指定任务的进度计数（总数/已完成/失败数），并同步 updated_at。
func UpdateTaskProgress(db *sql.DB, id int64, total, completed, failed int) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(
		`UPDATE tasks SET total_files = ?, completed_files = ?, failed_files = ?, updated_at = ? WHERE id = ?`,
		total, completed, failed, now, id,
	)
	if err != nil {
		return fmt.Errorf("更新任务 #%d 进度失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取任务 #%d 进度更新影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("任务 #%d 不存在", id)
	}
	return nil
}

// UpdateTaskScanOffset 更新指定任务的扫描位置（最新已扫描消息 ID）。
func UpdateTaskScanOffset(db *sql.DB, id int64, offset int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(
		`UPDATE tasks SET scan_offset = ?, updated_at = ? WHERE id = ?`,
		offset, now, id,
	)
	if err != nil {
		return fmt.Errorf("更新任务 #%d 扫描位置失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取任务 #%d 扫描位置更新影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("任务 #%d 不存在", id)
	}
	return nil
}

// DeleteTask 删除指定任务，同时删除关联的 download_records（CASCADE）。
// 在事务中执行，确保任务与记录的删除具有原子性。
func DeleteTask(db *sql.DB, id int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 先删除关联的下载记录
	if _, err := tx.Exec(`DELETE FROM download_records WHERE task_id = ?`, id); err != nil {
		return fmt.Errorf("删除任务 #%d 关联记录失败: %w", id, err)
	}
	// 再删除任务本身
	res, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除任务 #%d 失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取任务 #%d 删除影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("任务 #%d 不存在", id)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// CreateRecord 在数据库中创建一条下载记录，返回新记录的自增 ID。
// 同时持久化 sort_order / episode_number / episode_raw 字段（由扫描阶段填充）。
func CreateRecord(db *sql.DB, r DownloadRecord) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(
		`INSERT INTO download_records (task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.TaskID, r.MessageID, r.FileID, r.FileName, r.FileSize, r.MediaType, r.Status, r.LocalPath, r.Error, now,
		r.SortOrder, r.EpisodeNumber, r.EpisodeRaw,
	)
	if err != nil {
		return 0, fmt.Errorf("插入下载记录失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取下载记录自增 ID 失败: %w", err)
	}
	return id, nil
}

// UpdateRecordStatus 更新指定下载记录的状态、本地路径与错误信息。
// 空字符串表示不更新对应字段（status 总是更新）。
func UpdateRecordStatus(db *sql.DB, id int64, status string, localPath string, errMsg string) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	res, err := db.Exec(
		`UPDATE download_records SET status = ?, local_path = ?, error = ? WHERE id = ?`,
		status, localPath, errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("更新下载记录 #%d 状态失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取下载记录 #%d 状态更新影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("下载记录 #%d 不存在", id)
	}
	return nil
}

// UpdateRecordDownloadedBytes 更新指定下载记录的已下载字节数。
// 用于暂停时持久化进度，恢复时据此 seek 到对应位置继续写入（断点续传）。
func UpdateRecordDownloadedBytes(db *sql.DB, id int64, downloaded int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	_, err := db.Exec(
		`UPDATE download_records SET downloaded_bytes = ? WHERE id = ?`,
		downloaded, id,
	)
	if err != nil {
		return fmt.Errorf("更新下载记录 #%d 已下载字节数失败: %w", id, err)
	}
	return nil
}

// UpdateRecordStatusOnly 仅更新指定下载记录的状态，不覆盖 local_path/error/downloaded_bytes。
// 用于暂停时将 downloading 改为 pending 而保留已持久化的断点续传信息。
func UpdateRecordStatusOnly(db *sql.DB, id int64, status string) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	_, err := db.Exec(
		`UPDATE download_records SET status = ? WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("更新下载记录 #%d 状态失败: %w", id, err)
	}
	return nil
}

// ResetRecordBreakpoint 清除断点续传信息（downloaded_bytes=0, local_path=''）。
// 用于下载失败删除文件后重置，避免下次恢复时按已不存在的文件 Seek 产生稀疏文件。
// 仅重置断点元数据，不改 status（status 由调用方按场景另行更新）。
func ResetRecordBreakpoint(db *sql.DB, id int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	_, err := db.Exec(
		`UPDATE download_records SET downloaded_bytes = 0, local_path = '' WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("重置下载记录 #%d 断点失败: %w", id, err)
	}
	return nil
}

// ResetDownloadingToPending 批量将指定任务下所有 downloading 状态的记录改为 pending。
// 保留 local_path/downloaded_bytes（由 downloadFile 在暂停时持久化），仅改 status。
// 用于 PauseTask 主动保证 DB 一致：不依赖 worker 异步把 downloading->pending，
// 避免 ResumeTask 时 listPendingRecords(status='pending') 查不到卡在 downloading 的记录，
// 从而导致恢复顺序错乱或并发 worker 取不到足够任务。
func ResetDownloadingToPending(db *sql.DB, taskID int64) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	res, err := db.Exec(
		`UPDATE download_records SET status = 'pending' WHERE task_id = ? AND status = 'downloading'`,
		taskID,
	)
	if err != nil {
		return 0, fmt.Errorf("重置任务 #%d downloading 记录失败: %w", taskID, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteRecord 删除指定的下载记录。
// 用于用户手动移除匹配到的不需要的文件（如扫描时误匹配）。
// 注意：若记录正在下载中，调用方应先取消相关 goroutine（由 Scheduler 层处理）。
func DeleteRecord(db *sql.DB, id int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	res, err := db.Exec(`DELETE FROM download_records WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除下载记录 #%d 失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取下载记录 #%d 删除影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("下载记录 #%d 不存在", id)
	}
	return nil
}

// ListRecordsByTask 列出指定任务下的所有下载记录。
// 默认按 sort_order ASC 排序（控制下载顺序）；sort_order 相同则按 id ASC。
// 如需按其他键排序，参见 SortTaskRecords / ReorderTaskRecords。
func ListRecordsByTask(db *sql.DB, taskID int64) ([]DownloadRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT id, task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw, downloaded_bytes
		 FROM download_records WHERE task_id = ? ORDER BY sort_order ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询任务 #%d 的下载记录失败: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	return scanRecords(rows)
}

// GetRecordByMessageAndFile 按 message_id + file_id 查询单条下载记录。
// 用于扫描时去重：若记录已存在则跳过该文件。
// 不存在时返回错误（带 "不存在" 文本，调用方可据此判断）。
func GetRecordByMessageAndFile(db *sql.DB, messageID int64, fileID string) (DownloadRecord, error) {
	if db == nil {
		return DownloadRecord{}, fmt.Errorf("数据库未初始化")
	}
	var r DownloadRecord
	var episodeNumber sql.NullInt64
	err := db.QueryRow(
		`SELECT id, task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw, downloaded_bytes
		 FROM download_records WHERE message_id = ? AND file_id = ?`,
		messageID, fileID,
	).Scan(&r.ID, &r.TaskID, &r.MessageID, &r.FileID, &r.FileName, &r.FileSize, &r.MediaType, &r.Status, &r.LocalPath, &r.Error, &r.CreatedAt,
		&r.SortOrder, &episodeNumber, &r.EpisodeRaw, &r.DownloadedBytes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DownloadRecord{}, fmt.Errorf("下载记录 (message_id=%d, file_id=%s) 不存在", messageID, fileID)
		}
		return DownloadRecord{}, fmt.Errorf("查询下载记录失败: %w", err)
	}
	if episodeNumber.Valid {
		v := int(episodeNumber.Int64)
		r.EpisodeNumber = &v
	}
	return r, nil
}

// GetFailedRecords 列出指定任务下所有失败（status=failed）的下载记录。
func GetFailedRecords(db *sql.DB, taskID int64) ([]DownloadRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT id, task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw, downloaded_bytes
		 FROM download_records WHERE task_id = ? AND status = ? ORDER BY id ASC`,
		taskID, "failed",
	)
	if err != nil {
		return nil, fmt.Errorf("查询任务 #%d 的失败记录失败: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	return scanRecords(rows)
}

// scanRecords 从 sql.Rows 批量扫描出 DownloadRecord 列表。
// 字段顺序需与所有 SELECT 语句保持一致（见 ListRecordsByTask 等）。
func scanRecords(rows *sql.Rows) ([]DownloadRecord, error) {
	var records []DownloadRecord
	for rows.Next() {
		var r DownloadRecord
		var episodeNumber sql.NullInt64
		if err := rows.Scan(&r.ID, &r.TaskID, &r.MessageID, &r.FileID, &r.FileName, &r.FileSize, &r.MediaType, &r.Status, &r.LocalPath, &r.Error, &r.CreatedAt,
			&r.SortOrder, &episodeNumber, &r.EpisodeRaw, &r.DownloadedBytes); err != nil {
			return nil, fmt.Errorf("扫描下载记录行失败: %w", err)
		}
		if episodeNumber.Valid {
			v := int(episodeNumber.Int64)
			r.EpisodeNumber = &v
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历下载记录行失败: %w", err)
	}
	return records, nil
}

// UpdateRecordsSortOrder 批量更新指定任务下下载记录的 sort_order。
// 传入有序的记录 ID 列表（按用户期望的下载顺序排列），sort_order 从 1 开始递增。
// 在事务中执行以保证原子性；只更新 task_id 匹配的记录，避免越权更新其他任务的记录。
func UpdateRecordsSortOrder(db *sql.DB, taskID int64, orderedIDs []int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i, id := range orderedIDs {
		if _, err := tx.Exec(
			`UPDATE download_records SET sort_order = ? WHERE id = ? AND task_id = ?`,
			i+1, id, taskID,
		); err != nil {
			return fmt.Errorf("更新记录 #%d 的 sort_order 失败: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// SortBy 定义排序键。
type SortBy string

const (
	// SortByEpisode 按集数（episode_number）升序，未提取到集数的排在最后
	SortByEpisode SortBy = "episode"
	// SortByMessageID 按 Telegram 消息 ID 升序（频道内发布顺序，最稳定）
	SortByMessageID SortBy = "message_id"
	// SortByDate 按消息日期升序
	SortByDate SortBy = "date"
)

// SortRecordsBy 按指定键对 records 进行原地排序。
//   - episode: 按集数升序；未提取到集数的记录始终排在最后（无论 ascending）
//   - message_id: 按消息 ID 升降序
//   - date: 按 created_at 解析后排序（RFC3339 字符串可直接字典序比较）
//
// 相同键的记录按 message_id ASC 兜底，保证排序稳定。
func SortRecordsBy(records []DownloadRecord, sortBy SortBy, ascending bool) {
	// 先按 message_id ASC 兜底，保证稳定性
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].MessageID < records[j].MessageID
	})

	less := func(i, j int) bool {
		a, b := records[i], records[j]
		switch sortBy {
		case SortByEpisode:
			// 集数为 nil 的始终排到最后
			if a.EpisodeNumber == nil && b.EpisodeNumber == nil {
				return a.MessageID < b.MessageID
			}
			if a.EpisodeNumber == nil {
				return false
			}
			if b.EpisodeNumber == nil {
				return true
			}
			return *a.EpisodeNumber < *b.EpisodeNumber
		case SortByDate:
			return a.CreatedAt < b.CreatedAt
		case SortByMessageID:
			fallthrough
		default:
			return a.MessageID < b.MessageID
		}
	}

	if ascending {
		sort.SliceStable(records, less)
	} else {
		sort.SliceStable(records, func(i, j int) bool {
			return less(j, i)
		})
	}
}

// GetRecordByID 按 ID 查询单条下载记录。
// 字段映射方式与 GetRecordByMessageAndFile 一致；
// episode_number 为可空字段（ALTER 增量迁移），扫描时使用 sql.NullInt64 接收。
// 记录不存在时返回 fmt.Errorf("记录 #%d 不存在", id)。
func GetRecordByID(db *sql.DB, id int64) (DownloadRecord, error) {
	if db == nil {
		return DownloadRecord{}, fmt.Errorf("数据库未初始化")
	}
	var r DownloadRecord
	var episodeNumber sql.NullInt64
	err := db.QueryRow(
		`SELECT id, task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw, downloaded_bytes
		 FROM download_records WHERE id = ?`,
		id,
	).Scan(&r.ID, &r.TaskID, &r.MessageID, &r.FileID, &r.FileName, &r.FileSize, &r.MediaType, &r.Status, &r.LocalPath, &r.Error, &r.CreatedAt,
		&r.SortOrder, &episodeNumber, &r.EpisodeRaw, &r.DownloadedBytes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DownloadRecord{}, fmt.Errorf("记录 #%d 不存在", id)
		}
		return DownloadRecord{}, fmt.Errorf("查询下载记录 #%d 失败: %w", id, err)
	}
	if episodeNumber.Valid {
		v := int(episodeNumber.Int64)
		r.EpisodeNumber = &v
	}
	return r, nil
}

// ListRecordsByTaskForPlayback 查询同任务下所有已完成的视频记录，按 sort_order ASC 排序。
// 仅返回 status='completed' 且 media_type='video' 的记录，用于播放页集数列表。
// 复用 scanRecords 处理 episode_number 等 NULL 字段。
func ListRecordsByTaskForPlayback(db *sql.DB, taskID int64) ([]DownloadRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT id, task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw, downloaded_bytes
		 FROM download_records WHERE task_id = ? AND status = 'completed' AND media_type = 'video' ORDER BY sort_order ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询任务 #%d 的可播放记录失败: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	return scanRecords(rows)
}

// GetDialogScanOffset 获取指定频道的最新扫描位置。
// 通过查询该频道最新创建的任务（按 created_at DESC）的 scan_offset 实现，
// 用于新任务启动时恢复上次的扫描位置以实现增量扫描。
// 若该频道尚无任务则返回 0。
func GetDialogScanOffset(db *sql.DB, dialogID int64) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	var scanOffset int64
	err := db.QueryRow(
		`SELECT scan_offset FROM tasks WHERE dialog_id = ? ORDER BY created_at DESC LIMIT 1`,
		dialogID,
	).Scan(&scanOffset)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 该频道尚无任务，从 0 开始
			return 0, nil
		}
		return 0, fmt.Errorf("查询频道 (dialog_id=%d) 扫描位置失败: %w", dialogID, err)
	}
	return scanOffset, nil
}

// EpisodeDownloadStatus 分集下载状态项。
// 用于选集弹窗展示每集是否已下载。
type EpisodeDownloadStatus struct {
	// MessageID 消息 ID
	MessageID int64 `json:"message_id"`
	// Status 下载状态：pending/downloading/completed/failed
	// 空字符串表示无下载记录（未下载）
	Status string `json:"status"`
}

// ListRecordStatusByChannelAndMessages 按 channel_id（tasks.dialog_id）+ message_id 列表
// 批量查询下载记录的最新状态。
// 仅查询 completed 状态的记录（已下载完成的集数），用于选集弹窗"仅未下载"筛选。
// 返回 map[message_id]status，未在结果中的 message_id 表示无下载记录。
func ListRecordStatusByChannelAndMessages(db *sql.DB, dialogID int64, messageIDs []int64) (map[int64]string, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if len(messageIDs) == 0 {
		return map[int64]string{}, nil
	}
	// 构造 IN 占位符
	placeholders := make([]string, len(messageIDs))
	args := make([]interface{}, 0, len(messageIDs)+1)
	args = append(args, dialogID)
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `SELECT r.message_id, r.status
		FROM download_records r
		INNER JOIN tasks t ON r.task_id = t.id
		WHERE t.dialog_id = ? AND r.message_id IN (` + joinPlaceholders(placeholders) + `)
		ORDER BY r.id DESC`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询分集下载状态失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// 同一 message_id 可能有多条记录（不同任务），保留最新（已完成优先）
	result := make(map[int64]string)
	for rows.Next() {
		var msgID int64
		var status string
		if err := rows.Scan(&msgID, &status); err != nil {
			return nil, fmt.Errorf("扫描下载状态行失败: %w", err)
		}
		// 已存在的记录，completed 优先；其他状态按最新覆盖
		if existing, ok := result[msgID]; ok {
			if existing == "completed" {
				// 已完成的不被覆盖
				continue
			}
			if status == "completed" {
				result[msgID] = status
			} else {
				// 都非 completed，保留最新（覆盖）
				result[msgID] = status
			}
		} else {
			result[msgID] = status
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历下载状态行失败: %w", err)
	}
	return result, nil
}

// joinPlaceholders 将占位符切片用 ", " 连接为 SQL IN 子句字符串。
func joinPlaceholders(placeholders []string) string {
	result := ""
	for i, p := range placeholders {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}

// BatchCreateRecords 批量创建下载记录（事务内）。
// 用于选集下载场景：一次创建多条 pending 记录。
// sort_order 从 1 开始递增；episode_number 按传入记录的 EpisodeNumber 字段填充。
// 返回新记录的 ID 列表（与传入记录顺序一致）。
func BatchCreateRecords(db *sql.DB, records []DownloadRecord) ([]int64, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if len(records) == 0 {
		return []int64{}, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ids := make([]int64, 0, len(records))
	now := time.Now().Format(time.RFC3339)
	for i, r := range records {
		var episodeNum sql.NullInt64
		if r.EpisodeNumber != nil {
			episodeNum = sql.NullInt64{Int64: int64(*r.EpisodeNumber), Valid: true}
		}
		sortOrder := r.SortOrder
		if sortOrder == 0 {
			sortOrder = i + 1
		}
		res, err := tx.Exec(
			`INSERT INTO download_records (task_id, message_id, file_id, file_name, file_size, media_type, status, local_path, error, created_at, sort_order, episode_number, episode_raw)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.TaskID, r.MessageID, r.FileID, r.FileName, r.FileSize, r.MediaType, r.Status, r.LocalPath, r.Error, now,
			sortOrder, episodeNum, r.EpisodeRaw,
		)
		if err != nil {
			return nil, fmt.Errorf("插入下载记录失败 (message_id=%d): %w", r.MessageID, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("获取下载记录自增 ID 失败: %w", err)
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return ids, nil
}
