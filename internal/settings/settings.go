// Package settings 提供全局应用设置的持久化与读写能力。
// 设置以 key-value 形式存储于 SQLite 的 settings 表中。
package settings

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// AppSettings 表示应用的全部全局设置项。
// 各字段通过 JSON 标签与前端交互。
type AppSettings struct {
	Theme          string `json:"theme"`           // 主题："dark" 或 "light"
	Concurrency    int    `json:"concurrency"`     // 默认并发数（≤10）
	SpeedLimit     int64  `json:"speed_limit"`     // 默认限速 bytes/s（0 表示不限）
	NamingTemplate string `json:"naming_template"` // 默认命名模板
	DownloadDir    string `json:"download_dir"`     // 下载目录
	Language       string `json:"language"`         // 界面语言（固定 "zh-CN"）
	AutoDownload   bool   `json:"auto_download"`   // 默认自动下载开关
	SourceListURL  string `json:"source_list_url"` // 视频源频道清单 URL（GitHub Raw），空表示不启用
}

// DefaultSettings 返回带默认值的 AppSettings。
// SourceListURL 默认指向项目维护的 GitHub 清单，首次启动即可自动同步频道。
func DefaultSettings() AppSettings {
	return AppSettings{
		Theme:          "dark",
		Concurrency:    3,
		SpeedLimit:     0,
		NamingTemplate: "{date}_{filename}",
		DownloadDir:    "",
		Language:       "zh-CN",
		AutoDownload:   true,
		SourceListURL:  "https://raw.githubusercontent.com/YI-ARCE/sakura-cat-channel/master/sources.json",
	}
}

// settings 表中各字段对应的 key 常量。
const (
	keyTheme          = "theme"
	keyConcurrency    = "concurrency"
	keySpeedLimit     = "speed_limit"
	keyNamingTemplate = "naming_template"
	keyDownloadDir    = "download_dir"
	keyLanguage       = "language"
	keyAutoDownload   = "auto_download"
	keySourceListURL  = "source_list_url"
)

// now 返回当前时间的 RFC3339 字符串，用于 updated_at 字段。
func now() string {
	return time.Now().Format(time.RFC3339)
}

// LoadSettings 从数据库加载所有设置，缺失项用默认值填充。
func LoadSettings(db *sql.DB) (AppSettings, error) {
	s := DefaultSettings()

	// 逐个读取每个 key，缺失的 key 视为使用默认值。
	type kv struct {
		key string
		dst interface{}
	}
	items := []kv{
		{keyTheme, &s.Theme},
		{keyConcurrency, &s.Concurrency},
		{keySpeedLimit, &s.SpeedLimit},
		{keyNamingTemplate, &s.NamingTemplate},
		{keyDownloadDir, &s.DownloadDir},
		{keyLanguage, &s.Language},
		{keyAutoDownload, &s.AutoDownload},
		{keySourceListURL, &s.SourceListURL},
	}

	for _, it := range items {
		val, err := getRaw(db, it.key)
		if err != nil {
			if err == sql.ErrNoRows {
				// 缺失项保持默认值，跳过赋值。
				continue
			}
			return AppSettings{}, fmt.Errorf("读取设置 %q 失败: %w", it.key, err)
		}

		// 根据目标类型解析字符串值。
		switch dst := it.dst.(type) {
		case *string:
			*dst = val
		case *int:
			n, err := strconv.Atoi(val)
			if err != nil {
				return AppSettings{}, fmt.Errorf("解析设置 %q 为整数失败: %w", it.key, err)
			}
			*dst = n
		case *int64:
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return AppSettings{}, fmt.Errorf("解析设置 %q 为整数失败: %w", it.key, err)
			}
			*dst = n
		case *bool:
			b, err := strconv.ParseBool(val)
			if err != nil {
				return AppSettings{}, fmt.Errorf("解析设置 %q 为布尔值失败: %w", it.key, err)
			}
			*dst = b
		default:
			return AppSettings{}, fmt.Errorf("未知的设置字段类型: key=%s", it.key)
		}
	}

	return s, nil
}

// SaveSettings 保存所有设置到数据库，每个字段作为一个 key-value，使用 UPSERT。
func SaveSettings(db *sql.DB, s AppSettings) error {
	// 字段到字符串值的映射，按顺序逐一写入。
	pairs := []struct {
		key string
		val string
	}{
		{keyTheme, s.Theme},
		{keyConcurrency, strconv.Itoa(s.Concurrency)},
		{keySpeedLimit, strconv.FormatInt(s.SpeedLimit, 10)},
		{keyNamingTemplate, s.NamingTemplate},
		{keyDownloadDir, s.DownloadDir},
		{keyLanguage, s.Language},
		{keyAutoDownload, strconv.FormatBool(s.AutoDownload)},
		{keySourceListURL, s.SourceListURL},
	}

	for _, p := range pairs {
		if err := setRaw(db, p.key, p.val); err != nil {
			return fmt.Errorf("保存设置 %q 失败: %w", p.key, err)
		}
	}
	return nil
}

// GetSetting 读取单个设置项的原始字符串值。
// 若 key 不存在，返回空字符串与 nil error。
func GetSetting(db *sql.DB, key string) (string, error) {
	val, err := getRaw(db, key)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("读取设置 %q 失败: %w", key, err)
	}
	return val, nil
}

// SetSetting 保存单个设置项的字符串值，使用 UPSERT。
func SetSetting(db *sql.DB, key string, value string) error {
	if err := setRaw(db, key, value); err != nil {
		return fmt.Errorf("保存设置 %q 失败: %w", key, err)
	}
	return nil
}

// getRaw 从 settings 表读取单个 key 的 value。
// 若 key 不存在返回 sql.ErrNoRows。
func getRaw(db *sql.DB, key string) (string, error) {
	var val string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	return val, err
}

// setRaw 向 settings 表写入单个 key-value，使用 UPSERT 语义。
func setRaw(db *sql.DB, key string, value string) error {
	_, err := db.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now(),
	)
	return err
}
