// 视频源频道服务封装层
//
// 对 bindings/tg-download/services 的 ApiService 二次封装。
// 所有响应字段名直接使用数据库列名（vr_id/vr_name/ve_id/vrd_id 等），
// 前端直接消费，无后端二次映射。

// RepoSourceWithSubscription 带订阅状态的视频源频道
export interface RepoSourceWithSubscription {
  vs_name: string
  vs_channel_id: number
  vs_username: string
  is_subscribed: boolean
  dialog_name: string
}

// HomeVideoItem 首页视频项
export interface HomeVideoItem {
  vr_id: number
  vr_name: string
  vr_cover: string
  vr_episode_count: number
  // 播放量（仅热门榜单板块需要展示；后端未返回时为 undefined）
  vr_view_count?: number
}

// HomeBannerItem 首页横幅项（横幅封面取自 video_banner.vb_cover）
export interface HomeBannerItem {
  vr_id: number
  vr_name: string
  vb_cover: string
  vr_episode_count: number
}

// HomeData 首页聚合数据
export interface HomeData {
  banner: HomeBannerItem[]
  latest: HomeVideoItem[]
  hot: HomeVideoItem[]
}

// SearchRequest 分类检索查询参数（请求参数，非数据库字段）
export interface SearchRequest {
  category?: string
  tags?: number[]
  year?: number
  season?: string
  sort?: string
  keyword?: string
  page?: number
  page_size?: number
}

// VideoSearchItem 视频检索项
export interface VideoSearchItem {
  vr_id: number
  vr_name: string
  vr_cover: string
  vr_episode_count: number
  vr_category: string
  vr_year: number
  vr_season: string
  vr_view_count: number
  vr_bgm_id: number  // bangumi 条目 ID（0 表示未关联）
  update_time: number
}

// SearchResult 分类检索结果
export interface SearchResult {
  list: VideoSearchItem[]
  total: number
  page: number
  page_size: number
  has_more: boolean
}

// LocalDialog 本地频道（带服务器收录状态）
export interface LocalDialog {
  peer_id: number
  name: string
  username: string
  type: string
  is_repo_uploaded: boolean
}

// RecentMessage 频道消息预览
export interface RecentMessage {
  message_id: number
  media_type: string  // video/audio/photo/document/none
  file_name: string
  file_size: number
  message_text: string
  date: string  // RFC3339
  has_media: boolean
  ve_collect?: number  // 前端本地维护的集数（排序/初始排序后填充），上传分集时使用
  ve_title?: string    // 前端本地维护的分集标题（默认"第X集"），允许用户修改
}

// ChannelMessageSearchResult 频道消息搜索结果
export interface ChannelMessageSearchResult {
  list: RecentMessage[]
  next_offset_id: number  // 下一页 OffsetID，0 表示无更多
  total: number
}

// bindings 文件生成后从此路径导入；生成前调用会报错，属预期行为
let ApiServiceBinding: any = null
let DialogServiceBinding: any = null
let SourceListServiceBinding: any = null

async function loadBinding() {
  if (!ApiServiceBinding) {
    const mod: any = await import('../../bindings/tg-download/services/index')
    ApiServiceBinding = mod.ApiService
    DialogServiceBinding = mod.DialogService
    SourceListServiceBinding = mod.SourceListService
  }
  return ApiServiceBinding
}

// 同步结果（与后端 services.SourceListSyncResult 对应）
export interface SourceListSyncResult {
  total: number
  upserted: number
  subscribed: number
  failed: number
}

// syncSourceList 从 settings.source_list_url 拉取清单并同步本地表 + 自动订阅未加入的频道
export async function syncSourceList(): Promise<SourceListSyncResult> {
  await loadBinding()
  return SourceListServiceBinding.SyncSourceList()
}

// refreshDialogs 强制从 Telegram 拉取最新对话列表并更新本地缓存
export async function refreshDialogs(): Promise<void> {
  await loadBinding()
  return DialogServiceBinding.RefreshDialogs()
}

// listRepoSources 获取带订阅状态的视频源频道列表
export async function listRepoSources(): Promise<RepoSourceWithSubscription[]> {
  const svc = await loadBinding()
  return svc.ListRepoSources()
}

// subscribeRepoSource 订阅视频源频道
export async function subscribeRepoSource(channelId: number, username: string): Promise<void> {
  const svc = await loadBinding()
  return svc.SubscribeRepoSource(channelId, username)
}

// getHome 获取首页聚合数据
export async function getHome(): Promise<HomeData> {
  const svc = await loadBinding()
  return svc.GetHome()
}

// searchVideos 分类检索视频
export async function searchVideos(req: SearchRequest): Promise<SearchResult> {
  const svc = await loadBinding()
  return svc.SearchVideos(req)
}

// saveRepoChannel 保存频道到服务器（收录）
export async function saveRepoChannel(channelId: number, username: string, name: string): Promise<void> {
  const svc = await loadBinding()
  return svc.SaveRepoChannel(channelId, username, name)
}

// listLocalDialogs 列出本地已订阅频道（带服务器收录状态）
export async function listLocalDialogs(keyword?: string): Promise<LocalDialog[]> {
  const svc = await loadBinding()
  return svc.ListLocalDialogs(keyword || '')
}

// searchChannelMessages 在指定频道内按关键字搜索消息
export async function searchChannelMessages(
  peerId: number,
  keyword: string,
  offsetId: number,
  limit: number
): Promise<ChannelMessageSearchResult> {
  const svc = await loadBinding()
  return svc.SearchChannelMessages(peerId, keyword, offsetId, limit)
}

// EpisodeItem 分集项
export interface EpisodeItem {
  ve_id: number
  vs_channel_id: number
  ve_message_id: number
  ve_title: string
  ve_collect: number
  ve_status: number  // 1=正常, 2=失效
}

// DiscussItem 评论项
export interface DiscussItem {
  vrd_id: number
  vrd_content: string
  reply_count: number
  create_time: number  // 秒级时间戳
  replies?: DiscussItem[]  // 预览回复(仅顶级评论返回,最多3条)
}

// DiscussPostRequest 发表评论请求体
export interface DiscussPostRequest {
  ve_id: number
  vrd_content: string
  parent_id: number  // 0 表示顶级评论
}

// listEpisodes 获取视频分集列表
export async function listEpisodes(videoId: number): Promise<EpisodeItem[]> {
  const svc = await loadBinding()
  return svc.ListEpisodes(videoId)
}

// VideoDetail 视频简介详情
export interface VideoDetail {
  vr_id: number
  vr_name: string
  vr_desc: string
  vr_cover: string
  vr_category: string
  vr_episode_count: number
  vr_now_count: number
  vr_year: number
  vr_season: string
  vr_view_count: number
  vr_status: number  // 1=完结, 2=停更, 3=连载中
  vr_language: number
  vr_bgm_id: number  // bangumi 条目 ID（0 表示未关联）
  update_time: number  // 秒级时间戳
  is_followed: boolean       // 当前用户是否已追番
}

// getVideoDetail 获取视频简介详情
export async function getVideoDetail(videoId: number): Promise<VideoDetail> {
  const svc = await loadBinding()
  return svc.GetVideoDetail(videoId)
}

// ListDiscussRequest 评论列表游标分页请求
export interface ListDiscussRequest {
  ve_id: number
  parent_id: number         // 0=顶级评论
  sort: 'latest' | 'hot'    // 排序,仅顶级评论生效
  cursor_vrd_id: number     // 游标 vrd_id,0=第一页
  limit: number             // 每页条数
}

// listDiscuss 获取分集评论列表(游标分页)
export async function listDiscuss(req: ListDiscussRequest): Promise<DiscussItem[]> {
  const svc = await loadBinding()
  return svc.ListDiscuss(req)
}

// postDiscuss 发表评论或回复
export async function postDiscuss(req: DiscussPostRequest): Promise<DiscussItem> {
  const svc = await loadBinding()
  return svc.PostDiscuss(req)
}

// ============ 视频追番 / 播放数自增 ============

// VideoFollowResponse 视频追番响应
export interface VideoFollowResponse {
  is_followed: boolean  // 操作后的追番状态
  follow_count: number  // 操作后的追番数
}

// followVideo 视频追番（toggle 语义：已追则取消，未追则追番）
export async function followVideo(vrId: number): Promise<VideoFollowResponse> {
  const svc = await loadBinding()
  return svc.FollowVideo({ vr_id: vrId })
}

// incrementViewCount 视频播放数自增 1（失败静默，调用方需自行 catch）
export async function incrementViewCount(vrId: number): Promise<void> {
  const svc = await loadBinding()
  return svc.IncrementViewCount(vrId)
}

// ============ 弹幕 ============

// DanmakuItem 弹幕项
export interface DanmakuItem {
  vd_id: number
  vd_content: string
  vd_time: number   // 出现时间(毫秒)
  vd_mode: number   // 1=滚动, 2=顶部, 3=底部
  vd_color: string  // #RRGGBB
}

// DanmakuPostRequest 发送弹幕请求体
export interface DanmakuPostRequest {
  ve_id: number
  vd_content: string
  vd_time: number   // 毫秒
  vd_mode: number   // 1=滚动, 2=顶部, 3=底部
  vd_color: string
}

// listDanmaku 获取分集弹幕列表
export async function listDanmaku(episodeId: number): Promise<DanmakuItem[]> {
  const svc = await loadBinding()
  return svc.ListDanmaku(episodeId)
}

// postDanmaku 发送弹幕
export async function postDanmaku(req: DanmakuPostRequest): Promise<DanmakuItem> {
  const svc = await loadBinding()
  return svc.PostDanmaku(req)
}

// ============ 播放历史（续播）============

// HistoryProgress 播放进度
export interface HistoryProgress {
  vuh_history_time: number  // 上次播放位置(秒),0=无记录或已看完
}

// HistoryReportRequest 上报播放进度请求体
export interface HistoryReportRequest {
  vr_id: number
  ve_id: number
  vuh_history_time: number  // 续播进度(秒),看完时传 0
  vuh_duration: number      // 实际观看时长(秒),看完时传视频总时长
  finished: number          // 是否看完(0/1)
}

// HistoryListItem 最近观看列表项
export interface HistoryListItem {
  vr_id: number              // 视频 ID
  ve_id: number              // 最后观看的分集 ID
  vuh_history_time: number   // 播放进度(秒),0=已看完
  update_time: number        // 最后观看时间(秒级时间戳)
  vr_name: string            // 视频标题
  vr_cover: string           // 封面图(相对路径,前端拼接 CDN)
  vr_episode_count: number    // 集数(0 表示未知/连载中)
  ve_collect: number          // 最后观看的分集集数
}

// getHistory 获取分集续播进度
export async function getHistory(episodeId: number): Promise<HistoryProgress> {
  const svc = await loadBinding()
  return svc.GetHistory(episodeId)
}

// reportHistory 上报播放进度
export async function reportHistory(req: HistoryReportRequest): Promise<void> {
  const svc = await loadBinding()
  return svc.ReportHistory(req)
}

// listHistory 获取最近观看列表(按 vr_id 去重,最多 10 条)
export async function listHistory(): Promise<HistoryListItem[]> {
  const svc = await loadBinding()
  return svc.ListHistory()
}

// ============ 视频基本信息（分类、语种、标签）============

// VideoCategory 视频分类项
export interface VideoCategory {
  vc_id: number
  vc_name: string
}

// VideoLanguage 视频语种项
export interface VideoLanguage {
  vl_id: number
  vl_name: string
}

// VideoTag 视频标签项
export interface VideoTag {
  vt_id: number
  vt_name: string
}

// BaseInfo 视频基本信息聚合数据
export interface BaseInfo {
  categories: VideoCategory[]
  languages: VideoLanguage[]
  tags: VideoTag[]
}

// getBaseInfo 获取视频基本信息（分类、语种、标签）
export async function getBaseInfo(): Promise<BaseInfo> {
  const svc = await loadBinding()
  return svc.GetBaseInfo()
}

// ============ 新建视频源信息 ============

// CreateVideoRepoRequest 新建视频源信息请求体
export interface CreateVideoRepoRequest {
  vr_name: string            // 视频标题（必填）
  vr_category: string       // 分类（必填，枚举）
  vr_desc?: string          // 简介
  vr_cover?: string         // 封面图相对路径
  vr_episode_count?: number // 集数
  vr_year?: number          // 年份
  vr_season?: string        // 季度（枚举）
  vr_status?: number        // 状态（默认3=连载中）
  vr_language?: number     // 语种 ID
  vr_bgm_id?: number        // bangumi 条目 ID（0 表示未关联）
  tags?: number[]           // 标签 ID 数组
}

// CreateVideoRepoResponse 新建视频源信息响应
export interface CreateVideoRepoResponse {
  vr_id: number
}

// createVideoRepo 新建视频源信息
export async function createVideoRepo(req: CreateVideoRepoRequest): Promise<CreateVideoRepoResponse> {
  const svc = await loadBinding()
  return svc.CreateVideoRepo(req)
}

// ============ 批量上传分集 ============

// EpisodeUploadItem 单条分集上传项
export interface EpisodeUploadItem {
  vs_channel_id: number  // 频道 ID（TG peer_id）
  ve_message_id: number  // TG 消息 ID
  ve_title: string       // 分集标题
  ve_collect: number     // 集数
}

// UploadEpisodesRequest 批量上传分集请求体
export interface UploadEpisodesRequest {
  vr_id: number
  episodes: EpisodeUploadItem[]
}

// UploadEpisodesResponse 批量上传分集响应
// 后端仅返回成功/失败，不返回分集列表
export interface UploadEpisodesResponse {
}

// uploadEpisodes 批量上传分集
export async function uploadEpisodes(req: UploadEpisodesRequest): Promise<UploadEpisodesResponse> {
  const svc = await loadBinding()
  return svc.UploadEpisodes(req)
}

// ============ 删除视频源 ============

// deleteVideoRepo 批量删除视频源及其关联数据（分集/标签关联/播放历史/追番/弹幕/评论）
export async function deleteVideoRepo(ids: number[]): Promise<void> {
  const svc = await loadBinding()
  return svc.DeleteVideoRepo(ids)
}
