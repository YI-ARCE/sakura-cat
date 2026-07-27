// Package download 提供下载任务调度与 TG 媒体拉取能力。
// 本文件导出分集流式拉取所需的原语，供 services/episode_stream.go 调用。
package download

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// FetchMessageMediaInfo 拉取指定频道消息并提取媒体下载位置与元数据。
// 返回值：location（下载位置）、mimeType（MIME 类型）、contentLength（文件大小，0 表示未知）。
//
// accessHash 传 0 即可，函数内部会从 dialogs 表查询精度无损的值。
func FetchMessageMediaInfo(ctx context.Context, db *sql.DB, api *tg.Client, peerID, messageID int64) (location tg.InputFileLocationClass, mimeType string, contentLength int64, err error) {
	msg, err := fetchMessageByPeer(ctx, db, api, peerID, 0, messageID)
	if err != nil {
		return nil, "", 0, err
	}

	location, err = buildFileLocation(msg)
	if err != nil {
		return nil, "", 0, err
	}

	mimeType, contentLength = extractMediaMeta(msg)
	return location, mimeType, contentLength, nil
}

// StreamMediaToWriter 用给定的下载位置流式拉取媒体并写入 w。
// 返回写入的字节数。
func StreamMediaToWriter(ctx context.Context, api *tg.Client, location tg.InputFileLocationClass, w io.Writer) (int64, error) {
	dl := downloader.NewDownloader()
	builder := dl.Download(api, location)
	cw := &countingWriter{w: w}
	if _, err := builder.Stream(ctx, cw); err != nil {
		log.Printf("[download] StreamMediaToWriter 流式下载失败 written=%d: %v", cw.n, err)
		return cw.n, fmt.Errorf("流式下载失败: %w", err)
	}
	return cw.n, nil
}

// countingWriter 包装 io.Writer 并统计写入字节数
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// StreamRangeToWriter 从 startOffset 开始并行拉取媒体并写入 w，最多写 length 字节。
// 用于 HTTP Range 请求与断点续传：用 upload.GetFile 的 offset 参数从 TG 指定位置拉取。
//
// 参数：
//   - startOffset: 起始字节偏移（无需 4KB 对齐，函数内部对齐并裁剪首块头部）
//   - length: 期望写入的字节数；0 表示拉到文件末尾
//
// 实现模型：分片生成器 + worker pool + 有序写入
//   - 分片生成器：按 partSize/windowSize/alignSize 约束持续生成 chunkTask，直到 length 用完或 ctx 取消
//   - worker pool：默认 3 个 worker 并发调用 UploadGetFile，裁剪首块头部后塞入结果 channel
//   - 主 goroutine：按 seq 顺序从结果 channel 读取并写入 w，裁剪末块超出 length 的部分
//
// 并发让多个分片的网络等待时间重叠，相比串行版本速度提升 2-3 倍。
// workerCount=3 为保守值，避免触发 TG FLOOD_WAIT 限流。
//
// TG 约束：upload.GetFile 的 offset 必须 4KB 对齐，limit 必须能被 4096 整除。
// partSize 固定 512KB（满足约束）。Precise=true 允许视频流式按 keyframe 跳跃。
func StreamRangeToWriter(ctx context.Context, api *tg.Client, location tg.InputFileLocationClass, startOffset, length int64, w io.Writer) (int64, error) {
	const (
		partSize    = 512 * 1024  // 512KB，默认拉取块大小
		windowSize  = 1024 * 1024 // 1MB，TG 请求不得跨越 1MB 窗口边界
		alignSize   = 4096        // 4KB，offset 和 limit 必须能被 4096 整除
		workerCount = 3           // 并发 worker 数
	)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	limiter := newAdaptiveLimiter(workerCount)

	taskCh := make(chan chunkTask, workerCount*2)
	resultCh := make(chan chunkResult, workerCount*2)

	// 分片生成器：按约束持续生成 chunkTask
	go func() {
		defer close(taskCh)
		currentOffset := startOffset / alignSize * alignSize
		headSkip := startOffset - currentOffset
		remaining := length
		seq := 0

		for {
			if ctx.Err() != nil {
				return
			}
			// 剩余需求已被首块 headSkip 覆盖，无需再请求
			if remaining > 0 && remaining <= headSkip {
				return
			}
			// 计算 limit：默认 partSize，不跨越 1MB 窗口边界
			limit := partSize
			windowEnd := (currentOffset/windowSize + 1) * windowSize
			if maxLimit := windowEnd - currentOffset; int64(limit) > maxLimit {
				limit = int(maxLimit)
			}
			// 剩余需求约束
			if remaining > 0 {
				need := remaining + headSkip
				if int64(limit) > need {
					limit = int(need / alignSize * alignSize)
					if limit == 0 {
						limit = alignSize
					}
				}
			}
			limit = limit / alignSize * alignSize
			if limit == 0 {
				limit = alignSize
			}

			t := chunkTask{
				seq:      seq,
				offset:   currentOffset,
				limit:    limit,
				headSkip: headSkip,
			}
			select {
			case taskCh <- t:
			case <-ctx.Done():
				return
			}

			// 更新状态（假设本次返回 limit 字节，有效数据 = limit - headSkip）
			usable := int64(limit) - headSkip
			if remaining > 0 {
				remaining -= usable
				if remaining <= 0 {
					return
				}
			}
			currentOffset += int64(limit)
			headSkip = 0
			seq++
		}
	}()

	// worker pool：并发拉取分片（受 adaptiveLimiter 控制并发度与频率）
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				if ctx.Err() != nil {
					return
				}
				r, err := fetchChunkWithAdaptiveLimit(ctx, api, location, t, limiter)
				if err != nil {
					select {
					case resultCh <- chunkResult{seq: t.seq, err: err}:
					case <-ctx.Done():
					}
					return
				}
				data := r.Bytes
				eof := len(data) < t.limit
				// 裁剪首块头部
				if t.headSkip > 0 && len(data) > 0 {
					if int(t.headSkip) >= len(data) {
						data = nil
					} else {
						data = data[t.headSkip:]
					}
				}
				select {
				case resultCh <- chunkResult{seq: t.seq, data: data, eof: eof}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// 所有 worker 退出后关闭 resultCh
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 主 goroutine：按 seq 顺序写入 w
	var written int64
	nextSeq := 0
	buffer := make(map[int]chunkResult)
	remaining := length

	// writeOne 写入一个分片（处理末块裁剪），返回是否应停止
	writeOne := func(r chunkResult) (stop bool, err error) {
		data := r.data
		if len(data) == 0 {
			return false, nil
		}
		if remaining > 0 && int64(len(data)) > remaining {
			data = data[:remaining]
			stop = true
		}
		n, err := w.Write(data)
		written += int64(n)
		if remaining > 0 {
			remaining -= int64(n)
		}
		return stop, err
	}

	// drain 排空 resultCh（错误/停止时调用，避免 goroutine 泄漏）
	drain := func() {
		cancel()
		for range resultCh {
		}
	}

	for r := range resultCh {
		if r.err != nil {
			drain()
			return written, r.err
		}
		// 乱序：暂存
		if r.seq != nextSeq {
			buffer[r.seq] = r
			continue
		}
		// 写入 nextSeq
		stop, err := writeOne(r)
		if err != nil {
			drain()
			return written, err
		}
		if stop || r.eof {
			drain()
			return written, nil
		}
		nextSeq++
		// 从 buffer 取出后续连续的分片
		for {
			nr, ok := buffer[nextSeq]
			if !ok {
				break
			}
			delete(buffer, nextSeq)
			stop, err = writeOne(nr)
			if err != nil {
				drain()
				return written, err
			}
			if stop || nr.eof {
				drain()
				return written, nil
			}
			nextSeq++
		}
	}

	return written, nil
}

// chunkTask 表示一个分片拉取任务（由分片生成器产出，worker 消费）
type chunkTask struct {
	seq      int   // 顺序号，用于有序写入
	offset   int64 // 4KB 对齐的请求 offset
	limit    int   // 4KB 对齐的请求 limit
	headSkip int64 // 首块裁剪量（仅 seq=0 可能为非零）
}

// chunkResult 表示一个分片的拉取结果（worker 产出，主 goroutine 消费）
type chunkResult struct {
	seq  int    // 顺序号
	data []byte // 已裁剪首块头部的数据（nil 表示空响应）
	eof  bool   // 是否到达文件末尾（len(data) < limit）
	err  error  // 拉取错误
}

// fetchMaxRetries 单个分片因 FLOOD_WAIT 重试的最大次数（与 scanner.go 保持一致）
const fetchMaxRetries = 5

// fetchChunkWithAdaptiveLimit 使用自适应限流器拉取单个分片。
//
// 流程：
//  1. 等待全局冷却期（若处于冷却态）
//  2. 获取并发槽位（受 currentWorkers 控制）
//  3. 等待请求间隔（降速态非零）
//  4. 调用 UploadGetFile
//  5. 成功：记录成功，释放槽位，返回结果
//  6. FLOOD_WAIT：记录限流（触发全局冷却+降速），释放槽位，等待冷却后重试
//  7. 其他错误：释放槽位，返回错误
//  8. 重试次数超限：返回错误
func fetchChunkWithAdaptiveLimit(ctx context.Context, api *tg.Client, location tg.InputFileLocationClass, t chunkTask, limiter *adaptiveLimiter) (*tg.UploadFile, error) {
	req := &tg.UploadGetFileRequest{
		Location: location,
		Offset:   t.offset,
		Limit:    t.limit,
		Precise:  true,
	}

	var lastErr error
	for attempt := 0; attempt <= fetchMaxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 等待全局冷却
		if !limiter.waitCooldown(ctx) {
			return nil, ctx.Err()
		}
		// 获取并发槽位
		if !limiter.acquireSlot(ctx) {
			return nil, ctx.Err()
		}
		// 请求间隔（降速态非零）
		if d := limiter.requestDelay(); d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				limiter.releaseSlot()
				return nil, ctx.Err()
			}
		}
		// 发起请求
		r, err := api.UploadGetFile(ctx, req)
		limiter.releaseSlot()

		if err == nil {
			limiter.recordSuccess()
			uf, ok := r.(*tg.UploadFile)
			if !ok {
				return nil, fmt.Errorf("意外的响应类型 %T", r)
			}
			return uf, nil
		}
		lastErr = err
		// 检查 FLOOD_WAIT
		waitDur, ok := tgerr.AsFloodWait(err)
		if !ok {
			// 非限流错误，直接返回
			return nil, err
		}
		// 记录限流，触发全局冷却 + 降速
		if !limiter.recordFloodWait(waitDur) {
			return nil, fmt.Errorf("FLOOD_WAIT 连续限流次数超限: %w", err)
		}
		w, i, c := limiter.floodStats()
		log.Printf("[download] FLOOD_WAIT offset=%d attempt=%d 等待%v 降速到 workers=%d interval=%v cooldown=%v",
			t.offset, attempt+1, waitDur, w, i, c)
		// 循环回到顶部，waitCooldown 会阻塞到冷却结束
	}
	return nil, fmt.Errorf("FLOOD_WAIT 重试 %d 次后仍未成功: %w", fetchMaxRetries+1, lastErr)
}

// extractMediaMeta 从消息媒体中提取 MIME 类型与文件大小。
func extractMediaMeta(msg *tg.Message) (mimeType string, size int64) {
	media, ok := msg.GetMedia()
	if !ok || media == nil {
		return "", 0
	}
	switch m := media.(type) {
	case *tg.MessageMediaDocument:
		if doc, ok := m.Document.(*tg.Document); ok {
			return doc.MimeType, doc.Size
		}
	case *tg.MessageMediaPhoto:
		return "image/jpeg", 0
	}
	return "", 0
}
