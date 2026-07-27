// Package download 提供文件下载的任务调度、扫描与持久化能力。
// 本文件实现基于消息 ID 列表直接创建下载任务的流程（选集下载场景），
// 跳过扫描阶段，直接拉取消息元数据 → 创建下载记录 → 启动下载。
package download

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"tg-download/internal/settings"
	"tg-download/internal/telegram"

	"github.com/gotd/td/tg"
)

// EpisodeDownloadInput 选集下载输入项（由前端选集弹窗提交）。
type EpisodeDownloadInput struct {
	// MessageID 频道消息 ID
	MessageID int64 `json:"message_id"`
	// EpisodeNumber 集数（来自 video_episodes.ve_collect，0 表示未识别）
	EpisodeNumber int `json:"episode_number"`
	// Title 分集标题（来自 video_episodes.ve_title，可空）
	Title string `json:"title"`
}

// CreateTaskFromMessagesRequest 选集下载创建任务请求。
type CreateTaskFromMessagesRequest struct {
	// VideoID 视频源 ID（video_repo.vr_id）
	VideoID int64 `json:"vr_id"`
	// VideoName 视频标题（下载页展示用）
	VideoName string `json:"vr_name"`
	// VideoCover 视频封面相对路径
	VideoCover string `json:"vr_cover"`
	// ChannelID 频道 ID（TG peer_id，对应 dialog_id 与 DialogPeerID）
	ChannelID int64 `json:"channel_id"`
	// Episodes 选中的分集列表
	Episodes []EpisodeDownloadInput `json:"episodes"`
	// VideoPrefix 视频文件名前缀（可空）
	VideoPrefix string `json:"video_prefix"`
}

// CreateTaskFromMessages 基于消息 ID 列表直接创建下载任务并启动下载。
//
// 流程：
//  1. 校验登录态与频道信息
//  2. 批量拉取消息元数据（ChannelsGetMessages/MessagesGetMessages）
//  3. 提取每条消息的媒体文件信息（file_id/file_name/file_size/media_type）
//  4. CreateTaskWithVideo 创建任务（status=pending）
//  5. BatchCreateRecords 批量创建 pending 下载记录
//  6. Scheduler.StartTask 启动下载
//
// 与 Scanner.Scan 的区别：
//   - 不扫描频道历史消息，直接按传入的 message_id 列表拉取
//   - 不创建 awaiting_sort 状态，直接进入下载
//   - 关联视频源信息（vr_id/vr_name/vr_cover）便于下载页展示
//
// 返回新任务 ID。
func (s *Scanner) CreateTaskFromMessages(ctx context.Context, req CreateTaskFromMessagesRequest, scheduler *Scheduler) (int64, error) {
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
	if scheduler == nil {
		return 0, fmt.Errorf("调度器未初始化")
	}
	if len(req.Episodes) == 0 {
		return 0, fmt.Errorf("未选择任何分集")
	}

	// 查询对话信息（access_hash 与 type，用于构造 InputChannel）
	dialog, err := telegram.LoadDialogByPeerID(s.db, req.ChannelID)
	if err != nil {
		return 0, fmt.Errorf("查询对话信息失败: %w", err)
	}

	// 读取全局并发设置（与设置页"并发数"滑块一致），≤0 兜底为 1。
	// 不再硬编码为 3，否则用户在设置页将并发数调为 1 时选集下载仍并发 3。
	concurrency := 1
	if appCfg, err := settings.LoadSettings(s.db); err == nil && appCfg.Concurrency > 0 {
		concurrency = appCfg.Concurrency
	} else if err != nil {
		log.Printf("[scanner] CreateTaskFromMessages 读取全局并发设置失败，使用默认值 1: %v", err)
	}

	// 构造任务配置
	cfg := TaskConfig{
		DialogPeerID:     req.ChannelID,
		DialogAccessHash: dialog.AccessHash,
		DialogName:       dialog.Name,
		// 选集下载场景：媒体类型固定为 video，无关键词、无搜索词、无集数正则
		MediaTypes:       []string{"video"},
		SaveDirName:      sanitizeSaveDirName(req.VideoName, req.VideoID),
		VideoPrefix:      req.VideoPrefix,
		Concurrency:      concurrency,
		NamingTemplate:   "{filename}",
		AutoDownload:     true,
	}

	// 创建任务（status=pending，等待记录创建后由 StartTask 切换为 downloading）
	taskID, err := CreateTaskWithVideo(s.db, cfg, req.VideoID, req.VideoName, req.VideoCover, StatusPending)
	if err != nil {
		return 0, fmt.Errorf("创建任务失败: %w", err)
	}
	log.Printf("[scanner] CreateTaskFromMessages: task #%d 已创建 (vr_id=%d, channel_id=%d, episodes=%d)",
		taskID, req.VideoID, req.ChannelID, len(req.Episodes))

	// 批量拉取消息元数据并构造下载记录
	api := client.API()
	records := make([]DownloadRecord, 0, len(req.Episodes))
	failedMsgs := make([]string, 0)
	for i, ep := range req.Episodes {
		msg, err := fetchMessageByIDForTask(ctx, api, req.ChannelID, dialog, ep.MessageID)
		if err != nil {
			log.Printf("[scanner] task #%d 拉取消息 %d 失败: %v", taskID, ep.MessageID, err)
			failedMsgs = append(failedMsgs, fmt.Sprintf("消息 %d: %v", ep.MessageID, err))
			continue
		}

		// 提取媒体信息
		mediaType, fileID, fileName, fileSize, _, _, _, ok := extractMediaInfoFromMsg(msg)
		if !ok {
			log.Printf("[scanner] task #%d 消息 %d 无可下载媒体", taskID, ep.MessageID)
			failedMsgs = append(failedMsgs, fmt.Sprintf("消息 %d: 无可下载媒体", ep.MessageID))
			continue
		}

		// 集数指针
		var episodeNum *int
		if ep.EpisodeNumber > 0 {
			n := ep.EpisodeNumber
			episodeNum = &n
		}

		// 文件名兜底
		if fileName == "" {
			if ep.Title != "" {
				fileName = ep.Title
			} else {
				fileName = strconv.FormatInt(ep.MessageID, 10)
			}
		}

		rec := DownloadRecord{
			TaskID:        taskID,
			MessageID:     ep.MessageID,
			FileID:        fileID,
			FileName:      fileName,
			FileSize:      fileSize,
			MediaType:     mediaType,
			Status:        "pending",
			SortOrder:     i + 1,
			EpisodeNumber: episodeNum,
			EpisodeRaw:    ep.Title,
		}
		records = append(records, rec)
	}

	if len(records) == 0 {
		// 全部拉取失败，标记任务失败
		_ = UpdateTaskStatus(s.db, taskID, StatusFailed)
		// 推送失败事件
		if t, e := GetTask(s.db, taskID); e == nil {
			t.Status = StatusFailed
			scheduler.Emit(EventTaskStatus, t)
		}
		return taskID, fmt.Errorf("所有分集消息拉取失败")
	}

	// 批量创建下载记录
	if _, err := BatchCreateRecords(s.db, records); err != nil {
		_ = UpdateTaskStatus(s.db, taskID, StatusFailed)
		return taskID, fmt.Errorf("批量创建下载记录失败: %w", err)
	}

	// 更新任务 total_files
	if err := UpdateTaskProgress(s.db, taskID, len(records), 0, 0); err != nil {
		log.Printf("[scanner] task #%d 更新任务进度失败: %v", taskID, err)
	}

	// 启动下载
	if err := scheduler.StartTask(ctx, taskID); err != nil {
		log.Printf("[scanner] task #%d 启动下载失败: %v", taskID, err)
		return taskID, fmt.Errorf("启动下载失败: %w", err)
	}

	log.Printf("[scanner] CreateTaskFromMessages: task #%d 已启动下载 (records=%d, failed_fetch=%d)",
		taskID, len(records), len(failedMsgs))
	return taskID, nil
}

// fetchMessageByIDForTask 拉取指定频道的单条消息。
// 频道使用 ChannelsGetMessages，其他对话使用 MessagesGetMessages。
// access_hash 优先从 dialog_infos 表查询（精度无损），避免 Wails 序列化精度丢失。
func fetchMessageByIDForTask(ctx context.Context, api *tg.Client, peerID int64, dialog telegram.DialogInfo, messageID int64) (*tg.Message, error) {
	id := int(messageID)
	inputMsg := []tg.InputMessageClass{&tg.InputMessageID{ID: id}}

	var resp tg.MessagesMessagesClass
	var err error

	if dialog.Type == telegram.DialogTypeChannel {
		resp, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  peerID,
				AccessHash: dialog.AccessHash,
			},
			ID: inputMsg,
		})
	} else {
		resp, err = api.MessagesGetMessages(ctx, inputMsg)
	}
	if err != nil {
		return nil, fmt.Errorf("API 调用失败: %w", err)
	}

	messages, err := extractMessages(resp)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("消息 %d 不存在", messageID)
	}

	msg, ok := messages[0].(*tg.Message)
	if !ok || msg == nil {
		return nil, fmt.Errorf("消息 %d 类型异常", messageID)
	}
	return msg, nil
}

// extractMediaInfoFromMsg 从 tg.Message 中提取媒体信息。
// 与 extractMediaInfo 的区别：直接接收 *tg.Message，先取 Media 再提取。
func extractMediaInfoFromMsg(msg *tg.Message) (mediaType, fileID, fileName string, fileSize int64, fileRef []byte, accessHash int64, date int, ok bool) {
	if msg == nil {
		return
	}
	media, hasMedia := msg.GetMedia()
	if !hasMedia || media == nil {
		return
	}
	return extractMediaInfo(media)
}

// sanitizeSaveDirName 为选集下载场景生成保存目录名。
// 优先用视频标题，加上 vr_id 后缀避免重名视频冲突。
// 经 Windows 路径净化后返回。
func sanitizeSaveDirName(videoName string, videoID int64) string {
	name := videoName
	if name == "" {
		name = fmt.Sprintf("video_%d", videoID)
	}
	// 加上 vr_id 后缀避免不同视频重名
	if videoID > 0 {
		name = fmt.Sprintf("%s_%d", name, videoID)
	}
	return sanitizeDirName(name)
}
