// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件实现基于 gotd/td 的登录流程：API 凭据 → 手机号 → 验证码 →（可选）2FA。
package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// LoginStep 表示当前登录流程所处的步骤。
type LoginStep string

// 支持的登录步骤常量。
const (
	// StepWaitCode 表示已发送验证码，等待用户提交验证码。
	StepWaitCode LoginStep = "wait_code"
	// StepWaitPassword 表示登录需要 2FA，等待用户提交密码。
	StepWaitPassword LoginStep = "wait_password"
	// StepLoggedIn 表示已完成登录。
	StepLoggedIn LoginStep = "logged_in"
)

// codeTTL 是验证码的有效期（5 分钟）。
// 超过后 SubmitCode 将拒绝并要求重新发送。
const codeTTL = 5 * time.Minute

// loginFlow 记录当前登录流程的中间状态。
// 一次登录流程从 SendCode 开始，到 SubmitCode/SubmitPassword 成功（step=StepLoggedIn）或超时结束。
// 字段受 ClientManager.mu 保护（与 ClientManager 共用同一把锁）。
type loginFlow struct {
	mu        sync.Mutex // 保护以下字段的并发访问
	phone     string     // 当前登录的手机号
	codeHash  string     // SendCode 返回的 phone_code_hash，SignIn 时需要
	step      LoginStep  // 当前步骤
	expiresAt time.Time  // 验证码过期时间（SendCode 时刻 + codeTTL）
}

// SendCode 发起登录流程：向指定手机号发送验证码。
// 前置条件：客户端已启动且未登录。
// 成功后内部 loginFlow.step 设置为 StepWaitCode，前端应展示验证码输入框。
func (m *ClientManager) SendCode(ctx context.Context, phone string) error {
	m.mu.Lock()
	running := m.running
	authenticated := m.authenticated
	client := m.client
	m.mu.Unlock()

	if !running || client == nil {
		return fmt.Errorf("客户端未启动")
	}
	if authenticated {
		return fmt.Errorf("已登录，无需重复发送验证码")
	}
	if phone == "" {
		return fmt.Errorf("手机号为空")
	}

	// 规范化为 E.164 格式：去掉空格/横线/括号，确保以 + 开头
	phone = normalizePhone(phone)
	if !isE164(phone) {
		return fmt.Errorf("手机号必须为 E.164 格式（如 +8613800138000），需包含国家区号")
	}

	// 调用 gotd/td 发送验证码
	sentCode, err := client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return fmt.Errorf("发送验证码失败: %w", err)
	}

	// 解析返回的 code hash
	// SendCode 返回 tg.AuthSentCodeClass，常见实现为 *tg.AuthSentCode（含 PhoneCodeHash）
	sc, ok := sentCode.(*tg.AuthSentCode)
	if !ok {
		// 若返回 *tg.AuthSentCodeSuccess，表示已授权，无需验证码
		if _, ok := sentCode.(*tg.AuthSentCodeSuccess); ok {
			m.mu.Lock()
			m.authenticated = true
			if m.authFlow != nil {
				m.authFlow.step = StepLoggedIn
			}
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("未预期的验证码响应类型: %T", sentCode)
	}

	// 保存登录流程状态
	m.mu.Lock()
	if m.authFlow == nil {
		m.authFlow = &loginFlow{}
	}
	flow := m.authFlow
	m.mu.Unlock()

	flow.mu.Lock()
	flow.phone = phone
	flow.codeHash = sc.PhoneCodeHash
	flow.step = StepWaitCode
	flow.expiresAt = time.Now().Add(codeTTL)
	flow.mu.Unlock()

	return nil
}

// SubmitCode 提交用户收到的验证码完成登录。
// 返回当前登录步骤：
//   - StepLoggedIn：登录成功（无需 2FA）
//   - StepWaitPassword：需要 2FA 密码，前端应展示密码输入框
//
// 若验证码已过期返回错误，前端应调用 ResendCode 重新发送。
func (m *ClientManager) SubmitCode(ctx context.Context, code string) (LoginStep, error) {
	m.mu.Lock()
	running := m.running
	authenticated := m.authenticated
	client := m.client
	flow := m.authFlow
	m.mu.Unlock()

	if !running || client == nil {
		return "", fmt.Errorf("客户端未启动")
	}
	if authenticated {
		return StepLoggedIn, nil
	}
	if flow == nil {
		return "", fmt.Errorf("尚未发送验证码，请先调用 SendCode")
	}

	flow.mu.Lock()
	step := flow.step
	phone := flow.phone
	codeHash := flow.codeHash
	expiresAt := flow.expiresAt
	flow.mu.Unlock()

	if step != StepWaitCode {
		return "", fmt.Errorf("当前登录步骤非等待验证码（当前: %s）", step)
	}
	if time.Now().After(expiresAt) {
		// 验证码已过期，前端应调用 ResendCode 重新发送
		return "", fmt.Errorf("验证码已过期，请重新发送")
	}
	if code == "" {
		return "", fmt.Errorf("验证码为空")
	}

	// 调用 SignIn
	_, err := client.Auth().SignIn(ctx, phone, code, codeHash)
	if err != nil {
		// 检查是否需要 2FA
		if errors.Is(err, auth.ErrPasswordAuthNeeded) {
			// 设置步骤为等待密码
			flow.mu.Lock()
			flow.step = StepWaitPassword
			flow.mu.Unlock()
			return StepWaitPassword, nil
		}
		return "", fmt.Errorf("登录失败: %w", err)
	}

	// 登录成功（无需 2FA）
	flow.mu.Lock()
	flow.step = StepLoggedIn
	flow.mu.Unlock()

	m.mu.Lock()
	m.authenticated = true
	m.mu.Unlock()

	return StepLoggedIn, nil
}

// SubmitPassword 提交 2FA 密码完成登录。
// 前置条件：当前步骤为 StepWaitPassword（即 SignIn 已返回需要 2FA）。
func (m *ClientManager) SubmitPassword(ctx context.Context, password string) error {
	m.mu.Lock()
	running := m.running
	authenticated := m.authenticated
	client := m.client
	flow := m.authFlow
	m.mu.Unlock()

	if !running || client == nil {
		return fmt.Errorf("客户端未启动")
	}
	if authenticated {
		return nil
	}
	if flow == nil {
		return fmt.Errorf("尚未发起登录流程")
	}

	flow.mu.Lock()
	step := flow.step
	flow.mu.Unlock()

	if step != StepWaitPassword {
		return fmt.Errorf("当前登录步骤非等待密码（当前: %s）", step)
	}
	if password == "" {
		return fmt.Errorf("密码为空")
	}

	// 调用 Password 完成 2FA
	if _, err := client.Auth().Password(ctx, password); err != nil {
		return fmt.Errorf("2FA 验证失败: %w", err)
	}

	// 登录成功
	flow.mu.Lock()
	flow.step = StepLoggedIn
	flow.mu.Unlock()

	m.mu.Lock()
	m.authenticated = true
	m.mu.Unlock()

	return nil
}

// ResendCode 重新发送验证码到上一次使用的手机号。
// 更新 loginFlow 中的 codeHash 与 expiresAt。
func (m *ClientManager) ResendCode(ctx context.Context) error {
	m.mu.Lock()
	running := m.running
	authenticated := m.authenticated
	client := m.client
	flow := m.authFlow
	m.mu.Unlock()

	if !running || client == nil {
		return fmt.Errorf("客户端未启动")
	}
	if authenticated {
		return fmt.Errorf("已登录，无需重新发送验证码")
	}
	if flow == nil {
		return fmt.Errorf("尚未发送过验证码，请先调用 SendCode")
	}

	flow.mu.Lock()
	phone := flow.phone
	oldHash := flow.codeHash
	flow.mu.Unlock()

	if phone == "" {
		return fmt.Errorf("手机号为空")
	}

	// 调用 gotd/td 的 ResendCode（需要原 code hash）
	sentCode, err := client.Auth().ResendCode(ctx, phone, oldHash)
	if err != nil {
		// 若 resend 失败，回退到 SendCode 重新发送
		sentCode, err = client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return fmt.Errorf("重新发送验证码失败: %w", err)
		}
	}

	// 解析新的 code hash
	sc, ok := sentCode.(*tg.AuthSentCode)
	if !ok {
		if _, ok := sentCode.(*tg.AuthSentCodeSuccess); ok {
			m.mu.Lock()
			m.authenticated = true
			flow.mu.Lock()
			flow.step = StepLoggedIn
			flow.mu.Unlock()
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("未预期的验证码响应类型: %T", sentCode)
	}

	// 更新登录流程状态
	flow.mu.Lock()
	flow.codeHash = sc.PhoneCodeHash
	flow.step = StepWaitCode
	flow.expiresAt = time.Now().Add(codeTTL)
	flow.mu.Unlock()

	return nil
}

// GetLoginStatus 返回当前登录状态。
// 返回值：
//   - bool：是否已登录（authenticated）
//   - LoginStep：当前登录流程步骤（若无进行中的流程，返回空字符串）
func (m *ClientManager) GetLoginStatus() (bool, LoginStep) {
	m.mu.RLock()
	authenticated := m.authenticated
	flow := m.authFlow
	m.mu.RUnlock()

	var step LoginStep
	if flow != nil {
		flow.mu.Lock()
		step = flow.step
		flow.mu.Unlock()
	}
	return authenticated, step
}

// normalizePhone 规范化手机号为 E.164 格式：
// 去除空格、横线、括号；若不含 + 前缀且以国际格式开头则补 +。
// 注意：此函数不会猜测国家区号，仅做格式整理。
func normalizePhone(phone string) string {
	// 去除空白、横线、括号、点
	cleaned := strings.NewReplacer(
		" ", "",
		"-", "",
		"(", "",
		")", "",
		".", "",
		"\t", "",
	).Replace(phone)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return ""
	}
	// 去除可能的多前缀（如 00 国际冠码也视为合法，但 Telegram 不接受，统一转 +）
	if strings.HasPrefix(cleaned, "00") {
		cleaned = "+" + cleaned[2:]
	}
	// 若纯数字无 +，补 +（要求用户已包含国家码）
	if !strings.HasPrefix(cleaned, "+") {
		cleaned = "+" + cleaned
	}
	return cleaned
}

// isE164 校验是否为合法 E.164 格式：+ 后跟 6-15 位数字。
func isE164(phone string) bool {
	if len(phone) < 7 || len(phone) > 16 {
		return false
	}
	if phone[0] != '+' {
		return false
	}
	for _, r := range phone[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
