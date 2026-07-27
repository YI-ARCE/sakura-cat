// 本文件实现视频元数据（video_repo / video_episode / 字典表）在本地 SQLite 的读写操作，
// 供 ApiService 的 Wails 绑定方法调用，替代原服务器接口交互。
package services

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tg-download/internal/api"
)

// EnsureDictSeeds 幂等插入字典种子数据。
// 当 video_category 表为空时插入 5 条分类：anime、movie、tv、ova、special。
// 当 video_language 表为空时插入常用语种：日语、英语、汉语、韩语、法语、德语、西班牙语、意大利语、俄语。
// 当 video_tag 表为空时插入 bangumi 常见 meta_tags，使新建视频源时能按名称自动匹配标签 ID。
// 重复启动不会重复插入。
func EnsureDictSeeds(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}

	// 分类种子
	var catCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM video_category`).Scan(&catCount); err != nil {
		return fmt.Errorf("查询分类数量失败: %w", err)
	}
	if catCount == 0 {
		catSeeds := []string{"anime", "movie", "tv", "ova", "special"}
		for _, name := range catSeeds {
			if _, err := db.Exec(`INSERT INTO video_category (vc_name) VALUES (?)`, name); err != nil {
				return fmt.Errorf("插入分类种子 %q 失败: %w", name, err)
			}
		}
	}

	// 语种种子
	var langCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM video_language`).Scan(&langCount); err != nil {
		return fmt.Errorf("查询语种数量失败: %w", err)
	}
	if langCount == 0 {
		langSeeds := []string{"日语", "英语", "汉语", "韩语", "法语", "德语", "西班牙语", "意大利语", "俄语"}
		for _, name := range langSeeds {
			if _, err := db.Exec(`INSERT INTO video_language (vl_name) VALUES (?)`, name); err != nil {
				return fmt.Errorf("插入语种种子 %q 失败: %w", name, err)
			}
		}
	}

	// 标签种子（对齐 bangumi 常见 meta_tags，便于新建视频源时按名称自动匹配）
	var tagCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM video_tag`).Scan(&tagCount); err != nil {
		return fmt.Errorf("查询标签数量失败: %w", err)
	}
	if tagCount == 0 {
		tagSeeds := []string{
			"原创", "漫画改", "小说改", "游戏改",
			"科幻", "热血", "恋爱", "校园",
			"治愈", "致郁", "悬疑", "推理",
			"机战", "奇幻", "魔法", "战斗",
			"日常", "搞笑", "职场", "历史",
			"战争", "竞技", "恐怖",
		}
		for _, name := range tagSeeds {
			if _, err := db.Exec(`INSERT INTO video_tag (vt_name) VALUES (?)`, name); err != nil {
				return fmt.Errorf("插入标签种子 %q 失败: %w", name, err)
			}
		}
	}

	return nil
}

// CreateVideoRepoLocal 在本地 video_repo 表新建一条视频源记录。
// vr_status 为 0 时默认设为 3（连载中），update_time 取当前秒级时间戳。
// 若 req.Tags 非空，逐条写入 video_tag_relation 关联。返回新建的 vr_id。
func CreateVideoRepoLocal(db *sql.DB, req api.CreateVideoRepoRequest) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	status := req.Status
	if status == 0 {
		status = 3
	}
	now := time.Now().Unix()
	res, err := db.Exec(
		`INSERT INTO video_repo (vr_name, vr_desc, vr_cover, vr_category, vr_episode_count, vr_now_count, vr_year, vr_season, vr_view_count, vr_status, vr_language, vr_bgm_id, update_time)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?, 0, ?, ?, ?, ?)`,
		req.Name, req.Desc, req.Cover, req.Category, req.EpisodeCount, req.Year, req.Season, status, req.Language, req.BgmID, now,
	)
	if err != nil {
		return 0, fmt.Errorf("插入视频源失败: %w", err)
	}
	vrID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取视频源自增 ID 失败: %w", err)
	}
	for _, tagID := range req.Tags {
		if _, err := db.Exec(
			`INSERT INTO video_tag_relation (vr_id, vt_id) VALUES (?, ?)`,
			vrID, tagID,
		); err != nil {
			return 0, fmt.Errorf("插入标签关联 (vr_id=%d, vt_id=%d) 失败: %w", vrID, tagID, err)
		}
	}
	return vrID, nil
}

// UploadEpisodesLocal 批量上传分集到本地 video_episode 表。
// video_episode 表无 (vs_channel_id, ve_message_id) 的 UNIQUE 约束，
// 故采用 DELETE + INSERT 实现 UPSERT 去重语义。
// 全部处理完成后同步更新 video_repo.vr_now_count 为该视频的分集总数。
// 整体在事务中执行，任一条失败全部回滚。
func UploadEpisodesLocal(db *sql.DB, req api.UploadEpisodesRequest) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, ep := range req.Episodes {
		if _, err := tx.Exec(
			`DELETE FROM video_episode WHERE vs_channel_id = ? AND ve_message_id = ?`,
			ep.ChannelID, ep.MessageID,
		); err != nil {
			return fmt.Errorf("删除分集 (vs_channel_id=%d, ve_message_id=%d) 失败: %w", ep.ChannelID, ep.MessageID, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO video_episode (vr_id, vs_channel_id, ve_message_id, ve_title, ve_collect, ve_status) VALUES (?, ?, ?, ?, ?, 3)`,
			req.VideoID, ep.ChannelID, ep.MessageID, ep.Title, ep.Collect,
		); err != nil {
			return fmt.Errorf("插入分集 (vs_channel_id=%d, ve_message_id=%d) 失败: %w", ep.ChannelID, ep.MessageID, err)
		}
	}

	if _, err := tx.Exec(
		`UPDATE video_repo SET vr_now_count = (SELECT COUNT(*) FROM video_episode WHERE vr_id = ?) WHERE vr_id = ?`,
		req.VideoID, req.VideoID,
	); err != nil {
		return fmt.Errorf("更新视频 #%d 分集数失败: %w", req.VideoID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// GetBaseInfoLocal 从本地字典表读取视频基本信息（分类、语种、标签）。
// 空表返回空切片（非 nil）。
func GetBaseInfoLocal(db *sql.DB) (*api.BaseInfo, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	categories := make([]api.VideoCategory, 0)
	rows, err := db.Query(`SELECT vc_id, vc_name FROM video_category ORDER BY vc_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询分类列表失败: %w", err)
	}
	for rows.Next() {
		var c api.VideoCategory
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描分类行失败: %w", err)
		}
		categories = append(categories, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历分类行失败: %w", err)
	}
	_ = rows.Close()

	languages := make([]api.VideoLanguage, 0)
	rows, err = db.Query(`SELECT vl_id, vl_name FROM video_language ORDER BY vl_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询语种列表失败: %w", err)
	}
	for rows.Next() {
		var l api.VideoLanguage
		if err := rows.Scan(&l.ID, &l.Name); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描语种行失败: %w", err)
		}
		languages = append(languages, l)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历语种行失败: %w", err)
	}
	_ = rows.Close()

	tags := make([]api.VideoTag, 0)
	rows, err = db.Query(`SELECT vt_id, vt_name FROM video_tag ORDER BY vt_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询标签列表失败: %w", err)
	}
	for rows.Next() {
		var t api.VideoTag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描标签行失败: %w", err)
		}
		tags = append(tags, t)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历标签行失败: %w", err)
	}
	_ = rows.Close()

	return &api.BaseInfo{
		Categories: categories,
		Languages:  languages,
		Tags:       tags,
	}, nil
}

// SearchVideosLocal 在本地 video_repo 表按分类/标签/年份/季度/关键词检索分页。
// 默认值：Page<=0 设 1，PageSize<=0 设 20、>50 设 50，Sort 空设 "latest"。
// 排序：hot 按 vr_view_count DESC，否则按 update_time DESC。
// 所有筛选值均以占位符 ? 传参，避免 SQL 注入。空结果返回非 nil 空切片。
func SearchVideosLocal(db *sql.DB, req api.SearchRequest) (*api.SearchResult, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.PageSize > 50 {
		req.PageSize = 50
	}
	if req.Sort == "" {
		req.Sort = "latest"
	}

	var whereBuilder strings.Builder
	args := make([]interface{}, 0, 8)
	if req.Category != "" {
		whereBuilder.WriteString("vr_category = ?")
		args = append(args, req.Category)
	}
	if req.Year > 0 {
		if whereBuilder.Len() > 0 {
			whereBuilder.WriteString(" AND ")
		}
		whereBuilder.WriteString("vr_year = ?")
		args = append(args, req.Year)
	}
	if req.Season != "" {
		if whereBuilder.Len() > 0 {
			whereBuilder.WriteString(" AND ")
		}
		whereBuilder.WriteString("vr_season = ?")
		args = append(args, req.Season)
	}
	if req.Keyword != "" {
		if whereBuilder.Len() > 0 {
			whereBuilder.WriteString(" AND ")
		}
		whereBuilder.WriteString("vr_name LIKE ?")
		args = append(args, "%"+req.Keyword+"%")
	}
	if len(req.Tags) > 0 {
		if whereBuilder.Len() > 0 {
			whereBuilder.WriteString(" AND ")
		}
		placeholders := make([]string, len(req.Tags))
		for i, t := range req.Tags {
			placeholders[i] = "?"
			args = append(args, t)
		}
		whereBuilder.WriteString("vr_id IN (SELECT vr_id FROM video_tag_relation WHERE vt_id IN (")
		whereBuilder.WriteString(strings.Join(placeholders, ","))
		whereBuilder.WriteString("))")
	}

	whereClause := ""
	if whereBuilder.Len() > 0 {
		whereClause = " WHERE " + whereBuilder.String()
	}

	var total int64
	if err := db.QueryRow("SELECT COUNT(*) FROM video_repo"+whereClause, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("查询视频总数失败: %w", err)
	}

	orderClause := " ORDER BY update_time DESC"
	if req.Sort == "hot" {
		orderClause = " ORDER BY vr_view_count DESC"
	}
	offset := (req.Page - 1) * req.PageSize
	listArgs := append(args, req.PageSize, offset)
	listQuery := "SELECT vr_id, vr_name, vr_cover, vr_episode_count, vr_category, vr_year, vr_season, vr_view_count, vr_bgm_id, update_time FROM video_repo" + whereClause + orderClause + " LIMIT ? OFFSET ?"

	list := make([]api.VideoSearchItem, 0)
	rows, err := db.Query(listQuery, listArgs...)
	if err != nil {
		return nil, fmt.Errorf("查询视频列表失败: %w", err)
	}
	for rows.Next() {
		var item api.VideoSearchItem
		if err := rows.Scan(
			&item.VideoID, &item.Title, &item.Cover, &item.EpisodeCount,
			&item.Category, &item.Year, &item.Season, &item.ViewCount, &item.BgmID, &item.UpdatedAt,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描视频行失败: %w", err)
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历视频行失败: %w", err)
	}
	_ = rows.Close()

	return &api.SearchResult{
		List:     list,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
		HasMore:  total > int64(req.Page*req.PageSize),
	}, nil
}

// GetVideoDetailLocal 从本地 video_repo 表读取视频详情。
// 记录不存在（sql.ErrNoRows）返回零值 VideoDetail，不报错。
// IsFollowed 由 video_follow 表 EXISTS 判定。
func GetVideoDetailLocal(db *sql.DB, videoID int64) (*api.VideoDetail, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var d api.VideoDetail
	err := db.QueryRow(
		`SELECT vr_id, vr_name, vr_desc, vr_cover, vr_category, vr_episode_count, vr_now_count, vr_year, vr_season, vr_view_count, vr_status, vr_language, vr_bgm_id, update_time FROM video_repo WHERE vr_id = ?`,
		videoID,
	).Scan(
		&d.VideoID, &d.Name, &d.Desc, &d.Cover, &d.Category, &d.EpisodeCount, &d.NowCount, &d.Year, &d.Season, &d.ViewCount, &d.Status, &d.Language, &d.BgmID, &d.UpdateTime,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return &api.VideoDetail{}, nil
		}
		return nil, fmt.Errorf("查询视频详情失败: %w", err)
	}

	var followed bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM video_follow WHERE vr_id = ?)`, videoID).Scan(&followed); err != nil {
		return nil, fmt.Errorf("查询追番状态失败: %w", err)
	}
	d.IsFollowed = followed
	return &d, nil
}

// ListEpisodesLocal 从本地 video_episode 表按集数升序读取分集列表。
// 空结果返回非 nil 空切片。
func ListEpisodesLocal(db *sql.DB, videoID int64) ([]api.EpisodeItem, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT ve_id, vr_id, vs_channel_id, ve_message_id, ve_title, ve_collect, ve_status FROM video_episode WHERE vr_id = ? ORDER BY ve_collect ASC`,
		videoID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询分集列表失败: %w", err)
	}
	list := make([]api.EpisodeItem, 0)
	for rows.Next() {
		var ep api.EpisodeItem
		var vrID int64
		if err := rows.Scan(&ep.EpisodeID, &vrID, &ep.ChannelID, &ep.MessageID, &ep.Title, &ep.EpisodeNumber, &ep.Status); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描分集行失败: %w", err)
		}
		list = append(list, ep)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历分集行失败: %w", err)
	}
	_ = rows.Close()
	return list, nil
}

// IncrementViewCountLocal 将本地 video_repo.vr_view_count 自增 1。
func IncrementViewCountLocal(db *sql.DB, vrID int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if _, err := db.Exec(`UPDATE video_repo SET vr_view_count = vr_view_count + 1 WHERE vr_id = ?`, vrID); err != nil {
		return fmt.Errorf("更新播放数失败: %w", err)
	}
	return nil
}

// ReportHistoryLocal 上报播放进度到本地 video_user_history 表。
// UPSERT 语义：按 (vr_id, ve_id) 去重，先 DELETE 再 INSERT，整体在事务中执行。
func ReportHistoryLocal(db *sql.DB, req api.HistoryReportRequest) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`DELETE FROM video_user_history WHERE vr_id = ? AND ve_id = ?`,
		req.VideoID, req.EpisodeID,
	); err != nil {
		return fmt.Errorf("删除历史记录 (vr_id=%d, ve_id=%d) 失败: %w", req.VideoID, req.EpisodeID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO video_user_history (vr_id, ve_id, vuh_history_time, vuh_duration, finished, update_time) VALUES (?, ?, ?, ?, ?, ?)`,
		req.VideoID, req.EpisodeID, req.HistoryTime, req.Duration, req.Finished, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("插入历史记录 (vr_id=%d, ve_id=%d) 失败: %w", req.VideoID, req.EpisodeID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// GetHistoryLocal 从本地 video_user_history 表读取分集续播进度。
// 同一分集可能多条历史，取 update_time 最新一条。无记录返回零值 HistoryProgress，不报错。
func GetHistoryLocal(db *sql.DB, episodeID int64) (*api.HistoryProgress, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var p api.HistoryProgress
	err := db.QueryRow(
		`SELECT vuh_history_time FROM video_user_history WHERE ve_id = ? ORDER BY update_time DESC LIMIT 1`,
		episodeID,
	).Scan(&p.HistoryTime)
	if err != nil {
		if err == sql.ErrNoRows {
			return &api.HistoryProgress{}, nil
		}
		return nil, fmt.Errorf("查询播放进度失败: %w", err)
	}
	return &p, nil
}

// FollowVideoLocal 本地追番 toggle：已追番则取消，未追番则新增。
// 返回最新追番状态与该视频追番总数。
func FollowVideoLocal(db *sql.DB, req api.VideoFollowRequest) (*api.VideoFollowResponse, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var followed bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM video_follow WHERE vr_id = ?)`, req.VrID).Scan(&followed); err != nil {
		return nil, fmt.Errorf("查询追番状态失败: %w", err)
	}
	if followed {
		if _, err := db.Exec(`DELETE FROM video_follow WHERE vr_id = ?`, req.VrID); err != nil {
			return nil, fmt.Errorf("取消追番失败: %w", err)
		}
	} else {
		if _, err := db.Exec(`INSERT INTO video_follow (vr_id, create_time) VALUES (?, ?)`, req.VrID, time.Now().Unix()); err != nil {
			return nil, fmt.Errorf("追番失败: %w", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM video_follow WHERE vr_id = ?`, req.VrID).Scan(&count); err != nil {
		return nil, fmt.Errorf("查询追番数失败: %w", err)
	}
	return &api.VideoFollowResponse{
		IsFollowed:  !followed,
		FollowCount: count,
	}, nil
}

// PostDiscussLocal 写入本地 video_discuss 表。
// reply_count 初始 0。返回新建评论项（Replies 为 nil）。
func PostDiscussLocal(db *sql.DB, req api.DiscussPostRequest) (*api.DiscussItem, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Unix()
	res, err := db.Exec(
		`INSERT INTO video_discuss (ve_id, vrd_content, parent_id, reply_count, create_time) VALUES (?, ?, ?, 0, ?)`,
		req.EpisodeID, req.Content, req.ParentID, now,
	)
	if err != nil {
		return nil, fmt.Errorf("插入评论失败: %w", err)
	}
	vrdID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("获取评论自增 ID 失败: %w", err)
	}
	return &api.DiscussItem{
		CommentID: vrdID,
		Content:   req.Content,
		CreatedAt: now,
	}, nil
}

// ListDiscussLocal 从本地 video_discuss 表读取评论列表。
// 顶级评论按 sort=hot(reply_count DESC, vrd_id DESC) 或 latest(vrd_id DESC) 排序，游标分页（vrd_id < cursor）。
// 回复列表按 vrd_id ASC，无游标。空结果返回非 nil 空切片。
func ListDiscussLocal(db *sql.DB, req api.ListDiscussRequest) ([]api.DiscussItem, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 50 {
		req.Limit = 50
	}
	if req.Sort == "" {
		req.Sort = "latest"
	}

	var (
		query string
		args  []interface{}
	)
	if req.ParentID > 0 {
		query = `SELECT vrd_id, vrd_content, reply_count, create_time FROM video_discuss WHERE parent_id = ? ORDER BY vrd_id ASC LIMIT ?`
		args = []interface{}{req.ParentID, req.Limit}
	} else {
		orderClause := " ORDER BY vrd_id DESC"
		if req.Sort == "hot" {
			orderClause = " ORDER BY reply_count DESC, vrd_id DESC"
		}
		whereClause := " WHERE ve_id = ? AND parent_id = 0"
		args = []interface{}{req.EpisodeID}
		if req.CursorVrdID > 0 {
			whereClause += " AND vrd_id < ?"
			args = append(args, req.CursorVrdID)
		}
		query = "SELECT vrd_id, vrd_content, reply_count, create_time FROM video_discuss" + whereClause + orderClause + " LIMIT ?"
		args = append(args, req.Limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询评论列表失败: %w", err)
	}
	list := make([]api.DiscussItem, 0)
	for rows.Next() {
		var item api.DiscussItem
		if err := rows.Scan(
			&item.CommentID, &item.Content, &item.ReplyCount, &item.CreatedAt,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描评论行失败: %w", err)
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历评论行失败: %w", err)
	}
	_ = rows.Close()
	return list, nil
}

// PostDanmakuLocal 写入本地 video_danmaku 表。
// create_time 取当前秒级时间戳。返回新建弹幕项。
func PostDanmakuLocal(db *sql.DB, req api.DanmakuPostRequest) (*api.DanmakuItem, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	now := time.Now().Unix()
	res, err := db.Exec(
		`INSERT INTO video_danmaku (ve_id, vd_content, vd_time, vd_mode, vd_color, create_time) VALUES (?, ?, ?, ?, ?, ?)`,
		req.EpisodeID, req.Content, req.Time, req.Mode, req.Color, now,
	)
	if err != nil {
		return nil, fmt.Errorf("插入弹幕失败: %w", err)
	}
	vdID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("获取弹幕自增 ID 失败: %w", err)
	}
	return &api.DanmakuItem{
		ID:      vdID,
		Content: req.Content,
		Time:    req.Time,
		Mode:    req.Mode,
		Color:   req.Color,
	}, nil
}

// ListDanmakuLocal 从本地 video_danmaku 表按时间升序读取弹幕。
// 空结果返回非 nil 空切片。
func ListDanmakuLocal(db *sql.DB, episodeID int64) ([]api.DanmakuItem, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT vd_id, vd_content, vd_time, vd_mode, vd_color FROM video_danmaku WHERE ve_id = ? ORDER BY vd_time ASC`,
		episodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询弹幕列表失败: %w", err)
	}
	list := make([]api.DanmakuItem, 0)
	for rows.Next() {
		var item api.DanmakuItem
		if err := rows.Scan(&item.ID, &item.Content, &item.Time, &item.Mode, &item.Color); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描弹幕行失败: %w", err)
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历弹幕行失败: %w", err)
	}
	_ = rows.Close()
	return list, nil
}

// ListHistoryLocal 从本地 video_user_history 表读取最近观看列表。
// 按 vr_id 去重取每个视频最近一条历史，按 update_time 倒序，最多 10 条。空结果返回非 nil 空切片。
func ListHistoryLocal(db *sql.DB) ([]api.HistoryListItem, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	rows, err := db.Query(
		`SELECT h.vr_id, h.ve_id, h.vuh_history_time, h.update_time, r.vr_name, r.vr_cover, r.vr_episode_count, e.ve_collect
		 FROM video_user_history h
		 JOIN video_repo r ON h.vr_id = r.vr_id
		 LEFT JOIN video_episode e ON h.ve_id = e.ve_id
		 WHERE h.vuh_id IN (SELECT MAX(vuh_id) FROM video_user_history GROUP BY vr_id ORDER BY MAX(vuh_id) DESC LIMIT 10)
		 ORDER BY h.update_time DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("查询最近观看列表失败: %w", err)
	}
	list := make([]api.HistoryListItem, 0)
	for rows.Next() {
		var item api.HistoryListItem
		if err := rows.Scan(
			&item.VideoID, &item.EpisodeID, &item.HistoryTime, &item.UpdateTime,
			&item.Title, &item.Cover, &item.EpisodeCount, &item.VeCollect,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描历史行失败: %w", err)
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历历史行失败: %w", err)
	}
	_ = rows.Close()
	return list, nil
}

// DeleteVideoRepoLocal 批量删除视频源及其关联数据。
// 因 SQLite schema 未声明 FOREIGN KEY / ON DELETE CASCADE，需在事务中手动级联清理：
//   - video_danmaku / video_discuss（通过 ve_id 间接关联，需先收集 ve_id）
//   - video_episode / video_tag_relation / video_user_history / video_follow（直接引用 vr_id）
//   - video_repo 本体
//
// tasks 表的 vr_id 字段为可选关联（默认 0），不清理，保留下载任务历史记录。
// 传入空 ids 切片直接返回 nil（无操作）。
func DeleteVideoRepoLocal(db *sql.DB, ids []int64) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if len(ids) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 构造 IN 占位符：?,?,?
	ph := strings.Repeat("?,", len(ids))
	ph = ph[:len(ph)-1]
	// ids 转 []any 供 Exec 使用
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	// 1. 收集所有 ve_id（用于清理弹幕、评论）
	veIDs := make([]int64, 0)
	veRows, err := tx.Query(`SELECT ve_id FROM video_episode WHERE vr_id IN (`+ph+`)`, args...)
	if err != nil {
		return fmt.Errorf("查询待删分集 ve_id 失败: %w", err)
	}
	for veRows.Next() {
		var veID int64
		if err := veRows.Scan(&veID); err != nil {
			_ = veRows.Close()
			return fmt.Errorf("扫描 ve_id 失败: %w", err)
		}
		veIDs = append(veIDs, veID)
	}
	_ = veRows.Close()

	// 2. 按依赖顺序逐表删除
	if len(veIDs) > 0 {
		vePh := strings.Repeat("?,", len(veIDs))
		vePh = vePh[:len(vePh)-1]
		veArgs := make([]any, 0, len(veIDs))
		for _, id := range veIDs {
			veArgs = append(veArgs, id)
		}
		if _, err := tx.Exec(`DELETE FROM video_danmaku WHERE ve_id IN (`+vePh+`)`, veArgs...); err != nil {
			return fmt.Errorf("删除弹幕失败: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM video_discuss WHERE ve_id IN (`+vePh+`)`, veArgs...); err != nil {
			return fmt.Errorf("删除评论失败: %w", err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM video_episode WHERE vr_id IN (`+ph+`)`, args...); err != nil {
		return fmt.Errorf("删除分集失败: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM video_tag_relation WHERE vr_id IN (`+ph+`)`, args...); err != nil {
		return fmt.Errorf("删除标签关联失败: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM video_user_history WHERE vr_id IN (`+ph+`)`, args...); err != nil {
		return fmt.Errorf("删除播放历史失败: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM video_follow WHERE vr_id IN (`+ph+`)`, args...); err != nil {
		return fmt.Errorf("删除追番记录失败: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM video_repo WHERE vr_id IN (`+ph+`)`, args...); err != nil {
		return fmt.Errorf("删除视频源失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}
