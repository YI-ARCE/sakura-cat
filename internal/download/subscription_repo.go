// Package download 提供文件下载的任务调度、扫描与持久化能力。
// 本文件实现订阅（Subscription）模型及其在 SQLite 中的 CRUD 操作。
// 订阅用于实时监听：当指定对话有新消息时，按媒体类型与关键词筛选并自动触发下载。
package download

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Subscription 表示对一个对话的实时监听订阅。
// 每条订阅绑定一个 dialog（通过 DialogPeerID 唯一标识），并携带筛选条件与命名配置。
type Subscription struct {
	// ID 自增主键
	ID int64 `json:"id"`
	// DialogPeerID 对话对端 ID（频道/群组/用户 ID），唯一
	DialogPeerID int64 `json:"dialog_peer_id"`
	// DialogAccessHash 访问哈希（频道/用户必填，群组为 0）
	DialogAccessHash int64 `json:"dialog_access_hash"`
	// DialogName 对话名称（用于下载目录命名）
	DialogName string `json:"dialog_name"`
	// MediaTypes 媒体类型筛选：video/audio/photo/document（为空表示不过滤）
	MediaTypes []string `json:"media_types"`
	// Keywords 关键词过滤（消息文本/caption 包含任一关键词则匹配；为空表示不过滤）
	Keywords []string `json:"keywords"`
	// VideoPrefix 视频文件名前缀
	VideoPrefix string `json:"video_prefix"`
	// NamingTemplate 命名模板（参考 template.NamingVars 支持的变量）
	NamingTemplate string `json:"naming_template"`
	// Enabled 是否启用监听
	Enabled bool `json:"enabled"`
	// CreatedAt 创建时间（RFC3339）
	CreatedAt string `json:"created_at"`
	// UpdatedAt 更新时间（RFC3339）
	UpdatedAt string `json:"updated_at"`
}

// EnsureSubscriptionsTable 创建 subscriptions 表（如果不存在）。
// 应在应用初始化阶段调用一次。
func EnsureSubscriptionsTable(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS subscriptions (
		id                 INTEGER PRIMARY KEY AUTOINCREMENT,
		dialog_peer_id     INTEGER UNIQUE,
		dialog_access_hash INTEGER,
		dialog_name        TEXT,
		media_types        TEXT,
		keywords           TEXT,
		video_prefix       TEXT,
		naming_template    TEXT,
		enabled            INTEGER,
		created_at         DATETIME,
		updated_at         DATETIME
	);`)
	if err != nil {
		return fmt.Errorf("创建 subscriptions 表失败: %w", err)
	}
	return nil
}

// CreateSubscription 创建或更新订阅（dialog_peer_id 唯一，存在则更新）。
// 返回订阅的 ID。
func CreateSubscription(db *sql.DB, sub Subscription) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}

	mediaTypesJSON, err := json.Marshal(sub.MediaTypes)
	if err != nil {
		return 0, fmt.Errorf("序列化 media_types 失败: %w", err)
	}
	keywordsJSON, err := json.Marshal(sub.Keywords)
	if err != nil {
		return 0, fmt.Errorf("序列化 keywords 失败: %w", err)
	}

	now := time.Now().Format(time.RFC3339)
	enabledVal := 0
	if sub.Enabled {
		enabledVal = 1
	}

	// 使用 INSERT OR REPLACE 实现 UPSERT（dialog_peer_id 有 UNIQUE 约束）
	// 若记录已存在，REPLACE 会删除旧行并插入新行（id 会重新分配）。
	// 为保留原 id，改用显式查询判断。
	var existingID int64
	err = db.QueryRow(`SELECT id FROM subscriptions WHERE dialog_peer_id = ?`, sub.DialogPeerID).Scan(&existingID)
	switch {
	case err == nil:
		// 已存在：UPDATE
		_, err = db.Exec(
			`UPDATE subscriptions
			 SET dialog_access_hash = ?, dialog_name = ?, media_types = ?, keywords = ?,
			     video_prefix = ?, naming_template = ?, enabled = ?, updated_at = ?
			 WHERE id = ?`,
			sub.DialogAccessHash, sub.DialogName, string(mediaTypesJSON), string(keywordsJSON),
			sub.VideoPrefix, sub.NamingTemplate, enabledVal, now, existingID,
		)
		if err != nil {
			return 0, fmt.Errorf("更新订阅 (peer_id=%d) 失败: %w", sub.DialogPeerID, err)
		}
		return existingID, nil
	case errors.Is(err, sql.ErrNoRows):
		// 不存在：INSERT
		res, err := db.Exec(
			`INSERT INTO subscriptions
			 (dialog_peer_id, dialog_access_hash, dialog_name, media_types, keywords,
			  video_prefix, naming_template, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sub.DialogPeerID, sub.DialogAccessHash, sub.DialogName, string(mediaTypesJSON), string(keywordsJSON),
			sub.VideoPrefix, sub.NamingTemplate, enabledVal, now, now,
		)
		if err != nil {
			return 0, fmt.Errorf("插入订阅 (peer_id=%d) 失败: %w", sub.DialogPeerID, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("获取订阅自增 ID 失败: %w", err)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("查询订阅 (peer_id=%d) 失败: %w", sub.DialogPeerID, err)
	}
}

// ListSubscriptions 列出所有订阅，按 created_at 倒序。
func ListSubscriptions(db *sql.DB) ([]Subscription, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT id, dialog_peer_id, IFNULL(dialog_access_hash, 0), IFNULL(dialog_name, ''),
		        IFNULL(media_types, '[]'), IFNULL(keywords, '[]'),
		        IFNULL(video_prefix, ''), IFNULL(naming_template, ''),
		        IFNULL(enabled, 0), IFNULL(created_at, ''), IFNULL(updated_at, '')
		 FROM subscriptions ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("查询订阅列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSubscriptions(rows)
}

// GetSubscription 按 dialog_peer_id 查询单条订阅。
// 不存在时返回错误。
func GetSubscription(db *sql.DB, dialogPeerID int64) (Subscription, error) {
	if db == nil {
		return Subscription{}, fmt.Errorf("数据库未初始化")
	}
	var (
		sub            Subscription
		mediaTypesJSON string
		keywordsJSON   string
		enabledVal     int
	)
	err := db.QueryRow(
		`SELECT id, dialog_peer_id, IFNULL(dialog_access_hash, 0), IFNULL(dialog_name, ''),
		        IFNULL(media_types, '[]'), IFNULL(keywords, '[]'),
		        IFNULL(video_prefix, ''), IFNULL(naming_template, ''),
		        IFNULL(enabled, 0), IFNULL(created_at, ''), IFNULL(updated_at, '')
		 FROM subscriptions WHERE dialog_peer_id = ?`,
		dialogPeerID,
	).Scan(&sub.ID, &sub.DialogPeerID, &sub.DialogAccessHash, &sub.DialogName,
		&mediaTypesJSON, &keywordsJSON,
		&sub.VideoPrefix, &sub.NamingTemplate,
		&enabledVal, &sub.CreatedAt, &sub.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Subscription{}, fmt.Errorf("订阅 (peer_id=%d) 不存在", dialogPeerID)
		}
		return Subscription{}, fmt.Errorf("查询订阅失败: %w", err)
	}
	sub.Enabled = enabledVal != 0
	if err := json.Unmarshal([]byte(mediaTypesJSON), &sub.MediaTypes); err != nil {
		return Subscription{}, fmt.Errorf("解析 media_types JSON 失败: %w", err)
	}
	if err := json.Unmarshal([]byte(keywordsJSON), &sub.Keywords); err != nil {
		return Subscription{}, fmt.Errorf("解析 keywords JSON 失败: %w", err)
	}
	return sub, nil
}

// UpdateSubscription 更新指定订阅的全部字段（按 sub.ID）。
func UpdateSubscription(db *sql.DB, sub Subscription) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	mediaTypesJSON, err := json.Marshal(sub.MediaTypes)
	if err != nil {
		return fmt.Errorf("序列化 media_types 失败: %w", err)
	}
	keywordsJSON, err := json.Marshal(sub.Keywords)
	if err != nil {
		return fmt.Errorf("序列化 keywords 失败: %w", err)
	}
	now := time.Now().Format(time.RFC3339)
	enabledVal := 0
	if sub.Enabled {
		enabledVal = 1
	}
	res, err := db.Exec(
		`UPDATE subscriptions
		 SET dialog_peer_id = ?, dialog_access_hash = ?, dialog_name = ?, media_types = ?,
		     keywords = ?, video_prefix = ?, naming_template = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		sub.DialogPeerID, sub.DialogAccessHash, sub.DialogName, string(mediaTypesJSON),
		string(keywordsJSON), sub.VideoPrefix, sub.NamingTemplate, enabledVal, now, sub.ID,
	)
	if err != nil {
		return fmt.Errorf("更新订阅 #%d 失败: %w", sub.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取订阅 #%d 更新影响行数失败: %w", sub.ID, err)
	}
	if rows == 0 {
		return fmt.Errorf("订阅 #%d 不存在", sub.ID)
	}
	return nil
}

// DeleteSubscription 按 ID 删除订阅。
func DeleteSubscription(db *sql.DB, id int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	res, err := db.Exec(`DELETE FROM subscriptions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除订阅 #%d 失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取订阅 #%d 删除影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("订阅 #%d 不存在", id)
	}
	return nil
}

// ListEnabledSubscriptions 仅返回 enabled=true 的订阅。
// 监听服务启动时调用以加载活跃订阅。
func ListEnabledSubscriptions(db *sql.DB) ([]Subscription, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT id, dialog_peer_id, IFNULL(dialog_access_hash, 0), IFNULL(dialog_name, ''),
		        IFNULL(media_types, '[]'), IFNULL(keywords, '[]'),
		        IFNULL(video_prefix, ''), IFNULL(naming_template, ''),
		        IFNULL(enabled, 0), IFNULL(created_at, ''), IFNULL(updated_at, '')
		 FROM subscriptions WHERE enabled = 1 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("查询启用的订阅失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSubscriptions(rows)
}

// scanSubscriptions 从 sql.Rows 批量扫描出 Subscription 列表。
func scanSubscriptions(rows *sql.Rows) ([]Subscription, error) {
	var result []Subscription
	for rows.Next() {
		var (
			sub            Subscription
			mediaTypesJSON string
			keywordsJSON   string
			enabledVal     int
		)
		if err := rows.Scan(&sub.ID, &sub.DialogPeerID, &sub.DialogAccessHash, &sub.DialogName,
			&mediaTypesJSON, &keywordsJSON,
			&sub.VideoPrefix, &sub.NamingTemplate,
			&enabledVal, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
			return nil, fmt.Errorf("扫描订阅行失败: %w", err)
		}
		sub.Enabled = enabledVal != 0
		// 解析 JSON 数组字段，空字符串或 "[]" 视为空切片
		if mediaTypesJSON == "" {
			mediaTypesJSON = "[]"
		}
		if err := json.Unmarshal([]byte(mediaTypesJSON), &sub.MediaTypes); err != nil {
			return nil, fmt.Errorf("解析 media_types JSON 失败: %w", err)
		}
		if keywordsJSON == "" {
			keywordsJSON = "[]"
		}
		if err := json.Unmarshal([]byte(keywordsJSON), &sub.Keywords); err != nil {
			return nil, fmt.Errorf("解析 keywords JSON 失败: %w", err)
		}
		result = append(result, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历订阅行失败: %w", err)
	}
	return result, nil
}
