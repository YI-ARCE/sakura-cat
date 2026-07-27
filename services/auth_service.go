// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 AuthService，负责向 Wails 绑定暴露 Telegram 登录流程方法。
package services

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tg-download/internal/download"
	"tg-download/internal/telegram"
)

// readyTimeout 是 Login 方法等待客户端启动就绪的最大时长。
// 超过此时间仍未就绪视为启动失败（可能网络不通或代理不可用）。
const readyTimeout = 60 * time.Second

// callTimeout 是单次 Telegram API 调用（如发送验证码、登出）的最大时长。
const callTimeout = 30 * time.Second

// AuthService 是 Wails 绑定的登录认证服务。
// 它通过持有 *telegram.ClientManager，向前端提供完整的登录流程方法：
// API 凭据 + 手机号 → 验证码 →（可选）2FA 密码 → 登录完成，以及登出与状态查询。
//
// 登出（Logout）时会联动停止 Scheduler 的所有下载任务与 Listener 的实时监听，
// 清除本地会话与 dialogs 缓存并停止客户端，但保留任务历史、下载记录、模板、设置、代理与订阅。
type AuthService struct {
	manager   *telegram.ClientManager
	scheduler *download.Scheduler
	listener  *download.Listener
	db        *sql.DB
}

// NewAuthService 创建一个新的 AuthService 实例。
// manager 必须已初始化（包含有效的 *sql.DB）。
// scheduler 与 listener 用于登出时停止下载任务与实时监听；可为 nil（此时跳过对应步骤）。
// db 用于登出时清空 dialogs 缓存表。
func NewAuthService(manager *telegram.ClientManager, scheduler *download.Scheduler, listener *download.Listener, db *sql.DB) *AuthService {
	return &AuthService{
		manager:   manager,
		scheduler: scheduler,
		listener:  listener,
		db:        db,
	}
}

// Login 发起登录流程：设置 API 凭据，在后台启动客户端，
// 等待客户端就绪后向指定手机号发送验证码。
// 返回 nil 表示验证码已发送，前端应展示验证码输入框并调用 SubmitCode。
//
// 若客户端已启动则复用现有连接；若已登录则直接返回 nil。
func (s *AuthService) Login(apiID int, apiHash string, phone string) error {
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	// 空凭据回退到内置默认值（前端不收集 apiID/apiHash）
	if apiID == 0 || apiHash == "" {
		apiID = telegram.DefaultAPIID
		apiHash = telegram.DefaultAPIHash
	}
	if phone == "" {
		return fmt.Errorf("手机号为空")
	}

	// 已登录则无需重复登录
	if s.manager.IsAuthenticated() {
		return nil
	}

	// 设置凭据
	s.manager.SetCredentials(apiID, apiHash)

	// 若客户端未启动，则启动客户端。
	// Start 为非阻塞：仅同步完成初始化（设置 m.ready），Run 在后台 goroutine 中运行。
	// 因此可同步调用并获取初始化阶段的错误（如代理配置无效）。
	if s.manager.GetClient() == nil {
		if err := s.manager.Start(context.Background()); err != nil {
			return fmt.Errorf("客户端初始化失败: %w", err)
		}
	}

	// 等待客户端就绪（连接建立且 checkAuth 完成）
	readyCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	if err := s.manager.WaitReady(readyCtx); err != nil {
		return fmt.Errorf("客户端启动失败或超时: %w", err)
	}

	// 发送验证码
	sendCtx, cancelSend := context.WithTimeout(context.Background(), callTimeout)
	defer cancelSend()
	if err := s.manager.SendCode(sendCtx, phone); err != nil {
		return fmt.Errorf("发送验证码失败: %w", err)
	}

	// 持久化 API 凭据，用于应用重启后自动恢复会话。
	// 即使后续登录失败，凭据本身是有效的，可下次复用。
	_ = telegram.SaveCredentials(s.manager.DB(), apiID, apiHash)

	return nil
}

// RestoreSession 在应用启动时尝试自动恢复已保存的会话。
// 参数 fallbackAPIID / fallbackAPIHash 为前端传入的后备凭据（来自 localStorage），
// 当后端 settings 表未保存凭据时使用。
//
// 流程：
//  1. 若已认证，直接返回 true
//  2. 若客户端未启动：
//     a. 先从后端 settings 表加载凭据；若无则用前端传入的后备凭据
//     b. 检查 sessions 表是否存在会话记录
//     c. 有凭据+有会话 → 启动客户端并等待就绪
//  3. 返回当前认证状态
//
// 返回 true 表示已登录，前端应跳转主界面；false 表示需要走登录流程。
func (s *AuthService) RestoreSession(fallbackAPIID int, fallbackAPIHash string) (bool, error) {
	if s.manager == nil {
		return false, fmt.Errorf("客户端管理器未初始化")
	}

	// 已认证直接返回
	if s.manager.IsAuthenticated() {
		return true, nil
	}

	// 客户端已启动（可能在恢复中），等待就绪后返回状态
	if s.manager.GetClient() != nil {
		readyCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
		defer cancel()
		_ = s.manager.WaitReady(readyCtx)
		return s.manager.IsAuthenticated(), nil
	}

	// 客户端未启动：尝试加载凭据
	creds, err := telegram.LoadCredentials(s.manager.DB())
	if err != nil {
		return false, fmt.Errorf("加载凭据失败: %w", err)
	}

	apiID := creds.APIID
	apiHash := creds.APIHash

	// 后端无凭据时，用前端传入的后备凭据；仍为空则回退到内置默认凭据
	if apiID == 0 || apiHash == "" {
		if fallbackAPIID != 0 && fallbackAPIHash != "" {
			apiID = fallbackAPIID
			apiHash = fallbackAPIHash
			_ = telegram.SaveCredentials(s.manager.DB(), apiID, apiHash)
		} else {
			// 回退到内置默认凭据
			apiID = telegram.DefaultAPIID
			apiHash = telegram.DefaultAPIHash
		}
	}

	// 检查是否存在会话记录
	storage := telegram.NewSQLiteSessionStorage(s.manager.DB())
	exists, err := storage.SessionExists(context.Background())
	if err != nil {
		return false, fmt.Errorf("检查会话记录失败: %w", err)
	}
	if !exists {
		// 无会话记录，无需恢复
		return false, nil
	}

	// 有凭据+有会话：设置凭据并启动客户端
	s.manager.SetCredentials(apiID, apiHash)
	if err := s.manager.Start(context.Background()); err != nil {
		return false, fmt.Errorf("客户端启动失败: %w", err)
	}

	// 等待就绪
	readyCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	if err := s.manager.WaitReady(readyCtx); err != nil {
		return false, fmt.Errorf("客户端就绪超时: %w", err)
	}

	return s.manager.IsAuthenticated(), nil
}

// SubmitCode 提交用户收到的验证码。
// 返回当前登录步骤：
//   - logged_in：登录成功（无需 2FA）
//   - wait_password：需要 2FA 密码，前端应展示密码输入框
func (s *AuthService) SubmitCode(code string) (telegram.LoginStep, error) {
	if s.manager == nil {
		return "", fmt.Errorf("客户端管理器未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	step, err := s.manager.SubmitCode(ctx, code)
	if err != nil {
		return "", fmt.Errorf("提交验证码失败: %w", err)
	}
	return step, nil
}

// ResendCode 重新发送验证码到上一次使用的手机号。
// 验证码过期或未收到时可调用。
func (s *AuthService) ResendCode() error {
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := s.manager.ResendCode(ctx); err != nil {
		return fmt.Errorf("重新发送验证码失败: %w", err)
	}
	return nil
}

// SubmitPassword 提交 2FA 密码完成登录。
// 仅在 SubmitCode 返回 wait_password 后调用有效。
func (s *AuthService) SubmitPassword(password string) error {
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := s.manager.SubmitPassword(ctx, password); err != nil {
		return fmt.Errorf("2FA 验证失败: %w", err)
	}
	return nil
}

// Logout 登出当前账号并清理本地会话。
// 流程：
//  1. 停止所有下载任务（将进行中的任务状态改为 paused，不删除任务记录）
//  2. 停止实时监听服务
//  3. 若客户端已启动且已登录，调用 Telegram API 登出
//  4. 清除 SQLite sessions 表中的会话记录
//  5. 停止 Telegram 客户端（Stop 内部会重置 authenticated=false、authFlow=nil）
//
// 保留：tasks 表、download_records 表、task_templates 表、proxy 表、settings 表、subscriptions 表。
// 登出后下次启动将无法恢复登录态，需重新走登录流程。
func (s *AuthService) Logout() error {
	if s.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}

	// 1. 停止所有下载任务（将进行中任务标记为 paused）
	if s.scheduler != nil {
		if err := s.scheduler.StopAll(); err != nil {
			return fmt.Errorf("停止下载任务失败: %w", err)
		}
	}

	// 2. 停止实时监听服务
	if s.listener != nil {
		s.listener.Stop()
	}

	// 3. 若客户端已启动且已登录，调用 Telegram API 登出
	if client := s.manager.GetClient(); client != nil && s.manager.IsAuthenticated() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		_, err := client.API().AuthLogOut(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("调用 Telegram 登出失败: %w", err)
		}
	}

	// 4. 清除本地会话记录
	clearCtx, cancelClear := context.WithTimeout(context.Background(), callTimeout)
	defer cancelClear()
	if err := s.manager.ClearSession(clearCtx); err != nil {
		return fmt.Errorf("清除本地会话失败: %w", err)
	}

	// 5. 清空 dialogs 缓存表（已在第1步停掉下载任务，此时无并发写入风险）
	// 下载任务用 peer_id 关联（非 dialogs.id），清空不影响 tasks/subscriptions 关联
	if s.db != nil {
		if _, err := s.db.Exec(`DELETE FROM dialogs`); err != nil {
			return fmt.Errorf("清空 dialogs 表失败: %w", err)
		}
	}

	// 6. 停止客户端（Stop 会在 Start 的清理逻辑中重置 authenticated=false、authFlow=nil）
	s.manager.Stop()
	return nil
}

// GetLoginStatus 返回当前登录状态。
// 返回值：
//   - bool：是否已登录（authenticated）
//   - LoginStep：当前登录流程步骤
//   - error：仅在系统级错误时返回
func (s *AuthService) GetLoginStatus() (bool, telegram.LoginStep, error) {
	if s.manager == nil {
		return false, "", fmt.Errorf("客户端管理器未初始化")
	}
	authenticated, step := s.manager.GetLoginStatus()
	return authenticated, step, nil
}
