// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 ListenerService，负责向 Wails 绑定暴露实时监听服务的
// 订阅管理（创建/删除/查询）与监听启停控制方法。
package services

import (
	"context"
	"database/sql"
	"fmt"

	"tg-download/internal/download"
)

// ListenerService 是 Wails 绑定的实时监听服务。
// 它组合了 *sql.DB 与 *download.Listener，向前端提供：
//   - 订阅的 CRUD（Subscribe / Unsubscribe / ListSubscriptions / GetSubscription）
//   - 监听服务的启停控制（StartListener / StopListener / IsListenerRunning）
//
// 所有方法首字母大写，供 Wails 自动绑定暴露给前端。
type ListenerService struct {
	db       *sql.DB
	listener *download.Listener
}

// NewListenerService 创建一个新的 ListenerService 实例。
// db 与 listener 均需已初始化。
func NewListenerService(db *sql.DB, listener *download.Listener) *ListenerService {
	return &ListenerService{
		db:       db,
		listener: listener,
	}
}

// Subscribe 创建或更新订阅。
// 若 dialog_peer_id 已存在则更新，否则创建新订阅。
// 返回订阅的 ID。
func (s *ListenerService) Subscribe(sub download.Subscription) (int64, error) {
	if s.db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	id, err := download.CreateSubscription(s.db, sub)
	if err != nil {
		return 0, fmt.Errorf("创建订阅失败: %w", err)
	}

	// 若监听正在运行，刷新内存中的订阅列表
	if s.listener != nil && s.listener.IsRunning() {
		_ = s.listener.ReloadSubscriptions()
	}
	return id, nil
}

// Unsubscribe 按 ID 删除订阅。
func (s *ListenerService) Unsubscribe(id int64) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := download.DeleteSubscription(s.db, id); err != nil {
		return fmt.Errorf("删除订阅失败: %w", err)
	}

	// 若监听正在运行，刷新内存中的订阅列表
	if s.listener != nil && s.listener.IsRunning() {
		_ = s.listener.ReloadSubscriptions()
	}
	return nil
}

// ListSubscriptions 列出所有订阅。
func (s *ListenerService) ListSubscriptions() ([]download.Subscription, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	subs, err := download.ListSubscriptions(s.db)
	if err != nil {
		return nil, fmt.Errorf("列出订阅失败: %w", err)
	}
	return subs, nil
}

// GetSubscription 按 dialog_peer_id 查询单条订阅。
func (s *ListenerService) GetSubscription(dialogPeerID int64) (download.Subscription, error) {
	if s.db == nil {
		return download.Subscription{}, fmt.Errorf("数据库未初始化")
	}
	sub, err := download.GetSubscription(s.db, dialogPeerID)
	if err != nil {
		return download.Subscription{}, fmt.Errorf("获取订阅失败: %w", err)
	}
	return sub, nil
}

// StartListener 启动监听服务。
// 若监听已在运行则直接返回 nil。
// 客户端必须已登录，否则返回错误。
func (s *ListenerService) StartListener() error {
	if s.listener == nil {
		return fmt.Errorf("监听器未初始化")
	}
	if err := s.listener.Start(context.Background()); err != nil {
		return fmt.Errorf("启动监听失败: %w", err)
	}
	return nil
}

// StopListener 停止监听服务。
// 正在进行的下载不受影响。
func (s *ListenerService) StopListener() error {
	if s.listener == nil {
		return fmt.Errorf("监听器未初始化")
	}
	s.listener.Stop()
	return nil
}

// IsListenerRunning 返回监听服务是否正在运行。
func (s *ListenerService) IsListenerRunning() bool {
	if s.listener == nil {
		return false
	}
	return s.listener.IsRunning()
}
