package quota

import (
	"context"
	"sync"
	"time"
)

// DefaultVendorGap 是同一 quota 接口（endpoint）两次请求之间的默认最小间隔。
//
// 为什么需要它（quota-per-vendor-rate-limit §D1）：厂商的限流是**时间窗口内的
// 请求数**，不是"同一时刻"。相位错开只保证不同池不在同一时刻到期——实测
// Windows 主环境的两个讯飞 provider（**不同的 key**）的 429 时刻只隔 8-20 秒，
// 而轮内的 500ms 间隔管不到跨轮的场景。
//
// 10 秒的来历：日志里 8-20 秒的间隔就触发了限流 → 约束必须大于这个量级；
// 又远小于 60 秒的刷新周期，不会显著拖慢刷新。可按 provider 用
// `quota.vendorGapMs` 覆盖。
const DefaultVendorGap = 10 * time.Second

// EndpointThrottle 按 quota 接口限制请求速率。
//
// **状态跨轮有效**：节流器由 daemon 持有（常驻进程），因此"上一轮末尾的请求"
// 与"下一轮开头的请求"之间也受间隔约束 —— 而这正是实测出问题的那种形态。
//
// 零值不可用，用 NewEndpointThrottle 构造；nil 指针的方法调用是安全的
// （不做任何节流）。
type EndpointThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// NewEndpointThrottle 造一个节流器。
func NewEndpointThrottle() *EndpointThrottle {
	return &EndpointThrottle{last: map[string]time.Time{}}
}

// Wait 阻塞到"距上次向该接口发请求已满 gap"为止。
//
// 从没向该接口发过请求 → 立即返回（不等待）。
// ctx 取消 → 返回 ctx.Err()。
func (t *EndpointThrottle) Wait(ctx context.Context, endpoint string, gap time.Duration) error {
	if t == nil || gap <= 0 || endpoint == "" {
		return nil
	}
	t.mu.Lock()
	last, ok := t.last[endpoint]
	t.mu.Unlock()
	if !ok {
		return nil
	}
	wait := gap - time.Since(last)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Mark 记录"刚刚向该接口发了请求"。
//
// 必须在**请求发出时**调用（不是本轮开始时）——间隔是两次**实际请求**之间的
// 时间，不是两次"准备请求"之间的时间。
func (t *EndpointThrottle) Mark(endpoint string) {
	if t == nil || endpoint == "" {
		return
	}
	t.mu.Lock()
	if t.last == nil {
		t.last = map[string]time.Time{}
	}
	t.last[endpoint] = time.Now()
	t.mu.Unlock()
}
