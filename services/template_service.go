// Package services 提供 Wails3 暴露给前端调用的服务集合。
package services

import (
	"database/sql"
	"fmt"

	"tg-download/internal/template"
)

// TemplateService 任务模板服务，对外暴露任务模板的增删改查方法供 Wails 绑定。
type TemplateService struct {
	db *sql.DB
}

// NewTemplateService 创建任务模板服务实例。
func NewTemplateService(db *sql.DB) *TemplateService {
	return &TemplateService{db: db}
}

// ListTemplates 列出所有任务模板（按更新时间倒序）。
func (s *TemplateService) ListTemplates() ([]template.TaskTemplate, error) {
	templates, err := template.ListTemplates(s.db)
	if err != nil {
		return nil, fmt.Errorf("列出任务模板失败: %w", err)
	}
	return templates, nil
}

// GetTemplate 获取单个任务模板。
func (s *TemplateService) GetTemplate(id int64) (template.TaskTemplate, error) {
	t, err := template.GetTemplate(s.db, id)
	if err != nil {
		return template.TaskTemplate{}, fmt.Errorf("获取任务模板失败: %w", err)
	}
	return t, nil
}

// CreateTemplate 创建任务模板，返回新模板 ID。
func (s *TemplateService) CreateTemplate(name string, config template.TaskTemplateConfig) (int64, error) {
	id, err := template.CreateTemplate(s.db, name, config)
	if err != nil {
		return 0, fmt.Errorf("创建任务模板失败: %w", err)
	}
	return id, nil
}

// UpdateTemplate 更新任务模板。
func (s *TemplateService) UpdateTemplate(id int64, name string, config template.TaskTemplateConfig) error {
	if err := template.UpdateTemplate(s.db, id, name, config); err != nil {
		return fmt.Errorf("更新任务模板失败: %w", err)
	}
	return nil
}

// DeleteTemplate 删除任务模板。
func (s *TemplateService) DeleteTemplate(id int64) error {
	if err := template.DeleteTemplate(s.db, id); err != nil {
		return fmt.Errorf("删除任务模板失败: %w", err)
	}
	return nil
}
