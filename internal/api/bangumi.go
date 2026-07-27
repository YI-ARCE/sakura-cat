// Package api 提供外部平台 API 的 Go 客户端封装。
// 本文件实现 Bangumi (bgm.tv) v0 API 客户端，用于条目元数据检索。
//
// 基础地址：https://api.bgm.tv
// 鉴权：v0 读接口大多可选 Bearer token，无 token 也可用（看不到 NSFW 且有频率限制）。
// 本客户端默认不带 token；如需提高速率上限或获取 R18，后续可在 Client 注入 token。
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// BangumiDefaultBaseURL Bangumi v0 API 默认地址。
const BangumiDefaultBaseURL = "https://api.bgm.tv"

// truncate 截断字节切片用于错误展示。
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// BangumiDefaultTimeout Bangumi 请求默认超时。
const BangumiDefaultTimeout = 30 * time.Second

// BangumiClient Bangumi (bgm.tv) v0 API 客户端。
// 独立于视频提交服务的 Client，base URL、鉴权方式均不同。
type BangumiClient struct {
	baseURL   string
	http      *http.Client
	token     string // 可选 Access Token，为空时不带鉴权头
	userAgent string // User-Agent，由前端透传，bangumi 要求 UA 格式为 用户名/应用名
}

// NewBangumiClient 创建 Bangumi 客户端。
// baseURL 为空时使用 BangumiDefaultBaseURL。
func NewBangumiClient(baseURL string) *BangumiClient {
	if baseURL == "" {
		baseURL = BangumiDefaultBaseURL
	}
	if len(baseURL) > 0 && baseURL[len(baseURL)-1] == '/' {
		baseURL = baseURL[:len(baseURL)-1]
	}
	return &BangumiClient{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: BangumiDefaultTimeout,
		},
	}
}

// SetToken 设置可选 Access Token。
// 设置后所有请求会携带 Authorization: Bearer <token> 头。
func (c *BangumiClient) SetToken(token string) {
	c.token = token
}

// SetUserAgent 设置 User-Agent（由前端透传）。
// bangumi 要求 UA 格式为 用户名/应用名，为空时请求会被 bangumi 拦截。
func (c *BangumiClient) SetUserAgent(ua string) {
	c.userAgent = ua
}

// userAgentValue 返回当前 UA（纯透传，不再兜底默认值）。
func (c *BangumiClient) userAgentValue() string {
	return c.userAgent
}

// SetProxy 设置代理。
// proxyType: socks5 / http / https；为空或 enabled=false 时直连。
// 复用 telegram.ProxyConfig 的字段语义。
func (c *BangumiClient) SetProxy(proxyType, address string, port int, username, password string, enabled bool) error {
	if !enabled || proxyType == "" || address == "" || port <= 0 {
		// 清除代理：恢复直连 transport
		c.http = &http.Client{Timeout: BangumiDefaultTimeout}
		return nil
	}
	tr, err := BuildProxyTransport(proxyType, address, port, username, password)
	if err != nil {
		return err
	}
	c.http = &http.Client{
		Timeout:   BangumiDefaultTimeout,
		Transport: tr,
	}
	return nil
}

// BuildProxyTransport 按代理类型构建 http.RoundTripper。
// socks5 走 golang.org/x/net/proxy；http/https 走 http.Transport.Proxy。
// 公开供 services 层复用（如 SourceListService 拉取 GitHub 清单时走同一份代理配置）。
func BuildProxyTransport(proxyType, address string, port int, username, password string) (http.RoundTripper, error) {
	host := fmt.Sprintf("%s:%d", address, port)
	switch proxyType {
	case "socks5":
		var auth *proxy.Auth
		if username != "" {
			auth = &proxy.Auth{User: username, Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("构建 socks5 代理失败: %w", err)
		}
		return &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}, nil
	case "http", "https":
		proxyURL := &url.URL{
			Scheme: proxyType,
			Host:   host,
		}
		if username != "" {
			proxyURL.User = url.UserPassword(username, password)
		}
		return &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}, nil
	default:
		return nil, fmt.Errorf("不支持的代理类型: %s", proxyType)
	}
}

// BaseURL 返回当前基础地址。
func (c *BangumiClient) BaseURL() string {
	return c.baseURL
}

// doBangumi 执行 Bangumi API 请求并解析 JSON。
// Bangumi 响应体非 {code,msg,data} 包装，直接是数据对象本身。
func (c *BangumiClient) doBangumi(req *http.Request, out interface{}) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgentValue())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bangumi 请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取 bangumi 响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bangumi HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("解析 bangumi 响应失败: %w (raw=%s)", err, truncate(raw, 200))
		}
	}
	return nil
}

// ===================== 类型定义 =====================
// 字段对照 api.yaml 的 components/schemas，仅保留前端需要的子集。

// BangumiSubjectType 条目类型枚举。
// 1=书籍 2=动画 3=音乐 4=游戏 6=三次元（没有 5）。
type BangumiSubjectType int

const (
	BangumiSubjectBook  BangumiSubjectType = 1
	BangumiSubjectAnime BangumiSubjectType = 2
	BangumiSubjectMusic BangumiSubjectType = 3
	BangumiSubjectGame  BangumiSubjectType = 4
	BangumiSubjectReal  BangumiSubjectType = 6
)

// BangumiImages 条目封面图，包含多种尺寸 URL。
type BangumiImages struct {
	Large  string `json:"large"`
	Common string `json:"common"`
	Medium string `json:"medium"`
	Small  string `json:"small"`
	Grid   string `json:"grid"`
}

// BangumiRating 评分聚合。
type BangumiRating struct {
	Rank  int     `json:"rank"`
	Total int     `json:"total"`
	Score float64 `json:"score"`
	Count map[string]int `json:"count"` // "1"~"10" 各分数段人数
}

// BangumiCollection 收藏人数聚合。
type BangumiCollection struct {
	Wish    int `json:"wish"`
	Collect int `json:"collect"`
	Doing   int `json:"doing"`
	OnHold  int `json:"on_hold"`
	Dropped int `json:"dropped"`
}

// BangumiTag 条目标签。
type BangumiTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// BangumiSubject Bangumi 条目。
type BangumiSubject struct {
	ID            int                 `json:"id"`
	Type          int                 `json:"type"`
	Name          string              `json:"name"`
	NameCN        string              `json:"name_cn"`
	Summary       string              `json:"summary"`
	Series        bool                `json:"series"`
	NSFW          bool                `json:"nsfw"`
	Locked        bool                `json:"locked"`
	Date          string              `json:"date"`            // YYYY-MM-DD
	Platform      string              `json:"platform"`        // TV, Web, 欧美剧...
	Images        *BangumiImages      `json:"images"`
	Volumes       int                 `json:"volumes"`
	Eps           int                 `json:"eps"`
	TotalEpisodes int                 `json:"total_episodes"`
	Rating        *BangumiRating      `json:"rating"`
	Collection    *BangumiCollection  `json:"collection"`
	MetaTags      []string            `json:"meta_tags"`
	Tags          []BangumiTag        `json:"tags"`
}

// BangumiPagedSubject 分页条目列表（GET /v0/subjects、POST /v0/search/subjects 共用）。
type BangumiPagedSubject struct {
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
	Data   []BangumiSubject  `json:"data"`
}

// BangumiBrowseRequest 浏览条目查询参数。
// 对照 GET /v0/subjects：type 必填，其余可选。
type BangumiBrowseRequest struct {
	Type    BangumiSubjectType // 必填，条目类型
	Cat     string             // 可选，条目分类（动画下 TV/OVA/Movie/WEB）
	Series  bool               // 可选，仅书籍有效
	Plat    string             // 可选，仅游戏有效
	Sort    string             // 可选，date / rank
	Year    int                // 可选，年份
	Month   int                // 可选，月份 1~12
	Limit   int                // 可选，≤50，默认 30
	Offset  int                // 可选，默认 0
}

// BrowseSubjects 浏览条目。
// GET /v0/subjects?type=&cat=&year=&month=&sort=&limit=&offset=
// 第一页 cache 24h，之后 cache 1h。
func (c *BangumiClient) BrowseSubjects(req BangumiBrowseRequest) (*BangumiPagedSubject, error) {
	if req.Type == 0 {
		return nil, fmt.Errorf("type 不能为空")
	}
	q := url.Values{}
	q.Set("type", strconv.Itoa(int(req.Type)))
	if req.Cat != "" {
		q.Set("cat", req.Cat)
	}
	if req.Series {
		q.Set("series", "true")
	}
	if req.Plat != "" {
		q.Set("platform", req.Plat)
	}
	if req.Sort != "" {
		q.Set("sort", req.Sort)
	}
	if req.Year > 0 {
		q.Set("year", strconv.Itoa(req.Year))
	}
	if req.Month > 0 {
		q.Set("month", strconv.Itoa(req.Month))
	}
	if req.Limit <= 0 {
		req.Limit = 30
	}
	if req.Limit > 50 {
		req.Limit = 50
	}
	q.Set("limit", strconv.Itoa(req.Limit))
	q.Set("offset", strconv.Itoa(req.Offset))

	path := "/v0/subjects?" + q.Encode()
	httpReq, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 bangumi 浏览请求失败: %w", err)
	}
	var out BangumiPagedSubject
	if err := c.doBangumi(httpReq, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		out.Data = []BangumiSubject{}
	}
	return &out, nil
}

// GetSubject 获取条目详情。
// GET /v0/subjects/{subject_id}，cache 300s。
func (c *BangumiClient) GetSubject(subjectID int) (*BangumiSubject, error) {
	if subjectID <= 0 {
		return nil, fmt.Errorf("subject_id 非法")
	}
	path := fmt.Sprintf("/v0/subjects/%d", subjectID)
	httpReq, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 bangumi 详情请求失败: %w", err)
	}
	var out BangumiSubject
	if err := c.doBangumi(httpReq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BangumiEpType 章节类型枚举。
// 0=本篇 1=SP 2=OP 3=ED 4=PV 5=MAD 6=其他。
type BangumiEpType int

// BangumiEpisode 章节。
type BangumiEpisode struct {
	ID             int     `json:"id"`
	Type           int     `json:"type"`
	Name           string  `json:"name"`
	NameCN         string  `json:"name_cn"`
	Sort           float64 `json:"sort"`
	Ep             float64 `json:"ep"`
	Airdate        string  `json:"airdate"`
	Comment        int     `json:"comment"`
	Duration       string  `json:"duration"`
	Desc           string  `json:"desc"`
	Disc           int     `json:"disc"`
	DurationSeconds int    `json:"duration_seconds"`
}

// BangumiPagedEpisode 分页章节列表。
type BangumiPagedEpisode struct {
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
	Data   []BangumiEpisode  `json:"data"`
}

// GetEpisodes 获取条目章节列表。
// GET /v0/episodes?subject_id=&type=&limit=&offset=
func (c *BangumiClient) GetEpisodes(subjectID int, epType BangumiEpType, limit, offset int) (*BangumiPagedEpisode, error) {
	if subjectID <= 0 {
		return nil, fmt.Errorf("subject_id 非法")
	}
	q := url.Values{}
	q.Set("subject_id", strconv.Itoa(subjectID))
	if epType >= 0 {
		q.Set("type", strconv.Itoa(int(epType)))
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))

	path := "/v0/episodes?" + q.Encode()
	httpReq, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 bangumi 章节请求失败: %w", err)
	}
	var out BangumiPagedEpisode
	if err := c.doBangumi(httpReq, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		out.Data = []BangumiEpisode{}
	}
	return &out, nil
}

// BangumiImageResponse 下载图片响应。
// 由后端代理下载 bangumi 图片，返回字节给前端，避免前端 CORS 问题。
type BangumiImageResponse struct {
	FileName string `json:"file_name"` // 文件名（含扩展名）
	Bytes    []byte `json:"bytes"`     // 图片内容字节数组
	Size     int    `json:"size"`      // 字节数
	MimeType string `json:"mime_type"` // MIME 类型
}

// DownloadImage 代理下载 bangumi 图片。
// imageUrl 为 bangumi 图片直链（如 lain.bgm.tv 域）。
// 后端下载后返回字节，前端再走 UploadFile 上传到自己的存储，得到本地 path。
func (c *BangumiClient) DownloadImage(imageURL string) (*BangumiImageResponse, error) {
	if strings.TrimSpace(imageURL) == "" {
		return nil, fmt.Errorf("imageUrl 不能为空")
	}
	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建图片下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgentValue())
	req.Header.Set("Accept", "image/*")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 bangumi 图片失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载图片 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取图片内容失败: %w", err)
	}

	// 从 URL 路径提取文件名
	fileName := "bangumi_cover.jpg"
	if u, perr := url.Parse(imageURL); perr == nil {
		segs := strings.Split(strings.TrimRight(u.Path, "/"), "/")
		if len(segs) > 0 && segs[len(segs)-1] != "" {
			fileName = segs[len(segs)-1]
		}
	}
	// 确保扩展名
	if !strings.Contains(fileName, ".") {
		fileName += ".jpg"
	}

	return &BangumiImageResponse{
		FileName: fileName,
		Bytes:    data,
		Size:     len(data),
		MimeType: resp.Header.Get("Content-Type"),
	}, nil
}
