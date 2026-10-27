package collector

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// makePayload 造一个带 resource attrs 的 OTLP payload（session.id 在 datapoint attrs）。
func makePayload(t *testing.T, sessionID string, resAttrs map[string]string) []byte {
	t.Helper()

	attrs := []*commonpb.KeyValue{
		kv("service.name", "claude-code"),
	}
	for k, v := range resAttrs {
		attrs = append(attrs, kv(k, v))
	}

	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{Attributes: attrs},
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Scope: &commonpb.InstrumentationScope{Name: "com.anthropic.claude_code"},
						Metrics: []*metricpb.Metric{
							{
								Name: "claude_code.token.usage",
								Data: &metricpb.Metric_Sum{
									Sum: &metricpb.Sum{
										DataPoints: []*metricpb.NumberDataPoint{
											{
												TimeUnixNano: 1_700_000_000_000_000_000,
												Value:        &metricpb.NumberDataPoint_AsInt{AsInt: 100},
												Attributes: []*commonpb.KeyValue{
													kv("session.id", sessionID),
													kv("type", "input"),
													kv("model", "deepseek-chat"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	b, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// readSessionRow 读 session 行的 provider / provider_endpoint / cwd。
func readSessionRow(t *testing.T, dbPath, id string) (provider, endpoint, cwd string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.QueryRow(`SELECT provider, provider_endpoint, cwd FROM sessions WHERE id=?`, id).
		Scan(&provider, &endpoint, &cwd)
	if err != nil {
		t.Fatalf("read session %q: %v", id, err)
	}
	return
}

// TestParse_ReadsCclAttrs 断言 parser 从 resource attrs 读出 ccl.* 三个属性。
func TestParse_ReadsCclAttrs(t *testing.T) {
	body := makePayload(t, "sess-1", map[string]string{
		"ccl.provider":          "minimax",
		"ccl.cwd":               "/home/u/proj",
		"ccl.provider_endpoint": "api.minimax.cn",
	})
	res, err := ParseMetricsRequest(body)
	if err != nil {
		t.Fatalf("ParseMetricsRequest: %v", err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(res.Sessions))
	}
	s := res.Sessions[0]
	if s.Provider != "minimax" {
		t.Errorf("provider: got %q", s.Provider)
	}
	if s.Cwd != "/home/u/proj" {
		t.Errorf("cwd: got %q", s.Cwd)
	}
	if s.ProviderEndpoint != "api.minimax.cn" {
		t.Errorf("provider_endpoint: got %q", s.ProviderEndpoint)
	}
}

// TestParse_NoCclAttrs 断言不带 ccl.* 的 payload 不报错、三个字段为空。
func TestParse_NoCclAttrs(t *testing.T) {
	body := makePayload(t, "sess-1", nil)
	res, err := ParseMetricsRequest(body)
	if err != nil {
		t.Fatalf("ParseMetricsRequest: %v", err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(res.Sessions))
	}
	s := res.Sessions[0]
	if s.Provider != "" || s.Cwd != "" || s.ProviderEndpoint != "" {
		t.Errorf("无 ccl.* 时三字段应为空: %+v", s)
	}
}

// TestStore_PersistsLaunchContext 断言带 ccl.* 的 payload → session 行三字段落库。
func TestStore_PersistsLaunchContext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	body := makePayload(t, "sess-1", map[string]string{
		"ccl.provider":          "minimax",
		"ccl.cwd":               "/home/u/proj",
		"ccl.provider_endpoint": "api.minimax.cn",
	})
	res, err := ParseMetricsRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range res.Sessions {
		if err := s.WriteSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	provider, endpoint, cwd := readSessionRow(t, dbPath, "sess-1")
	if provider != "minimax" || endpoint != "api.minimax.cn" || cwd != "/home/u/proj" {
		t.Fatalf("落库不对: provider=%q endpoint=%q cwd=%q", provider, endpoint, cwd)
	}
}

// TestStore_EmptyDoesNotOverwrite 断言空值不覆盖非空、后到的非空值补上空字段。
func TestStore_EmptyDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// 先写一条带完整上下文的
	if err := s.WriteSession(Session{
		ID: "sess-1", StartedAt: now,
		Provider: "minimax", Cwd: "/home/u/proj", ProviderEndpoint: "api.minimax.cn",
		Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	// 再写一条同 id、不带上下文的
	if err := s.WriteSession(Session{
		ID: "sess-1", StartedAt: now,
		Provider: "", Cwd: "", ProviderEndpoint: "",
		Model: "m",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	provider, endpoint, cwd := readSessionRow(t, dbPath, "sess-1")
	if provider != "minimax" || endpoint != "api.minimax.cn" || cwd != "/home/u/proj" {
		t.Fatalf("空值不应覆盖非空: provider=%q endpoint=%q cwd=%q", provider, endpoint, cwd)
	}
}

// TestStore_LaterValueFillsEmpty 断言后到的非空值能补上先前的空字段。
func TestStore_LaterValueFillsEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	if err := s.WriteSession(Session{ID: "sess-1", StartedAt: now, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSession(Session{
		ID: "sess-1", StartedAt: now,
		Provider: "minimax", Cwd: "/home/u/proj", ProviderEndpoint: "api.minimax.cn",
		Model: "m",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	provider, endpoint, cwd := readSessionRow(t, dbPath, "sess-1")
	if provider != "minimax" || endpoint != "api.minimax.cn" || cwd != "/home/u/proj" {
		t.Fatalf("后到的非空值应补上空字段: provider=%q endpoint=%q cwd=%q", provider, endpoint, cwd)
	}
}

// TestMigrate_AddsProviderEndpointColumn 断言老库启动后新列存在、旧行不丢、
// 重复启动不重复加列。
func TestMigrate_AddsProviderEndpointColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	// 手工造一个"老版本"的库：sessions 表没有 provider_endpoint 列，user_version=1
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		started_at INTEGER NOT NULL,
		ended_at INTEGER,
		provider TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, started_at, provider, cwd, model)
		VALUES ('old-1', 1700000000000, 'deepseek', '/old/proj', 'm')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// 第一次启动：迁移
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	provider, endpoint, cwd := readSessionRow(t, dbPath, "old-1")
	if provider != "deepseek" || cwd != "/old/proj" {
		t.Fatalf("旧行数据丢失: provider=%q cwd=%q", provider, cwd)
	}
	if endpoint != "" {
		t.Fatalf("新列应为空默认, got %q", endpoint)
	}

	// 列确实存在
	if !columnExistsInFile(t, dbPath, "sessions", "provider_endpoint") {
		t.Fatal("provider_endpoint 列未创建")
	}

	// 第二次启动：不报错、不重复加列
	s2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("第二次 OpenStore: %v", err)
	}
	if err := s2.Close(ctx); err != nil {
		t.Fatal(err)
	}
	provider, endpoint, cwd = readSessionRow(t, dbPath, "old-1")
	if provider != "deepseek" || cwd != "/old/proj" || endpoint != "" {
		t.Fatalf("重复启动后数据应不变: provider=%q endpoint=%q cwd=%q", provider, endpoint, cwd)
	}
}

// columnExistsInFile 打开 db 文件检查列是否存在（绕过 Store 的私有 helper）。
func columnExistsInFile(t *testing.T, dbPath, table, column string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	return false
}

// fakeResolver 是测试用的 CwdResolver。
type fakeResolver struct {
	byID map[string]string
}

func (f fakeResolver) ResolveCwd(sessionID string) string { return f.byID[sessionID] }

// TestStore_ResolverEnrich 断言 resolver 解析出的 cwd 写进 sessions.cwd。
func TestStore_ResolverEnrich(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	s, err := OpenStore(dbPath, WithCwdResolver(fakeResolver{byID: map[string]string{
		"sess-1": "/home/u/resolved",
	}}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.WriteSession(Session{ID: "sess-1", StartedAt: now, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	_, _, cwd := readSessionRow(t, dbPath, "sess-1")
	if cwd != "/home/u/resolved" {
		t.Fatalf("cwd: got %q, want /home/u/resolved", cwd)
	}
}

// TestStore_ResolverFailureDoesNotFailWrite 断言解析失败不影响写 session 行。
func TestStore_ResolverFailureDoesNotFailWrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	s, err := OpenStore(dbPath, WithCwdResolver(fakeResolver{byID: map[string]string{}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSession(Session{ID: "sess-1", StartedAt: time.Now(), Model: "m"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	_, _, cwd := readSessionRow(t, dbPath, "sess-1")
	if cwd != "" {
		t.Fatalf("解析失败应留空, got %q", cwd)
	}
	// 行仍然写进去了（readSessionRow 不报错即说明存在）
}

// TestStore_ResolverDoesNotOverridePayloadCwd 断言 payload 带 ccl.cwd 时
// resolver 的结果不生效。
func TestStore_ResolverDoesNotOverridePayloadCwd(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	s, err := OpenStore(dbPath, WithCwdResolver(fakeResolver{byID: map[string]string{
		"sess-1": "/home/u/from-resolver",
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSession(Session{
		ID: "sess-1", StartedAt: time.Now(), Cwd: "/home/u/from-payload", Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	_, _, cwd := readSessionRow(t, dbPath, "sess-1")
	if cwd != "/home/u/from-payload" {
		t.Fatalf("launch 上下文优先, got %q", cwd)
	}
}

// TestBackfillCwd_Idempotent 断言回填只补空行、幂等、不改 schema。
//
// 场景：这些行是**回填机制存在之前**写下的（写时没有 resolver），
// 现在补上 resolver 再跑回填。
func TestBackfillCwd_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	// 第一阶段：无 resolver，写入三行（两条 cwd 空，一条已有 cwd）
	s0, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s0.WriteSession(Session{ID: "sess-1", StartedAt: now, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := s0.WriteSession(Session{ID: "sess-2", StartedAt: now, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := s0.WriteSession(Session{ID: "sess-3", StartedAt: now, Cwd: "/keep/me", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s0.Close(ctx); err != nil {
		t.Fatal(err)
	}

	before := sessionColumns(t, dbPath)

	// 第二阶段：带 resolver 打开，跑回填
	s, err := OpenStore(dbPath, WithCwdResolver(fakeResolver{byID: map[string]string{
		"sess-1": "/home/u/one",
		"sess-2": "", // 查不到
	}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()

	resolved, failed, err := s.BackfillCwd(0)
	if err != nil {
		t.Fatalf("BackfillCwd: %v", err)
	}
	if resolved != 1 {
		t.Errorf("resolved: want 1, got %d", resolved)
	}
	if failed != 1 {
		t.Errorf("failed: want 1, got %d", failed)
	}

	after := sessionColumns(t, dbPath)
	if len(before) != len(after) {
		t.Fatalf("回填不应改 schema: before=%v after=%v", before, after)
	}

	// 第二次回填：幂等（没有新的空行可补）
	resolved2, _, err := s.BackfillCwd(0)
	if err != nil {
		t.Fatalf("第二次 BackfillCwd: %v", err)
	}
	if resolved2 != 0 {
		t.Errorf("第二次回填应无新增, got %d", resolved2)
	}

	// 非空 cwd 不被改写
	_, _, cwd3 := readSessionRow(t, dbPath, "sess-3")
	if cwd3 != "/keep/me" {
		t.Errorf("非空 cwd 被改写: %q", cwd3)
	}
	_, _, cwd1 := readSessionRow(t, dbPath, "sess-1")
	if cwd1 != "/home/u/one" {
		t.Errorf("sess-1 cwd: got %q", cwd1)
	}
}

// TestBackfillCwd_Bounded 断言 limit 生效。
func TestBackfillCwd_Bounded(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	// 先用无 resolver 的 store 写 5 行空 cwd
	s0, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := range 5 {
		if err := s0.WriteSession(Session{ID: string(rune('a' + i)), StartedAt: now, Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s0.Close(ctx); err != nil {
		t.Fatal(err)
	}

	resolver := fakeResolver{byID: map[string]string{}}
	for i := range 5 {
		resolver.byID[string(rune('a'+i))] = "/home/u/x"
	}
	s, err := OpenStore(dbPath, WithCwdResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()

	resolved, _, err := s.BackfillCwd(2)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 2 {
		t.Fatalf("limit=2 时 resolved 应为 2, got %d", resolved)
	}
}

// sessionColumns 返回 sessions 表的列名列表。
func sessionColumns(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA table_info(sessions)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &primaryKey); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name)
	}
	return cols
}
