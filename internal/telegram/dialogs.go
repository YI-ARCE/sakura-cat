// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件实现对话（频道/群组/私聊）列表的拉取、缓存与权限校验。
package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// DialogType 表示对话的类型。
type DialogType string

// 支持的对话类型常量。
const (
	// DialogTypeChannel 表示频道（含超级群组）。
	DialogTypeChannel DialogType = "channel"
	// DialogTypeChat 表示普通群组（basic group）。
	DialogTypeChat DialogType = "chat"
	// DialogTypeUser 表示私聊用户。
	DialogTypeUser DialogType = "user"
)

// DialogInfo 表示单个对话的元信息。
// 该结构同时用于 API 返回与 SQLite 持久化（JSON 标签供前端直接消费）。
type DialogInfo struct {
	PeerID        int64       `json:"peer_id"`         // 对话对端 ID（频道/群组/用户 ID）
	AccessHash    int64       `json:"access_hash"`     // 访问哈希（频道/用户需要，群组为 0）
	Name          string      `json:"name"`            // 显示名称（频道/群组标题，或用户姓名）
	Type          DialogType  `json:"type"`            // 对话类型
	LastMessageID int64       `json:"last_message_id"` // 最新消息 ID（top_message）
	Username      string      `json:"username"`        // 频道/用户用户名（@xxx，可空）
	AvatarFileID  string      `json:"avatar_file_id"`  // 头像 file_id（暂存，不下载）
}

// dialogsFetchLimit 是单次 messages.getDialogs 请求的页面大小。
// Telegram 允许的最大值为 100，账号对话数超过该值时通过 OffsetID 分页拉取。
const dialogsFetchLimit = 100

// dialogsMaxPages 是分页拉取的安全上限，避免异常情况下死循环。
// 100 页 * 100/页 = 10000 个对话上限，覆盖正常账号规模。
const dialogsMaxPages = 100

// FetchDialogs 拉取当前账号已加入的所有对话（频道、群组、私聊）。
//
// 实现方式：调用 messages.getDialogs，按 OffsetID 分页拉取直至无更多数据。
// 解析返回的 *tg.MessagesDialogs / *tg.MessagesDialogsSlice：
//   - Chats 字段包含频道（*tg.Channel）与普通群组（*tg.Chat）
//   - Users 字段包含私聊用户（*tg.User）
//   - Dialogs 字段提供每个对话的 Peer 与 TopMessage
//
// 返回的对话列表按服务端返回顺序（通常按 TopMessage 倒序）。
func (m *ClientManager) FetchDialogs(ctx context.Context) ([]DialogInfo, error) {
	if !m.IsAuthenticated() {
		return nil, fmt.Errorf("未登录，请先完成登录流程")
	}
	client := m.GetClient()
	if client == nil {
		return nil, fmt.Errorf("客户端未启动")
	}
	api := client.API()

	var result []DialogInfo
	offsetID := 0

	for page := 0; page < dialogsMaxPages; page++ {
		// 构造请求：使用 OffsetID 实现分页，OffsetPeer 为空对端（起点）。
		req := &tg.MessagesGetDialogsRequest{
			OffsetPeer: &tg.InputPeerEmpty{},
			OffsetID:   offsetID,
			Limit:      dialogsFetchLimit,
		}

		resp, err := api.MessagesGetDialogs(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("拉取对话列表失败（第 %d 页）: %w", page+1, err)
		}

		// 解析本批次
		batch, minTopMsg, err := parseDialogsClass(resp)
		if err != nil {
			return nil, fmt.Errorf("解析对话列表失败: %w", err)
		}
		result = append(result, batch...)

		// 本批次数量小于 limit：已到末尾
		if len(batch) < dialogsFetchLimit {
			break
		}
		// 无法继续分页（没有有效的 TopMessage 用于 OffsetID）
		if minTopMsg <= 0 || minTopMsg >= offsetID {
			break
		}
		offsetID = minTopMsg
	}

	return result, nil
}

// parseDialogsClass 解析 messages.getDialogs 的返回结果。
// 返回本批次的 DialogInfo 列表，以及本批次中最小的 TopMessage（用于下一页 OffsetID）。
//
// 可能的返回类型：
//   - *tg.MessagesDialogs：完整结果
//   - *tg.MessagesDialogsSlice：分页结果（含 Count）
//   - *tg.MessagesDialogsNotModified：缓存未变化（无数据，返回空列表）
func parseDialogsClass(resp tg.MessagesDialogsClass) ([]DialogInfo, int, error) {
	switch r := resp.(type) {
	case *tg.MessagesDialogs:
		return parseDialogBatch(r.Dialogs, r.Chats, r.Users)
	case *tg.MessagesDialogsSlice:
		return parseDialogBatch(r.Dialogs, r.Chats, r.Users)
	case *tg.MessagesDialogsNotModified:
		// 服务端缓存命中，无数据返回
		return nil, 0, nil
	default:
		return nil, 0, fmt.Errorf("未预期的对话响应类型: %T", resp)
	}
}

// parseDialogBatch 从 Dialogs/Chats/Users 三个列表中组装 DialogInfo。
// 同时计算本批次中最小的 TopMessage（用于分页）。
func parseDialogBatch(dialogs []tg.DialogClass, chats []tg.ChatClass, users []tg.UserClass) ([]DialogInfo, int, error) {
	// 构造 channel/chat 索引（按 ID），便于后续根据 Peer 快速查找。
	channelMap := make(map[int64]*tg.Channel)
	chatMap := make(map[int64]*tg.Chat)
	for _, c := range chats {
		switch ch := c.(type) {
		case *tg.Channel:
			channelMap[ch.GetID()] = ch
		case *tg.Chat:
			chatMap[ch.GetID()] = ch
			// ChatForbidden / ChannelForbidden / ChatEmpty 不纳入可操作对话，
			// 直接跳过（前端不展示无法访问的对话）。
		}
	}

	// 构造用户索引（按 ID）。
	userMap := make(map[int64]*tg.User)
	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			userMap[user.GetID()] = user
		}
	}

	result := make([]DialogInfo, 0, len(dialogs))
	minTopMsg := 0

	for _, d := range dialogs {
		dialog, ok := d.(*tg.Dialog)
		if !ok || dialog == nil {
			continue
		}
		// Dialog.Peer 为 nil 时跳过
		if dialog.Peer == nil {
			continue
		}

		info, matched := buildDialogInfo(dialog, channelMap, chatMap, userMap)
		if !matched {
			continue
		}

		result = append(result, info)

		// 跟踪本批次最小 TopMessage（用于分页 OffsetID）
		if minTopMsg == 0 || dialog.TopMessage < minTopMsg {
			if dialog.TopMessage > 0 {
				minTopMsg = dialog.TopMessage
			}
		}
	}

	return result, minTopMsg, nil
}

// buildDialogInfo 根据 Dialog.Peer 的实际类型，从对应的索引中提取信息。
// matched=false 表示该对话类型暂不纳入（如 ChatEmpty）。
func buildDialogInfo(
	dialog *tg.Dialog,
	channelMap map[int64]*tg.Channel,
	chatMap map[int64]*tg.Chat,
	userMap map[int64]*tg.User,
) (DialogInfo, bool) {
	switch peer := dialog.Peer.(type) {
	case *tg.PeerChannel:
		ch, ok := channelMap[peer.GetChannelID()]
		if !ok {
			return DialogInfo{}, false
		}
		info := DialogInfo{
			PeerID:        ch.GetID(),
			Name:          ch.GetTitle(),
			Type:          DialogTypeChannel,
			LastMessageID: int64(dialog.TopMessage),
		}
		// AccessHash / Username 为可选字段，通过 Get* 辅助方法安全读取
		if ah, ok := ch.GetAccessHash(); ok {
			info.AccessHash = ah
		}
		if un, ok := ch.GetUsername(); ok {
			info.Username = un
		}
		return info, true

	case *tg.PeerChat:
		chat, ok := chatMap[peer.GetChatID()]
		if !ok {
			return DialogInfo{}, false
		}
		return DialogInfo{
			PeerID:        chat.GetID(),
			Name:          chat.GetTitle(),
			Type:          DialogTypeChat,
			LastMessageID: int64(dialog.TopMessage),
			// 普通群组没有 AccessHash 与 Username
		}, true

	case *tg.PeerUser:
		user, ok := userMap[peer.GetUserID()]
		if !ok {
			return DialogInfo{}, false
		}
		info := DialogInfo{
			PeerID:        user.GetID(),
			Type:          DialogTypeUser,
			LastMessageID: int64(dialog.TopMessage),
		}
		// 拼接用户姓名：FirstName + " " + LastName
		first, _ := user.GetFirstName()
		last, _ := user.GetLastName()
		if first == "" && last == "" {
			info.Name = fmt.Sprintf("用户#%d", user.GetID())
		} else if last == "" {
			info.Name = first
		} else if first == "" {
			info.Name = last
		} else {
			info.Name = first + " " + last
		}
		if ah, ok := user.GetAccessHash(); ok {
			info.AccessHash = ah
		}
		if un, ok := user.GetUsername(); ok {
			info.Username = un
		}
		return info, true

	default:
		// 未知 Peer 类型（如 PeerEmpty），跳过
		return DialogInfo{}, false
	}
}

// SaveDialogs 将对话列表写入 dialogs 表（按 peer_id 去重，UPSERT 语义）。
//
// 由于 dialogs 表未对 peer_id 添加 UNIQUE 约束（不修改 schema.go），
// 此处通过事务 + 查询判断实现 UPSERT：
//   - 若 peer_id 已存在：UPDATE 各字段（保留原 id 与 created_at）
//   - 若 peer_id 不存在：INSERT 新记录
//
// 全部记录写入同一个事务，失败时整体回滚。
func SaveDialogs(db *sql.DB, dialogs []DialogInfo) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	// 失败时回滚；成功提交后 Rollback 返回 sql.ErrTxDone，可忽略。
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Format(time.RFC3339)

	for _, d := range dialogs {
		var existingID int64
		err := tx.QueryRow(`SELECT id FROM dialogs WHERE peer_id = ?`, d.PeerID).Scan(&existingID)
		switch {
		case err == nil:
			// 已存在：UPDATE（保留 id、created_at；更新 access_hash、name、type、last_message_id、avatar、username、updated_at）
			_, err = tx.Exec(
				`UPDATE dialogs
				 SET access_hash = ?, name = ?, type = ?, last_message_id = ?, avatar = ?, username = ?, updated_at = ?
				 WHERE peer_id = ?`,
				d.AccessHash, d.Name, string(d.Type), d.LastMessageID, d.AvatarFileID, d.Username, now, d.PeerID,
			)
			if err != nil {
				return fmt.Errorf("更新对话 (peer_id=%d) 失败: %w", d.PeerID, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			// 不存在：INSERT
			_, err = tx.Exec(
				`INSERT INTO dialogs (peer_id, access_hash, name, type, last_message_id, avatar, username, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				d.PeerID, d.AccessHash, d.Name, string(d.Type), d.LastMessageID, d.AvatarFileID, d.Username, now, now,
			)
			if err != nil {
				return fmt.Errorf("插入对话 (peer_id=%d) 失败: %w", d.PeerID, err)
			}
		default:
			return fmt.Errorf("查询对话 (peer_id=%d) 失败: %w", d.PeerID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// LoadDialogs 从 SQLite 读取全部缓存的对话，按 last_message_id 倒序。
// SQLite 中 NULL 视为最小值，DESC 排序时 NULL 排在最后。
func LoadDialogs(db *sql.DB) ([]DialogInfo, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	rows, err := db.Query(
		`SELECT peer_id, IFNULL(access_hash, 0), IFNULL(name, ''), IFNULL(type, ''),
		        IFNULL(last_message_id, 0), IFNULL(avatar, ''), IFNULL(username, '')
		 FROM dialogs
		 ORDER BY last_message_id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("查询对话列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanDialogs(rows)
}

// LoadDialogByPeerID 按 peer_id 查询单个对话。
// 不存在时返回错误。
func LoadDialogByPeerID(db *sql.DB, peerID int64) (DialogInfo, error) {
	if db == nil {
		return DialogInfo{}, fmt.Errorf("数据库未初始化")
	}

	var (
		peerIDCol, accessHash, lastMsgID int64
		name, typeStr, avatar, username  string
	)
	// 由于 COALESCE 难以直接覆盖所有 NULL 列，此处使用 sql.Null* 不太便利，
	// 因此改用将 NULL 通过 IFNULL 转为空字符串/0 的方式。
	row := db.QueryRow(
		`SELECT peer_id, IFNULL(access_hash, 0), IFNULL(name, ''), IFNULL(type, ''),
		        IFNULL(last_message_id, 0), IFNULL(avatar, ''), IFNULL(username, '')
		 FROM dialogs WHERE peer_id = ?`,
		peerID,
	)
	if err := row.Scan(&peerIDCol, &accessHash, &name, &typeStr, &lastMsgID, &avatar, &username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DialogInfo{}, fmt.Errorf("未找到对话 (peer_id=%d)", peerID)
		}
		return DialogInfo{}, fmt.Errorf("查询对话失败: %w", err)
	}

	return DialogInfo{
		PeerID:        peerIDCol,
		AccessHash:    accessHash,
		Name:          name,
		Type:          DialogType(typeStr),
		LastMessageID: lastMsgID,
		AvatarFileID:  avatar,
		Username:      username,
	}, nil
}

// scanDialogs 从 sql.Rows 批量扫描出 DialogInfo 列表。
func scanDialogs(rows *sql.Rows) ([]DialogInfo, error) {
	var result []DialogInfo
	for rows.Next() {
		var (
			peerID, accessHash, lastMsgID int64
			name, typeStr, avatar, username string
		)
		if err := rows.Scan(&peerID, &accessHash, &name, &typeStr, &lastMsgID, &avatar, &username); err != nil {
			return nil, fmt.Errorf("扫描对话记录失败: %w", err)
		}
		result = append(result, DialogInfo{
			PeerID:        peerID,
			AccessHash:    accessHash,
			Name:          name,
			Type:          DialogType(typeStr),
			LastMessageID: lastMsgID,
			AvatarFileID:  avatar,
			Username:      username,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历对话记录失败: %w", err)
	}
	return result, nil
}

// SearchDialogsByName 从 SQLite 按 name 模糊搜索对话（不区分大小写）。
// keyword 为空时返回全部对话（与 LoadDialogs 等价）。
func SearchDialogsByName(db *sql.DB, keyword string) ([]DialogInfo, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	// 转义 SQL LIKE 中的特殊字符（%、_）以避免误匹配。
	// 实际匹配使用 ESCAPE '\' 子句。
	escaped := strings.ReplaceAll(keyword, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `%`, `\%`)
	escaped = strings.ReplaceAll(escaped, `_`, `\_`)
	pattern := "%" + escaped + "%"

	rows, err := db.Query(
		`SELECT peer_id, IFNULL(access_hash, 0), IFNULL(name, ''), IFNULL(type, ''),
		        IFNULL(last_message_id, 0), IFNULL(avatar, ''), IFNULL(username, '')
		 FROM dialogs
		 WHERE name LIKE ? ESCAPE '\'
		 ORDER BY last_message_id DESC`,
		pattern,
	)
	if err != nil {
		return nil, fmt.Errorf("搜索对话失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanDialogs(rows)
}

// RefreshDialogs 拉取最新对话列表并写入 SQLite，返回最新列表。
// 用于"刷新频道列表"功能：先调用 FetchDialogs，再调用 SaveDialogs 持久化。
func (m *ClientManager) RefreshDialogs(ctx context.Context) ([]DialogInfo, error) {
	dialogs, err := m.FetchDialogs(ctx)
	if err != nil {
		return nil, err
	}
	if err := SaveDialogs(m.db, dialogs); err != nil {
		return nil, fmt.Errorf("持久化对话列表失败: %w", err)
	}
	return dialogs, nil
}

// CheckDialogAccess 校验当前账号是否有权限访问指定频道/群组/用户的消息。
//
// 实现方式：调用 messages.getHistory 拉取 1 条消息，
// 若返回错误（如 CHANNEL_PRIVATE），视为无权限。
//
// 参数：
//   - peerID: 对端 ID
//   - accessHash: 访问哈希（频道/用户必填，群组可传 0）
//
// 返回 nil 表示有访问权限；返回错误描述具体原因。
func (m *ClientManager) CheckDialogAccess(ctx context.Context, peerID int64, accessHash int64) error {
	if !m.IsAuthenticated() {
		return fmt.Errorf("未登录，请先完成登录流程")
	}
	client := m.GetClient()
	if client == nil {
		return fmt.Errorf("客户端未启动")
	}

	// 由于调用方传入的是 peerID + accessHash，但不区分类型，
	// 此处尝试两种最常见的形式：先按频道（InputPeerChannel）尝试，
	// 失败再按用户（InputPeerUser）尝试。普通群组（InputPeerChat）不需要 access_hash。
	//
	// 注意：调用方应在已知类型时优先使用对应方法，本函数为通用兜底。
	peersToTry := []tg.InputPeerClass{
		&tg.InputPeerChannel{ChannelID: peerID, AccessHash: accessHash},
		&tg.InputPeerUser{UserID: peerID, AccessHash: accessHash},
		&tg.InputPeerChat{ChatID: peerID},
	}

	var lastErr error
	for _, peer := range peersToTry {
		_, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			Limit:    1,
			OffsetID: 0,
		})
		if err == nil {
			return nil
		}
		lastErr = err
		// CHANNEL_PRIVATE / USER_ID_INVALID 等错误表示此 peer 形式不适用，
		// 继续尝试下一种形式。
	}

	// 所有形式均失败
	if lastErr != nil {
		return fmt.Errorf("无权限访问该频道: %w", lastErr)
	}
	return nil
}
