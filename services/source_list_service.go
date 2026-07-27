// Package services 提供 Wails3 应用的后端服务层。
// 本文件定义 SourceListService，负责从 GitHub Raw URL 拉取视频源频道清单，
// 写入本地 video_source 表，并自动订阅未加入的 TG 频道。
package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"tg-download/internal/api"
	"tg-download/internal/settings"
	"tg-download/internal/telegram"
)

// SourceListItem 清单单条项，字段直接对应 video_source 表列。
// vs_username 必填（订阅用）；vs_name 与 vs_channel_id 可选。
type SourceListItem struct {
	VsName      string `json:"vs_name"`
	VsChannelID int64  `json:"vs_channel_id"`
	VsUsername  string `json:"vs_username"`
}

// SourceListSyncResult 同步结果汇总，供前端展示。
type SourceListSyncResult struct {
	Total      int `json:"total"`       // 清单总条数
	Upserted   int `json:"upserted"`    // 写入/更新条数
	Subscribed int `json:"subscribed"`  // 本次新订阅条数
	Failed     int `json:"failed"`      // 订阅失败条数
}

// SourceListService 是 Wails 绑定的视频源清单同步服务。
// 依赖 settings 读取清单 URL，依赖 telegram manager 执行订阅。
type SourceListService struct {
	db      *sql.DB
	manager *telegram.ClientManager
	http    *http.Client
}

// NewSourceListService 创建 SourceListService 实例。
// manager 可为 nil（未登录时仍可拉取清单并写表，仅跳过订阅步骤）。
func NewSourceListService(db *sql.DB, manager *telegram.ClientManager) *SourceListService {
	return &SourceListService{
		db:      db,
		manager: manager,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// SyncSourceList 执行一次完整的清单同步流程：
//  1. 从 settings 读取 source_list_url（走 LoadSettings，缺失时用默认值），为空直接返回零值结果
//  2. HTTP GET 拉取 JSON 清单
//  3. 逐条 UPSERT 到 video_source 表（按 vs_channel_id 去重，0 时按 vs_username）
//  4. 若 manager 已登录，拉取本地 dialogs，对清单中未订阅的 username 逐个订阅
//  5. 订阅完成后刷新 dialogs 缓存
//
// 拉取失败直接返回 error；单条订阅失败仅累加 Failed 计数，不中断流程。
func (s *SourceListService) SyncSourceList() (*SourceListSyncResult, error) {
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	// 读 settings：走 LoadSettings 拿带默认值的完整配置，避免 DB 无 key 时取空值
	cfg, err := settings.LoadSettings(s.db)
	if err != nil {
		return nil, fmt.Errorf("读取 settings 失败: %w", err)
	}
	url := strings.TrimSpace(cfg.SourceListURL)
	if url == "" {
		// 未配置清单 URL，视为未启用，返回空结果
		return &SourceListSyncResult{}, nil
	}

	// 同步前刷新代理：复用 proxy 表配置（与 TG / bangumi 同一份），确保 GitHub raw 可达
	if err := s.applyProxy(); err != nil {
		log.Printf("[source-list] 应用代理配置失败（将尝试直连）: %v", err)
	}

	// 1. 拉取清单
	items, err := s.fetchList(url)
	if err != nil {
		return nil, fmt.Errorf("拉取清单失败: %w", err)
	}

	result := &SourceListSyncResult{Total: len(items)}
	if len(items) == 0 {
		return result, nil
	}

	// 2. UPSERT 到 video_source 表
	for _, it := range items {
		if err := s.upsertSource(it); err != nil {
			log.Printf("[source-list] 写入 video_source 失败 (username=%s): %v", it.VsUsername, err)
			continue
		}
		result.Upserted++
	}

	// 3. 自动订阅未加入的频道
	if s.manager == nil || !s.manager.IsAuthenticated() {
		// 未登录或 manager 未初始化，跳过订阅
		return result, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dialogs, err := s.manager.FetchDialogs(ctx)
	if err != nil {
		log.Printf("[source-list] 拉取本地 dialogs 失败，跳过订阅步骤: %v", err)
		return result, nil
	}

	// 收集已订阅的 username（小写比较）
	subscribedSet := make(map[string]struct{}, len(dialogs))
	for _, d := range dialogs {
		if d.Type == telegram.DialogTypeChannel && d.Username != "" {
			subscribedSet[strings.ToLower(d.Username)] = struct{}{}
		}
	}

	for _, it := range items {
		uname := strings.TrimSpace(it.VsUsername)
		if uname == "" {
			continue
		}
		key := strings.ToLower(strings.TrimPrefix(uname, "@"))
		if _, ok := subscribedSet[key]; ok {
			continue
		}
		// 单个频道订阅：超时 60s，失败继续下一个
		subCtx, subCancel := context.WithTimeout(ctx, 60*time.Second)
		if err := s.manager.SubscribeChannel(subCtx, uname); err != nil {
			log.Printf("[source-list] 订阅频道 %s 失败: %v", uname, err)
			result.Failed++
		} else {
			result.Subscribed++
		}
		subCancel()
	}

	// 4. 订阅后刷新 dialogs 缓存（失败仅日志）
	if result.Subscribed > 0 {
		if _, err := s.manager.RefreshDialogs(ctx); err != nil {
			log.Printf("[source-list] 订阅后刷新 dialogs 缓存失败: %v", err)
		}
	}

	return result, nil
}

// fetchList 从指定 URL 拉取 JSON 清单并解析。
func (s *SourceListService) fetchList(rawURL string) ([]SourceListItem, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	var items []SourceListItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	return items, nil
}

// upsertSource 写入单条 video_source 记录。
// 去重键：vs_channel_id > 0 时按 channel_id，否则按非空 vs_username。
// video_source 表无 UNIQUE 约束，用手动 SELECT + INSERT/UPDATE 实现 UPSERT。
func (s *SourceListService) upsertSource(it SourceListItem) error {
	if s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if it.VsChannelID == 0 && it.VsUsername == "" {
		return fmt.Errorf("vs_channel_id 与 vs_username 均为空")
	}

	var existingID int64
	if it.VsChannelID > 0 {
		err := s.db.QueryRow(
			`SELECT vs_id FROM video_source WHERE vs_channel_id = ?`,
			it.VsChannelID,
		).Scan(&existingID)
		if err == nil {
			_, err = s.db.Exec(
				`UPDATE video_source SET vs_name = ?, vs_username = ? WHERE vs_id = ?`,
				it.VsName, it.VsUsername, existingID,
			)
			return err
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("查询 video_source 失败: %w", err)
		}
	} else {
		err := s.db.QueryRow(
			`SELECT vs_id FROM video_source WHERE vs_username = ? AND vs_channel_id = 0`,
			it.VsUsername,
		).Scan(&existingID)
		if err == nil {
			_, err = s.db.Exec(
				`UPDATE video_source SET vs_name = ? WHERE vs_id = ?`,
				it.VsName, existingID,
			)
			return err
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("查询 video_source 失败: %w", err)
		}
	}

	_, err := s.db.Exec(
		`INSERT INTO video_source (vs_name, vs_channel_id, vs_username) VALUES (?, ?, ?)`,
		it.VsName, it.VsChannelID, it.VsUsername,
	)
	return err
}

// applyProxy 从 proxy 表读取代理配置并应用到 s.http。
// 复用 telegram.ProxyConfig（与 TG / bangumi 同一份配置），确保拉取 GitHub raw 时走代理。
// 未启用代理或读取失败时回退为直连 client（不返回 error，仅日志）。
func (s *SourceListService) applyProxy() error {
	if s.db == nil {
		return nil
	}
	cfg, err := telegram.LoadProxy(s.db)
	if err != nil {
		return fmt.Errorf("读取代理配置失败: %w", err)
	}
	if !cfg.Enabled || cfg.Type == "" || cfg.Address == "" || cfg.Port <= 0 {
		// 未启用代理：恢复直连 transport
		s.http = &http.Client{Timeout: 30 * time.Second}
		return nil
	}
	tr, err := api.BuildProxyTransport(string(cfg.Type), cfg.Address, cfg.Port, cfg.Username, cfg.Password)
	if err != nil {
		return fmt.Errorf("构建代理 transport 失败: %w", err)
	}
	s.http = &http.Client{
		Timeout:   30 * time.Second,
		Transport: tr,
	}
	return nil
}
