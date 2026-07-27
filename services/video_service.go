// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 VideoService，负责向前端返回视频播放所需的元数据
// （标题、集数、上下集、集数列表、流接口 URL）。
package services

import (
	"database/sql"
	"fmt"
	"strings"

	"tg-download/internal/download"
)

// VideoPlayInfo 视频播放元数据。
// 前端调用 VideoService.GetVideoPlayInfo 后可直接据此渲染播放页。
type VideoPlayInfo struct {
	// RecordID 当前播放的下载记录 ID
	RecordID int64 `json:"recordId"`
	// TaskID 所属任务 ID
	TaskID int64 `json:"taskId"`
	// Title 视频标题（取自任务 TaskConfig.SaveDirName）
	Title string `json:"title"`
	// EpisodeNumber 集数（nil 表示未提取到集数）
	EpisodeNumber *int `json:"episodeNumber"`
	// EpisodeRaw 集数 capture group 原始文本
	EpisodeRaw string `json:"episodeRaw"`
	// FileName 文件名
	FileName string `json:"fileName"`
	// StreamURL 流接口 URL（/api/video/stream?record_id=xxx）
	StreamURL string `json:"streamUrl"`
	// PrevRecordID 上一集记录 ID（nil 表示已是第一集）
	PrevRecordID *int64 `json:"prevRecordId"`
	// NextRecordID 下一集记录 ID（nil 表示已是最后一集）
	NextRecordID *int64 `json:"nextRecordId"`
	// EpisodeList 同任务下所有可播放视频记录（按 sort_order ASC）
	EpisodeList []EpisodeListItem `json:"episodeList"`
}

// EpisodeListItem 集数列表项。
type EpisodeListItem struct {
	// RecordID 下载记录 ID
	RecordID int64 `json:"recordId"`
	// EpisodeNumber 集数（nil 表示未提取到集数）
	EpisodeNumber *int `json:"episodeNumber"`
	// EpisodeRaw 集数原始文本
	EpisodeRaw string `json:"episodeRaw"`
	// FileName 文件名
	FileName string `json:"fileName"`
	// IsCurrent 是否为当前播放项
	IsCurrent bool `json:"isCurrent"`
}

// VideoService 视频播放服务。
// 通过持有 *sql.DB 与 download_records 表交互，向前端提供播放元数据查询方法。
type VideoService struct {
	db            *sql.DB
	streamBaseURL string // 独立流服务基础地址，如 http://127.0.0.1:12345
}

// NewVideoService 创建一个新的 VideoService 实例。
// db 需已初始化。
func NewVideoService(db *sql.DB) *VideoService {
	return &VideoService{db: db}
}

// SetStreamBaseURL 设置独立流服务的基础地址。
// 由 main.go 在启动独立 HTTP server 后注入，形如 http://127.0.0.1:12345。
// GetEpisodeStream 会据此返回绝对地址，绕过 Wails3 AssetServer 的响应缓冲机制。
func (s *VideoService) SetStreamBaseURL(baseURL string) {
	s.streamBaseURL = strings.TrimRight(baseURL, "/")
}

// GetVideoPlayInfo 获取视频播放所需的完整元数据。
// 前端调用此方法后可直接渲染播放页（VideoPlayer + 标题 + 上下集 + 集数列表）。
//
// 流程：
//  1. 查询当前记录并校验 status=completed 且 media_type=video
//  2. 加载任务，用 TaskConfig.SaveDirName 作为标题
//  3. 查询同任务下所有可播放记录（集数列表）
//  4. 定位当前记录在集数列表中的位置，计算 prev/next
//  5. 拼接 streamUrl 并构建 EpisodeList（标记 isCurrent）
func (s *VideoService) GetVideoPlayInfo(recordID int64) (*VideoPlayInfo, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	// 1. 查询当前记录
	record, err := download.GetRecordByID(s.db, recordID)
	if err != nil {
		return nil, fmt.Errorf("获取下载记录失败: %w", err)
	}

	// 2. 校验状态：仅 completed 可播放
	if record.Status != "completed" {
		return nil, fmt.Errorf("记录 #%d 未完成下载（当前状态: %s）", recordID, record.Status)
	}

	// 3. 校验媒体类型：仅 video 可播放
	if record.MediaType != "video" {
		return nil, fmt.Errorf("记录 #%d 非视频类型（当前类型: %s）", recordID, record.MediaType)
	}

	// 4. 加载任务，取 SaveDirName 作为标题
	task, err := download.GetTask(s.db, record.TaskID)
	if err != nil {
		return nil, fmt.Errorf("获取任务失败: %w", err)
	}

	// 5. 查询同任务下所有可播放记录（集数列表）
	records, err := download.ListRecordsByTaskForPlayback(s.db, record.TaskID)
	if err != nil {
		return nil, fmt.Errorf("获取集数列表失败: %w", err)
	}

	// 6. 定位当前记录在集数列表中的索引，计算 prev/next
	currentIndex := -1
	for i, r := range records {
		if r.ID == recordID {
			currentIndex = i
			break
		}
	}

	var prevRecordID *int64
	var nextRecordID *int64
	if currentIndex >= 0 {
		if currentIndex > 0 {
			prevID := records[currentIndex-1].ID
			prevRecordID = &prevID
		}
		if currentIndex < len(records)-1 {
			nextID := records[currentIndex+1].ID
			nextRecordID = &nextID
		}
	}

	// 7. 拼接 streamUrl
	streamURL := fmt.Sprintf("/api/video/stream?record_id=%d", recordID)

	// 8. 构建 EpisodeList，标记 isCurrent
	episodeList := make([]EpisodeListItem, 0, len(records))
	for _, r := range records {
		episodeList = append(episodeList, EpisodeListItem{
			RecordID:      r.ID,
			EpisodeNumber: r.EpisodeNumber,
			EpisodeRaw:    r.EpisodeRaw,
			FileName:      r.FileName,
			IsCurrent:     r.ID == recordID,
		})
	}

	// 9. 返回 VideoPlayInfo
	return &VideoPlayInfo{
		RecordID:      record.ID,
		TaskID:        record.TaskID,
		Title:         task.Config.SaveDirName,
		EpisodeNumber: record.EpisodeNumber,
		EpisodeRaw:    record.EpisodeRaw,
		FileName:      record.FileName,
		StreamURL:     streamURL,
		PrevRecordID:  prevRecordID,
		NextRecordID:  nextRecordID,
		EpisodeList:   episodeList,
	}, nil
}

// GetEpisodeStream 根据分集的 TG 频道与消息 ID 返回可播放的流地址。
// 返回独立流服务的绝对地址（http://127.0.0.1:<port>/api/video/episode/stream?...），
// 由独立 HTTP server 处理，原生支持流式响应与 Range 请求，
// 绕过 Wails3 AssetServer 对响应体的全量缓冲。
func (s *VideoService) GetEpisodeStream(channelID, messageID int64) string {
	path := fmt.Sprintf("/api/video/episode/stream?channel_id=%d&message_id=%d", channelID, messageID)
	if s.streamBaseURL == "" {
		return path
	}
	return s.streamBaseURL + path
}
