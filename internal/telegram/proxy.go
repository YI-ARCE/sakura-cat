// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件定义代理配置数据结构及其与 SQLite 的持久化读写。
package telegram

import (
	"database/sql"
	"fmt"
	"time"
)

// ProxyType 表示代理协议类型。
type ProxyType string

// 支持的代理类型常量。
const (
	ProxyTypeSOCKS5  ProxyType = "socks5"  // SOCKS5 代理
	ProxyTypeHTTP    ProxyType = "http"    // HTTP 代理（CONNECT 隧道）
	ProxyTypeHTTPS   ProxyType = "https"   // HTTPS 代理（CONNECT 隧道）
	ProxyTypeMTProto ProxyType = "mtproto" // MTProto 代理（暂不支持）
)

// ProxyConfig 表示一份代理配置。
// 项目为单代理配置（同时仅一份生效配置，数据库 id 固定为 1）。
type ProxyConfig struct {
	Type     ProxyType `json:"type"`     // 代理类型
	Address  string    `json:"address"`  // 代理服务器地址（IP 或域名）
	Port     int       `json:"port"`     // 代理服务器端口
	Username string    `json:"username"` // 用户名（可空）
	Password string    `json:"password"` // 密码（可空）
	Enabled  bool      `json:"enabled"`  // 是否启用代理
}

// proxyID 为 proxy 表中固定配置行的主键。
// 项目约定同时仅有一份生效配置，因此固定为 1。
const proxyID = 1

// SaveProxy 将代理配置写入 proxy 表（id=1，使用 UPSERT 语义）。
// 若记录已存在则更新，否则插入。时间戳使用 RFC3339 格式。
func SaveProxy(db *sql.DB, cfg ProxyConfig) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Format(time.RFC3339)
	// enabled 字段以整数 0/1 存储，适配 SQLite 布尔约定。
	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}
	_, err := db.Exec(
		`INSERT INTO proxy (id, type, address, port, username, password, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   type = excluded.type,
		   address = excluded.address,
		   port = excluded.port,
		   username = excluded.username,
		   password = excluded.password,
		   enabled = excluded.enabled,
		   updated_at = excluded.updated_at`,
		proxyID, string(cfg.Type), cfg.Address, cfg.Port, cfg.Username, cfg.Password, enabled, now, now,
	)
	if err != nil {
		return fmt.Errorf("保存代理配置失败: %w", err)
	}
	return nil
}

// LoadProxy 从 proxy 表读取代理配置（id=1）。
// 若无记录返回空配置与 nil error（视为尚未配置代理）。
func LoadProxy(db *sql.DB) (ProxyConfig, error) {
	if db == nil {
		return ProxyConfig{}, fmt.Errorf("数据库未初始化")
	}
	var (
		cfg      ProxyConfig
		typ      string
		enabled  int
		user     string
		password string
	)
	err := db.QueryRow(
		`SELECT type, address, port, username, password, enabled FROM proxy WHERE id = ?`,
		proxyID,
	).Scan(&typ, &cfg.Address, &cfg.Port, &user, &password, &enabled)
	if err != nil {
		if err == sql.ErrNoRows {
			// 无记录返回空配置，不视为错误。
			return ProxyConfig{}, nil
		}
		return ProxyConfig{}, fmt.Errorf("读取代理配置失败: %w", err)
	}
	cfg.Type = ProxyType(typ)
	cfg.Username = user
	cfg.Password = password
	cfg.Enabled = enabled != 0
	return cfg, nil
}
