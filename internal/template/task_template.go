// Package template 提供任务模板的模型定义与持久化操作。
package template

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TaskTemplateConfig 任务模板配置，用于下载任务的复用配置。
type TaskTemplateConfig struct {
	MediaTypes          []string `json:"media_types"`           // 媒体类型: video/audio/photo/document
	Keywords            []string `json:"keywords"`              // 关键词过滤
	SearchTerm          string   `json:"search_term"`           // 搜索词（可选，Telegram 服务器端全文搜索，与 Keywords 正交）
	SaveDirName         string   `json:"save_dir_name"`         // 保存目录名（必填，作为下载子目录名）
	KeywordMatchScope   string   `json:"keyword_match_scope"`   // 关键词匹配范围: filename/message/both
	KeywordMatchMode    string   `json:"keyword_match_mode"`    // 关键词命中模式: any(默认)/all
	EpisodeRegex        string   `json:"episode_regex"`         // 集数提取正则（含 capture group）
	RequireEpisodeMatch bool     `json:"require_episode_match"` // 集数正则是否必须命中
	ScanFromNewest      bool     `json:"scan_from_newest"`       // 是否从最新消息开始向前扫描
	MaxMatches          int      `json:"max_matches"`            // 最大匹配数量（0=不限制）
	StopAtFirstEpisode  bool     `json:"stop_at_first_episode"`  // 遇到集数1时停止（仅 ScanFromNewest+EpisodeRegex 生效）
	VideoPrefix         string   `json:"video_prefix"`           // 视频文件名前缀
	Concurrency         int      `json:"concurrency"`           // 并发数（≤10）
	SpeedLimit          int64    `json:"speed_limit"`            // 限速 bytes/s（0 表示不限）
	NamingTemplate      string   `json:"naming_template"`       // 命名模板
	AutoDownload        bool     `json:"auto_download"`          // 扫描后是否自动下载
}

// TaskTemplate 任务模板实体。
type TaskTemplate struct {
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	Config    TaskTemplateConfig `json:"config"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
}

// ListTemplates 列出所有模板（按更新时间倒序）。
func ListTemplates(db *sql.DB) ([]TaskTemplate, error) {
	rows, err := db.Query(`SELECT id, name, config, created_at, updated_at FROM task_templates ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询任务模板列表失败: %w", err)
	}
	defer rows.Close()

	var templates []TaskTemplate
	for rows.Next() {
		var t TaskTemplate
		var configJSON string
		if err := rows.Scan(&t.ID, &t.Name, &configJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("扫描任务模板行失败: %w", err)
		}
		if err := json.Unmarshal([]byte(configJSON), &t.Config); err != nil {
			return nil, fmt.Errorf("解析模板 #%d 的 config JSON 失败: %w", t.ID, err)
		}
		templates = append(templates, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务模板行失败: %w", err)
	}
	return templates, nil
}

// GetTemplate 获取单个模板。
func GetTemplate(db *sql.DB, id int64) (TaskTemplate, error) {
	var t TaskTemplate
	var configJSON string
	err := db.QueryRow(`SELECT id, name, config, created_at, updated_at FROM task_templates WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &configJSON, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskTemplate{}, fmt.Errorf("任务模板 #%d 不存在", id)
		}
		return TaskTemplate{}, fmt.Errorf("查询任务模板 #%d 失败: %w", id, err)
	}
	if err := json.Unmarshal([]byte(configJSON), &t.Config); err != nil {
		return TaskTemplate{}, fmt.Errorf("解析模板 #%d 的 config JSON 失败: %w", id, err)
	}
	return t, nil
}

// CreateTemplate 创建模板，返回新模板的 ID。
func CreateTemplate(db *sql.DB, name string, config TaskTemplateConfig) (int64, error) {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return 0, fmt.Errorf("序列化模板 config 失败: %w", err)
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(`INSERT INTO task_templates (name, config, created_at, updated_at) VALUES (?, ?, ?, ?)`, name, string(configJSON), now, now)
	if err != nil {
		return 0, fmt.Errorf("插入任务模板失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取任务模板自增 ID 失败: %w", err)
	}
	return id, nil
}

// UpdateTemplate 更新模板。
func UpdateTemplate(db *sql.DB, id int64, name string, config TaskTemplateConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("序列化模板 config 失败: %w", err)
	}
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(`UPDATE task_templates SET name = ?, config = ?, updated_at = ? WHERE id = ?`, name, string(configJSON), now, id)
	if err != nil {
		return fmt.Errorf("更新任务模板 #%d 失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取任务模板 #%d 更新影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("任务模板 #%d 不存在", id)
	}
	return nil
}

// DeleteTemplate 删除模板。
func DeleteTemplate(db *sql.DB, id int64) error {
	res, err := db.Exec(`DELETE FROM task_templates WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除任务模板 #%d 失败: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取任务模板 #%d 删除影响行数失败: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("任务模板 #%d 不存在", id)
	}
	return nil
}
