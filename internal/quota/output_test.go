package quota

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixedNow 是测试里固定的"当前时刻"（stale 判定的基准）。
var fixedNow = time.Unix(1_700_000_000, 0)

// TestBuildOutput_OlderThanIntervalIsStale 断言：记录比 refreshInterval 老
// → 标 stale。
//
// **边界是"周期 + 1 秒容忍"且严格大于**：fetched_at 只存到秒（向下取整），
// 正好等于周期的数据其实还很新鲜 → 不标 stale（见 StaleTolerance 的注释）。
func TestBuildOutput_OlderThanIntervalIsStale(t *testing.T) {
	ref := ProviderRef{Name: "p1", Config: &Config{RefreshInterval: 60 * time.Second}}
	entry := Entry{
		Source:    "minimax",
		FetchedAt: fixedNow.Add(-62 * time.Second).Unix(),
		Dimensions: []Dimension{
			{ID: "interval", Label: "5h", Kind: KindWindow, Duration: 18000 * time.Second},
		},
	}

	out, ok := BuildOutput(ref, entry, fixedNow)
	if !ok {
		t.Fatal("有记录的条目应返回 ok=true")
	}
	if !out.Stale {
		t.Error("比 refreshInterval 老的记录应标 stale")
	}
	if out.Provider != "p1" {
		t.Errorf("provider = %q, 想要 %q", out.Provider, "p1")
	}
	if out.Source != "minimax" {
		t.Errorf("source = %q, 想要 %q", out.Source, "minimax")
	}
	if out.FetchedAt != entry.FetchedAt {
		t.Errorf("fetched_at = %d, 想要 %d", out.FetchedAt, entry.FetchedAt)
	}
}

// TestBuildOutput_WithinIntervalIsNotStale 断言：记录比 refreshInterval 新
// → 不标 stale。
func TestBuildOutput_WithinIntervalIsNotStale(t *testing.T) {
	ref := ProviderRef{Name: "p1", Config: &Config{RefreshInterval: 60 * time.Second}}
	entry := Entry{Source: "minimax", FetchedAt: fixedNow.Add(-59 * time.Second).Unix()}

	out, ok := BuildOutput(ref, entry, fixedNow)
	if !ok {
		t.Fatal("有记录的条目应返回 ok=true")
	}
	if out.Stale {
		t.Error("比 refreshInterval 新的记录不该标 stale")
	}
}

// TestBuildOutput_StaleTolerance 断言：阈值额外给 1 秒容忍（fetched_at 只有
// 秒精度）——正好等于周期时**不**标 stale，超过容忍量才标。
func TestBuildOutput_StaleTolerance(t *testing.T) {
	ref := ProviderRef{Name: "p1", Config: &Config{RefreshInterval: 60 * time.Second}}

	// age = 60.5s：周期 + 取整误差的容忍范围内 → 不 stale
	entry := Entry{FetchedAt: fixedNow.Add(-60500 * time.Millisecond).Unix()}
	out, _ := BuildOutput(ref, entry, fixedNow)
	if out.Stale {
		t.Errorf("age=60.5s（周期 60s + 1s 容忍内）不该标 stale")
	}

	// age = 61.5s：越过容忍量 → stale
	entry = Entry{FetchedAt: fixedNow.Add(-61500 * time.Millisecond).Unix()}
	out, _ = BuildOutput(ref, entry, fixedNow)
	if !out.Stale {
		t.Errorf("age=61.5s（越过周期 + 1s 容忍）该标 stale")
	}
}

// TestBuildOutput_NoConfigFallsBackTo60s 断言：provider 没配 quota 块
// （读不到 refreshInterval）→ **兜底 60 秒**。
func TestBuildOutput_NoConfigFallsBackTo60s(t *testing.T) {
	ref := ProviderRef{Name: "p1"} // Config == nil → 读不到 refreshInterval

	if got := IntervalOf(ref); got != 60*time.Second {
		t.Errorf("兜底周期 = %v, 想要 60s", got)
	}

	// 59s → 不 stale（按 60s 周期算）
	out, _ := BuildOutput(ref, Entry{FetchedAt: fixedNow.Add(-59 * time.Second).Unix()}, fixedNow)
	if out.Stale {
		t.Error("按兜底的 60s 周期，59s 前的记录不该标 stale")
	}
	// 61s = 周期 + 1 秒容忍 → 恰好在阈值上 → 仍不 stale
	out, _ = BuildOutput(ref, Entry{FetchedAt: fixedNow.Add(-61 * time.Second).Unix()}, fixedNow)
	if out.Stale {
		t.Error("age 正好等于 周期+1s 容忍 不该标 stale（阈值是严格大于）")
	}
	// 62s → stale
	out, _ = BuildOutput(ref, Entry{FetchedAt: fixedNow.Add(-62 * time.Second).Unix()}, fixedNow)
	if !out.Stale {
		t.Error("按兜底的 60s 周期，62s 前的记录该标 stale")
	}
}

// TestBuildOutput_NoRecord 断言：条目从没抓取过（FetchedAt <= 0）→ ok=false
// （调用方据此回"没有额度记录"）。
func TestBuildOutput_NoRecord(t *testing.T) {
	ref := ProviderRef{Name: "p1", Config: &Config{}}
	if _, ok := BuildOutput(ref, Entry{}, fixedNow); ok {
		t.Error("零值条目应返回 ok=false")
	}
	if _, ok := BuildOutput(ref, Entry{FetchedAt: -1}, fixedNow); ok {
		t.Error("FetchedAt <= 0 的条目应返回 ok=false")
	}
}

// TestBuildOutput_EmptyDimensionsIsEmptyArray 断言：没有维度时输出**空数组**
// 而不是 null（消费方 jq 的 `.dimensions[]` 对 null 会报错）。
func TestBuildOutput_EmptyDimensionsIsEmptyArray(t *testing.T) {
	ref := ProviderRef{Name: "p1", Config: &Config{}}
	out, ok := BuildOutput(ref, Entry{FetchedAt: fixedNow.Unix()}, fixedNow)
	if !ok {
		t.Fatal("有记录的条目应返回 ok=true")
	}
	if out.Dimensions == nil {
		t.Fatal("Dimensions 应是非 nil 的空切片")
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"dimensions":[]`) {
		t.Errorf("dimensions 应输出 [] 而非 null: %s", data)
	}
}

// TestOutput_JSONContract 断言：**对外的 JSON 形状**逐字锁定。
//
// 字段名 / json tag 改名 = 破坏对外契约（仓库外的状态栏脚本会静默读不到值）
// → 这个测试必须红。
func TestOutput_JSONContract(t *testing.T) {
	out := Output{
		Provider:  "minimax-m3",
		Source:    "minimax",
		Stale:     false,
		FetchedAt: 1793943914,
		Dimensions: []Dimension{
			{ID: "interval", Label: "5h", Kind: KindWindow, Duration: 18000 * time.Second,
				Used: f64(59), Limit: f64(100), Unit: "%"},
		},
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"provider":"minimax-m3","source":"minimax","stale":false,"fetched_at":1793943914,` +
		`"dimensions":[{"id":"interval","label":"5h","kind":"window","duration_seconds":18000,` +
		`"used_percentage":59,"used":59,"limit":100,"unit":"%"}]}`
	if string(data) != want {
		t.Errorf("输出契约变了:\n got: %s\nwant: %s", data, want)
	}
}

// TestBuildOutput_SameAsCommandPath 断言：BuildOutput 组装出的文档与"命令层
// 自己拼"的形状一致（字段集合相同）。
func TestBuildOutput_SameAsCommandPath(t *testing.T) {
	ref := ProviderRef{Name: "p1", Config: &Config{RefreshInterval: 30 * time.Second}}
	entry := Entry{
		Source:    "newapi",
		FetchedAt: fixedNow.Unix(),
		Dimensions: []Dimension{
			{ID: "quota", Label: "USD", Kind: KindQuota, Limit: f64(10)},
		},
	}
	out, ok := BuildOutput(ref, entry, fixedNow)
	if !ok {
		t.Fatal("ok 应为 true")
	}
	if out.Stale {
		t.Error("刚抓到的记录不该标 stale")
	}
	if len(out.Dimensions) != 1 || out.Dimensions[0].ID != "quota" {
		t.Errorf("维度没原样带过来: %+v", out.Dimensions)
	}
}
