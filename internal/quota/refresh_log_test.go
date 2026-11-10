package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// logCapture 收集 RefreshAll 打到 logf 的行。
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (c *logCapture) logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, format)
	_ = args
}

func (c *logCapture) has(sub string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// reset 清空已收集的行。
func (c *logCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = nil
}

func (c *logCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lines)
}

// TestRefreshAll_SummaryLogOnlyWhenRequestHappened 断言：**没有发出任何请求**
// 的刷新轮次不产生汇总日志行。
//
// 为什么这条要锁住：ticker 每 8 秒一轮，全打的话绝大多数行都是"查了 0 /
// 跳过 7"——真正的限流与失败信息被淹在噪音里（实测 Windows 上一小时 450 行里
// 445 行是"查了 0"）。但这行日志是**行为契约**：一旦有人把它改回无条件打印，
// 噪音会立刻回来，且没人会注意到。
func TestRefreshAll_SummaryLogOnlyWhenRequestHappened(t *testing.T) {
	stub := newQuotaStub(t)

	st := NewStore(t.TempDir())
	ref := stub.ref("p1", "sk-1")
	cap := &logCapture{}

	// 第 1 轮：**首次**查询（新库没有记录 → 活跃池必查一次）→ 有请求 → 该打
	if err := RefreshAll(context.Background(), []ProviderRef{ref}, st, cap.logf, map[string]bool{"p1": true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if !cap.has("额度刷新一轮") {
		t.Errorf("首次查询（发了请求）该打汇总日志，实际日志:\n%s", strings.Join(cap.lines, "\n"))
	}
	if stub.count() != 1 {
		t.Fatalf("首次查询应发 1 个请求, got %d", stub.count())
	}

	// 第 2 轮：刚查过 → 没到期 → **零请求** → 不该有汇总行
	cap.reset()
	if err := RefreshAll(context.Background(), []ProviderRef{ref}, st, cap.logf, map[string]bool{"p1": true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if cap.has("额度刷新一轮") {
		t.Errorf("零请求的轮次不该打汇总日志，实际打了:\n%s", strings.Join(cap.lines, "\n"))
	}
	if stub.count() != 1 {
		t.Errorf("未到期的轮次不该再发请求, 累计 %d", stub.count())
	}

	// 第 3 轮：把记录改成很久以前 → 到期 → 发了请求 → 该有汇总行
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := f.Entries["p1"]
	e.FetchedAt = time.Now().Add(-time.Hour).Unix()
	f.Entries["p1"] = e
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	cap.reset()
	if err := RefreshAll(context.Background(), []ProviderRef{ref}, st, cap.logf, map[string]bool{"p1": true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if !cap.has("额度刷新一轮") {
		t.Errorf("发了请求的轮次该打汇总日志，实际日志:\n%s", strings.Join(cap.lines, "\n"))
	}
}

// TestRefreshAll_SummaryLogOnFailure 断言：**有失败**的轮次仍打汇总日志
// （失败数 > 0 时汇总行是排查问题的第一眼）。
func TestRefreshAll_SummaryLogOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	st := NewStore(t.TempDir())
	ref := ProviderRef{Name: "p1", Config: &Config{Source: "newapi", Endpoint: srv.URL}, APIKey: "sk-1"}
	cap := &logCapture{}

	if err := RefreshAll(context.Background(), []ProviderRef{ref}, st, cap.logf, map[string]bool{"p1": true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if !cap.has("额度刷新一轮") {
		t.Errorf("失败的轮次该打汇总日志，实际日志:\n%s", strings.Join(cap.lines, "\n"))
	}
}
