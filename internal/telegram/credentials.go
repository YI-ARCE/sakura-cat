// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件实现 API 凭据（apiID/apiHash）的持久化，用于应用重启后自动恢复会话。
package telegram

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// credentialsKey 为 settings 表中存储 API 凭据的 key。
const credentialsKey = "api_credentials"

// DefaultAPIID / DefaultAPIHash 为内置默认 API 凭据。
// 开发者请在此填入自有凭据（https://my.telegram.org 申请），勿提交真实值。
// 留空（0 / ""）时，前端登录需用户手动输入 apiID/apiHash。
const (
	DefaultAPIID   = 0
	DefaultAPIHash = ""
)

// StoredCredentials 表示持久化到数据库的 API 凭据。
type StoredCredentials struct {
	APIID   int    `json:"api_id"`
	APIHash string `json:"api_hash"`
}

// SaveCredentials 将 API 凭据持久化到 settings 表。
// 用于登录成功后保存，以便应用重启后自动恢复会话。
func SaveCredentials(db *sql.DB, apiID int, apiHash string) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	data, err := json.Marshal(StoredCredentials{APIID: apiID, APIHash: apiHash})
	if err != nil {
		return fmt.Errorf("序列化凭据失败: %w", err)
	}
	now := time.Now().Format(time.RFC3339)
	_, err = db.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		credentialsKey, string(data), now,
	)
	if err != nil {
		return fmt.Errorf("保存凭据失败: %w", err)
	}
	return nil
}

// LoadCredentials 从 settings 表读取持久化的 API 凭据。
// 无记录时返回零值与 nil error。
func LoadCredentials(db *sql.DB) (StoredCredentials, error) {
	var creds StoredCredentials
	if db == nil {
		return creds, fmt.Errorf("数据库未初始化")
	}
	var val string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, credentialsKey).Scan(&val)
	if err != nil {
		if err == sql.ErrNoRows {
			return creds, nil
		}
		return creds, fmt.Errorf("读取凭据失败: %w", err)
	}
	if err := json.Unmarshal([]byte(val), &creds); err != nil {
		return creds, fmt.Errorf("解析凭据失败: %w", err)
	}
	return creds, nil
}
