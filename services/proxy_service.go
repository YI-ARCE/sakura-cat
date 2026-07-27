// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 ProxyService，负责向 Wails 绑定暴露代理配置的读写与测试能力。
package services

import (
	"database/sql"
	"fmt"

	"tg-download/internal/telegram"
)

// ProxyService 是 Wails 绑定的代理配置服务。
// 它通过持有 *telegram.ClientManager 与 *sql.DB，向前端提供
// 代理配置的读取、保存（含客户端重连）与连接测试方法。
type ProxyService struct {
	manager *telegram.ClientManager
	db      *sql.DB
}

// NewProxyService 创建一个新的 ProxyService 实例。
// 参数 db 用于直接读取代理配置，manager 用于触达客户端管理逻辑。
func NewProxyService(db *sql.DB, manager *telegram.ClientManager) *ProxyService {
	return &ProxyService{
		manager: manager,
		db:      db,
	}
}

// GetProxy 读取当前代理配置。
// 优先从数据库读取持久化配置；若数据库无记录则返回空配置。
// 该方法供 Wails 自动绑定暴露给前端。
func (s *ProxyService) GetProxy() (telegram.ProxyConfig, error) {
	if s.db == nil {
		return telegram.ProxyConfig{}, fmt.Errorf("数据库未初始化")
	}
	cfg, err := telegram.LoadProxy(s.db)
	if err != nil {
		return telegram.ProxyConfig{}, fmt.Errorf("读取代理配置失败: %w", err)
	}
	return cfg, nil
}

// SaveProxy 保存代理配置并触发客户端重连（若客户端已启动）。
// 保存成功后，若客户端正在运行，将使用新代理重建网络层；会话不会丢失。
func (s *ProxyService) SaveProxy(cfg telegram.ProxyConfig) error {
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	if err := s.manager.UpdateProxy(cfg); err != nil {
		return fmt.Errorf("保存代理配置失败: %w", err)
	}
	return nil
}

// TestProxy 测试给定代理配置能否成功连接到 Telegram DC。
// 返回测试结果（成功状态、消息、延迟）。测试本身不会抛出错误，
// 连接失败时通过 TestResult.Success=false 与 Message 描述原因；
// 仅在系统级错误（如构造 dialer 失败）时返回 error。
func (s *ProxyService) TestProxy(cfg telegram.ProxyConfig) (telegram.TestResult, error) {
	if s.manager == nil {
		return telegram.TestResult{}, fmt.Errorf("客户端管理器未初始化")
	}
	result, err := s.manager.TestProxy(cfg)
	if err != nil {
		return telegram.TestResult{}, fmt.Errorf("测试代理连接失败: %w", err)
	}
	return result, nil
}
