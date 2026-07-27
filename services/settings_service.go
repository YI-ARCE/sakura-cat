// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 SettingsService，负责向 Wails 绑定暴露应用设置的读写能力。
package services

import (
	"database/sql"
	"fmt"

	"tg-download/internal/settings"
)

// SettingsService 是 Wails 绑定的设置服务。
// 它通过持有 *sql.DB 与 settings 表交互，向前端提供设置读写方法。
type SettingsService struct {
	db *sql.DB
}

// NewSettingsService 创建一个新的 SettingsService 实例。
func NewSettingsService(db *sql.DB) *SettingsService {
	return &SettingsService{db: db}
}

// GetSettings 返回当前应用设置，缺失项以默认值填充。
// 该方法供 Wails 自动绑定暴露给前端。
func (s *SettingsService) GetSettings() (settings.AppSettings, error) {
	if s.db == nil {
		return settings.AppSettings{}, fmt.Errorf("数据库未初始化")
	}
	return settings.LoadSettings(s.db)
}

// SaveSettings 保存应用设置到数据库（UPSERT）。
func (s *SettingsService) SaveSettings(cfg settings.AppSettings) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := settings.SaveSettings(s.db, cfg); err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	return nil
}

// GetDownloadDir 仅返回下载目录。
// 前端首次启动引导时使用，避免加载全部设置。
func (s *SettingsService) GetDownloadDir() (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("数据库未初始化")
	}
	dir, err := settings.GetSetting(s.db, "download_dir")
	if err != nil {
		return "", fmt.Errorf("读取下载目录失败: %w", err)
	}
	return dir, nil
}

// SetDownloadDir 仅设置下载目录。
func (s *SettingsService) SetDownloadDir(dir string) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := settings.SetSetting(s.db, "download_dir", dir); err != nil {
		return fmt.Errorf("保存下载目录失败: %w", err)
	}
	return nil
}
