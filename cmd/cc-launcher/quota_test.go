package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cc-launcher/internal/collector"
	"cc-launcher/internal/quota"
)

// quotaHome 隔离 HOME（~/.cc-launcher 跟着走），返回 base 目录。
func quotaHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// 关掉环境变量回落，避免测试受外部环境影响。
	t.Setenv("CCL_PROVIDER", "")
	// **默认不走 daemon 端点**：开发机上可能恰好跑着真 daemon → 命令会读到
	// 真数据，测试就不确定了。指向一个没人监听的端口 → 确定性地回落本地文件。
	old := quotaDaemonBaseURL
	quotaDaemonBaseURL = func() string { return "http://127.0.0.1:1" }
	t.Cleanup(func() { quotaDaemonBaseURL = old })
	return filepath.Join(dir, ".cc-launcher")
}

// writeQuotaProvider 写一个 provider 配置文件。
func writeQuotaProvider(t *testing.T, baseDir, name string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(baseDir, "providers")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// captureQuotaRun 跑一次 runQuota，捕获 stdout / stderr 与退出码。
func captureQuotaRun(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	oldExit := quotaExitFn
	var gotCode int
	quotaExitFn = func(c int) { gotCode = c }
	t.Cleanup(func() { quotaExitFn = oldExit })

	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr

	done := make(chan struct{})
	var outBuf, errBuf []byte
	go func() {
		outBuf, _ = io.ReadAll(rOut)
		done <- struct{}{}
	}()
	go func() {
		errBuf, _ = io.ReadAll(rErr)
		done <- struct{}{}
	}()

	runErr := runQuota(args)

	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldStdout, oldStderr
	<-done
	<-done
	if runErr != nil {
		t.Fatalf("runQuota 返回错误: %v", runErr)
	}
	return string(outBuf), string(errBuf), gotCode
}

// TestRunQuota_ReadsLocalFileWithoutNetwork 断言：**不带 --refresh 一次网络都不发**。
func TestRunQuota_ReadsLocalFileWithoutNetwork(t *testing.T) {
	baseDir := quotaHome(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://api.minimax.cn/anthropic",
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
		},
		"quota": map[string]any{"source": "newapi", "endpoint": srv.URL},
	})

	// 造一条数据
	st := quota.NewStore(baseDir)
	if err := st.Update("p1", "newapi", "pool-1", []quota.Dimension{
		{ID: "interval", Label: "5h", Kind: quota.KindWindow, Duration: 18000 * time.Second,
			Used: quotaFloat(59), Limit: quotaFloat(100), Unit: "%"},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("不带 --refresh 不该发网络请求, calls=%d", calls)
	}

	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, stdout)
	}
	if out.Provider != "p1" {
		t.Errorf("provider = %q", out.Provider)
	}
	if len(out.Dimensions) != 1 {
		t.Fatalf("应有 1 个维度: %+v", out)
	}
	if out.Dimensions[0].Label != "5h" {
		t.Errorf("label = %q", out.Dimensions[0].Label)
	}
	if out.Stale {
		t.Error("刚写的数据不该是 stale")
	}
	if out.FetchedAt == 0 {
		t.Error("输出应带 fetched_at")
	}
}

// TestRunQuota_RefreshMakesRequest 断言：--refresh 会发网络请求并写回数据文件。
func TestRunQuota_RefreshMakesRequest(t *testing.T) {
	baseDir := quotaHome(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":200000000,"total_used":2576,"total_available":199997424}}`)
	}))
	defer srv.Close()

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://one.example.com/api/llm/console/chat",
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
		},
		"quota": map[string]any{"source": "newapi", "endpoint": srv.URL, "quotaPerUnit": float64(500000)},
	})

	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1", "--refresh", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("--refresh 应发 1 次请求, calls=%d", calls)
	}

	// 数据文件被写回
	st := quota.NewStore(baseDir)
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := f.EntryFor("p1")
	if !ok {
		t.Fatal("数据文件应有 p1 的条目")
	}
	if len(e.Dimensions) != 1 {
		t.Errorf("应有 1 个维度: %+v", e.Dimensions)
	}

	// 输出的是**刚查到的**值
	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, stdout)
	}
	if len(out.Dimensions) != 1 {
		t.Errorf("输出应有维度: %+v", out)
	}
}

// TestRunQuota_RefreshFailureKeepsOldEntry 断言：--refresh 失败 → 退出码 1，
// **旧条目保留**。
func TestRunQuota_RefreshFailureKeepsOldEntry(t *testing.T) {
	baseDir := quotaHome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env":   map[string]any{"ANTHROPIC_BASE_URL": "https://one.example.com/x"},
		"quota": map[string]any{"source": "newapi", "endpoint": srv.URL},
	})

	st := quota.NewStore(baseDir)
	if err := st.Update("p1", "newapi", "pool-1", []quota.Dimension{
		{ID: "quota", Label: "USD", Kind: quota.KindQuota, Unit: "USD"},
	}); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := captureQuotaRun(t, "--provider", "p1", "--refresh", "--json")
	if code != 1 {
		t.Fatalf("退出码 = %d, want 1 (stderr=%s)", code, stderr)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := f.EntryFor("p1")
	if !ok || len(e.Dimensions) != 1 {
		t.Errorf("失败后旧条目不该被丢: %+v", e)
	}
}

// TestRunQuota_NoQuotaInterfaceExit2 断言：没有额度接口 → 退出码 2，
// **不打印额度文档**。
func TestRunQuota_NoQuotaInterfaceExit2(t *testing.T) {
	baseDir := quotaHome(t)

	writeQuotaProvider(t, baseDir, "official", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-official"},
	})

	stdout, stderr, code := captureQuotaRun(t, "--provider", "official", "--json")
	if code != 2 {
		t.Fatalf("退出码 = %d, want 2 (stderr=%s)", code, stderr)
	}
	if strings.Contains(stdout, "dimensions") {
		t.Errorf("失败时不该打印额度文档: %s", stdout)
	}
	if !strings.Contains(stderr, "official") {
		t.Errorf("stderr 应说明原因: %s", stderr)
	}
}

// TestRunQuota_RefreshWithoutQuotaInterfaceMakesNoRequest 断言：
// 没有额度接口时 **--refresh 也不联网**（离线判定在联网之前）。
func TestRunQuota_RefreshWithoutQuotaInterfaceMakesNoRequest(t *testing.T) {
	baseDir := quotaHome(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer srv.Close()

	writeQuotaProvider(t, baseDir, "official", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-official"},
	})

	_, _, code := captureQuotaRun(t, "--provider", "official", "--refresh", "--json")
	if code != 2 {
		t.Fatalf("退出码 = %d, want 2", code)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("没有额度接口时不该发请求, calls=%d", calls)
	}
}

// TestRunQuota_ProviderFromEnv 断言：provider 来自 CCL_PROVIDER 环境变量。
func TestRunQuota_ProviderFromEnv(t *testing.T) {
	baseDir := quotaHome(t)

	writeQuotaProvider(t, baseDir, "envp", map[string]any{
		"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
	})
	st := quota.NewStore(baseDir)
	if err := st.Update("envp", "minimax", "pool-1", []quota.Dimension{
		{ID: "interval", Label: "5h", Kind: quota.KindWindow, Unit: "%"},
	}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CCL_PROVIDER", "envp")
	stdout, stderr, code := captureQuotaRun(t, "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Provider != "envp" {
		t.Errorf("provider = %q, want envp", out.Provider)
	}
}

// TestRunQuota_ExplicitProviderWinsOverEnv 断言：显式 --provider 赢过环境变量。
func TestRunQuota_ExplicitProviderWinsOverEnv(t *testing.T) {
	baseDir := quotaHome(t)

	for _, name := range []string{"a", "b"} {
		writeQuotaProvider(t, baseDir, name, map[string]any{
			"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
		})
	}
	st := quota.NewStore(baseDir)
	for _, name := range []string{"a", "b"} {
		if err := st.Update(name, "minimax", "pool-"+name, []quota.Dimension{
			{ID: "interval", Label: "5h", Kind: quota.KindWindow, Unit: "%"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("CCL_PROVIDER", "a")
	stdout, stderr, code := captureQuotaRun(t, "--provider", "b", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Provider != "b" {
		t.Errorf("provider = %q, want b", out.Provider)
	}
}

// TestRunQuota_StaleFlag 断言：旧数据被标 stale。
func TestRunQuota_StaleFlag(t *testing.T) {
	baseDir := quotaHome(t)

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
	})

	// 手写一条 fetched_at 很旧的条目
	st := quota.NewStore(baseDir)
	f := &quota.File{Version: 1, Entries: map[string]quota.Entry{
		"p1": {
			Source:    "minimax",
			Pool:      "pool-1",
			FetchedAt: time.Now().Add(-10 * time.Minute).Unix(),
			Dimensions: []quota.Dimension{
				{ID: "interval", Label: "5h", Kind: quota.KindWindow, Unit: "%"},
			},
		},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Stale {
		t.Error("10 分钟前的数据应被标 stale")
	}
}

// TestRunQuota_NoRecordExit1 断言：没有记录 → 退出码 1，不打印额度文档。
func TestRunQuota_NoRecordExit1(t *testing.T) {
	baseDir := quotaHome(t)

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
	})

	stdout, _, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 1 {
		t.Fatalf("退出码 = %d, want 1", code)
	}
	if strings.Contains(stdout, "dimensions") {
		t.Errorf("失败时不该打印额度文档: %s", stdout)
	}
}

// TestRunQuota_UnknownProviderExit1 断言：provider 不存在 → 退出码 1。
func TestRunQuota_UnknownProviderExit1(t *testing.T) {
	quotaHome(t)
	_, _, code := captureQuotaRun(t, "--provider", "nope", "--json")
	if code != 1 {
		t.Fatalf("退出码 = %d, want 1", code)
	}
}

// TestRunQuota_TextOutput 断言：非 --json 时输出给人看的表格。
func TestRunQuota_TextOutput(t *testing.T) {
	baseDir := quotaHome(t)

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
	})
	st := quota.NewStore(baseDir)
	if err := st.Update("p1", "minimax", "pool-1", []quota.Dimension{
		{ID: "interval", Label: "5h", Kind: quota.KindWindow, Duration: 18000 * time.Second,
			Used: quotaFloat(59), Limit: quotaFloat(100), Unit: "%"},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "p1") || !strings.Contains(stdout, "5h") {
		t.Errorf("表格输出应含 provider 与维度标签: %s", stdout)
	}
	if !strings.Contains(stdout, "59") {
		t.Errorf("表格输出应含已用百分比: %s", stdout)
	}
}

// quotaFloat 是测试里造 *float64 的小助手。
func quotaFloat(v float64) *float64 { return &v }

// TestRunQuota_RefreshRespectsBackoff 断言：退避中的 provider → `--refresh`
// **不发请求**（手动刷不能冲掉退避）+ 提示还剩多少秒。
func TestRunQuota_RefreshRespectsBackoff(t *testing.T) {
	baseDir := quotaHome(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `{"code":true,"data":{"total_granted":1,"total_used":0,"total_available":1}}`)
	}))
	defer srv.Close()

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://one.example.com/x",
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
		},
		"quota": map[string]any{"source": "newapi", "endpoint": srv.URL},
	})

	// 造一条"正在退避中"的记录（退避按**厂商**记录 —— quota-per-vendor-rate-limit §D2）
	st := quota.NewStore(baseDir)
	pool := quota.PoolID(srv.URL, "sk-x")
	f := &quota.File{
		Version: 1,
		Entries: map[string]quota.Entry{
			"p1": {
				Source:    "newapi",
				Pool:      pool,
				FetchedAt: time.Now().Unix(),
			},
		},
		Backoffs: map[string]quota.Backoff{
			quota.VendorBackoffKey(srv.URL): {RetryAfter: time.Now().Add(30 * time.Second).Unix(), Seconds: 60},
		},
	}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureQuotaRun(t, "--provider", "p1", "--refresh", "--json")
	if code != 1 {
		t.Fatalf("退出码 = %d, want 1 (stderr=%s)", code, stderr)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("退避中不该发请求, calls=%d", calls)
	}
	if !strings.Contains(stderr, "退避") {
		t.Errorf("stderr 应说明在退避中: %s", stderr)
	}
	if strings.Contains(stdout, "dimensions") {
		t.Errorf("失败时不该打印额度文档: %s", stdout)
	}
}

// TestRunQuota_StaleTolerance 断言：stale 判定给 1 秒容忍（取整误差）。
//
// fetched_at 只存到秒 → 数据年龄最多比真实值大 1 秒。阈值正好等于刷新周期时，
// 稳态下每次刷新前约 1 秒会误报 stale（实测 age 峰值 60.8~61.5 秒）→ 状态栏
// 上 "(stale)" 偶尔闪一下。
func TestRunQuota_StaleTolerance(t *testing.T) {
	baseDir := quotaHome(t)

	writeQuotaProvider(t, baseDir, "p1", map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://api.minimax.cn/anthropic",
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
		},
	})

	st := quota.NewStore(baseDir)
	writeEntry := func(age time.Duration) {
		f := &quota.File{Version: 1, Entries: map[string]quota.Entry{
			"p1": {
				Source:    "minimax",
				Pool:      "pool-1",
				FetchedAt: time.Now().Add(-age).Unix(),
				Dimensions: []quota.Dimension{
					{ID: "interval", Label: "5h", Kind: quota.KindWindow, Unit: "%"},
				},
			},
		}}
		if err := st.Save(f); err != nil {
			t.Fatal(err)
		}
	}

	// age ≈ 60 秒（周期 60 + 取整误差 ≤ 1 秒）→ **不**标 stale
	writeEntry(60 * time.Second)
	_, stderr, code := captureQuotaRun(t, "--provider", "p1", "--json")
	if code != 0 {
		t.Fatalf("退出码 = %d, stderr=%s", code, stderr)
	}
	stdout, _, _ := captureQuotaRun(t, "--provider", "p1", "--json")
	var out quota.Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Stale {
		t.Errorf("age 60 秒（周期 60）不该标 stale（取整误差容忍）")
	}

	// age 明显超过周期 + 容忍 → 标 stale
	writeEntry(70 * time.Second)
	stdout2, _, _ := captureQuotaRun(t, "--provider", "p1", "--json")
	var out2 quota.Output
	if err := json.Unmarshal([]byte(stdout2), &out2); err != nil {
		t.Fatal(err)
	}
	if !out2.Stale {
		t.Errorf("age 70 秒应标 stale")
	}
}

// TestActiveProviders 断言：serve 只刷新**最近有活动的** provider。
func TestActiveProviders(t *testing.T) {
	act := collector.NewActivity()
	act.Mark("p1", time.Now())

	refs := []quota.ProviderRef{{Name: "p1"}, {Name: "p2"}, {Name: "p3"}}
	out := activeProviders(refs, act)
	if len(out) != 1 || !out["p1"] {
		t.Errorf("只该把活跃的 p1 放进名单, got %+v", out)
	}

	// 一个都不活跃 → 名单为空（一个请求都不发）
	act2 := collector.NewActivity()
	if out := activeProviders(refs, act2); len(out) != 0 {
		t.Errorf("没人活跃时不该查任何 provider, got %+v", out)
	}

	// 拿不到活动信息 → nil（= 不过滤，宁可多查不要不查）
	if out := activeProviders(refs, nil); out != nil {
		t.Errorf("activity 为 nil 时应返回 nil（不过滤）, got %+v", out)
	}
}
