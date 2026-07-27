// Package download 提供文件下载的任务调度、扫描与持久化能力。
// 本文件实现扫描阶段：遍历频道历史消息，按媒体类型与关键词筛选匹配文件，
// 创建下载记录并推送事件给前端。
package download

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tg-download/internal/telegram"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// EventEmitter 定义事件推送接口，用于将扫描与下载事件转发给前端。
// 实际实现可由 Wails app 注入（封装 application.Event.Emit）。
type EventEmitter interface {
	// Emit 推送一个事件，eventName 为事件名，data 为事件数据。
	Emit(eventName string, data interface{}) error
}

// 事件名常量。
const (
	// EventScanMatched 扫描到匹配文件时推送，data 为 MatchedFile
	EventScanMatched = "scan:matched"
	// EventScanComplete 扫描完成时推送，data 为 Task
	EventScanComplete = "scan:complete"
	// EventScanProgress 扫描进度推送，data 为 ScanProgress。
	// 每扫描完一个分页批次（默认 100 条）推送一次，前端据此展示当前进度与总数。
	EventScanProgress = "scan:progress"
	// EventScanPreviewMatched 测试匹配阶段推送匹配文件，data 为 MatchedFile
	// 与 EventScanMatched 区分：测试模式不创建下载记录
	EventScanPreviewMatched = "scan:preview:matched"
	// EventScanPreviewProgress 测试匹配进度推送，data 为 PreviewProgress
	EventScanPreviewProgress = "scan:preview:progress"
	// EventTaskProgress 下载进度更新时推送，data 为 Task
	EventTaskProgress = "task:progress"
	// EventTaskStatus 任务状态变更时推送，data 为 Task
	EventTaskStatus = "task:status"
	// EventFileStatus 单文件状态变更时推送，data 为 DownloadRecord
	EventFileStatus = "file:status"
	// EventFileProgress 单文件下载进度推送，data 为 FileProgress。
	// 下载过程中每 500ms 推送一次，前端据此显示进度条与速度。
	EventFileProgress = "file:progress"
)

// scanPageLimit 是单次 MessagesGetHistory 请求的页面大小（最大 100）。
const scanPageLimit = 100

// Scanner 负责扫描频道历史消息，匹配待下载文件。
type Scanner struct {
	// manager 复用 telegram 客户端管理器
	manager *telegram.ClientManager
	// db SQLite 数据库句柄
	db *sql.DB
	// emitter 事件推送器（可为 nil，表示不推送事件）
	emitter EventEmitter
}

// NewScanner 创建一个新的 Scanner 实例。
func NewScanner(manager *telegram.ClientManager, db *sql.DB, emitter EventEmitter) *Scanner {
	return &Scanner{
		manager: manager,
		db:      db,
		emitter: emitter,
	}
}

// Scan 扫描指定任务对应的频道历史消息，匹配文件并创建下载记录。
//
// 流程：
//  1. 校验客户端登录态，构建 InputPeer
//  2. 确定起始 OffsetID（task.ScanOffset 或 GetDialogScanOffset 的历史值，0 表示从最新开始）
//  3. 分页遍历 MessagesGetHistory（每页 100 条），按 MediaTypes + Keywords 筛选
//  4. 匹配文件去重（GetRecordByMessageAndFile），创建 pending 下载记录，推送 EventScanMatched
//  5. 每页更新 task.scan_offset（最新已扫描位置）与 total_files
//  6. 扫描完成后更新任务状态：AutoDownload=true → downloading，否则 → pending
//  7. 推送 EventScanComplete
func (s *Scanner) Scan(ctx context.Context, task Task) error {
	log.Printf("[scanner] task #%d 开始扫描 (channel=%s, peer_id=%d, scan_from_newest=%v, episode_regex=%q, keywords=%v)",
		task.ID, task.Config.DialogName, task.Config.DialogPeerID, task.Config.ScanFromNewest, task.Config.EpisodeRegex, task.Config.Keywords)

	if s.manager == nil {
		s.emitScanError(task.ID, "客户端管理器未初始化")
		return fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		s.emitScanError(task.ID, "未登录，请先完成登录流程")
		return fmt.Errorf("未登录，请先完成登录流程")
	}
	client := s.manager.GetClient()
	if client == nil {
		s.emitScanError(task.ID, "客户端未启动")
		return fmt.Errorf("客户端未启动")
	}

	// 查找 DialogInfo 以获取对话类型，用于构建 InputPeer
	dialog, err := telegram.LoadDialogByPeerID(s.db, task.Config.DialogPeerID)
	if err != nil {
		s.emitScanError(task.ID, fmt.Sprintf("查询对话信息失败: %v", err))
		log.Printf("[scanner] task #%d 查询对话信息失败: %v", task.ID, err)
		return fmt.Errorf("查询对话信息失败: %w", err)
	}

	// 构建 InputPeer
	peer := buildInputPeer(dialog)
	if peer == nil {
		s.emitScanError(task.ID, fmt.Sprintf("无法构建 InputPeer（对话类型: %s）", dialog.Type))
		log.Printf("[scanner] task #%d 无法构建 InputPeer（对话类型: %s）", task.ID, dialog.Type)
		return fmt.Errorf("无法构建 InputPeer（对话类型: %s）", dialog.Type)
	}

	// 确定起始 OffsetID：
	//   - ScanFromNewest=true：强制从最新消息开始（offsetID=0，忽略历史 scan_offset）
	//   - ScanFromNewest=false（默认）：优先用 task.ScanOffset，其次查询历史扫描位置，最后 0
	offsetID := int(task.ScanOffset)
	if task.Config.ScanFromNewest {
		offsetID = 0
	} else if offsetID == 0 {
		// 查询该频道之前任务的最新扫描位置，用于增量扫描
		prevOffset, err := GetDialogScanOffset(s.db, task.Config.DialogPeerID)
		if err != nil {
			s.emitScanError(task.ID, fmt.Sprintf("查询历史扫描位置失败: %v", err))
			return fmt.Errorf("查询历史扫描位置失败: %w", err)
		}
		offsetID = int(prevOffset)
	}

	// 预编译集数正则（若配置）以便在扫描循环中复用
	var episodeRegex *regexp.Regexp
	if regexStr := strings.TrimSpace(task.Config.EpisodeRegex); regexStr != "" {
		compiled, err := regexp.Compile(regexStr)
		if err == nil {
			episodeRegex = compiled
		}
		// 编译失败时 episodeRegex 为 nil，processMessage 内部会再次尝试编译并跳过
	}
	// stopAtFirstEffective 是否启用"遇到集数1即停止"
	stopAtFirstEffective := task.Config.StopAtFirstEpisode &&
		task.Config.ScanFromNewest &&
		episodeRegex != nil

	api := client.API()
	matchedCount := 0
	scannedTotal := 0       // 累计扫描消息数（含未匹配）
	estimatedTotal := 0     // Telegram 报告的频道总消息数（0 表示未知）
	stopReason := "completed"
	scanError := ""         // 错误信息（stopReason=error 时填充）

	// 延迟函数：扫描结束时推送 Done=true 的最终进度（含错误信息）
	defer func() {
		s.emit(EventScanProgress, ScanProgress{
			TaskID:         task.ID,
			Page:           0,
			ScannedTotal:   scannedTotal,
			MatchedCount:   matchedCount,
			EstimatedTotal: estimatedTotal,
			Done:           true,
			StopReason:     stopReason,
			Error:          scanError,
		})
	}()

	// 分页遍历历史消息（无页数上限，依靠空响应/limit不足/minMsgID不递减等自然终止条件）
	page := 0
	for {
		// 检查上下文是否取消
		if err := ctx.Err(); err != nil {
			stopReason = "canceled"
			scanError = fmt.Sprintf("扫描被取消: %v", err)
			return fmt.Errorf("扫描被取消: %w", err)
		}

		// 根据 SearchTerm 选择 API：
		//   - SearchTerm 非空：使用 messages.search（服务器端搜索预过滤，大幅减少返回消息数）
		//   - SearchTerm 为空：使用 messages.getHistory（全量历史扫描）
		var resp tg.MessagesMessagesClass
		if strings.TrimSpace(task.Config.SearchTerm) != "" {
			searchReq := &tg.MessagesSearchRequest{
				Peer:     peer,
				Q:        strings.TrimSpace(task.Config.SearchTerm),
				Filter:   &tg.InputMessagesFilterEmpty{},
				OffsetID: offsetID,
				Limit:    scanPageLimit,
			}
			resp, err = s.fetchSearchWithRetry(ctx, api, searchReq, task.ID, page+1)
		} else {
			histReq := &tg.MessagesGetHistoryRequest{
				Peer:     peer,
				OffsetID: offsetID,
				Limit:    scanPageLimit,
			}
			resp, err = s.fetchHistoryWithRetry(ctx, api, histReq, task.ID, page+1)
		}
		if err != nil {
			stopReason = "error"
			scanError = fmt.Sprintf("拉取消息失败（第 %d 页）: %v", page+1, err)
			log.Printf("[scanner] task #%d 拉取消息失败（第 %d 页，offsetID=%d, has_search=%v）: %v",
				task.ID, page+1, offsetID, task.Config.SearchTerm != "", err)
			return fmt.Errorf("拉取消息失败（第 %d 页）: %w", page+1, err)
		}

		// 解析响应中的消息列表与总数
		messages, count, err := extractMessagesAndCount(resp)
		if err != nil {
			stopReason = "error"
			scanError = fmt.Sprintf("解析历史消息失败: %v", err)
			log.Printf("[scanner] task #%d 解析历史消息失败: %v", task.ID, err)
			return fmt.Errorf("解析历史消息失败: %w", err)
		}
		// 记录 Telegram 报告的总数（取最大值，避免某些页返回 0）
		if count > estimatedTotal {
			estimatedTotal = count
		}

		// 没有消息：已到末尾
		if len(messages) == 0 {
			break
		}

		// 处理本批次消息
		minMsgID := 0
		stopScan := false
		for _, mc := range messages {
			msg, ok := mc.(*tg.Message)
			if !ok || msg == nil {
				// 跳过 MessageService / MessageEmpty
				continue
			}
			scannedTotal++
			// 跟踪本批次最小消息 ID（用于下一页 OffsetID 与 scan_offset）
			if minMsgID == 0 || msg.ID < minMsgID {
				if msg.ID > 0 {
					minMsgID = msg.ID
				}
			}

			// 提取并匹配媒体文件
			matched, episodeNum, err := s.processMessage(ctx, task, msg)
			if err != nil {
				// 单条消息处理失败不中断整体扫描，仅记录
				continue
			}
			if matched {
				matchedCount++
				// 检查"遇到集数1即停止"条件
				if stopAtFirstEffective && episodeNum != nil && *episodeNum == 1 {
					stopScan = true
					stopReason = "episode_one"
				}
				// 检查"最大匹配数量"条件
				if task.Config.MaxMatches > 0 && matchedCount >= task.Config.MaxMatches {
					stopScan = true
					stopReason = "max_matches"
				}
				if stopScan {
					break
				}
			}
		}

		// 每页推送一次进度事件（Done=false，仅更新数字）
		s.emit(EventScanProgress, ScanProgress{
			TaskID:         task.ID,
			Page:           page + 1,
			ScannedTotal:   scannedTotal,
			MatchedCount:   matchedCount,
			EstimatedTotal: estimatedTotal,
			Done:           false,
		})

		if stopScan {
			break
		}

		// 本批次数量小于 limit：已到末尾
		if len(messages) < scanPageLimit {
			break
		}
		// 无法继续分页：当前批次无有效消息 ID
		// 注意：不能判断 minMsgID >= offsetID，因为 ScanFromNewest 时初始 offsetID=0，
		// 第一页的 minMsgID 必然大于 0，会误判为"没有更旧的消息"。
		// 真正的分页结束信号是"下一页返回空"或"本批次 < limit"，上面已经处理。
		if minMsgID <= 0 {
			break
		}
		// 增量扫描场景下（offsetID > 0），如果当前页 minMsgID 没有变得更小，说明没有更旧消息
		if offsetID > 0 && minMsgID >= offsetID {
			break
		}
		// 更新 OffsetID 为本批次最小消息 ID，继续向更旧方向翻页
		offsetID = minMsgID

		// 持久化扫描位置（每页更新一次，便于中断后恢复）
		if err := UpdateTaskScanOffset(s.db, task.ID, int64(minMsgID)); err != nil {
			stopReason = "error"
			scanError = fmt.Sprintf("更新扫描位置失败: %v", err)
			log.Printf("[scanner] task #%d 更新扫描位置失败: %v", task.ID, err)
			return fmt.Errorf("更新扫描位置失败: %w", err)
		}
		// 同步内存中的 task 字段
		task.ScanOffset = int64(minMsgID)

		// 请求间隔：每页之间主动 sleep，降低被 Telegram 限流（FLOOD_WAIT）的概率
		// 1 秒为经验值，可平衡扫描速度与稳定性
		select {
		case <-ctx.Done():
			stopReason = "canceled"
			scanError = fmt.Sprintf("扫描被取消: %v", ctx.Err())
			return fmt.Errorf("扫描被取消: %w", ctx.Err())
		case <-time.After(time.Second):
		}
		page++
	}

	// 更新任务进度（total_files）与状态
	if err := UpdateTaskProgress(s.db, task.ID, matchedCount, 0, 0); err != nil {
		stopReason = "error"
		scanError = fmt.Sprintf("更新任务进度失败: %v", err)
		log.Printf("[scanner] task #%d 更新任务进度失败: %v", task.ID, err)
		return fmt.Errorf("更新任务进度失败: %w", err)
	}
	task.TotalFiles = matchedCount

	// 规范化 sort_order：扫描时 sort_order 用 msg.ID 初始化（仅保证顺序），
	// 这里按当前 sort_order ASC, id ASC 重新编号为 1-based 连续序号。
	// 这样：
	//   - StatusAwaitingSort 路径：用户在此基础上手动/自动排序，再覆盖 sort_order
	//   - 直接下载路径：{episode} 模板变量能渲染为 01、02、03...
	if err := normalizeSortOrder(s.db, task.ID); err != nil {
		// 规范化失败不中断主流程（不影响下载，仅影响 {episode} 渲染）
		_ = err
	}

	// 扫描完成后统一进入 StatusAwaitingSort 状态：
	//   - 若配置了集数正则，先按集数自动排序（便于用户确认）
	//   - 用户可在前端手动调整顺序（拖拽或按钮），确认后点击"开始下载"
	//   - 不再自动启动下载，留出时间让用户确认排序与匹配结果
	var newStatus TaskStatus
	newStatus = StatusAwaitingSort

	// 配置了集数正则时先按集数自动排序（升降序遵循配置）
	if strings.TrimSpace(task.Config.EpisodeRegex) != "" {
		records, err := ListRecordsByTask(s.db, task.ID)
		if err != nil {
			log.Printf("[scanner] task #%d 自动排序加载记录失败: %v", task.ID, err)
		} else {
			SortRecordsBy(records, SortByEpisode, true)
			orderedIDs := make([]int64, 0, len(records))
			for _, r := range records {
				orderedIDs = append(orderedIDs, r.ID)
			}
			if err := UpdateRecordsSortOrder(s.db, task.ID, orderedIDs); err != nil {
				log.Printf("[scanner] task #%d 自动排序更新失败: %v", task.ID, err)
			} else {
				log.Printf("[scanner] task #%d 已按集数自动排序 (%d 项)", task.ID, len(records))
			}
		}
	}

	if err := UpdateTaskStatus(s.db, task.ID, newStatus); err != nil {
		stopReason = "error"
		scanError = fmt.Sprintf("更新任务状态失败: %v", err)
		log.Printf("[scanner] task #%d 更新任务状态失败: %v", task.ID, err)
		return fmt.Errorf("更新任务状态失败: %w", err)
	}
	task.Status = newStatus

	// 推送扫描完成事件
	log.Printf("[scanner] task #%d 扫描完成 (scanned=%d, matched=%d, estimated_total=%d, new_status=%s)",
		task.ID, scannedTotal, matchedCount, estimatedTotal, newStatus)
	s.emit(EventScanComplete, task)
	return nil
}

// emitScanError 推送扫描失败事件（携带详细错误信息，便于前端展示具体失败原因）。
// 用于 Scan 函数入口校验与早期 return 路径，避免 defer 未触发导致前端无法收到错误。
func (s *Scanner) emitScanError(taskID int64, errMsg string) {
	s.emit(EventScanProgress, ScanProgress{
		TaskID:     taskID,
		Done:       true,
		StopReason: "error",
		Error:      errMsg,
	})
}

// floodWaitMaxRetries 是单个 MessagesGetHistory 请求因 FLOOD_WAIT 重试的最大次数。
// 每次重试会等待 Telegram 返回的秒数 + 1 秒缓冲，超过本次次数后视为失败。
const floodWaitMaxRetries = 5

// floodWaitMaxTotalWait 单个请求因 FLOOD_WAIT 累计等待的最大秒数。
// 超过此上限后即使重试次数未用尽也放弃，避免单次请求阻塞过久。
const floodWaitMaxTotalWait = 120 // 秒

// fetchHistoryWithRetry 调用 MessagesGetHistory，遇到 FLOOD_WAIT 自动等待后重试。
// 返回值：
//   - 成功：返回响应与 nil error
//   - 失败：返回零值与原始 error（可能是 FLOOD_WAIT 重试用尽或其他错误）
//
// 等待期间会推送 ScanProgress 事件（Done=false），便于前端展示"等待中"状态。
func (s *Scanner) fetchHistoryWithRetry(
	ctx context.Context,
	api *tg.Client,
	req *tg.MessagesGetHistoryRequest,
	taskID int64,
	page int,
) (tg.MessagesMessagesClass, error) {
	var totalWaited time.Duration
	for attempt := 0; attempt <= floodWaitMaxRetries; attempt++ {
		resp, err := api.MessagesGetHistory(ctx, req)
		if err == nil {
			return resp, nil
		}
		// 检查是否为 FLOOD_WAIT
		waitDur, ok := tgerr.AsFloodWait(err)
		if !ok {
			// 非 FLOOD_WAIT 错误，直接返回
			return nil, err
		}
		// FLOOD_WAIT：等待对应时间后重试
		waitSec := int(waitDur.Seconds())
		if waitSec <= 0 {
			waitSec = 1
		}
		// 加 1 秒缓冲避免 Telegram 仍未就绪
		waitSec = waitSec + 1
		// 超过累计等待上限：放弃
		if totalWaited+time.Duration(waitSec)*time.Second > time.Duration(floodWaitMaxTotalWait)*time.Second {
			log.Printf("[scanner] task #%d FLOOD_WAIT 累计等待将超过 %d 秒上限，放弃重试 (attempt=%d, last_wait=%ds)",
				taskID, floodWaitMaxTotalWait, attempt, waitSec)
			return nil, fmt.Errorf("FLOOD_WAIT 等待超限（累计 %v，本次需 %ds）: %w",
				totalWaited, waitSec, err)
		}
		log.Printf("[scanner] task #%d FLOOD_WAIT 第 %d 页拉取遇到限流（attempt=%d/%d，等待 %ds 后重试）",
			taskID, page, attempt+1, floodWaitMaxRetries+1, waitSec)
		// 推送一条进度提示（保持 Done=false，仅告知前端当前在等待）
		s.emit(EventScanProgress, ScanProgress{
			TaskID:       taskID,
			Page:         page,
			ScannedTotal: 0, // 等待期间没有新进展，前端可显示"等待中"
			MatchedCount: 0,
			EstimatedTotal: 0,
			Done:         false,
			StopReason:   "flood_wait",
			Error:        fmt.Sprintf("Telegram 限流，等待 %d 秒后重试", waitSec),
		})
		// 等待期间可取消，使用 time + ctx
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(waitSec) * time.Second):
		}
		totalWaited += time.Duration(waitSec) * time.Second
	}
	// 重试用尽
	return nil, fmt.Errorf("FLOOD_WAIT 重试 %d 次后仍未成功", floodWaitMaxRetries+1)
}

// fetchSearchWithRetry 调用 MessagesSearch，遇到 FLOOD_WAIT 自动等待后重试。
// 与 fetchHistoryWithRetry 的区别：使用 messages.search API，支持服务器端搜索词过滤。
//
// 参数：
//   - ctx: 上下文
//   - api: Telegram 客户端 API
//   - req: MessagesSearchRequest 请求（含 Peer / Q / Filter / OffsetID 等）
//   - taskID: 任务 ID（用于日志）
//   - page: 当前页数（用于日志）
func (s *Scanner) fetchSearchWithRetry(
	ctx context.Context,
	api *tg.Client,
	req *tg.MessagesSearchRequest,
	taskID int64,
	page int,
) (tg.MessagesMessagesClass, error) {
	var totalWaited time.Duration
	for attempt := 0; attempt <= floodWaitMaxRetries; attempt++ {
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
		if totalWaited+time.Duration(waitSec)*time.Second > time.Duration(floodWaitMaxTotalWait)*time.Second {
			log.Printf("[scanner] task #%d search FLOOD_WAIT 累计等待将超过 %d 秒上限，放弃重试 (attempt=%d, last_wait=%ds)",
				taskID, floodWaitMaxTotalWait, attempt, waitSec)
			return nil, fmt.Errorf("FLOOD_WAIT 等待超限（累计 %v，本次需 %ds）: %w",
				totalWaited, waitSec, err)
		}
		log.Printf("[scanner] task #%d search FLOOD_WAIT 第 %d 页拉取遇到限流（attempt=%d/%d，等待 %ds 后重试）",
			taskID, page, attempt+1, floodWaitMaxRetries+1, waitSec)
		s.emit(EventScanProgress, ScanProgress{
			TaskID:         taskID,
			Page:           page,
			ScannedTotal:   0,
			MatchedCount:   0,
			EstimatedTotal: 0,
			Done:           false,
			StopReason:     "flood_wait",
			Error:          fmt.Sprintf("Telegram 限流，等待 %d 秒后重试", waitSec),
		})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(waitSec) * time.Second):
		}
		totalWaited += time.Duration(waitSec) * time.Second
	}
	return nil, fmt.Errorf("FLOOD_WAIT 重试 %d 次后仍未成功", floodWaitMaxRetries+1)
}

// processMessage 处理单条消息：检查媒体类型与关键词，匹配则创建下载记录并推送事件。
// 返回 (matched, episodeNumber, error)：
//   - matched=true 表示匹配到文件
//   - episodeNumber：本条消息提取到的集数（nil 表示未提取或未配置正则）
func (s *Scanner) processMessage(ctx context.Context, task Task, msg *tg.Message) (bool, *int, error) {
	// 提取消息媒体
	media, ok := msg.GetMedia()
	if !ok || media == nil {
		return false, nil, nil
	}

	// 解析媒体类型与文件信息
	// 注意：extractMediaInfo 还返回 fileRef 与 accessHash（用于下载阶段构造 InputFileLocation），
	// 但 DownloadRecord 模型未存储这两个字段。下载阶段会重新拉取消息以获取最新的 FileReference
	// （FileReference 可能过期，需在下载时刷新），因此此处用 _ 丢弃。
	mediaType, fileID, fileName, fileSize, _, _, _, ok := extractMediaInfo(media)
	if !ok {
		// 不支持的媒体类型，跳过
		return false, nil, nil
	}

	// 媒体类型筛选：若配置了 MediaTypes，必须匹配其中之一
	if len(task.Config.MediaTypes) > 0 {
		if !containsString(task.Config.MediaTypes, mediaType) {
			return false, nil, nil
		}
	}

	// 关键词筛选：根据 KeywordMatchScope 决定匹配范围
	messageText := msg.GetMessage()
	matchedBy := matchKeywords(fileName, messageText, task.Config.Keywords, task.Config.KeywordMatchScope, task.Config.KeywordMatchMode)
	if !matchedBy.IsValid() {
		return false, nil, nil
	}

	// 集数正则提取（若配置了 EpisodeRegex）
	//   - 命中 → 提取 capture group 并尝试解析为整数
	//   - 未命中 + RequireEpisodeMatch=true → 过滤该消息（不创建下载记录）
	//   - 未命中 + RequireEpisodeMatch=false → episodeNumber=nil，后续排序时排在最后
	var episodeNumber *int
	var episodeRaw string
	if regexStr := strings.TrimSpace(task.Config.EpisodeRegex); regexStr != "" {
		compiled, err := regexp.Compile(regexStr)
		if err != nil {
			// 正则编译失败：跳过提取但不中断扫描（避免单条消息失败影响整体）
			// 不进入 RequireEpisodeMatch 过滤分支，因为正则本身无效
		} else {
			raw, ok := extractEpisodeNumber(compiled, fileName, messageText)
			if ok {
				episodeRaw = raw
				if n, perr := strconv.Atoi(raw); perr == nil {
					episodeNumber = &n
				}
			} else if task.Config.RequireEpisodeMatch {
				// 必须命中但未命中：过滤
				return false, nil, nil
			}
		}
	}

	// 去重：检查该文件是否已存在下载记录
	existing, err := GetRecordByMessageAndFile(s.db, int64(msg.ID), fileID)
	if err == nil && existing.ID > 0 {
		// 已存在记录，跳过（避免重复创建）
		// 若之前下载失败（status=failed），保持原记录不变，由 RetryFailed 重试
		return false, nil, nil
	}

	// 消息日期格式化（使用消息自身的 Date 字段，而非 Document/Photo 的上传日期）
	dateStr := time.Unix(int64(msg.GetDate()), 0).Format(time.RFC3339)

	// 创建下载记录（status=pending）
	// SortOrder 初始用 message_id（int32 截断到 int），后续由 SortTaskRecords / ReorderTaskRecords 覆盖。
	// 这里直接用 msg.ID 作为初始排序键，等价于按消息发布顺序升序。
	record := DownloadRecord{
		TaskID:         task.ID,
		MessageID:      int64(msg.ID),
		FileID:         fileID,
		FileName:       fileName,
		FileSize:       fileSize,
		MediaType:      mediaType,
		Status:         "pending",
		SortOrder:      int(msg.ID),
		EpisodeNumber:  episodeNumber,
		EpisodeRaw:     episodeRaw,
	}
	recordID, err := CreateRecord(s.db, record)
	if err != nil {
		return false, nil, fmt.Errorf("创建下载记录失败: %w", err)
	}
	record.ID = recordID

	// 推送匹配事件给前端
	matched := MatchedFile{
		MessageID:      int64(msg.ID),
		FileID:         fileID,
		FileName:       fileName,
		FileSize:       fileSize,
		MediaType:      mediaType,
		Date:           dateStr,
		MessageText:    messageText,
		MatchedBy:      matchedBy.String(),
		EpisodeNumber:  episodeNumber,
		EpisodeRaw:     episodeRaw,
	}
	s.emit(EventScanMatched, matched)
	return true, episodeNumber, nil
}

// extractEpisodeNumber 用预编译的正则在文件名或消息文本中提取集数。
// 优先匹配文件名，未命中再匹配消息文本（含 caption）。
// 返回 (captureGroup原始字符串, 是否命中)。
// 调用方负责将原始字符串解析为整数（如需）。
//
// 正则要求至少包含一个 capture group；若多个 group，仅取第一个。
func extractEpisodeNumber(re *regexp.Regexp, fileName, messageText string) (string, bool) {
	if re == nil {
		return "", false
	}
	// 优先匹配文件名
	if fileName != "" {
		if m := re.FindStringSubmatch(fileName); len(m) >= 2 {
			return m[1], true
		}
	}
	// 文件名未命中再匹配消息文本
	if messageText != "" {
		if m := re.FindStringSubmatch(messageText); len(m) >= 2 {
			return m[1], true
		}
	}
	return "", false
}

// normalizeSortOrder 将指定任务的下载记录按当前 sort_order ASC, id ASC 重新编号为 1-based 连续序号。
// 用于扫描完成后规范化 sort_order（扫描时初始化为 msg.ID），
// 使 {episode} 模板变量能渲染为 01、02、03... 而非消息 ID。
func normalizeSortOrder(db *sql.DB, taskID int64) error {
	records, err := ListRecordsByTask(db, taskID)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	orderedIDs := make([]int64, 0, len(records))
	for _, r := range records {
		orderedIDs = append(orderedIDs, r.ID)
	}
	return UpdateRecordsSortOrder(db, taskID, orderedIDs)
}

// extractMessages 从 MessagesGetHistory 响应中提取消息列表。
// 支持的响应类型：
//   - *tg.MessagesMessages：普通对话的完整结果（Count=0，无法预估总数）
//   - *tg.MessagesMessagesSlice：分页结果（Count=频道的总消息数，可用于进度展示）
//   - *tg.MessagesChannelMessages：频道/超级群组消息（Count=频道总消息数）
//   - *tg.MessagesMessagesNotModified：缓存未变化（返回空）
func extractMessages(resp tg.MessagesMessagesClass) ([]tg.MessageClass, error) {
	switch r := resp.(type) {
	case *tg.MessagesMessages:
		return r.Messages, nil
	case *tg.MessagesMessagesSlice:
		return r.Messages, nil
	case *tg.MessagesChannelMessages:
		return r.Messages, nil
	case *tg.MessagesMessagesNotModified:
		return nil, nil
	default:
		return nil, fmt.Errorf("未预期的消息响应类型: %T", resp)
	}
}

// extractMessagesAndCount 与 extractMessages 相同，额外返回 Telegram 报告的频道总消息数。
// 用于扫描进度展示：Count > 0 时可作为预估总数；Count = 0 表示类型不支持（如 *tg.MessagesMessages）。
func extractMessagesAndCount(resp tg.MessagesMessagesClass) ([]tg.MessageClass, int, error) {
	switch r := resp.(type) {
	case *tg.MessagesMessages:
		return r.Messages, 0, nil
	case *tg.MessagesMessagesSlice:
		return r.Messages, r.Count, nil
	case *tg.MessagesChannelMessages:
		return r.Messages, r.Count, nil
	case *tg.MessagesMessagesNotModified:
		return nil, 0, nil
	default:
		return nil, 0, fmt.Errorf("未预期的消息响应类型: %T", resp)
	}
}

// extractMediaInfo 从 MessageMediaClass 中提取媒体类型与文件信息。
//
// 返回值：
//   - mediaType: video/audio/photo/document
//   - fileID: 文件唯一标识（Document.ID 或 Photo.ID 的字符串形式）
//   - fileName: 文件名（来自 DocumentAttributeFilename，可能为空）
//   - fileSize: 文件大小（字节）
//   - fileReference: 文件引用（用于下载时构造 Location）
//   - accessHash: 访问哈希（用于下载时构造 Location）
//   - date: 消息/文件日期（Unix 时间戳）
//   - ok: 是否成功提取（不支持的媒体类型返回 false）
func extractMediaInfo(media tg.MessageMediaClass) (mediaType, fileID, fileName string, fileSize int64, fileRef []byte, accessHash int64, date int, ok bool) {
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
		fname := extractDocumentFileName(doc)
		return mt, strconv.FormatInt(doc.ID, 10), fname, doc.Size, doc.FileReference, doc.AccessHash, doc.Date, true

	case *tg.MessageMediaPhoto:
		if m.Photo == nil {
			return
		}
		photo, isPhoto := m.Photo.(*tg.Photo)
		if !isPhoto {
			return
		}
		// 计算最大尺寸的 Size 字段（用于显示文件大小）
		var size int64
		if largestType, largestSize, hasSize := pickLargestPhotoSize(photo.Sizes); hasSize {
			_ = largestType
			size = int64(largestSize)
		}
		// 照片默认文件名
		fname := fmt.Sprintf("photo_%d.jpg", photo.ID)
		return "photo", strconv.FormatInt(photo.ID, 10), fname, size, photo.FileReference, photo.AccessHash, photo.Date, true

	default:
		// MessageMediaWebPage / MessageMediaContact 等不含可下载文件
		return
	}
}

// extractDocumentFileName 从 Document.Attributes 中提取文件名。
// 遍历 Attributes 寻找 *tg.DocumentAttributeFilename，返回其 FileName 字段。
// 若不存在则返回空字符串（调用方可根据消息 ID 与 MimeType 生成默认名）。
func extractDocumentFileName(doc *tg.Document) string {
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
// 用于照片下载时确定要请求的 ThumbSize 与文件大小估算。
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

// buildInputPeer 根据 DialogInfo 构建 tg.InputPeerClass。
// 不同对话类型使用不同的 InputPeer 子类：
//   - channel: InputPeerChannel（需要 AccessHash）
//   - chat: InputPeerChat（不需要 AccessHash）
//   - user: InputPeerUser（需要 AccessHash）
func buildInputPeer(dialog telegram.DialogInfo) tg.InputPeerClass {
	switch dialog.Type {
	case telegram.DialogTypeChannel:
		return &tg.InputPeerChannel{ChannelID: dialog.PeerID, AccessHash: dialog.AccessHash}
	case telegram.DialogTypeChat:
		return &tg.InputPeerChat{ChatID: dialog.PeerID}
	case telegram.DialogTypeUser:
		return &tg.InputPeerUser{UserID: dialog.PeerID, AccessHash: dialog.AccessHash}
	default:
		return nil
	}
}

// containsString 判断 slice 中是否包含 str（大小写敏感）。
func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// matchAnyKeyword 判断 text 是否包含 keywords 中的任意一个（大小写不敏感）。
// keywords 为空时返回 false（与 matchKeywords 配合：空 keywords 由调用方处理）。
func matchAnyKeyword(text string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	lowerText := strings.ToLower(text)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(lowerText, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// matchAllKeywords 判断 text 是否包含 keywords 中的全部关键词（大小写不敏感）。
// 用于 keyword_match_mode="all" 模式：所有关键词都必须命中同一字段才算匹配。
// keywords 为空时返回 false。
func matchAllKeywords(text string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	lowerText := strings.ToLower(text)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if !strings.Contains(lowerText, strings.ToLower(kw)) {
			return false
		}
	}
	return true
}

// allKeywordsHitEither 判断每个关键词是否在 fileName 或 messageText 中至少命中一处。
// 用于 keyword_match_mode="all" + keyword_match_scope="both" 组合：
// 每个关键词可以在文件名或消息文本任一字段命中，但所有关键词都必须命中（不要求同一字段）。
func allKeywordsHitEither(fileName, messageText string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	lowerFile := strings.ToLower(fileName)
	lowerMsg := strings.ToLower(messageText)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		lowerKw := strings.ToLower(kw)
		if !strings.Contains(lowerFile, lowerKw) && !strings.Contains(lowerMsg, lowerKw) {
			return false
		}
	}
	return true
}

// MatchedBy 表示关键词命中的字段范围。
// 空字符串（零值）表示未命中。
type MatchedBy string

// 命中类型常量。
const (
	MatchedByNone     MatchedBy = ""                  // 未命中
	MatchedByFilename  MatchedBy = "filename"          // 仅文件名命中
	MatchedByMessage  MatchedBy = "message"            // 仅消息文本命中
	MatchedByBoth     MatchedBy = "filename+message"   // 文件名与消息文本均命中（或 scope=both 任一命中）
	MatchedByNoKeywords MatchedBy = "media_type"       // 未配置关键词，仅靠媒体类型命中
)

// IsValid 返回是否命中（非空）。
func (m MatchedBy) IsValid() bool {
	return m != MatchedByNone
}

// String 返回字符串表示。
func (m MatchedBy) String() string {
	return string(m)
}

// matchKeywords 根据匹配范围（scope）与命中模式（mode）判断关键词是否命中。
//
// 参数：
//   - fileName: 文件名
//   - messageText: 消息文本（含 caption）
//   - keywords: 关键词列表
//   - scope: 匹配范围 "filename" / "message" / "both"（默认 both）
//   - mode: 命中模式 "any"（任一关键词命中即匹配，默认）/ "all"（所有关键词都必须命中）
//
// scope 与 mode 是正交的两个维度：
//   - scope 控制“在哪里匹配”（文件名/消息文本/两者）
//   - mode 控制“匹配多少个关键词”（任意/全部）
//
// 组合语义示例（keywords=["A","B"]）：
//   - scope=filename, mode=any: 文件名含 A 或 B
//   - scope=filename, mode=all: 文件名同时含 A 且 B
//   - scope=both, mode=any: 文件名或消息文本任一含 A 或 B
//   - scope=both, mode=all: 每个关键词（A、B）都必须在文件名或消息文本中至少出现一次
//
// keywords 为空时返回 MatchedByNoKeywords（仅靠媒体类型命中）。
// 返回 MatchedByNone 表示未命中。
func matchKeywords(fileName, messageText string, keywords []string, scope, mode string) MatchedBy {
	// 未配置关键词：仅靠媒体类型命中
	if len(keywords) == 0 {
		return MatchedByNoKeywords
	}

	// 归一化 scope（默认 both）
	s := strings.ToLower(strings.TrimSpace(scope))
	if s == "" {
		s = "both"
	}

	// 归一化 mode（默认 any）
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		m = "any"
	}

	// 全部命中模式：所有关键词都必须命中
	if m == "all" {
		switch s {
		case "filename":
			if matchAllKeywords(fileName, keywords) {
				return MatchedByFilename
			}
			return MatchedByNone
		case "message":
			if matchAllKeywords(messageText, keywords) {
				return MatchedByMessage
			}
			return MatchedByNone
		case "both":
			fallthrough
		default:
			// 每个关键词必须在文件名或消息文本中至少命中一处
			if !allKeywordsHitEither(fileName, messageText, keywords) {
				return MatchedByNone
			}
			// 统计命中字段：是否所有关键词都命中文件名 / 是否所有关键词都命中消息文本
			allHitFile := matchAllKeywords(fileName, keywords)
			allHitMsg := matchAllKeywords(messageText, keywords)
			if allHitFile && allHitMsg {
				return MatchedByBoth
			}
			if allHitFile {
				return MatchedByFilename
			}
			if allHitMsg {
				return MatchedByMessage
			}
			// 部分关键词仅命中文件名、部分仅命中消息文本（合起来全部命中）
			return MatchedByBoth
		}
	}

	// any 模式（默认）：任一关键词命中即匹配
	hitFile := matchAnyKeyword(fileName, keywords)
	hitMsg := matchAnyKeyword(messageText, keywords)

	switch s {
	case "filename":
		if hitFile {
			return MatchedByFilename
		}
		return MatchedByNone
	case "message":
		if hitMsg {
			return MatchedByMessage
		}
		return MatchedByNone
	case "both":
		fallthrough
	default:
		// 任一命中即视为匹配
		if hitFile && hitMsg {
			return MatchedByBoth
		}
		if hitFile {
			return MatchedByFilename
		}
		if hitMsg {
			return MatchedByMessage
		}
		return MatchedByNone
	}
}

// emit 安全推送事件：emitter 为 nil 或 Emit 失败时静默忽略。
func (s *Scanner) emit(eventName string, data interface{}) {
	if s.emitter == nil {
		return
	}
	_ = s.emitter.Emit(eventName, data)
}

// previewMaxPages 是测试匹配的最大页数上限。
// 测试模式仅用于快速预览匹配效果，限制为 20 页（2000 条消息）以控制耗时。
const previewMaxPages = 20

// PreviewMatch 测试匹配：扫描频道历史消息，按当前配置匹配文件，
// 但不创建下载记录、不更新任务状态，仅推送匹配文件与进度事件给前端。
//
// 与 Scan 的区别：
//   - 不创建任务/记录（无副作用）
//   - 不更新 scan_offset（不污染增量扫描位置）
//   - 限制扫描页数（previewMaxPages）以控制耗时
//   - 推送独立事件（EventScanPreviewMatched / EventScanPreviewProgress）
//
// 事件流：
//  1. 每条消息处理后推送 EventScanPreviewProgress（含 scanned/matched 计数）
//  2. 匹配到文件时推送 EventScanPreviewMatched（含文件名、消息内容、命中字段）
//  3. 扫描结束（完成或出错）推送最终 EventScanPreviewProgress（Done=true）
//
// 参数 cfg 包含匹配所需的 MediaTypes / Keywords / KeywordMatchScope 与频道信息。
// 返回最终匹配数（与事件中的 matched 一致）。
func (s *Scanner) PreviewMatch(ctx context.Context, cfg TaskConfig) (int, error) {
	if s.manager == nil {
		return 0, fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return 0, fmt.Errorf("未登录，请先完成登录流程")
	}
	client := s.manager.GetClient()
	if client == nil {
		return 0, fmt.Errorf("客户端未启动")
	}

	// 查找 DialogInfo 以构建 InputPeer
	dialog, err := telegram.LoadDialogByPeerID(s.db, cfg.DialogPeerID)
	if err != nil {
		return 0, fmt.Errorf("查询对话信息失败: %w", err)
	}
	peer := buildInputPeer(dialog)
	if peer == nil {
		return 0, fmt.Errorf("无法构建 InputPeer（对话类型: %s）", dialog.Type)
	}

	api := client.API()
	offsetID := 0
	scanned := 0
	matchedCount := 0

	// 推送初始进度（done=false）
	s.emit(EventScanPreviewProgress, PreviewProgress{
		Scanned: 0,
		Matched: 0,
		Done:    false,
	})

	// 分页遍历历史消息
	for page := 0; page < previewMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			// 取消：推送完成事件
			s.emit(EventScanPreviewProgress, PreviewProgress{
				Scanned: scanned,
				Matched: matchedCount,
				Done:    true,
				Error:   "已取消",
			})
			return matchedCount, nil
		}

		req := &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			OffsetID: offsetID,
			Limit:    scanPageLimit,
		}
		resp, err := api.MessagesGetHistory(ctx, req)
		if err != nil {
			// 推送错误事件
			s.emit(EventScanPreviewProgress, PreviewProgress{
				Scanned: scanned,
				Matched: matchedCount,
				Done:    true,
				Error:   fmt.Sprintf("拉取历史消息失败（第 %d 页）: %v", page+1, err),
			})
			return matchedCount, fmt.Errorf("拉取历史消息失败（第 %d 页）: %w", page+1, err)
		}

		messages, err := extractMessages(resp)
		if err != nil {
			s.emit(EventScanPreviewProgress, PreviewProgress{
				Scanned: scanned,
				Matched: matchedCount,
				Done:    true,
				Error:   fmt.Sprintf("解析历史消息失败: %v", err),
			})
			return matchedCount, fmt.Errorf("解析历史消息失败: %w", err)
		}

		if len(messages) == 0 {
			break
		}

		// 处理本批次消息
		minMsgID := 0
		for _, mc := range messages {
			msg, ok := mc.(*tg.Message)
			if !ok || msg == nil {
				continue
			}
			if minMsgID == 0 || (msg.ID > 0 && msg.ID < minMsgID) {
				minMsgID = msg.ID
			}

			scanned++

			// 测试匹配：仅匹配不写库
			matched := s.previewMessage(cfg, msg)
			if matched {
				matchedCount++
			}
		}

		// 推送本页进度
		s.emit(EventScanPreviewProgress, PreviewProgress{
			Scanned: scanned,
			Matched: matchedCount,
			Done:    false,
		})

		if len(messages) < scanPageLimit {
			break
		}
		if minMsgID <= 0 || minMsgID >= offsetID {
			break
		}
		offsetID = minMsgID
	}

	// 推送完成事件
	s.emit(EventScanPreviewProgress, PreviewProgress{
		Scanned: scanned,
		Matched: matchedCount,
		Done:    true,
	})

	return matchedCount, nil
}

// previewMessage 测试匹配单条消息：按配置匹配但不写库，命中则推送事件。
// 返回是否命中。
func (s *Scanner) previewMessage(cfg TaskConfig, msg *tg.Message) bool {
	media, ok := msg.GetMedia()
	if !ok || media == nil {
		return false
	}

	mediaType, fileID, fileName, fileSize, _, _, _, ok := extractMediaInfo(media)
	if !ok {
		return false
	}

	// 媒体类型筛选
	if len(cfg.MediaTypes) > 0 {
		if !containsString(cfg.MediaTypes, mediaType) {
			return false
		}
	}

	// 关键词筛选（按 scope）
	messageText := msg.GetMessage()
	matchedBy := matchKeywords(fileName, messageText, cfg.Keywords, cfg.KeywordMatchScope, cfg.KeywordMatchMode)
	if !matchedBy.IsValid() {
		return false
	}

	// 消息日期
	dateStr := time.Unix(int64(msg.GetDate()), 0).Format(time.RFC3339)

	// 推送匹配事件（含文件名与消息内容，便于前端判断是否真命中）
	matched := MatchedFile{
		MessageID:   int64(msg.ID),
		FileID:      fileID,
		FileName:    fileName,
		FileSize:    fileSize,
		MediaType:   mediaType,
		Date:        dateStr,
		MessageText: messageText,
		MatchedBy:   matchedBy.String(),
	}
	s.emit(EventScanPreviewMatched, matched)
	return true
}

// RecentMessage 表示一条频道最新消息的预览数据。
// 用于前端"测试匹配"弹窗中展示消息列表，供用户复制文本、调整关键词。
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

// fetchRecentLimit 是 FetchRecentMessages 的默认拉取条数上限。
// 100 条覆盖大多数频道最新内容，便于用户快速预览与复制文本。
const fetchRecentLimit = 100

// FetchRecentMessages 拉取频道最新的若干条消息，返回完整内容供前端展示与本地筛选。
//
// 用途：在"测试匹配"弹窗中一次性拉取最新 100 条消息，前端在本地做实时筛选，
// 用户可复制消息文本作为关键词，无需反复调用后端扫描。
//
// 参数：
//   - peerID: 频道/群组/用户 ID
//   - limit: 拉取条数（≤100，0 表示用默认值 100）
func (s *Scanner) FetchRecentMessages(ctx context.Context, peerID int64, limit int) ([]RecentMessage, error) {
	if s.manager == nil {
		return nil, fmt.Errorf("客户端管理器未初始化")
	}
	if !s.manager.IsAuthenticated() {
		return nil, fmt.Errorf("未登录，请先完成登录流程")
	}
	client := s.manager.GetClient()
	if client == nil {
		return nil, fmt.Errorf("客户端未启动")
	}

	// 限制拉取条数
	if limit <= 0 || limit > fetchRecentLimit {
		limit = fetchRecentLimit
	}

	// 查找 DialogInfo 以构建 InputPeer
	dialog, err := telegram.LoadDialogByPeerID(s.db, peerID)
	if err != nil {
		return nil, fmt.Errorf("查询对话信息失败: %w", err)
	}
	peer := buildInputPeer(dialog)
	if peer == nil {
		return nil, fmt.Errorf("无法构建 InputPeer（对话类型: %s）", dialog.Type)
	}

	api := client.API()
	// 拉取最新消息（OffsetID=0 表示从最新开始）
	resp, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     peer,
		OffsetID: 0,
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("拉取历史消息失败: %w", err)
	}

	messages, err := extractMessages(resp)
	if err != nil {
		return nil, fmt.Errorf("解析历史消息失败: %w", err)
	}

	// 转换为 RecentMessage 列表
	result := make([]RecentMessage, 0, len(messages))
	for _, mc := range messages {
		msg, ok := mc.(*tg.Message)
		if !ok || msg == nil {
			continue
		}

		rm := RecentMessage{
			MessageID:   int64(msg.ID),
			MessageText: msg.GetMessage(),
			Date:         time.Unix(int64(msg.GetDate()), 0).Format(time.RFC3339),
		}

		// 提取媒体信息（若有）
		if media, ok := msg.GetMedia(); ok && media != nil {
			mediaType, fileID, fileName, fileSize, _, _, _, mediaOK := extractMediaInfo(media)
			if mediaOK {
				rm.HasMedia = true
				rm.MediaType = mediaType
				rm.FileName = fileName
				rm.FileSize = fileSize
				_ = fileID // RecentMessage 不需要 fileID
			}
		}

		if !rm.HasMedia {
			rm.MediaType = "none"
		}

		result = append(result, rm)
	}

	return result, nil
}

// PreviewMessageList 在给定消息列表上做本地匹配筛选，返回每条消息是否命中及命中字段。
//
// 用途：前端已通过 FetchRecentMessages 拉取消息列表，用户调整关键词/匹配范围后，
// 由前端调用此方法（或前端本地实现）对列表做筛选，避免反复请求后端。
//
// 参数：
//   - messages: 已拉取的消息列表
//   - cfg: 匹配配置（MediaTypes / Keywords / KeywordMatchScope）
//
// 返回与 messages 等长的切片，每个元素表示对应消息的匹配结果。
type PreviewMatchResult struct {
	// MessageID 消息 ID
	MessageID int64 `json:"message_id"`
	// Matched 是否命中
	Matched bool `json:"matched"`
	// MatchedBy 命中字段（"filename"/"message"/"filename+message"/"media_type"/""）
	MatchedBy string `json:"matched_by"`
}

// MatchMessages 对给定消息列表做匹配筛选，返回每条消息的匹配结果。
// 用于前端"测试匹配"弹窗的本地筛选模式：一次拉取 100 条，多次调整配置实时筛选。
//
// 匹配逻辑与 PreviewMatch 一致：
//   - 媒体类型筛选：若配置了 MediaTypes，消息媒体类型必须匹配其中之一
//   - 关键词筛选：根据 KeywordMatchScope 决定匹配范围
//   - 无媒体文件的消息（media_type=none）：若配置了 MediaTypes 则不命中；否则按关键词匹配
func (s *Scanner) MatchMessages(messages []RecentMessage, cfg TaskConfig) []PreviewMatchResult {
	results := make([]PreviewMatchResult, len(messages))
	for i, msg := range messages {
		// 媒体类型筛选
		mediaTypeOK := true
		if len(cfg.MediaTypes) > 0 {
			mediaTypeOK = containsString(cfg.MediaTypes, msg.MediaType)
		}

		// 关键词筛选
		matchedBy := matchKeywords(msg.FileName, msg.MessageText, cfg.Keywords, cfg.KeywordMatchScope, cfg.KeywordMatchMode)

		// 命中判定：媒体类型 + 关键词都满足
		matched := mediaTypeOK && matchedBy.IsValid()
		if !mediaTypeOK {
			// 媒体类型不匹配，整体不命中
			results[i] = PreviewMatchResult{MessageID: msg.MessageID, Matched: false, MatchedBy: ""}
			continue
		}
		results[i] = PreviewMatchResult{
			MessageID: msg.MessageID,
			Matched:   matched,
			MatchedBy: matchedBy.String(),
		}
	}
	return results
}
