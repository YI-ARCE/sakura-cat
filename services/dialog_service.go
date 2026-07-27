// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 DialogService，负责向 Wails 绑定暴露 Telegram 对话（频道/群组）
// 列表的查询、刷新、搜索与权限校验方法。
package services

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tg-download/internal/telegram"
)

// dialogCallTimeout 是单次 Telegram API 调用（如拉取对话列表、校验权限）的最大时长。
const dialogCallTimeout = 60 * time.Second

// DialogService 是 Wails 绑定的对话列表服务。
// 它同时持有 *telegram.ClientManager 与 *sql.DB：
//   - 通过 ClientManager 调用 Telegram API 拉取最新对话
//   - 通过 sql.DB 读取/写入本地缓存的对话列表
//
// 所有方法首字母大写，供 Wails 自动绑定暴露给前端。
type DialogService struct {
	manager *telegram.ClientManager
	db      *sql.DB
}

// NewDialogService 创建一个新的 DialogService 实例。
// db 与 manager 均需已初始化。
func NewDialogService(db *sql.DB, manager *telegram.ClientManager) *DialogService {
	return &DialogService{
		manager: manager,
		db:      db,
	}
}

// ListDialogs 实时拉取并返回当前账户的对话列表。
// 不读本地缓存，每次都从 Telegram API 拉取最新数据。
// 返回错误仅在系统级故障（未登录、API 调用失败等）时出现。
func (s *DialogService) ListDialogs() ([]telegram.DialogInfo, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return nil, fmt.Errorf("未登录，请先完成登录")
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialogCallTimeout)
	defer cancel()
	dialogs, err := s.manager.FetchDialogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("拉取对话列表失败: %w", err)
	}
	return dialogs, nil
}

// RefreshDialogs 强制从 Telegram 拉取最新对话列表并更新 SQLite 缓存。
// 适用于用户手动点击"刷新"按钮、或登录后重建 accessHash 缓存的场景。
func (s *DialogService) RefreshDialogs() ([]telegram.DialogInfo, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return nil, fmt.Errorf("未登录，请先完成登录")
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialogCallTimeout)
	defer cancel()
	dialogs, err := s.manager.RefreshDialogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("刷新对话列表失败: %w", err)
	}
	return dialogs, nil
}

// SearchDialogs 实时拉取并按 name 模糊搜索当前账户的对话（不区分大小写）。
// keyword 为空时返回全部对话。
// 每次都从 Telegram API 拉取最新数据，不读本地缓存。
func (s *DialogService) SearchDialogs(keyword string) ([]telegram.DialogInfo, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return nil, fmt.Errorf("未登录，请先完成登录")
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialogCallTimeout)
	defer cancel()
	dialogs, err := s.manager.FetchDialogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("拉取对话列表失败: %w", err)
	}

	// 内存过滤
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return dialogs, nil
	}
	keywordLower := strings.ToLower(keyword)
	filtered := make([]telegram.DialogInfo, 0, len(dialogs))
	for _, d := range dialogs {
		if strings.Contains(strings.ToLower(d.Name), keywordLower) {
			filtered = append(filtered, d)
		}
	}
	return filtered, nil
}

// GetDialog 按 peer_id 查询单个对话。
// 不存在时返回错误。
func (s *DialogService) GetDialog(peerID int64) (telegram.DialogInfo, error) {
	if s.db == nil {
		return telegram.DialogInfo{}, fmt.Errorf("数据库未初始化")
	}
	dialog, err := telegram.LoadDialogByPeerID(s.db, peerID)
	if err != nil {
		return telegram.DialogInfo{}, err
	}
	return dialog, nil
}

// CheckAccess 校验当前账号是否有权限访问指定对话（频道/群组/用户）。
//
// 参数：
//   - peerID: 对端 ID
//   - accessHash: 访问哈希（频道/用户必填，群组可传 0）
//
// 返回 nil 表示有访问权限；返回错误描述具体原因。
// 该方法供前端在用户选择某个频道后、进入频道详情前调用，提前发现无权限情况。
func (s *DialogService) CheckAccess(peerID int64, accessHash int64) error {
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return fmt.Errorf("未登录，请先完成登录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialogCallTimeout)
	defer cancel()
	if err := s.manager.CheckDialogAccess(ctx, peerID, accessHash); err != nil {
		return err
	}
	return nil
}
