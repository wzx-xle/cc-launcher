package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"cc-launcher/internal/config"
)

// backup-on-control-handover §1.2 + §1.3：isUnderCclControl 主路径 + 边界 case。
func TestIsUnderCclControl(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]any
		want bool
	}{
		// §1.2 主路径：4 个 case
		{
			name: "fallback endpoint is ccl default → in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
			},
			want: true,
		},
		{
			name: "per-signal metrics endpoint is ccl default → in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318/v1/metrics",
			},
			want: true,
		},
		{
			name: "both endpoints are ccl defaults → in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318/v1/metrics",
			},
			want: true,
		},
		{
			name: "neither endpoint matches ccl defaults → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "https://company-otel:4317",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://other-tool:4318/v1/metrics",
			},
			want: false,
		},
		// §1.3 边界 case
		{
			name: "env is nil → NOT in control (treated as handover boundary)",
			env:  nil,
			want: false,
		},
		{
			name: "endpoint key absent → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
			},
			want: false,
		},
		{
			name: "endpoint value is nil → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": nil,
			},
			want: false,
		},
		{
			name: "endpoint value is non-string (int) → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": 4318,
			},
			want: false,
		},
		{
			name: "endpoint value is localhost:4318 but wrong scheme → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://localhost:4318",
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isUnderCclControl(tc.env)
			if got != tc.want {
				t.Errorf("isUnderCclControl(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

// telemetry-config-compat §4.3

func TestInjectOTelEnv_FORWORD_Propagation(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 3)

	// 准备典型公司远程 collector 配置：用户设了 per-signal metrics endpoint
	initial := map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT":      "http://remote:4318",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://remote:4318/v1/metrics",
		},
	}
	if err := cm.SaveSettings(initial); err != nil {
		t.Fatal(err)
	}

	const fwdKey = "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	// 确保初始 env 是干净的，避免被其它测试污染
	t.Setenv(fwdKey, "")

	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	got := os.Getenv(fwdKey)
	if got != "http://remote:4318/v1/metrics" {
		t.Errorf("FORWORD_ env must be set to user original value: got %q", got)
	}
}

// telemetry-config-compat §4.4

func TestInjectOTelEnv_FORWORD_EdgeCases(t *testing.T) {
	const fwdKey = "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"

	t.Run("user missing per-signal metrics → FORWORD_ not set", func(t *testing.T) {
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		// 仅设 fallback，无 per-signal metrics
		initial := map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://remote:4318",
			},
		}
		if err := cm.SaveSettings(initial); err != nil {
			t.Fatal(err)
		}
		t.Setenv(fwdKey, "")

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}
		if got := os.Getenv(fwdKey); got != "" {
			t.Errorf("FORWORD_ should NOT be set when user missing per-signal metrics: got %q", got)
		}
	})

	t.Run("pre-existing FORWORD_ env → overwritten with settings.json backup", func(t *testing.T) {
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		initial := map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://user-original:9999/v1/metrics",
			},
		}
		if err := cm.SaveSettings(initial); err != nil {
			t.Fatal(err)
		}
		// 预存一个不一样的值（模拟 daemon 已启动后残留）
		t.Setenv(fwdKey, "http://stale:1/v1/metrics")

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}
		if got := os.Getenv(fwdKey); got != "http://user-original:9999/v1/metrics" {
			t.Errorf("FORWORD_ must be overwritten with backup value: got %q", got)
		}
	})

	t.Run("idempotent — second call without daemon changes does not harm env", func(t *testing.T) {
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		initial := map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://user-original:9999/v1/metrics",
			},
		}
		if err := cm.SaveSettings(initial); err != nil {
			t.Fatal(err)
		}
		t.Setenv(fwdKey, "")

		// 第一次注入
		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("first injectOTelEnv: %v", err)
		}
		firstVal := os.Getenv(fwdKey)

		// 第二次注入（settings.json 没变 → injectOTelEnv 仍然写 env，幂等）
		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("second injectOTelEnv: %v", err)
		}
		secondVal := os.Getenv(fwdKey)
		if firstVal != secondVal {
			t.Errorf("FORWORD_ value changed between calls: %q → %q", firstVal, secondVal)
		}
		if firstVal != "http://user-original:9999/v1/metrics" {
			t.Errorf("FORWORD_ should hold user backup: %q", firstVal)
		}
	})
}

// backup-on-control-handover §3：完整 spec scenario 覆盖。

// clearOTelBackupEnvs 清理 injectOTelEnv 会 Setenv 的所有备份键，避免测试间污染。
func clearOTelBackupEnvs(t *testing.T) {
	t.Helper()
	keys := []string{
		"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT",
		"CC_ORIG_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"CC_ORIG_OTEL_LOGS_EXPORTER",
		"CC_ORIG_OTEL_METRICS_EXPORTER",
		"CC_ORIG_OTEL_EXPORTER_OTLP_PROTOCOL",
		"CC_ORIG_CLAUDE_CODE_ENABLE_TELEMETRY",
		"FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"CC_FORWARD_AUTH_HEADER",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}
//
// 每个 subtest 对应 spec "claude-code-env-config" 的一个 Scenario。
// 用 t.Run 集中管理便于 spec 修订时定位测试。

func TestInjectOTelEnv_SpecScenarios(t *testing.T) {
	// §3.5 回归测试：Manager.Backup 对不存在的 settings.json 返回 nil 且不创建目录。
	// 提到最前面因为 §3.1 等 scenario 隐含依赖此契约。
	t.Run("backup no-op when settings.json missing", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "settings.json"), filepath.Join(dir, "backups"), 3)
		if err := cm.Backup(); err != nil {
			t.Fatalf("Backup on missing settings.json must be no-op, got err: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
			t.Errorf("backupDir should NOT be created when settings.json is missing (stat err=%v)", err)
		}
	})

	// §3.1：首次 ccl 启动，settings.json 健康基线被备份。
	t.Run("first-run backup captures user original", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		entries, err := os.ReadDir(filepath.Join(dir, "b"))
		if err != nil {
			t.Fatalf("readDir backupDir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 backup, got %d", len(entries))
		}
		data, _ := os.ReadFile(filepath.Join(dir, "b", entries[0].Name()))
		var backup map[string]any
		if err := json.Unmarshal(data, &backup); err != nil {
			t.Fatal(err)
		}
		env := backup["env"].(map[string]any)
		if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
			t.Errorf("backup must capture user original endpoint, got %v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
		}
	})

	// §3.2：ccl 已在控制中（每次常规启动），跳过 Backup。
	t.Run("in-control skips backup", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
			entries, _ := os.ReadDir(filepath.Join(dir, "b"))
			t.Errorf("backupDir must be absent (in-control skip), got %d entries (stat err=%v)", len(entries), err)
		}
	})

	// §3.3：第三方覆盖 OTel 端点但 CC_ORIG_* 残留，触发 Backup。
	// ccl 重新接管但不擦除 CC_ORIG_*（那是 restore 的工作）。
	t.Run("third-party overwrite triggers backup and keeps CC_ORIG_", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":                "http://other-tool:4318",
				"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT":         "https://company-otel:4317",
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		// 1. 触发了 Backup
		entries, err := os.ReadDir(filepath.Join(dir, "b"))
		if err != nil {
			t.Fatalf("readDir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 backup, got %d", len(entries))
		}
		// 备份内容反映"接管前"——三方覆盖态
		data, _ := os.ReadFile(filepath.Join(dir, "b", entries[0].Name()))
		var backup map[string]any
		json.Unmarshal(data, &backup)
		bEnv := backup["env"].(map[string]any)
		if bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://other-tool:4318" {
			t.Errorf("backup must capture pre-takeover state, got endpoint=%v", bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
		}

		// 2. ccl 接管后 CC_ORIG_* 仍在（restore 才清）
		settings, _ := cm.LoadSettings()
		curEnv := settings["env"].(map[string]any)
		if curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
			t.Errorf("ccl must take over endpoint, got %v", curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
		}
		if curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
			t.Errorf("CC_ORIG_ must NOT be erased by injectOTelEnv (that's restore's job), got %v", curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"])
		}
	})

	// §3.4：per-signal metrics 接管路径识别为 ccl 控制中。
	t.Run("per-signal metrics takeover is recognized as in-control", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://user-company:4317", // 兜底非 localhost
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318/v1/metrics", // per-signal 是 ccl 默认
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
			entries, _ := os.ReadDir(filepath.Join(dir, "b"))
			t.Errorf("backupDir must be absent (per-signal takeover → in-control), got %d entries", len(entries))
		}
	})

	// §3.7：settings.json 存在但无 env 字段（用户从未用 OTel）。
	// 也是控制权交接边界 → Backup 触发。
	t.Run("settings.json without env field triggers backup", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"enabledPlugins": map[string]any{"some-plugin": true},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		entries, err := os.ReadDir(filepath.Join(dir, "b"))
		if err != nil {
			t.Fatalf("readDir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 backup (no env field = handover boundary), got %d", len(entries))
		}
	})
}

// backup-on-control-handover §3.6：Backup 失败仅警告，不阻断 ccl 主流程。
//
// 测试方式：os.Chmod(backupDir, 0) 制造不可写 + defer 恢复；断言
//   - injectOTelEnv 返回 nil（不向上传播 Backup 错误）
//   - SaveSettings 仍发生（OTel 注入仍完成）
//   - log 输出含警告行（用 log.SetOutput 重定向到 buffer）
//
// 平台限制：仅 Linux/macOS 生效；Windows 上 t.Skipf 跳过。
func TestInjectOTelEnv_BackupFailureOnlyWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("Backup 权限测试在 Windows 不可靠（chmod 语义差异）")
	}
	clearOTelBackupEnvs(t)

	dir := t.TempDir()
	backupDir := filepath.Join(dir, "b")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backupDir, 0); err != nil {
		t.Fatalf("chmod 0: %v", err)
	}
	if os.Geteuid() == 0 {
		_ = os.Chmod(backupDir, 0755)
		t.Skipf("root 用户不受 chmod 0 限制，跳过该测试")
	}
	t.Cleanup(func() { _ = os.Chmod(backupDir, 0755) })

	cm := config.NewManager(filepath.Join(dir, "s.json"), backupDir, 3)
	cm.SaveSettings(map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	})

	// 重定向 log 输出（log 包内部缓存了默认 stderr 的引用，
	// 替换 os.Stderr 变量无效，必须用 log.SetOutput）。
	var logBuf bytes.Buffer
	origLogOutput := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origLogOutput) })

	if err := injectOTelEnv(cm); err != nil {
		t.Errorf("injectOTelEnv must NOT return Backup error, got: %v", err)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "injectOTelEnv: 备份 settings.json 失败") {
		t.Errorf("log output should contain Backup warning, got: %q", logged)
	}

	// 验证 SaveSettings 仍发生（应用级备份 + endpoint 改写完成）
	settings, err := cm.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	env := settings["env"].(map[string]any)
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
		t.Errorf("OTel injection must complete despite Backup failure, got endpoint=%v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

// =============================================================================
// cli-shell-completion change: completion unit 测试（§8.1-8.4）
//
// 这些测试不依赖 daemon / 文件系统 / SQLite，纯函数（输入 args → 输出 token 流），
// 走 `go test ./...` 主路径。
// =============================================================================

// captureStdout 临时把 os.Stdout 接到 buffer，跑 fn，返回 buffer 内容。
//
// 注：smoke_test.go（带 build tag "smoke"）也用本 helper——同 package 不能重复声明，
// 故只在本文件定义一次。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

// captureStderr 临时把 os.Stderr 接到 buffer，跑 fn，返回 buffer 内容。
//
// 注：smoke_test.go（带 build tag "smoke"）也用本 helper——同 package 不能重复声明，
// 故只在本文件定义一次。
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

// linesFromOutput 把 stdout 文本拆成行（trim 空行），返回 slice。
func linesFromOutput(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// assertContainsLines 验证 out 包含 expected 所有行（顺序无关。
func assertContainsLines(t *testing.T, out string, expected []string) {
	t.Helper()
	got := linesFromOutput(out)
	gotSet := map[string]bool{}
	for _, g := range got {
		gotSet[g] = true
	}
	for _, want := range expected {
		if !gotSet[want] {
			t.Errorf("output missing %q\ngot: %v", want, got)
		}
	}
}

// §8.1.a: 顶层候选（ccl __complete ""）
func TestRunCompletion_TopLevel(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"serve", "stats", "provider", "mode",
		"-h", "--help", "help",
	})
}

// §8.1.b: serve 候选（ccl __complete serve ""）
func TestRunCompletion_ServeFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"serve", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"--stop", "--restore", "--status", "--restart",
		"-h", "--help",
	})
}

// §8.1.c: serve mutex 防御（ccl __complete serve --stop ""）
func TestRunCompletion_ServeMutexBlocks(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"serve", "--stop", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("mutex: expected empty stdout, got %q", out)
	}
}

// §8.1.d: stats 候选（ccl __complete stats ""）
func TestRunCompletion_StatsFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"stats", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"--since", "--by-provider", "--by-project", "--project", "--json",
		"-h", "--help",
	})
}

// §8.1.e: provider 候选（ccl __complete provider ""）
// 注意：spec 要求 provider 后位置不补（shell filename 接管）；但这里实际是触发 "无 flag
// 位置"——extractFlagNames 给出全部 flag（按 spec scenario）。
func TestRunCompletion_ProviderFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"provider", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"--list", "--new", "--delete", "--template", "-T", "--type", "--help", "-h",
	})
}

// §8.1.f: provider --template 位置补 7 个模板名
func TestRunCompletion_ProviderTemplate(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"provider", "--template", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"anthropic", "deepseek", "minimax", "glm",
		"aliyun-coding", "aliyun-pay", "aliyun-token",
	})
	// 应该不包含 flag
	for _, bad := range []string{"--list", "--new", "--delete"} {
		if strings.Contains(out, bad) {
			t.Errorf("template 位置不应补 flag, found %q in %q", bad, out)
		}
	}
}

// §8.1.g: provider --new --type 位置补 7 个模板名
func TestRunCompletion_ProviderNewType(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"provider", "--new", "--type", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"anthropic", "deepseek", "minimax", "glm",
		"aliyun-coding", "aliyun-pay", "aliyun-token",
	})
}

// §8.1.h: provider --delete 位置 → 空（spec §3.8）
func TestRunCompletion_ProviderDeleteEmpty(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"provider", "--delete", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("--delete 后位置应为空, got %q", out)
	}
}

// §8.1.i: provider --new 后位置补 --type / -T / --template / -h / --help（spec §3.9）
func TestRunCompletion_ProviderNewEmpty(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"provider", "--new", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"--type", "-T", "--template", "-h", "--help",
	})
}

// §8.1.j: provider --list 后位置 → 空（mutex 推广）
func TestRunCompletion_ProviderListEmpty(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"provider", "--list", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("--list 后位置应为空, got %q", out)
	}
}

// §8.1.k: mode 候选（ccl __complete mode ""）
func TestRunCompletion_Mode(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"mode", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"manual", "acceptEdits", "auto", "bypassPermissions", "dontAsk", "plan",
	})
}

// §8.1.l: unknown subcommand（如 "help"）→ 空（spec §3.10）
func TestRunCompletion_UnknownSubcommandEmpty(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"help", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("unknown subcommand 应输出空, got %q", out)
	}
}

// §8.1.m: bogus subcommand → 空
func TestRunCompletion_BogusEmpty(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"bogus", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("bogus subcommand 应输出空, got %q", out)
	}
}

// §8.1.n: 空 args（防御：runCompletion 收到 nil/空 slice）
func TestRunCompletion_EmptyArgs(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{"serve", "stats", "provider", "mode"})
}

// §8.2: bash wrapper 输出含 "complete -F" 关键字
func TestRunShellInit_Bash(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runShellInit("bash"); err != nil {
			t.Errorf("runShellInit(bash) returned error: %v", err)
		}
	})
	if out == "" {
		t.Fatal("bash wrapper 输出为空")
	}
	if !strings.Contains(out, "complete -F") {
		t.Errorf("bash wrapper 应含 'complete -F', got: %s", out)
	}
}

// §8.3: zsh wrapper 输出含 "compdef" 关键字
func TestRunShellInit_Zsh(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runShellInit("zsh"); err != nil {
			t.Errorf("runShellInit(zsh) returned error: %v", err)
		}
	})
	if out == "" {
		t.Fatal("zsh wrapper 输出为空")
	}
	if !strings.Contains(out, "compdef") {
		t.Errorf("zsh wrapper 应含 'compdef', got: %s", out)
	}
}

// §8.4: fish (未知 shell) → exit 非 0 + stderr 含 "unknown shell"
//
// 测试覆盖 exitFn 为 no-op 避免真退出整个 test 进程；验证 stderr + exitFn 被调用。
func TestRunShellInit_FishUnknown(t *testing.T) {
	// 覆盖 exitFn 记录调用
	exitCode := -1
	exitCalled := false
	origExit := exitFn
	exitFn = func(code int) { exitCalled = true; exitCode = code }
	t.Cleanup(func() { exitFn = origExit })

	stderr := captureStderr(t, func() {
		if err := runShellInit("fish"); err != nil {
			t.Errorf("runShellInit(fish) returned error: %v", err)
		}
	})

	if !exitCalled {
		t.Error("未知 shell 应触发 exitFn")
	}
	if exitCode != 1 {
		t.Errorf("未知 shell exit code 应为 1, got %d", exitCode)
	}
	if !strings.Contains(stderr, "unknown shell") {
		t.Errorf("stderr 应含 'unknown shell', got: %q", stderr)
	}
	if !strings.Contains(stderr, "fish") {
		t.Errorf("stderr 应含 shell 名 'fish', got: %q", stderr)
	}
}

// 辅助：sort 一份字符串 slice（用来比较候选集时消除 fs 内部顺序影响）。
func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}