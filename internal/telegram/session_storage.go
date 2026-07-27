// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件实现基于 SQLite 的 gotd/td 会话存储，替代默认的文件存储。
package telegram

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// sessionID 为 sessions 表中固定会话行的主键。
// 项目约定同时仅保存一份当前生效会话，因此固定为 1。
const sessionID = 1

// SQLiteSessionStorage 实现 gotd/td 的 session.Storage 接口，
// 将 Telegram 客户端会话数据持久化到 SQLite 的 sessions 表中。
//
// sessions 表结构见 internal/db/schema.go：
//
//	CREATE TABLE sessions (
//	  id           INTEGER PRIMARY KEY,
//	  session_data BLOB,
//	  created_at   DATETIME,
//	  updated_at   DATETIME
//	);
type SQLiteSessionStorage struct {
	db *sql.DB
}

// NewSQLiteSessionStorage 创建一个基于 SQLite 的会话存储实例。
// db 必须已初始化且 sessions 表已建好（由 db.InitDB 保证）。
func NewSQLiteSessionStorage(db *sql.DB) *SQLiteSessionStorage {
	return &SQLiteSessionStorage{db: db}
}

// LoadSession 实现 session.Storage 接口。
// 从 sessions 表读取 id=1 的 session_data。
// 无记录时返回 (nil, nil) 表示首次启动无会话；
// 有记录时返回 session_data 字节。
func (s *SQLiteSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var data []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT session_data FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			// 首次启动无会话，返回 nil 不视为错误
			return nil, nil
		}
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	return data, nil
}

// StoreSession 实现 session.Storage 接口。
// 使用 UPSERT（id=1）写入 session_data，并更新 updated_at 时间戳。
// created_at 仅在首次插入时设置，后续更新保持不变。
func (s *SQLiteSessionStorage) StoreSession(ctx context.Context, data []byte) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, session_data, created_at, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   session_data = excluded.session_data,
		   updated_at = excluded.updated_at`,
		sessionID, data, now, now,
	)
	if err != nil {
		return fmt.Errorf("保存会话失败: %w", err)
	}
	return nil
}

// SessionExists 检查 sessions 表中是否已存在会话记录。
// 用于启动时判断是否需要触发 onAuthFailed 回调（会话存在但认证失败）。
// 返回 true 表示存在记录（无论 session_data 是否为空）。
func (s *SQLiteSessionStorage) SessionExists(ctx context.Context) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("数据库未初始化")
	}
	var id int
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("查询会话记录失败: %w", err)
	}
	return true, nil
}

// DeleteSession 删除 sessions 表中的会话记录（id=1）。
// 用于登出操作，清除已保存的会话以避免下次启动时尝试恢复失效会话。
// 无记录时返回 nil（不视为错误）。
func (s *SQLiteSessionStorage) DeleteSession(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ?`,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

// 编译期断言：确保 SQLiteSessionStorage 实现了 session.Storage 接口。
// 注意：此处仅引用接口用于断言，不引入 session 包到运行时依赖（已由 client.go 引入）。
var _ interface {
	LoadSession(context.Context) ([]byte, error)
	StoreSession(context.Context, []byte) error
} = (*SQLiteSessionStorage)(nil)

// ClearSession 清除 ClientManager 中保存的 Telegram 会话记录。
// 用于登出操作：删除 sessions 表中 id=1 的记录，避免下次启动时尝试恢复失效会话。
// 该方法仅操作数据库，不影响客户端运行状态。
func (m *ClientManager) ClearSession(ctx context.Context) error {
	storage := NewSQLiteSessionStorage(m.db)
	return storage.DeleteSession(ctx)
}
