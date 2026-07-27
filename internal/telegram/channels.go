// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件实现频道订阅（Join Channel）能力。
package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
)

// SubscribeChannel 通过 username 订阅频道。
// 流程：
//  1. 去掉 username 的 @ 前缀
//  2. 调用 ContactsResolveUsername 解析，从 resolved.Chats 中找到 *tg.Channel 获取 AccessHash
//  3. 构造 InputChannel 并调用 ChannelsJoinChannel 订阅
//
// username 为空或仅含 @ 时返回错误。
// 未登录时返回错误。
func (m *ClientManager) SubscribeChannel(ctx context.Context, username string) error {
	if !m.IsAuthenticated() {
		return fmt.Errorf("未登录，请先完成登录流程")
	}
	client := m.GetClient()
	if client == nil {
		return fmt.Errorf("客户端未启动")
	}

	// 预处理：去掉 @ 前缀
	username = strings.TrimPrefix(username, "@")
	if username == "" {
		return fmt.Errorf("username 不能为空")
	}

	api := client.API()

	// 1. 解析 username
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: username,
	})
	if err != nil {
		return fmt.Errorf("解析频道用户名失败: %w", err)
	}

	// 2. 从 resolved.Chats 中找到 *tg.Channel
	var channel *tg.Channel
	for _, chat := range resolved.Chats {
		if ch, ok := chat.(*tg.Channel); ok {
			channel = ch
			break
		}
	}
	if channel == nil {
		return fmt.Errorf("用户名 %s 对应的不是频道", username)
	}

	// 3. 构造 InputChannel 并订阅
	inputChannel := &tg.InputChannel{
		ChannelID:  channel.ID,
		AccessHash: channel.AccessHash,
	}
	if _, err = api.ChannelsJoinChannel(ctx, inputChannel); err != nil {
		return fmt.Errorf("订阅频道失败: %w", err)
	}
	return nil
}
