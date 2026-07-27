// Package db 提供 SQLite 数据库的初始化与表结构定义。
package db

import (
	"database/sql"
	"strings"

	// 空白导入 modernc.org/sqlite 以注册纯 Go 实现的 SQLite 驱动（无 CGO 依赖）。
	// 驱动名为 "sqlite"。
	_ "modernc.org/sqlite"
)

// schemaStatements 包含所有表的建表语句，均使用 IF NOT EXISTS 以保证可重复执行。
var schemaStatements = []string{
	// sessions 表：存储 Telegram 会话数据。
	`CREATE TABLE IF NOT EXISTS sessions (
		id          INTEGER PRIMARY KEY,
		session_data BLOB,
		created_at  DATETIME,
		updated_at  DATETIME
	);`,
	// dialogs 表：缓存 Telegram 对话（会话）元信息。
	`CREATE TABLE IF NOT EXISTS dialogs (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		peer_id         INTEGER,
		access_hash     INTEGER,
		name            TEXT,
		type            TEXT,
		last_message_id INTEGER,
		avatar          BLOB,
		created_at      DATETIME,
		updated_at      DATETIME
	);`,
	// tasks 表：下载任务记录，config 字段以 JSON 字符串保存任务配置。
	`CREATE TABLE IF NOT EXISTS tasks (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		dialog_id        INTEGER,
		status           TEXT,
		scan_offset      INTEGER,
		total_files      INTEGER,
		completed_files  INTEGER,
		failed_files     INTEGER,
		config           TEXT,
		created_at       DATETIME,
		updated_at       DATETIME
	);`,
	// download_records 表：单个文件的下载记录，归属某个任务。
	// sort_order / episode_number / episode_raw 由后续 ALTER 增量迁移添加（见 migrateStatements）。
	`CREATE TABLE IF NOT EXISTS download_records (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id     INTEGER,
		message_id  INTEGER,
		file_id     TEXT,
		file_name   TEXT,
		file_size   INTEGER,
		media_type  TEXT,
		status      TEXT,
		local_path  TEXT,
		error       TEXT,
		created_at  DATETIME
	);`,
	// task_templates 表：下载任务模板，config 字段以 JSON 字符串保存模板配置。
	`CREATE TABLE IF NOT EXISTS task_templates (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT,
		config      TEXT,
		created_at  DATETIME,
		updated_at  DATETIME
	);`,
	// proxy 表：代理配置（单行，id 固定）。
	`CREATE TABLE IF NOT EXISTS proxy (
		id          INTEGER PRIMARY KEY,
		type        TEXT,
		address     TEXT,
		port        INTEGER,
		username    TEXT,
		password    TEXT,
		enabled     INTEGER,
		created_at  DATETIME,
		updated_at  DATETIME
	);`,
	// settings 表：键值对形式的通用应用设置。
	`CREATE TABLE IF NOT EXISTS settings (
		key         TEXT PRIMARY KEY,
		value       TEXT,
		updated_at  DATETIME
	);`,

	// ===== 视频元数据相关表 =====
	// 以下表结构镜像服务器端 MySQL 表，列名与服务器完全对齐，
	// 作为后续将服务器接口交互迁移到本地 SQLite 检索的 schema 基线。
	// 仅建结构，不含业务逻辑与索引；查询模式确定后再按需补充索引。

	// video_repo 视频主体表（对应服务器 video_repo）。
	// 承载 SearchVideos / GetVideoDetail / CreateVideoRepo 等接口的数据。
	`CREATE TABLE IF NOT EXISTS video_repo (
		vr_id            INTEGER PRIMARY KEY AUTOINCREMENT,
		vr_name          TEXT,
		vr_desc          TEXT,
		vr_cover         TEXT,
		vr_category      TEXT,
		vr_episode_count INTEGER,
		vr_now_count     INTEGER,
		vr_year          INTEGER,
		vr_season        TEXT,
		vr_view_count    INTEGER,
		vr_status        INTEGER,
		vr_language      INTEGER,
		vr_bgm_id        INTEGER,
		update_time      INTEGER
	);`,
	// video_episode 视频分集表（对应服务器 video_episode）。
	// 承载 ListEpisodes / UploadEpisodes 接口的数据。
	// vs_channel_id + ve_message_id 为 TG 回源拉流的定位键。
	`CREATE TABLE IF NOT EXISTS video_episode (
		ve_id          INTEGER PRIMARY KEY AUTOINCREMENT,
		vr_id          INTEGER,
		vs_channel_id  INTEGER,
		ve_message_id  INTEGER,
		ve_title       TEXT,
		ve_collect     INTEGER,
		ve_status      INTEGER
	);`,
	// video_category 视频分类字典表（对应服务器 video_category）。
	// 承载 GetBaseInfo 接口的分类下拉选项。
	`CREATE TABLE IF NOT EXISTS video_category (
		vc_id   INTEGER PRIMARY KEY AUTOINCREMENT,
		vc_name TEXT
	);`,
	// video_language 视频语种字典表（对应服务器 video_language）。
	// 承载 GetBaseInfo 接口的语种下拉选项。
	`CREATE TABLE IF NOT EXISTS video_language (
		vl_id   INTEGER PRIMARY KEY AUTOINCREMENT,
		vl_name TEXT
	);`,
	// video_tag 视频标签字典表（对应服务器 video_tag）。
	// 承载 GetBaseInfo 接口的标签下拉选项。
	`CREATE TABLE IF NOT EXISTS video_tag (
		vt_id   INTEGER PRIMARY KEY AUTOINCREMENT,
		vt_name TEXT
	);`,
	// video_tag_relation 视频与标签关联表（对应服务器 video_tag_relation）。
	// 承载 CreateVideoRepo 接口写入的标签关联。
	`CREATE TABLE IF NOT EXISTS video_tag_relation (
		vtr_id INTEGER PRIMARY KEY AUTOINCREMENT,
		vr_id  INTEGER,
		vt_id  INTEGER
	);`,
	// video_user_history 观看历史表（对应服务器 video_user_history）。
	// 承载 GetHistory / ReportHistory / ListHistory 接口的数据。
	// 本地视角为单机用户记录，user_id 不单独建列。
	`CREATE TABLE IF NOT EXISTS video_user_history (
		vuh_id           INTEGER PRIMARY KEY AUTOINCREMENT,
		vr_id            INTEGER,
		ve_id            INTEGER,
		vuh_history_time INTEGER,
		vuh_duration     INTEGER,
		finished         INTEGER,
		update_time      INTEGER
	);`,
	// video_follow 追番关系表（对应服务器 video_follow）。
	// 承载 FollowVideo / ListFavorites 接口的数据。
	`CREATE TABLE IF NOT EXISTS video_follow (
		vf_id       INTEGER PRIMARY KEY AUTOINCREMENT,
		vr_id       INTEGER,
		create_time INTEGER
	);`,
	// video_discuss 评论表（对应服务器 video_discuss）。
	// 承载 ListDiscuss / PostDiscuss / ListMyDiscuss 接口的数据。
	// parent_id=0 表示顶级评论。
	`CREATE TABLE IF NOT EXISTS video_discuss (
		vrd_id            INTEGER PRIMARY KEY AUTOINCREMENT,
		ve_id             INTEGER,
		vrd_content       TEXT,
		parent_id         INTEGER,
		reply_count       INTEGER,
		create_time       INTEGER
	);`,
	// video_danmaku 弹幕表（对应服务器 video_danmaku）。
	// 承载 ListDanmaku / PostDanmaku 接口的数据。
	// vd_mode：1=滚动，2=顶部，3=底部；vd_time 单位毫秒。
	`CREATE TABLE IF NOT EXISTS video_danmaku (
		vd_id      INTEGER PRIMARY KEY AUTOINCREMENT,
		ve_id      INTEGER,
		vd_content TEXT,
		vd_time    INTEGER,
		vd_mode    INTEGER,
		vd_color   TEXT,
		create_time INTEGER
	);`,
	// video_source 视频源频道表（对应服务器 video_source）。
	// 承载 ListRepoSources / SaveRepoChannel 接口的数据。
	// vs_channel_id 对应 TG peer_id，vs_username 为 @xxx（可空表示私人频道）。
	`CREATE TABLE IF NOT EXISTS video_source (
		vs_id          INTEGER PRIMARY KEY AUTOINCREMENT,
		vs_name        TEXT,
		vs_channel_id  INTEGER,
		vs_username    TEXT
	);`,
}

// migrateStatements 是增量迁移语句列表。
// 由于 SQLite 的 ALTER TABLE ADD COLUMN 不支持 IF NOT EXISTS，
// 这里通过捕获 "duplicate column name" 错误来兼容已存在的字段，
// 实现幂等的迁移（重复执行不会报错）。
//
// 新增字段时优先追加到此处，并在对应模型增加字段。
var migrateStatements = []string{
	// download_records 表新增 sort_order：排序顺序（从 1 开始）
	`ALTER TABLE download_records ADD COLUMN sort_order INTEGER DEFAULT 0;`,
	// download_records 表新增 episode_number：集数（可空）
	`ALTER TABLE download_records ADD COLUMN episode_number INTEGER;`,
	// download_records 表新增 episode_raw：集数 capture group 原始字符串
	`ALTER TABLE download_records ADD COLUMN episode_raw TEXT;`,
	// download_records 表新增 downloaded_bytes：已下载字节数（暂停时持久化，恢复时用于断点续传）
	`ALTER TABLE download_records ADD COLUMN downloaded_bytes INTEGER DEFAULT 0;`,
	// dialogs 表新增 username：频道/用户用户名（@xxx，可空）
	`ALTER TABLE dialogs ADD COLUMN username TEXT;`,
	// tasks 表新增 vr_id：关联视频源 video_repo.vr_id（选集下载场景填充）
	`ALTER TABLE tasks ADD COLUMN vr_id INTEGER DEFAULT 0;`,
	// tasks 表新增 vr_name：视频标题（下载页展示用，避免二次查询）
	`ALTER TABLE tasks ADD COLUMN vr_name TEXT DEFAULT '';`,
	// tasks 表新增 vr_cover：视频封面相对路径（下载页展示用）
	`ALTER TABLE tasks ADD COLUMN vr_cover TEXT DEFAULT '';`,
	// video_repo 表新增 vr_bgm_id：bangumi 条目 ID（用于回溯 bangumi 元数据，可空）
	`ALTER TABLE video_repo ADD COLUMN vr_bgm_id INTEGER;`,
}

// InitDB 初始化 SQLite 数据库。它打开位于 dbPath 的数据库文件，
// 并执行所有建表语句（均使用 IF NOT EXISTS，可安全重复执行），
// 随后执行增量迁移语句（ALTER TABLE，捕获字段已存在错误以保证幂等）。
// 返回的 *sql.DB 由调用方负责关闭。
func InitDB(dbPath string) (*sql.DB, error) {
	// 构造 DSN：通过 _pragma 查询参数为连接池中每个连接设置 SQLite pragma。
	//   - busy_timeout(5000)：写入冲突时等待 5 秒而非立即返回 SQLITE_BUSY。
	//     此前未设置（默认 0），3 个 worker 并发暂停持久化时部分 UPDATE
	//     因锁冲突失败（error 被 _ 忽略），downloaded_bytes 未写入 → 恢复时进度归 0。
	//   - journal_mode(WAL)：WAL 模式支持并发读 + 单写入，写入不阻塞读。
	//   - synchronous(NORMAL)：WAL 模式下 NORMAL 足够安全且性能更好。
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// 依次执行所有建表语句，确保表结构存在。
	for _, stmt := range schemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}

	// 执行增量迁移语句，捕获 "duplicate column" 错误以保证幂等。
	for _, stmt := range migrateStatements {
		if _, err := db.Exec(stmt); err != nil {
			// SQLite 在字段已存在时返回 "duplicate column name" 错误，忽略即可
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				db.Close()
				return nil, err
			}
		}
	}

	return db, nil
}
