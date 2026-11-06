package collector

import (
	"sync"
	"time"
)

// Activity 跟踪各 provider 最近的 OTel 活动时刻。
//
// **为什么需要它**：额度数据的唯一消费者是状态栏，而状态栏显示的是**当前会话
// 那个 provider** 的额度——其余 provider 的额度数据**当前根本没人看**。查它们
// = 纯浪费 + 触发厂商的速率限制（实测：8 个 provider 的机器一轮就 429）。
//
// **活动信号从哪来**：ccl 启动 Claude Code 时把 provider 名注入了
// OTEL_RESOURCE_ATTRIBUTES 的 `ccl.provider`（见 launchctx.KeyProvider），
// Claude Code 的 OTel SDK 把它当 resource attribute 随 metrics 推给本进程 →
// collector 解析每条 metrics 时就知道是哪个 provider（parser.go 里
// `meta.provider = resAttrs[launchctx.KeyProvider]`）。
//
// 所以"这个 provider 最近有没有人在用"**不需要新状态**——collector 手里就有。
//
// **实测频率**（2026-11-06，本机 telemetry.db）：Claude Code **每 60 秒**推一次
// metrics。所以活跃窗口必须 > 60 秒，否则两次推送之间会被误判成"没人用"。
type Activity struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// NewActivity 创建 Activity。
func NewActivity() *Activity {
	return &Activity{last: map[string]time.Time{}}
}

// Mark 记下某 provider 刚刚有活动。
//
// provider 为空（不是 ccl 启动的会话 → 没有 ccl.provider 属性）→ **不记**：
// 那不是 ccl 管的 provider，额度也查不了。
func (a *Activity) Mark(provider string, t time.Time) {
	if a == nil || provider == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		a.last = map[string]time.Time{}
	}
	a.last[provider] = t
}

// Recent 返回最近 window 内有活动的 provider 集合。
//
// now 为零值时用 time.Now()。
func (a *Activity) Recent(window time.Duration) map[string]bool {
	out := map[string]bool{}
	if a == nil || window <= 0 {
		return out
	}
	now := time.Now()
	cutoff := now.Add(-window)

	a.mu.Lock()
	defer a.mu.Unlock()
	for provider, last := range a.last {
		if last.After(cutoff) {
			out[provider] = true
		}
	}
	return out
}

// LastSeen 返回某 provider 最近一次活动的时刻；没有则返回零值。
func (a *Activity) LastSeen(provider string) (time.Time, bool) {
	if a == nil {
		return time.Time{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.last[provider]
	return t, ok
}
