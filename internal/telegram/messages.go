// Package telegram 提供 Telegram 客户端封装、代理配置与持久化能力。
// 本文件实现频道消息搜索能力，独立封装 messages.search API，
// 用于频道收录页面的关键字消息搜索（不创建任务、不创建下载记录、不更新 scan_offset）。
package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// RecentMessage 表示一条频道消息的预览数据。
// 本结构与 download.RecentMessage 字段完全相同（json tag 一致），
// 因 telegram 包不能反向导入 download 包（download 已导入 telegram，会形成循环导入），
// 故在此本地定义；ApiService 层会做类型转换。
type RecentMessage struct {
	// MessageID 消息 ID
	MessageID int64 `json:"message_id"`
	// MediaType 媒体类型：video/audio/photo/document/none
	// none 表示该消息无媒体文件（纯文本消息）
	MediaType string `json:"media_type"`
	// FileName 文件名（来自 DocumentAttributeFilename，无媒体或无文件名时为空）
	FileName string `json:"file_name"`
	// FileSize 文件大小（字节，无媒体时为 0）
	FileSize int64 `json:"file_size"`
	// MessageText 消息文本内容（含 caption）
	MessageText string `json:"message_text"`
	// Date 消息日期（RFC3339）
	Date string `json:"date"`
	// HasMedia 是否含媒体文件
	HasMedia bool `json:"has_media"`
}

// searchMaxLimit 是单次 messages.search 请求的最大条数上限。
// 超过该值会强制截断，避免单页过大触发限流。
const searchMaxLimit = 100

// searchFloodWaitMaxRetries 是单个 MessagesSearch 请求因 FLOOD_WAIT 重试的最大次数。
const searchFloodWaitMaxRetries = 5

// searchFloodWaitMaxTotalWait 单个请求因 FLOOD_WAIT 累计等待的最大秒数。
const searchFloodWaitMaxTotalWait = 300 // 秒

// SearchChannelMessages 在指定频道内按关键字搜索消息，独立封装 Telegram messages.search API。
//
// 用于频道收录页面：用户输入关键字后，前端按分页调用本方法拉取匹配消息，
// 仅返回消息预览数据，不复用 scanner 任务流程，不创建任务、不创建下载记录、不更新 scan_offset。
//
// 流程：
//  1. 预处理 keyword（去空校验）与 limit（边界处理）
//  2. 校验登录状态与客户端
//  3. 构造 InputPeerChannel 与 MessagesSearchRequest 调用 messages.search
//  4. 遇 FLOOD_WAIT 自动等待重试（最多 5 次，累计不超过 300 秒）
//  5. 解析响应并逐条转换为 RecentMessage
//  6. 取最后一条消息的 ID 作为下一页 OffsetID
//
// 参数：
//   - peerID: 频道 ID
//   - accessHash: 频道访问哈希
//   - keyword: 搜索关键字（去空后不能为空）
//   - offsetID: 分页偏移消息 ID（0 表示从最新开始）
//   - limit: 单页条数（≤0 或 >50 强制设为 50）
//
// 返回值：
//   - []RecentMessage: 当前页消息列表（按 date 倒序）
//   - int64: 下一页的 OffsetID（最后一条消息的 ID，0 表示无更多）
//   - error: 错误信息
func (m *ClientManager) SearchChannelMessages(ctx context.Context, peerID int64, accessHash int64, keyword string, offsetID int64, limit int) ([]RecentMessage, int64, error) {
	// keyword 预处理：去除首尾空白
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, 0, fmt.Errorf("keyword 不能为空")
	}

	// limit 边界处理
	if limit <= 0 || limit > searchMaxLimit {
		limit = searchMaxLimit
	}

	// 未登录校验
	if !m.IsAuthenticated() {
		return nil, 0, fmt.Errorf("未登录，请先完成登录流程")
	}
	client := m.GetClient()
	if client == nil {
		return nil, 0, fmt.Errorf("客户端未启动")
	}

	// 构造请求
	peer := &tg.InputPeerChannel{
		ChannelID:  peerID,
		AccessHash: accessHash,
	}
	req := &tg.MessagesSearchRequest{
		Peer:     peer,
		Q:        keyword,
		Filter:   &tg.InputMessagesFilterEmpty{},
		// gotd 的 MessagesSearchRequest.OffsetID 为 int 类型，需从 int64 转换
		OffsetID: int(offsetID),
		Limit:    limit,
	}

	// 调用 messages.search（带 FLOOD_WAIT 重试）
	resp, err := searchWithRetry(ctx, client.API(), req)
	if err != nil {
		return nil, 0, fmt.Errorf("搜索频道消息失败: %w", err)
	}

	// 解析响应
	messages, err := extractSearchMessages(resp)
	if err != nil {
		return nil, 0, fmt.Errorf("解析搜索结果失败: %w", err)
	}

	// 逐条转换为 RecentMessage
	result := make([]RecentMessage, 0, len(messages))
	for _, mc := range messages {
		msg, ok := mc.(*tg.Message)
		if !ok || msg == nil {
			continue
		}
		result = append(result, buildRecentMessage(msg))
	}

	// 计算下一页 OffsetID：取最后一条（最早一条）的 MessageID
	// 列表为空时 nextOffsetID=0，表示无更多数据
	var nextOffsetID int64
	if len(result) > 0 {
		nextOffsetID = result[len(result)-1].MessageID
	}

	return result, nextOffsetID, nil
}

// searchWithRetry 调用 MessagesSearch，遇到 FLOOD_WAIT 自动等待后重试。
// 参考 internal/download/scanner.go 中 fetchSearchWithRetry 的实现模式。
//
// 参数：
//   - ctx: 上下文（等待期间支持取消）
//   - api: Telegram 客户端 API
//   - req: MessagesSearchRequest 请求（含 Peer / Q / Filter / OffsetID 等）
func searchWithRetry(ctx context.Context, api *tg.Client, req *tg.MessagesSearchRequest) (tg.MessagesMessagesClass, error) {
	var totalWaited time.Duration
	for attempt := 0; attempt <= searchFloodWaitMaxRetries; attempt++ {
		resp, err := api.MessagesSearch(ctx, req)
		if err == nil {
			return resp, nil
		}
		waitDur, ok := tgerr.AsFloodWait(err)
		if !ok {
			return nil, err
		}
		waitSec := int(waitDur.Seconds())
		if waitSec <= 0 {
			waitSec = 1
		}
		waitSec = waitSec + 1
		if totalWaited+time.Duration(waitSec)*time.Second > time.Duration(searchFloodWaitMaxTotalWait)*time.Second {
			return nil, fmt.Errorf("FLOOD_WAIT 等待超限（累计 %v，本次需 %ds）: %w",
				totalWaited, waitSec, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(waitSec) * time.Second):
		}
		totalWaited += time.Duration(waitSec) * time.Second
	}
	return nil, fmt.Errorf("FLOOD_WAIT 重试 %d 次后仍未成功", searchFloodWaitMaxRetries+1)
}

// extractSearchMessages 从 MessagesSearch 响应中提取消息列表。
//
// 支持的响应类型：
//   - *tg.MessagesMessages：普通对话完整结果
//   - *tg.MessagesMessagesSlice：分页结果（含 Count）
//   - *tg.MessagesChannelMessages：频道消息（含 Count）
//   - *tg.MessagesMessagesNotModified：缓存未变化，返回空列表
func extractSearchMessages(resp tg.MessagesMessagesClass) ([]tg.MessageClass, error) {
	switch r := resp.(type) {
	case *tg.MessagesMessages:
		return r.Messages, nil
	case *tg.MessagesMessagesSlice:
		return r.Messages, nil
	case *tg.MessagesChannelMessages:
		return r.Messages, nil
	case *tg.MessagesMessagesNotModified:
		// 服务端缓存命中，无数据返回
		return nil, nil
	default:
		return nil, fmt.Errorf("未预期的消息响应类型: %T", resp)
	}
}

// buildRecentMessage 将单条 tg.Message 转换为 RecentMessage。
//
// 提取字段：
//   - MessageID: msg.GetID()
//   - MediaType: 根据 msg.Media 类型判断（video/audio/photo/document/none）
//   - FileName: 从 DocumentAttributeFilename 获取
//   - FileSize: document.Size
//   - MessageText: msg.GetMessage()（含 caption）
//   - Date: time.Unix(msg.GetDate(), 0).Format(time.RFC3339)
//   - HasMedia: 是否含可识别的媒体文件
func buildRecentMessage(msg *tg.Message) RecentMessage {
	rm := RecentMessage{
		MessageID:   int64(msg.ID),
		MessageText: msg.GetMessage(),
		Date:        time.Unix(int64(msg.GetDate()), 0).Format(time.RFC3339),
	}

	// 提取媒体信息（若有）
	if media, ok := msg.GetMedia(); ok && media != nil {
		mediaType, fileName, fileSize, mediaOK := extractSearchMediaInfo(media)
		if mediaOK {
			rm.HasMedia = true
			rm.MediaType = mediaType
			rm.FileName = fileName
			rm.FileSize = fileSize
		}
	}

	if !rm.HasMedia {
		rm.MediaType = "none"
	}

	return rm
}

// extractSearchMediaInfo 从 MessageMediaClass 中提取媒体类型、文件名与文件大小。
// 参考 internal/download/scanner.go 中 extractMediaInfo 的实现模式。
//
// 返回值：
//   - mediaType: video/audio/photo/document
//   - fileName: 文件名（来自 DocumentAttributeFilename，可能为空）
//   - fileSize: 文件大小（字节）
//   - ok: 是否成功提取（不支持的媒体类型返回 false）
func extractSearchMediaInfo(media tg.MessageMediaClass) (mediaType, fileName string, fileSize int64, ok bool) {
	switch m := media.(type) {
	case *tg.MessageMediaDocument:
		if m.Document == nil {
			return
		}
		doc, isDoc := m.Document.(*tg.Document)
		if !isDoc {
			// DocumentEmpty 等情况
			return
		}
		// 根据 MimeType 判断具体类型
		mt := "document"
		if strings.HasPrefix(doc.MimeType, "video/") {
			mt = "video"
		} else if strings.HasPrefix(doc.MimeType, "audio/") {
			mt = "audio"
		}
		// 提取文件名（从 Attributes 中的 DocumentAttributeFilename）
		fname := extractSearchDocumentFileName(doc)
		return mt, fname, doc.Size, true

	case *tg.MessageMediaPhoto:
		if m.Photo == nil {
			return
		}
		photo, isPhoto := m.Photo.(*tg.Photo)
		if !isPhoto {
			return
		}
		// 计算最大尺寸的文件大小（用于显示）
		var size int64
		if _, largestSize, hasSize := pickLargestPhotoSize(photo.Sizes); hasSize {
			size = int64(largestSize)
		}
		// 照片默认文件名
		fname := fmt.Sprintf("photo_%d.jpg", photo.ID)
		return "photo", fname, size, true

	default:
		// MessageMediaWebPage / MessageMediaContact 等不含可下载文件
		return
	}
}

// extractSearchDocumentFileName 从 Document.Attributes 中提取文件名。
// 遍历 Attributes 寻找 *tg.DocumentAttributeFilename，返回其 FileName 字段。
// 若不存在则返回空字符串。
func extractSearchDocumentFileName(doc *tg.Document) string {
	if doc == nil || doc.Attributes == nil {
		return ""
	}
	for _, attr := range doc.Attributes {
		if fn, ok := attr.(*tg.DocumentAttributeFilename); ok {
			return fn.FileName
		}
	}
	return ""
}

// pickLargestPhotoSize 从 Photo.Sizes 中选择文件尺寸最大的一个。
// 返回 (type, size, ok)：ok=false 表示无可用尺寸。
func pickLargestPhotoSize(sizes []tg.PhotoSizeClass) (string, int, bool) {
	var bestType string
	var bestSize int
	var found bool
	for _, s := range sizes {
		switch v := s.(type) {
		case *tg.PhotoSize:
			if v.Size > bestSize {
				bestSize = v.Size
				bestType = v.Type
				found = true
			}
		case *tg.PhotoSizeProgressive:
			// 取最大尺寸（数组最后一个）
			if len(v.Sizes) > 0 {
				last := v.Sizes[len(v.Sizes)-1]
				if last > bestSize {
					bestSize = last
					bestType = v.Type
					found = true
				}
			}
		case *tg.PhotoCachedSize:
			// PhotoCachedSize 没有 Size 字段，使用 Bytes 长度作为估算
			size := len(v.Bytes)
			if size > bestSize {
				bestSize = size
				bestType = v.Type
				found = true
			}
		}
	}
	return bestType, bestSize, found
}
