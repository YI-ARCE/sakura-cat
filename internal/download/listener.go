// Package download 提供文件下载的任务调度、扫描与持久化能力。
// 本文件实现实时监听服务（Listener），订阅指定对话的新消息事件，
// 按媒体类型与关键词筛选后自动触发下载。
//
// Listener 实现 gotd/td 的 telegram.UpdateHandler 接口（Handle 方法），
// 通过 ClientManager.SetUpdateHandler 注册到客户端，接收实时更新。
package download

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"tg-download/internal/telegram"

	"github.com/gotd/td/tg"
)

// Listener 实时监听服务，订阅指定对话的新消息并自动触发下载。
//
// 工作流程：
//  1. Start 时从 SQLite 加载所有 enabled 的订阅到内存 map
//  2. 注册自身为 ClientManager 的 UpdateHandler
//  3. 客户端收到新消息更新时调用 Handle 方法
//  4. Handle 提取新消息，检查所属对话是否在订阅列表中
//  5. 若订阅则按 MediaTypes + Keywords 筛选，匹配成功则创建下载记录并提交下载
type Listener struct {
	mu        sync.RWMutex
	db        *sql.DB
	manager   *telegram.ClientManager
	scheduler *Scheduler
	emitter   EventEmitter
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	subs      map[int64]*Subscription // dialogPeerID -> 活跃订阅
}

// NewListener 创建一个新的 Listener 实例。
// db、manager、scheduler、emitter 均需已初始化。
func NewListener(db *sql.DB, manager *telegram.ClientManager, scheduler *Scheduler, emitter EventEmitter) *Listener {
	return &Listener{
		db:        db,
		manager:   manager,
		scheduler: scheduler,
		emitter:   emitter,
		subs:      make(map[int64]*Subscription),
	}
}

// Start 启动监听服务。
//
// 流程：
//  1. 校验客户端已登录
//  2. 从 SQLite 加载所有 enabled 的订阅到内存 map
//  3. 注册自身为 ClientManager 的 UpdateHandler
//  4. 标记 running=true
//
// 若监听已在运行则直接返回 nil。
// 若客户端未登录则返回错误。
func (l *Listener) Start(ctx context.Context) error {
	if l.manager == nil {
		return fmt.Errorf("客户端管理器未初始化")
	}
	if !l.manager.IsAuthenticated() {
		return fmt.Errorf("未登录，请先完成登录流程")
	}

	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	// 加载所有 enabled 的订阅
	subs, err := ListEnabledSubscriptions(l.db)
	if err != nil {
		return fmt.Errorf("加载订阅列表失败: %w", err)
	}

	l.mu.Lock()
	// 重置 subs map
	l.subs = make(map[int64]*Subscription)
	for i := range subs {
		s := subs[i]
		l.subs[s.DialogPeerID] = &s
	}
	l.ctx, l.cancel = context.WithCancel(ctx)
	l.running = true
	l.mu.Unlock()

	// 注册为 ClientManager 的 UpdateHandler
	// ClientManager 在创建客户端时通过动态包装函数转发更新，
	// 因此即使客户端已在运行，注册后也会立即生效
	l.manager.SetUpdateHandler(l)

	return nil
}

// Handle 实现 telegram.UpdateHandler 接口。
// gotd/td 客户端收到更新时调用此方法。
//
// 处理的更新类型：
//   - *tg.Updates / *tg.UpdatesCombined：包含多个 Update
//   - *tg.UpdateShort：单个 Update
//
// 对每个 Update，检查是否为 *tg.UpdateNewMessage 或 *tg.UpdateNewChannelMessage，
// 提取消息并检查所属对话是否在订阅列表中。
// 单条消息处理失败不影响其他消息，最终返回 nil。
func (l *Listener) Handle(ctx context.Context, u tg.UpdatesClass) error {
	if !l.IsRunning() {
		return nil
	}

	// 提取所有 UpdateClass
	var updates []tg.UpdateClass
	switch r := u.(type) {
	case *tg.Updates:
		updates = r.Updates
	case *tg.UpdatesCombined:
		updates = r.Updates
	case *tg.UpdateShort:
		updates = []tg.UpdateClass{r.Update}
	default:
		// 其他类型（UpdateShortMessage 等）不含可下载媒体，忽略
		return nil
	}

	// 遍历所有更新
	for _, update := range updates {
		l.processUpdate(ctx, update)
	}
	return nil
}

// processUpdate 处理单个更新，提取新消息并匹配订阅。
func (l *Listener) processUpdate(ctx context.Context, update tg.UpdateClass) {
	var msg *tg.Message

	switch u := update.(type) {
	case *tg.UpdateNewMessage:
		msg = messageFromClass(u.Message)
	case *tg.UpdateNewChannelMessage:
		msg = messageFromClass(u.Message)
	default:
		return
	}

	if msg == nil {
		return
	}

	// 提取消息所属对话的 peer ID
	peerID, ok := extractPeerID(msg)
	if !ok {
		return
	}

	// 检查该对话是否在订阅列表中
	l.mu.RLock()
	sub, exists := l.subs[peerID]
	l.mu.RUnlock()
	if !exists {
		return
	}

	// 处理新消息（按订阅条件筛选并触发下载）
	l.processNewMessage(ctx, msg, *sub)
}

// messageFromClass 从 MessageClass 中提取 *tg.Message。
// 跳过 MessageService 和 MessageEmpty。
func messageFromClass(mc tg.MessageClass) *tg.Message {
	if mc == nil {
		return nil
	}
	msg, ok := mc.(*tg.Message)
	if !ok {
		return nil
	}
	return msg
}

// extractPeerID 从消息的 PeerID 字段提取对话对端 ID。
// 返回 (peerID, ok)：ok=false 表示无法提取。
func extractPeerID(msg *tg.Message) (int64, bool) {
	peer := msg.GetPeerID()
	if peer == nil {
		return 0, false
	}
	switch p := peer.(type) {
	case *tg.PeerChannel:
		return p.ChannelID, true
	case *tg.PeerChat:
		return p.ChatID, true
	case *tg.PeerUser:
		return p.UserID, true
	default:
		return 0, false
	}
}

// processNewMessage 处理新消息：按订阅条件筛选媒体文件，匹配成功则创建下载记录并提交下载。
//
// 流程：
//  1. 提取消息媒体信息（复用 scanner.go 的 extractMediaInfo）
//  2. 按订阅的 MediaTypes 筛选
//  3. 按订阅的 Keywords 筛选
//  4. 去重：检查该文件是否已存在下载记录
//  5. 匹配成功：推送 EventScanMatched 事件，创建 DownloadRecord（status=pending），
//     在后台 goroutine 中调用 Scheduler.DownloadSingleFile 执行下载
func (l *Listener) processNewMessage(ctx context.Context, msg *tg.Message, sub Subscription) {
	// 提取消息媒体
	media, ok := msg.GetMedia()
	if !ok || media == nil {
		return
	}

	// 解析媒体类型与文件信息
	mediaType, fileID, fileName, fileSize, _, _, _, ok := extractMediaInfo(media)
	if !ok {
		// 不支持的媒体类型，跳过
		return
	}

	// 媒体类型筛选：若配置了 MediaTypes，必须匹配其中之一
	if len(sub.MediaTypes) > 0 {
		if !containsString(sub.MediaTypes, mediaType) {
			return
		}
	}

	// 关键词筛选：若配置了 Keywords，消息文本必须包含其中之一
	if len(sub.Keywords) > 0 {
		text := msg.GetMessage()
		if !matchAnyKeyword(text, sub.Keywords) {
			return
		}
	}

	// 去重：检查该文件是否已存在下载记录（避免重复下载）
	existing, err := GetRecordByMessageAndFile(l.db, int64(msg.ID), fileID)
	if err == nil && existing.ID > 0 {
		// 已存在记录，跳过
		return
	}

	// 消息日期格式化
	dateStr := time.Unix(int64(msg.GetDate()), 0).Format(time.RFC3339)

	// 推送匹配事件给前端
	matched := MatchedFile{
		MessageID: int64(msg.ID),
		FileID:    fileID,
		FileName:  fileName,
		FileSize:  fileSize,
		MediaType: mediaType,
		Date:      dateStr,
	}
	l.emit(EventScanMatched, matched)

	// 创建下载记录（status=pending，TaskID=0 表示监听下载，无关联任务）
	record := DownloadRecord{
		TaskID:    0,
		MessageID: int64(msg.ID),
		FileID:    fileID,
		FileName:  fileName,
		FileSize:  fileSize,
		MediaType: mediaType,
		Status:    "pending",
	}
	recordID, err := CreateRecord(l.db, record)
	if err != nil {
		// 创建记录失败不影响后续消息处理
		return
	}
	record.ID = recordID

	// 构造 TaskConfig（从订阅转换）
	cfg := TaskConfig{
		DialogPeerID:     sub.DialogPeerID,
		DialogAccessHash: sub.DialogAccessHash,
		DialogName:       sub.DialogName,
		VideoPrefix:      sub.VideoPrefix,
		NamingTemplate:   sub.NamingTemplate,
	}

	// 在后台 goroutine 中执行下载
	go func() {
		dlCtx, cancel := context.WithCancel(l.ctx)
		defer cancel()
		_ = l.scheduler.DownloadSingleFile(dlCtx, record, cfg)
	}()
}

// Stop 停止监听服务。
// 取消 UpdateHandler 注册并停止处理新消息。
// 正在进行的下载不受影响（由 Scheduler 管理）。
func (l *Listener) Stop() {
	l.mu.Lock()
	l.running = false
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.ctx = nil
	l.mu.Unlock()

	// 取消 UpdateHandler 注册
	if l.manager != nil {
		l.manager.SetUpdateHandler(nil)
	}
}

// IsRunning 返回监听服务是否正在运行。
func (l *Listener) IsRunning() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.running
}

// ReloadSubscriptions 从 SQLite 重新加载订阅列表。
// 用于订阅变更后刷新内存中的订阅 map，无需重启监听。
func (l *Listener) ReloadSubscriptions() error {
	subs, err := ListEnabledSubscriptions(l.db)
	if err != nil {
		return fmt.Errorf("加载订阅列表失败: %w", err)
	}

	l.mu.Lock()
	l.subs = make(map[int64]*Subscription)
	for i := range subs {
		s := subs[i]
		l.subs[s.DialogPeerID] = &s
	}
	l.mu.Unlock()
	return nil
}

// emit 安全推送事件：emitter 为 nil 或 Emit 失败时静默忽略。
func (l *Listener) emit(eventName string, data interface{}) {
	if l.emitter == nil {
		return
	}
	_ = l.emitter.Emit(eventName, data)
}
