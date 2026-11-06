package collector

import (
	"sync"
	"testing"
	"time"
)

// TestActivity_Recent 断言：只有最近窗口内有活动的 provider 才算活跃。
func TestActivity_Recent(t *testing.T) {
	a := NewActivity()
	a.Mark("p1", time.Now())

	if !a.Recent(time.Minute)["p1"] {
		t.Error("刚标记的 provider 应算活跃")
	}
	// 窗口比活动时刻还短 → 不算活跃
	if a.Recent(time.Nanosecond)["p1"] {
		t.Error("超出窗口的 provider 不该算活跃")
	}
	// 没标记过的 provider 不在集合里
	if a.Recent(time.Minute)["p2"] {
		t.Error("没标记过的 provider 不该算活跃")
	}
}

// TestActivity_IgnoresEmptyProvider 断言：空 provider 名不记。
//
// 空 = 不是 ccl 启动的会话（没有 ccl.provider resource attribute）→
// 那不是 ccl 管的 provider，额度也查不了。
func TestActivity_IgnoresEmptyProvider(t *testing.T) {
	a := NewActivity()
	a.Mark("", time.Now())
	if len(a.Recent(time.Minute)) != 0 {
		t.Errorf("空 provider 不该被记录: %+v", a.Recent(time.Minute))
	}
}

// TestActivity_Concurrent 断言：并发 Mark/Recent 安全（serve 的 metrics handler
// 与额度刷新 goroutine 会并发访问）。
func TestActivity_Concurrent(t *testing.T) {
	a := NewActivity()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a.Mark("p", time.Now())
			_ = a.Recent(time.Minute)
		}(i)
	}
	wg.Wait()
	if !a.Recent(time.Minute)["p"] {
		t.Error("并发标记后应算活跃")
	}
}

// TestActivity_LastSeen 断言：LastSeen 返回最近活动时刻。
func TestActivity_LastSeen(t *testing.T) {
	a := NewActivity()
	if _, ok := a.LastSeen("nope"); ok {
		t.Error("没标记过的不该返回 ok")
	}
	now := time.Now()
	a.Mark("p1", now)
	got, ok := a.LastSeen("p1")
	if !ok || !got.Equal(now) {
		t.Errorf("LastSeen = %v, %v", got, ok)
	}
}
