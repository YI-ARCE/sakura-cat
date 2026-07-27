// Package services 提供 Wails3 应用的后端服务层。
// 本文件实现"一起看"功能的临时下载器:
// 单文件 + 240 块位图 + 三种下载模式(顺序填充/追播放头/预载下一集)。
// 复用 internal/download 包的 TG 拉流原语,不重新实现网络层。
package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"tg-download/internal/download"
	"tg-download/internal/telegram"
)

// DownloadMode 下载模式
type DownloadMode int

const (
	// ModeA 顺序填充:从块 0 开始顺序下载到文件末尾
	ModeA DownloadMode = iota
	// ModeB 追播放头:从指定块开始下载到末尾,然后回头从最低未下载块继续填充
	ModeB
	// ModeC 预载下一集:由 PartyService 通过另起 PartyDownloader 实例处理,本实例不参与预载
	ModeC
)

// TotalBlocks 总块数,固定 240 块(对应前端 15×16 区块图)
const TotalBlocks = 240

// PartyDownloader "一起看"临时下载器。
// 每个实例对应一个分集缓存文件,将文件划分为 240 块并维护位图记录下载进度。
// 复用 internal/download 包的 TG 拉流原语,不重新实现网络层。
type PartyDownloader struct {
	veID      int64
	channelID int64
	messageID int64
	fileSize  int64
	filePath  string
	file      *os.File

	blocks    [TotalBlocks]bool // 位图:true 表示该块已下载完成
	blockSize int64             // 每块字节数 = ceil(fileSize/240)

	currentBlock int // 当前正在下载的块索引,-1 表示无活动下载
	playingBlock int // 当前播放块(由 WS seek 事件更新)

	mode       DownloadMode // 当前下载模式
	modeBStart int          // 模式 B 的起始块(仅 mode=ModeB 时有效,-1 表示未设置)

	speed int64 // 字节/秒,滑动平均

	cancel context.CancelFunc
	mu     sync.Mutex

	db      *sql.DB
	manager *telegram.ClientManager

	// 缓存的媒体下载位置(FileReference 会过期,失败时通过 refreshLocation 刷新)
	location tg.InputFileLocationClass

	// onBlocksUpdate 每完成一块下载后调用,供 Wails Event 推送给前端
	onBlocksUpdate func()
}

// NewPartyDownloader 构造下载器实例。
// 创建缓存文件 <cacheDir>/<veID>.mp4,用 Truncate 预分配 fileSize 字节空间(跨平台 ftruncate 语义),
// 计算 blockSize = ceil(fileSize/240),默认 mode=ModeA。
//
// fileSize 为 0 时仍创建空文件,等首次 refreshLocation 拉到真实大小后重算 blockSize 并补做 Truncate。
func NewPartyDownloader(db *sql.DB, manager *telegram.ClientManager, cacheDir string, veID, channelID, messageID, fileSize int64) (*PartyDownloader, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("创建缓存目录失败: %w", err)
	}
	filePath := filepath.Join(cacheDir, fmt.Sprintf("%d.mp4", veID))
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("创建缓存文件失败: %w", err)
	}
	// 预分配文件空间(Truncate 实现 ftruncate 语义,跨平台)
	if fileSize > 0 {
		if err := f.Truncate(fileSize); err != nil {
			f.Close()
			return nil, fmt.Errorf("预分配文件空间失败: %w", err)
		}
	}

	blockSize := int64(0)
	if fileSize > 0 {
		blockSize = (fileSize + TotalBlocks - 1) / TotalBlocks
	}

	return &PartyDownloader{
		veID:         veID,
		channelID:    channelID,
		messageID:    messageID,
		fileSize:     fileSize,
		filePath:     filePath,
		file:         f,
		blockSize:    blockSize,
		mode:         ModeA,
		modeBStart:   -1,
		currentBlock: -1,
		db:           db,
		manager:      manager,
	}, nil
}

// Start 启动后台下载 goroutine,按当前模式选取下一块并下载。
// 已启动则无操作。goroutine 在 Cancel 时退出。
func (p *PartyDownloader) Start() {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.mu.Unlock()

	go p.run(ctx)
}

// run 下载主循环。每次循环选取下一块并下载,失败时退避重试。
func (p *PartyDownloader) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		p.mu.Lock()
		next := p.nextBlockLocked()
		p.currentBlock = next
		p.mu.Unlock()

		if next < 0 {
			// 所有块已下载完成,等待取消或新指令(模式切换可能引入新需求)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		if err := p.downloadBlock(ctx, next); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[party-dl] 下载块 %d 失败 veID=%d: %v", next, p.veID, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}

		p.mu.Lock()
		p.blocks[next] = true
		p.currentBlock = next
		cb := p.onBlocksUpdate
		p.mu.Unlock()
		if cb != nil {
			cb()
		}
	}
}

// nextBlockLocked 根据当前模式返回下一块索引;所有块都完成时返回 -1。
// 调用方必须持有 p.mu。
func (p *PartyDownloader) nextBlockLocked() int {
	switch p.mode {
	case ModeA:
		for i := 0; i < TotalBlocks; i++ {
			if !p.blocks[i] {
				return i
			}
		}
		return -1
	case ModeB:
		// Phase 1:从 modeBStart 开始向后找未下载块,直到文件末尾
		start := p.modeBStart
		if start < 0 {
			start = 0
		}
		for i := start; i < TotalBlocks; i++ {
			if !p.blocks[i] {
				return i
			}
		}
		// Phase 2:回头从最低未下载块继续填充(覆盖 modeBStart 之前的空洞)
		for i := 0; i < start; i++ {
			if !p.blocks[i] {
				return i
			}
		}
		return -1
	default:
		// ModeC 由 PartyService 通过另起实例处理,这里按顺序填充兜底
		for i := 0; i < TotalBlocks; i++ {
			if !p.blocks[i] {
				return i
			}
		}
		return -1
	}
}

// downloadBlock 拉取指定块并写入文件对应位置,更新速度统计。
// 内部使用 StreamRangeToWriter 从 TG 拉取,失败时刷新 location 后重试一次。
func (p *PartyDownloader) downloadBlock(ctx context.Context, blockIndex int) error {
	p.mu.Lock()
	blockSize := p.blockSize
	fileSize := p.fileSize
	p.mu.Unlock()

	// fileSize/blockSize 未就绪:先拉一次 location 同步真实大小
	if blockSize <= 0 || fileSize <= 0 {
		if err := p.refreshLocation(ctx); err != nil {
			return fmt.Errorf("刷新元数据失败: %w", err)
		}
		p.mu.Lock()
		blockSize = p.blockSize
		fileSize = p.fileSize
		p.mu.Unlock()
		if blockSize <= 0 || fileSize <= 0 {
			return fmt.Errorf("fileSize 未就绪")
		}
	}

	start := int64(blockIndex) * blockSize
	if start >= fileSize {
		return nil
	}
	end := start + blockSize
	if end > fileSize {
		end = fileSize
	}
	length := end - start

	startTime := time.Now()
	written, err := p.streamRangeToFile(ctx, start, length)
	if err != nil {
		return err
	}
	elapsed := time.Since(startTime).Seconds()
	if elapsed > 0 && written > 0 {
		newSpeed := int64(float64(written) / elapsed)
		p.mu.Lock()
		if p.speed == 0 {
			p.speed = newSpeed
		} else {
			// 滑动平均,平滑速度显示
			p.speed = (p.speed*7 + newSpeed*3) / 10
		}
		p.mu.Unlock()
	}
	return nil
}

// streamRangeToFile 从 TG 拉取 [start, start+length) 字节并写入文件对应位置(加锁)。
// 失败时刷新 location 后重试一次(用全新的 writer 避免偏移错误)。
func (p *PartyDownloader) streamRangeToFile(ctx context.Context, start, length int64) (int64, error) {
	if err := p.ensureLocation(ctx); err != nil {
		return 0, err
	}
	api := p.api()
	if api == nil {
		return 0, fmt.Errorf("TG 客户端未就绪")
	}
	p.mu.Lock()
	location := p.location
	file := p.file
	p.mu.Unlock()
	if file == nil {
		return 0, fmt.Errorf("文件已关闭")
	}

	w := &fileAtWriter{file: file, offset: start, mu: &p.mu}
	written, err := download.StreamRangeToWriter(ctx, api, location, start, length, w)
	if err == nil {
		return written, nil
	}
	log.Printf("[party-dl] 拉取失败,刷新 location 后重试 start=%d length=%d: %v", start, length, err)
	if rerr := p.refreshLocation(ctx); rerr != nil {
		return written, err
	}
	p.mu.Lock()
	location = p.location
	p.mu.Unlock()
	// 重建 writer,确保偏移从 start 重新开始
	w = &fileAtWriter{file: file, offset: start, mu: &p.mu}
	return download.StreamRangeToWriter(ctx, api, location, start, length, w)
}

// api 返回当前 TG API 客户端,未就绪返回 nil。
func (p *PartyDownloader) api() *tg.Client {
	if p.manager == nil || !p.manager.IsAuthenticated() {
		return nil
	}
	client := p.manager.GetClient()
	if client == nil {
		return nil
	}
	return client.API()
}

// ensureLocation 惰性初始化缓存的 location;已缓存则无操作。
func (p *PartyDownloader) ensureLocation(ctx context.Context) error {
	p.mu.Lock()
	has := p.location != nil
	p.mu.Unlock()
	if has {
		return nil
	}
	return p.refreshLocation(ctx)
}

// refreshLocation 强制重新拉取消息元数据并更新缓存。
// 若拿到 contentLength > 0 且当前 fileSize=0,则同步更新 fileSize 与 blockSize,
// 并对文件做 Truncate 预分配。
func (p *PartyDownloader) refreshLocation(ctx context.Context) error {
	if p.manager == nil || !p.manager.IsAuthenticated() {
		return fmt.Errorf("TG 客户端未就绪")
	}
	client := p.manager.GetClient()
	if client == nil {
		return fmt.Errorf("TG 客户端未就绪")
	}
	location, _, contentLength, err := download.FetchMessageMediaInfo(ctx, p.db, client.API(), p.channelID, p.messageID)
	if err != nil {
		return fmt.Errorf("拉取媒体元数据失败: %w", err)
	}

	p.mu.Lock()
	p.location = location
	needTruncate := false
	if p.fileSize == 0 && contentLength > 0 {
		p.fileSize = contentLength
		p.blockSize = (contentLength + TotalBlocks - 1) / TotalBlocks
		needTruncate = true
	}
	file := p.file
	p.mu.Unlock()

	if needTruncate && file != nil {
		if err := file.Truncate(contentLength); err != nil {
			log.Printf("[party-dl] Truncate 失败 veID=%d size=%d: %v", p.veID, contentLength, err)
		}
	}
	return nil
}

// SwitchToModeB 切换到模式 B(追播放头)。
// 从 blockIndex 开始顺序下载到文件末尾,然后回头从最低未下载块继续填充。
// 越界值会被裁剪到 [0, TotalBlocks-1]。
func (p *PartyDownloader) SwitchToModeB(blockIndex int) {
	if blockIndex < 0 {
		blockIndex = 0
	}
	if blockIndex >= TotalBlocks {
		blockIndex = TotalBlocks - 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = ModeB
	p.modeBStart = blockIndex
	p.playingBlock = blockIndex
}

// SwitchToModeBByTime 根据播放时间(currentTime/duration)估算块索引并切到模式 B。
// duration <= 0 时回退到块 0;否则 blockIndex = floor(currentTime / duration * TotalBlocks)。
// 由 PartyService.buildWSHooks 的 OnSeek 钩子调用,无需 PartyService 知道 fileSize/blockSize。
func (p *PartyDownloader) SwitchToModeBByTime(currentTime, duration float64) {
	blockIndex := 0
	if duration > 0 && currentTime >= 0 {
		ratio := currentTime / duration
		if ratio > 1 {
			ratio = 1
		}
		blockIndex = int(ratio * float64(TotalBlocks))
		if blockIndex >= TotalBlocks {
			blockIndex = TotalBlocks - 1
		}
	}
	p.SwitchToModeB(blockIndex)
}

// GetFileSize 返回当前文件总大小(字节)。
// fileSize 为 0 表示尚未从 TG 拉到真实大小(由 refreshLocation 填充)。
func (p *PartyDownloader) GetFileSize() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fileSize
}

// StartPreloadNext 预载下一集(模式 C)。
// 实际由 PartyService 调度,本下载器只负责当前集;预载由另一个 PartyDownloader 实例处理。
// 本方法保留为占位,供未来扩展或外部一致性调用。
func (p *PartyDownloader) StartPreloadNext() {
	// no-op: 预载由 PartyService 创建独立 PartyDownloader 实例处理
}

// Cancel 停止下载 goroutine 并关闭文件句柄。多次调用安全。
// 调用后所有进行中的 ReadRange 读取也会失败(文件已关闭)。
func (p *PartyDownloader) Cancel() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	file := p.file
	p.file = nil
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if file != nil {
		_ = file.Close()
	}
}

// SetOnBlocksUpdate 设置块更新回调,供 Wails Event 推送。
// 回调在下载 goroutine 中调用,调用方应避免长时间阻塞。
func (p *PartyDownloader) SetOnBlocksUpdate(cb func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onBlocksUpdate = cb
}

// GetBlocks 返回当前状态快照:位图副本、当前播放块、当前下载块、下载速度。
// downloadingBlock 为 -1 表示无活动下载(所有块已完成或未启动)。
func (p *PartyDownloader) GetBlocks() (downloaded [TotalBlocks]bool, playingBlock int, downloadingBlock int, speed int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	downloaded = p.blocks
	playingBlock = p.playingBlock
	downloadingBlock = p.currentBlock
	speed = p.speed
	return
}

// SetPlayingBlock 由 WS seek 事件触发,更新当前播放块。
// 越界值会被裁剪到 [0, TotalBlocks-1]。
func (p *PartyDownloader) SetPlayingBlock(blockIndex int) {
	if blockIndex < 0 {
		blockIndex = 0
	}
	if blockIndex >= TotalBlocks {
		blockIndex = TotalBlocks - 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playingBlock = blockIndex
}

// ReadRange 读取 [start, end] 字节范围(闭区间),返回 io.Reader。
//   - end=-1 表示读到 EOF,实际结束位置为 fileSize-1
//   - 命中本地缓存:用 io.NewSectionReader 直接从文件读(ReadAt);
//   - 未命中:走 download.StreamRangeToWriter 从 TG 直流,同时双写到本地文件(加锁),
//     并异步触发模式 B(从 startBlock 开始);整段成功后标记涉及块为已下载。
func (p *PartyDownloader) ReadRange(start, end int64) (io.Reader, error) {
	p.mu.Lock()
	fileSize := p.fileSize
	blockSize := p.blockSize
	p.mu.Unlock()

	// fileSize/blockSize 未就绪:同步刷新一次
	if blockSize <= 0 || fileSize <= 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := p.refreshLocation(ctx); err != nil {
			return nil, fmt.Errorf("刷新元数据失败: %w", err)
		}
		p.mu.Lock()
		fileSize = p.fileSize
		blockSize = p.blockSize
		p.mu.Unlock()
	}

	if start < 0 || start >= fileSize {
		return nil, fmt.Errorf("非法字节起始位置 start=%d size=%d", start, fileSize)
	}
	// end=-1 表示读到 EOF,裁剪到 fileSize-1
	if end < 0 {
		end = fileSize - 1
	}
	if end < start || end >= fileSize {
		return nil, fmt.Errorf("非法字节范围 start=%d end=%d size=%d", start, end, fileSize)
	}

	startBlock := int(start / blockSize)
	endBlock := int(end / blockSize)
	if startBlock >= TotalBlocks {
		startBlock = TotalBlocks - 1
	}
	if endBlock >= TotalBlocks {
		endBlock = TotalBlocks - 1
	}

	// 命中检查:涉及的所有块都已下载
	p.mu.Lock()
	allHit := true
	for i := startBlock; i <= endBlock; i++ {
		if !p.blocks[i] {
			allHit = false
			break
		}
	}
	file := p.file
	p.mu.Unlock()

	if allHit {
		if file == nil {
			return nil, fmt.Errorf("文件已关闭")
		}
		return io.NewSectionReader(file, start, end-start+1), nil
	}

	// 未命中:异步触发模式 B(从 startBlock 开始)
	go p.SwitchToModeB(startBlock)

	// 用 io.Pipe 流式返回给调用方,同时双写到本地文件
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		p.mu.Lock()
		file := p.file
		p.mu.Unlock()
		if file == nil {
			pw.CloseWithError(fmt.Errorf("文件已关闭"))
			return
		}

		written, err := p.streamRangeToPipeAndFile(ctx, file, start, end-start+1, pw)
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		// 整段成功:标记涉及块为已下载
		if written > 0 {
			p.mu.Lock()
			for i := startBlock; i <= endBlock; i++ {
				p.blocks[i] = true
			}
			cb := p.onBlocksUpdate
			p.mu.Unlock()
			if cb != nil {
				cb()
			}
		}
	}()

	return pr, nil
}

// streamRangeToPipeAndFile 从 TG 拉取 [start, start+length) 字节,
// 同时写入 pipe(供调用方读)和本地文件(加锁缓存)。
// 失败时若已写入 pipe 的字节数为 0,则刷新 location 后重试一次。
func (p *PartyDownloader) streamRangeToPipeAndFile(ctx context.Context, file *os.File, start, length int64, pw *io.PipeWriter) (int64, error) {
	if err := p.ensureLocation(ctx); err != nil {
		return 0, err
	}
	api := p.api()
	if api == nil {
		return 0, fmt.Errorf("TG 客户端未就绪")
	}
	p.mu.Lock()
	location := p.location
	p.mu.Unlock()

	w := &dualWriter{
		pipe:  pw,
		file:  file,
		mu:    &p.mu,
		start: start,
	}
	written, err := download.StreamRangeToWriter(ctx, api, location, start, length, w)
	if err == nil {
		return written, nil
	}
	// 客户端断开(浏览器发起重叠 Range 请求时取消旧请求)导致 pipe reader 关闭,
	// 写入 pipe writer 时返回 io.ErrClosedPipe。属于预期行为,播放不受影响,不记录错误日志。
	if errors.Is(err, io.ErrClosedPipe) {
		return written, err
	}
	log.Printf("[party-dl] ReadRange 拉取失败 start=%d length=%d written=%d: %v", start, length, written, err)
	// 已写入 pipe 的字节无法回退,仅在 written==0 时重试
	if written > 0 {
		return written, err
	}
	if rerr := p.refreshLocation(ctx); rerr != nil {
		return 0, err
	}
	p.mu.Lock()
	location = p.location
	p.mu.Unlock()
	w = &dualWriter{
		pipe:  pw,
		file:  file,
		mu:    &p.mu,
		start: start,
	}
	return download.StreamRangeToWriter(ctx, api, location, start, length, w)
}

// fileAtWriter 将写入的字节通过 os.File.WriteAt 写到文件指定偏移(加锁)。
// 用于下载器后台填充:单写目的地为本地文件。
// offset 字段仅被单次 StreamRangeToWriter 调用(主 goroutine 顺序写入)访问,无需加锁;
// 锁仅用于保护 WriteAt 与 ReadRange 路径的并发文件写入。
type fileAtWriter struct {
	file   *os.File
	offset int64
	mu     *sync.Mutex
}

func (w *fileAtWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	n, err := w.file.WriteAt(p, w.offset)
	w.mu.Unlock()
	if n > 0 {
		w.offset += int64(n)
	}
	return n, err
}

// dualWriter 同时将字节写入 pipe(供调用方读)和本地文件(加锁缓存)。
// 用于 ReadRange 未命中场景:边向调用方流式输出边缓存到本地。
// written 字段记录已累计写入字节数,用于文件偏移计算(单 goroutine 顺序写入,无需加锁)。
type dualWriter struct {
	pipe    io.Writer
	file    *os.File
	mu      *sync.Mutex
	start   int64
	written int64
}

func (w *dualWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// 先写 pipe(流向调用方)
	n, err := w.pipe.Write(p)
	if n > 0 {
		// 同步写文件(加锁);文件写入失败不影响流响应,仅记录日志
		w.mu.Lock()
		_, werr := w.file.WriteAt(p[:n], w.start+w.written)
		w.mu.Unlock()
		if werr != nil {
			log.Printf("[party-dl] ReadRange 缓存写入失败 offset=%d: %v", w.start+w.written, werr)
		}
		w.written += int64(n)
	}
	return n, err
}
