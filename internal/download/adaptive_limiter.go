// Package download 提供下载任务调度与 TG 媒体拉取能力。
// 本文件实现自适应限流器：并行下载场景下处理 TG FLOOD_WAIT 限流，
// 通过全局冷却 + 动态降速 + 逐步加速的机制，在保证不触发限流的前提下尽量利用并发。
package download

import (
	"context"
	"sync"
	"time"
)

// adaptiveLimiter 自适应限流器，协调多个 worker 的请求频率与并发度。
//
// 状态机：
//   - 正常态：currentWorkers = maxWorkers, interval = 0
//   - 冷却态：收到 FLOOD_WAIT 后，所有 worker 阻塞到 cooldownUntil
//   - 降速态：冷却结束后，currentWorkers 降到 1，interval 设为 500ms
//   - 试探加速：连续成功 successThreshold 次后，currentWorkers 升一档，interval 减半
//
// 线程安全：所有方法都通过 mu 保护，可被多个 worker 并发调用。
type adaptiveLimiter struct {
	mu sync.Mutex

	// 最大并发度（初始值，降速后会动态调整）
	maxWorkers int
	// 当前允许的并发度（1 ~ maxWorkers）
	currentWorkers int
	// 当前每个请求之间的最小间隔
	requestInterval time.Duration
	// 最近一次 FLOOD_WAIT 时间
	lastFloodAt time.Time
	// 连续成功计数（达到阈值后尝试加速）
	successStreak int
	// 冷却期截止时间（收到 FLOOD_WAIT 后设置，所有 worker 阻塞到此时间）
	cooldownUntil time.Time
	// 连续 FLOOD_WAIT 计数（超过 maxConsecutiveFloods 则放弃）
	consecutiveFloods int

	// 并发槽位信号量（容量等于 currentWorkers，动态重建）
	sem chan struct{}
	// sem 当前容量（用于判断是否需要重建信号量）
	semCap int
}

// 限流器常量
const (
	// successThreshold 连续成功多少次后尝试加速
	successThreshold = 10
	// maxConsecutiveFloods 连续 FLOOD_WAIT 次数上限，超过则放弃
	maxConsecutiveFloods = 3
	// baseInterval 降速态的初始请求间隔
	baseInterval = 500 * time.Millisecond
	// longFloodThreshold 长限流阈值，超过则更激进地降速
	longFloodThreshold = 60 * time.Second
)

// newAdaptiveLimiter 创建自适应限流器。
// maxWorkers 为最大并发度（通常为 3）。
func newAdaptiveLimiter(maxWorkers int) *adaptiveLimiter {
	al := &adaptiveLimiter{
		maxWorkers:     maxWorkers,
		currentWorkers: maxWorkers,
	}
	al.rebuildSem()
	return al
}

// rebuildSem 重建信号量（调用方需持有 mu）
func (al *adaptiveLimiter) rebuildSem() {
	cap := al.currentWorkers
	if cap < 1 {
		cap = 1
	}
	al.sem = make(chan struct{}, cap)
	al.semCap = cap
}

// waitCooldown 在全局冷却期内阻塞，冷却结束后返回。
// ctx 取消时立即返回。
func (al *adaptiveLimiter) waitCooldown(ctx context.Context) bool {
	al.mu.Lock()
	until := al.cooldownUntil
	al.mu.Unlock()

	if until.IsZero() || time.Now().After(until) {
		return true
	}
	wait := time.Until(until)
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// acquireSlot 获取并发槽位（阻塞，受 currentWorkers 控制）。
// 返回 false 表示 ctx 取消。
func (al *adaptiveLimiter) acquireSlot(ctx context.Context) bool {
	al.mu.Lock()
	sem := al.sem
	al.mu.Unlock()
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// releaseSlot 释放并发槽位。
func (al *adaptiveLimiter) releaseSlot() {
	al.mu.Lock()
	sem := al.sem
	al.mu.Unlock()
	select {
	case <-sem:
	default:
	}
}

// requestDelay 返回请求前的最小间隔（降速态非零）。
func (al *adaptiveLimiter) requestDelay() time.Duration {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.requestInterval
}

// recordSuccess 记录一次成功请求，达到阈值后尝试加速。
func (al *adaptiveLimiter) recordSuccess() {
	al.mu.Lock()
	defer al.mu.Unlock()
	al.successStreak++
	if al.successStreak >= successThreshold {
		al.tryAccelerate()
	}
}

// recordFloodWait 记录一次 FLOOD_WAIT，触发全局冷却 + 降速。
// 返回 true 表示继续重试，false 表示连续限流次数超限应放弃。
func (al *adaptiveLimiter) recordFloodWait(waitDur time.Duration) bool {
	al.mu.Lock()
	defer al.mu.Unlock()

	al.consecutiveFloods++
	if al.consecutiveFloods > maxConsecutiveFloods {
		return false
	}

	waitSec := int(waitDur.Seconds())
	if waitSec <= 0 {
		waitSec = 1
	}
	waitSec = waitSec + 1 // 加 1 秒缓冲

	al.cooldownUntil = time.Now().Add(time.Duration(waitSec) * time.Second)
	al.lastFloodAt = time.Now()
	al.successStreak = 0

	// 降速：currentWorkers 降到 1
	al.currentWorkers = 1
	// 长限流用更激进的间隔
	if waitDur > longFloodThreshold {
		al.requestInterval = time.Second
	} else {
		al.requestInterval = baseInterval
	}
	al.rebuildSem()
	return true
}

// tryAccelerate 尝试加速（调用方需持有 mu）。
// 加速策略：
//   - interval 先减半直到 0
//   - interval 已 0 后，currentWorkers 升一档直到 maxWorkers
func (al *adaptiveLimiter) tryAccelerate() {
	al.successStreak = 0 // 重置计数
	if al.requestInterval > 0 {
		al.requestInterval = al.requestInterval / 2
		if al.requestInterval < 50*time.Millisecond {
			al.requestInterval = 0
		}
		return
	}
	// interval 已 0，提升并发度
	if al.currentWorkers < al.maxWorkers {
		al.currentWorkers++
		al.rebuildSem()
	}
}

// floodStats 返回当前限流状态（用于日志）。
func (al *adaptiveLimiter) floodStats() (currentWorkers int, interval time.Duration, inCooldown bool) {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.currentWorkers, al.requestInterval, time.Now().Before(al.cooldownUntil)
}
