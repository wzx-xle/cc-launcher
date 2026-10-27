package stats

import (
	"encoding/json"
	"testing"
	"time"

	"cc-launcher/internal/collector"
)

// setupDimDB 造一个含 3 个 session / 2 个 model / 2 个 provider 的库。
//
//	s1: provider=deepseek,  cwd=/home/u/proj-a, model=deepseek-chat
//	s2: provider=deepseek,  cwd=/home/u/proj-b, model=deepseek-chat
//	s3: provider=anthropic, cwd=/home/u/proj-a, model=claude-opus
func setupDimDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/telemetry.db"
	store, err := collector.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sessions := []collector.Session{
		{ID: "s1", StartedAt: now.Add(-2 * time.Hour), Provider: "deepseek", Cwd: "/home/u/proj-a", Model: "deepseek-chat"},
		{ID: "s2", StartedAt: now.Add(-1 * time.Hour), Provider: "deepseek", Cwd: "/home/u/proj-b", Model: "deepseek-chat"},
		{ID: "s3", StartedAt: now.Add(-30 * time.Minute), Provider: "anthropic", Cwd: "/home/u/proj-a", Model: "claude-opus"},
	}
	for _, s := range sessions {
		if err := store.WriteSession(s); err != nil {
			t.Fatal(err)
		}
	}
	metrics := []collector.Metric{
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: now},
		{SessionID: "s1", Name: "claude_code.token.usage", Value: 10, Labels: map[string]string{"type": "output"}, CapturedAt: now},
		{SessionID: "s1", Name: "claude_code.cost.usage", Value: 0.01, CapturedAt: now},
		{SessionID: "s2", Name: "claude_code.token.usage", Value: 200, Labels: map[string]string{"type": "input"}, CapturedAt: now},
		{SessionID: "s2", Name: "claude_code.token.usage", Value: 20, Labels: map[string]string{"type": "output"}, CapturedAt: now},
		{SessionID: "s2", Name: "claude_code.cost.usage", Value: 0.02, CapturedAt: now},
		{SessionID: "s3", Name: "claude_code.token.usage", Value: 300, Labels: map[string]string{"type": "input"}, CapturedAt: now},
		{SessionID: "s3", Name: "claude_code.token.usage", Value: 30, Labels: map[string]string{"type": "output"}, CapturedAt: now},
		{SessionID: "s3", Name: "claude_code.cost.usage", Value: 0.03, CapturedAt: now},
	}
	for _, m := range metrics {
		if err := store.WriteMetric(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// TestQueryStats_ByModel 断言按 model 聚合出 2 行。
func TestQueryStats_ByModel(t *testing.T) {
	dbPath := setupDimDB(t)
	res, err := QueryStats(dbPath, Query{ByModel: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.GroupBy != "model" {
		t.Fatalf("group: want model, got %q", res.GroupBy)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("want 2 model 行, got %d", len(res.Sessions))
	}
	byModel := map[string]SessionStats{}
	for _, s := range res.Sessions {
		byModel[s.Model] = s
	}
	if got := byModel["deepseek-chat"].InputTokens; got != 300 {
		t.Errorf("deepseek-chat input: want 300, got %d", got)
	}
	if got := byModel["claude-opus"].InputTokens; got != 300 {
		t.Errorf("claude-opus input: want 300, got %d", got)
	}
}

// TestQueryStats_ModelFilter 断言 model 过滤生效且 totals == 行和。
func TestQueryStats_ModelFilter(t *testing.T) {
	dbPath := setupDimDB(t)
	res, err := QueryStats(dbPath, Query{Model: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(res.Sessions))
	}
	for _, s := range res.Sessions {
		if s.Model != "deepseek-chat" {
			t.Errorf("过滤未生效: %+v", s)
		}
	}
	// totals == Σ rows
	var in, out int64
	var cost float64
	for _, s := range res.Sessions {
		in += s.InputTokens
		out += s.OutputTokens
		cost += s.CostUSD
	}
	if res.Totals.InputTokens != in || res.Totals.OutputTokens != out || res.Totals.CostUSD != cost {
		t.Errorf("totals != 行和: %+v vs in=%d out=%d cost=%v", res.Totals, in, out, cost)
	}
}

// TestQueryStats_ByModelAndFilter 断言 --by-model 与 --model 可组合。
func TestQueryStats_ByModelAndFilter(t *testing.T) {
	dbPath := setupDimDB(t)
	res, err := QueryStats(dbPath, Query{ByModel: true, Model: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("want 1 model 行, got %d", len(res.Sessions))
	}
	if res.Sessions[0].Model != "deepseek-chat" {
		t.Errorf("model: got %q", res.Sessions[0].Model)
	}
}

// TestQueryStats_ProviderFilter 断言 provider 过滤生效且 totals == 行和。
func TestQueryStats_ProviderFilter(t *testing.T) {
	dbPath := setupDimDB(t)
	res, err := QueryStats(dbPath, Query{Provider: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(res.Sessions))
	}
	for _, s := range res.Sessions {
		if s.Provider != "deepseek" {
			t.Errorf("过滤未生效: %+v", s)
		}
	}
	var in int64
	for _, s := range res.Sessions {
		in += s.InputTokens
	}
	if res.Totals.InputTokens != in {
		t.Errorf("totals != 行和: %d vs %d", res.Totals.InputTokens, in)
	}
}

// TestQueryStats_ProviderAndProject 断言 --provider 与 --project 可组合。
func TestQueryStats_ProviderAndProject(t *testing.T) {
	dbPath := setupDimDB(t)
	res, err := QueryStats(dbPath, Query{Provider: "deepseek", Project: "proj-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(res.Sessions))
	}
	s := res.Sessions[0]
	if s.Provider != "deepseek" || s.Project != "proj-a" {
		t.Errorf("组合过滤未生效: %+v", s)
	}
}

// TestQueryStats_ByProviderAndProject 断言 --by-provider --by-project 组合可用
// （此前该组合会因 SELECT 多出一列而 scan 失败）。
func TestQueryStats_ByProviderAndProject(t *testing.T) {
	dbPath := setupDimDB(t)
	res, err := QueryStats(dbPath, Query{ByProvider: true, ByProject: true})
	if err != nil {
		t.Fatalf("by-provider + by-project 组合应可用: %v", err)
	}
	if res.GroupBy != "project+provider" {
		t.Fatalf("group: want project+provider, got %q", res.GroupBy)
	}
	if len(res.Sessions) != 3 {
		t.Fatalf("want 3 组合行, got %d", len(res.Sessions))
	}
}

// TestQueryStats_JSONShapeUnchanged 断言 Query 的新字段不进 JSON 输出
// （旧消费者的 JSON 形状不变）。
func TestQueryStats_JSONShapeUnchanged(t *testing.T) {
	dbPath := setupDimDB(t)
	res, err := QueryStats(dbPath, Query{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ByModel", "by_model", "Model\":", "\"Provider\":"} {
		if contains(string(b), forbidden) {
			t.Fatalf("JSON 里不应出现 %q: %s", forbidden, b)
		}
	}
}
