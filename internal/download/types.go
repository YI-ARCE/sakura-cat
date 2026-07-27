// Package download 提供文件下载的任务调度、扫描与持久化能力。
// 本文件定义下载任务的状态机、配置、任务与记录模型以及扫描匹配事件数据。
package download

// TaskStatus 表示下载任务的状态。
type TaskStatus string

// 任务状态常量。
const (
	// StatusScanning 扫描中：任务已创建，正在遍历频道历史消息匹配文件。
	StatusScanning TaskStatus = "scanning"
	// StatusAwaitingSort 待排序：扫描已完成且配置了集数正则，
	// 需用户在前端确认排序后再手动开始下载（不自动进入下载状态）。
	StatusAwaitingSort TaskStatus = "awaiting_sort"
	// StatusPending 待确认：扫描已完成，等待用户确认是否开始下载。
	StatusPending TaskStatus = "pending"
	// StatusDownloading 进行中：任务正在下载匹配到的文件。
	StatusDownloading TaskStatus = "downloading"
	// StatusPaused 已暂停：用户主动暂停，可恢复。
	StatusPaused TaskStatus = "paused"
	// StatusCompleted 已完成：所有文件下载完成。
	StatusCompleted TaskStatus = "completed"
	// StatusFailed 失败：任务执行过程中出现致命错误。
	StatusFailed TaskStatus = "failed"
)

// TaskConfig 任务配置。
// 字段与 template.TaskTemplateConfig 一致，额外携带频道（对话）信息，
// 用于在 SQLite 中以 JSON 字符串保存任务配置，便于任务恢复时直接还原。
type TaskConfig struct {
	// DialogPeerID 频道/群组/用户 ID
	DialogPeerID int64 `json:"dialog_peer_id"`
	// DialogAccessHash 访问哈希（频道/用户必填，群组为 0）
	DialogAccessHash int64 `json:"dialog_access_hash"`
	// DialogName 频道/群组名称或用户姓名（仅用于前端展示，下载目录改用 SearchTerm）
	DialogName string `json:"dialog_name"`
	// MediaTypes 媒体类型筛选：video/audio/photo/document
	MediaTypes []string `json:"media_types"`
	// Keywords 关键词过滤（为空表示不过滤）
	Keywords []string `json:"keywords"`
	// SearchTerm 搜索词（可选）。传给 Telegram 服务器的 messages.search q 参数，让服务器在索引层预过滤。
	// 与 Keywords 的区别：
	//   - SearchTerm 是服务器端全文搜索（减少返回的消息总量），由 Telegram 服务器执行
	//   - Keywords 是客户端本地精筛（在 SearchTerm 预过滤后的结果上再做精确匹配）
	// 两者正交、可同时配置：先用 SearchTerm 缩小范围，再用 Keywords 精确匹配。
	// 留空表示不启用服务器搜索，按原有 MessagesGetHistory 流程扫描。
	SearchTerm string `json:"search_term"`
	// SaveDirName 保存目录名（必填）。作为该任务下载文件的存放目录名（位于下载根目录下）。
	// 经 Windows 非法字符净化后创建实际目录，使每个任务的文件按主题分目录存放。
	// 与 SearchTerm 解耦：SearchTerm 控制服务器搜索范围，SaveDirName 控制文件存放位置。
	SaveDirName string `json:"save_dir_name"`
	// KeywordMatchScope 关键词匹配范围：
	//   "filename" 仅匹配文件名
	//   "message"  仅匹配消息文本（含 caption）
	//   "both"     文件名或消息文本任一命中即匹配（默认）
	KeywordMatchScope string `json:"keyword_match_scope"`
	// KeywordMatchMode 关键词命中数量模式：
	//   "any" 任意一个关键词命中即视为匹配（默认）
	//   "all" 所有关键词都必须命中才视为匹配
	// 与 KeywordMatchScope 正交：scope 控制“在哪里匹配”，mode 控制“匹配多少个”。
	KeywordMatchMode string `json:"keyword_match_mode"`
	// EpisodeRegex 集数提取正则（Go RE2 语法，至少包含一个 capture group）。
	// 配置后扫描时会从文件名与消息内容中提取集数，用于后续排序与重命名。
	// 留空表示不启用集数提取，扫描完成按原有流程（auto_download 决定是否立即下载）。
	EpisodeRegex string `json:"episode_regex"`
	// RequireEpisodeMatch 是否要求集数正则必须命中。
	//   true  正则未命中的消息会被过滤（不创建下载记录）
	//   false 正则仅用于提取集数与排序，未命中的消息集数为 nil，排序时排在最后
	RequireEpisodeMatch bool `json:"require_episode_match"`
	// ScanFromNewest 是否从最新消息开始向前扫描。
	//   true  忽略历史 scan_offset，从最新消息开始向更旧方向翻页（适合"找最新分集"场景）
	//   false 复用历史 scan_offset 增量扫描（避免重复扫描已处理过的旧消息）
	// 默认 false（增量扫描）。
	ScanFromNewest bool `json:"scan_from_newest"`
	// MaxMatches 最大匹配文件数量（0 表示不限制）。
	// 达到此数量后停止扫描。用于限制扫描范围、避免频道过大时拉取过多消息。
	// 注意：达到上限时已扫描到的所有匹配都会保留，不会截断。
	MaxMatches int `json:"max_matches"`
	// StopAtFirstEpisode 是否在遇到"集数 1"时停止扫描。
	// 仅当 EpisodeRegex 非空 + ScanFromNewest=true 时生效。
	// 用于"从最新往前找"场景：扫到第一季第一集（episode_number=1）时停止，
	// 假设再往前的消息已不是同一季内容。
	StopAtFirstEpisode bool `json:"stop_at_first_episode"`
	// VideoPrefix 视频文件名前缀
	VideoPrefix string `json:"video_prefix"`
	// Concurrency 单任务内并发数（≤10）
	Concurrency int `json:"concurrency"`
	// SpeedLimit 限速 bytes/s（0 表示不限）
	SpeedLimit int64 `json:"speed_limit"`
	// NamingTemplate 命名模板（参考 template.NamingVars 支持的变量）
	NamingTemplate string `json:"naming_template"`
	// AutoDownload 扫描完成后是否自动开始下载
	AutoDownload bool `json:"auto_download"`
}

// Task 下载任务模型，对应 SQLite tasks 表的一行。
type Task struct {
	// ID 任务自增主键
	ID int64 `json:"id"`
	// DialogID 频道/群组/用户 ID（与 TaskConfig.DialogPeerID 一致，单独冗余便于查询）
	DialogID int64 `json:"dialog_id"`
	// Status 当前任务状态
	Status TaskStatus `json:"status"`
	// ScanOffset 扫描位置（最新已扫描消息 ID），用于增量扫描
	ScanOffset int64 `json:"scan_offset"`
	// TotalFiles 匹配到的文件总数
	TotalFiles int `json:"total_files"`
	// CompletedFiles 已完成下载数
	CompletedFiles int `json:"completed_files"`
	// FailedFiles 失败下载数
	FailedFiles int `json:"failed_files"`
	// Config 任务配置（JSON 序列化存储）
	Config TaskConfig `json:"config"`
	// CreatedAt 创建时间（RFC3339）
	CreatedAt string `json:"created_at"`
	// UpdatedAt 更新时间（RFC3339）
	UpdatedAt string `json:"updated_at"`
	// VideoID 关联视频源 video_repo.vr_id（选集下载场景填充，0 表示扫描下载场景）
	VideoID int64 `json:"vr_id"`
	// VideoName 视频标题（下载页展示用，避免二次查询）
	VideoName string `json:"vr_name"`
	// VideoCover 视频封面相对路径（下载页展示用）
	VideoCover string `json:"vr_cover"`
}

// DownloadRecord 单个文件的下载记录，对应 SQLite download_records 表的一行。
type DownloadRecord struct {
	// ID 记录自增主键
	ID int64 `json:"id"`
	// TaskID 所属任务 ID
	TaskID int64 `json:"task_id"`
	// MessageID 文件所属消息 ID
	MessageID int64 `json:"message_id"`
	// FileID 文件唯一标识（gotd Document.ID/Photo.ID 的字符串形式）
	FileID string `json:"file_id"`
	// FileName 渲染后的最终文件名
	FileName string `json:"file_name"`
	// FileSize 文件大小（字节）
	FileSize int64 `json:"file_size"`
	// MediaType 媒体类型：video/audio/photo/document
	MediaType string `json:"media_type"`
	// Status 下载状态：pending/downloading/completed/failed
	Status string `json:"status"`
	// LocalPath 本地保存路径（下载完成后填充）
	LocalPath string `json:"local_path"`
	// Error 错误信息（status=failed 时填充）
	Error string `json:"error"`
	// CreatedAt 创建时间（RFC3339）
	CreatedAt string `json:"created_at"`
	// SortOrder 排序顺序（从 1 开始；按 sort_order ASC 控制下载顺序）
	// 扫描时按 message_id ASC 初始化；用户在前端确认排序后由 SortTaskRecords/ReorderTaskRecords 更新。
	SortOrder int `json:"sort_order"`
	// EpisodeNumber 集数（由 EpisodeRegex 提取的 capture group 解析为整数；nil 表示未提取到）
	EpisodeNumber *int `json:"episode_number"`
	// EpisodeRaw 集数 capture group 原始字符串（未解析为数字前的文本，便于前端展示与调试）
	EpisodeRaw string `json:"episode_raw"`
	// DownloadedBytes 已下载字节数（暂停时持久化，恢复时用于断点续传 seek 位置）
	DownloadedBytes int64 `json:"downloaded_bytes"`
	// RenderedName 按任务命名模板渲染后的最终文件名（不入库，仅 JSON 返回前端展示用）。
	// 在 ListRecordsByTask 后由 service 层根据 TaskConfig 渲染填充。
	// 注意：未做冲突解决（同名文件加 _1 _2 后缀），仅展示模板渲染结果。
	RenderedName string `json:"rendered_name"`
}

// MatchedFile 扫描阶段匹配到的文件事件数据，通过 EventScanMatched 事件推送给前端。
type MatchedFile struct {
	// MessageID 消息 ID
	MessageID int64 `json:"message_id"`
	// FileID 文件唯一标识
	FileID string `json:"file_id"`
	// FileName 原始文件名（来自 DocumentAttributeFilename，可能为空）
	FileName string `json:"file_name"`
	// FileSize 文件大小（字节）
	FileSize int64 `json:"file_size"`
	// MediaType 媒体类型：video/audio/photo/document
	MediaType string `json:"media_type"`
	// Date 消息日期（RFC3339）
	Date string `json:"date"`
	// MessageText 消息文本内容（含 caption，用于前端展示匹配上下文）
	MessageText string `json:"message_text"`
	// MatchedBy 命中的匹配字段："filename" / "message" / "media_type" / "filename+message"
	MatchedBy string `json:"matched_by"`
	// EpisodeNumber 集数（由 EpisodeRegex 提取并解析为整数；nil 表示未提取到）
	EpisodeNumber *int `json:"episode_number"`
	// EpisodeRaw 集数 capture group 原始字符串
	EpisodeRaw string `json:"episode_raw"`
}

// PreviewProgress 测试匹配进度事件数据，通过 EventScanPreviewProgress 事件推送。
type PreviewProgress struct {
	// Scanned 已扫描消息数
	Scanned int `json:"scanned"`
	// Matched 匹配成功数
	Matched int `json:"matched"`
	// Done 是否已完成（true 时表示扫描结束）
	Done bool `json:"done"`
	// Error 错误信息（非空表示扫描失败）
	Error string `json:"error"`
}

// ScanProgress 正式扫描进度事件数据，通过 EventScanProgress 事件推送给前端。
// 前端据此展示 "已扫描 X / 总数 Y，匹配 Z" 文本。
type ScanProgress struct {
	// TaskID 任务 ID
	TaskID int64 `json:"task_id"`
	// Page 当前页数（从 1 开始，每页 scanPageLimit=100 条）
	Page int `json:"page"`
	// ScannedTotal 累计已扫描的消息总数
	ScannedTotal int `json:"scanned_total"`
	// MatchedCount 累计已匹配的文件数
	MatchedCount int `json:"matched_count"`
	// EstimatedTotal 频道总消息数（Telegram 通过 Count 字段返回；0 表示无法预估）
	// 仅当响应类型为 *tg.MessagesMessagesSlice 或 *tg.MessagesChannelMessages 时才有值
	EstimatedTotal int `json:"estimated_total"`
	// Done 是否已完成扫描（true 表示扫描结束，可能正常结束/出错/触发停止条件）
	Done bool `json:"done"`
	// StopReason 停止原因（Done=true 时填充，便于前端展示为何停止）
	//   "completed"    正常扫描到末尾
	//   "max_matches"  达到 MaxMatches 上限
	//   "episode_one"  触发 StopAtFirstEpisode（遇到集数 1）
	//   "error"        出错（Error 字段填充错误信息）
	//   "canceled"     上下文取消
	StopReason string `json:"stop_reason"`
	// Error 错误信息（Done=true + StopReason=error 时填充）
	Error string `json:"error"`
}

// FileProgress 单文件下载进度事件数据，通过 EventFileProgress 事件推送。
// 下载过程中每 500ms 推送一次，结束时推送 Done=true。
// 前端据此在文件列表中显示进度条与下载速度。
type FileProgress struct {
	// TaskID 任务 ID
	TaskID int64 `json:"task_id"`
	// RecordID 下载记录 ID
	RecordID int64 `json:"record_id"`
	// MessageID 消息 ID
	MessageID int64 `json:"message_id"`
	// Downloaded 已下载字节数
	Downloaded int64 `json:"downloaded"`
	// Total 文件总字节数（0 表示未知）
	Total int64 `json:"total"`
	// Speed 当前瞬时速度 bytes/s（基于上次推送以来的字节差 / 时间差）
	Speed int64 `json:"speed"`
	// Done 是否下载完成（true 表示下载结束，可能成功/失败/取消）
	Done bool `json:"done"`
}
