package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cc-launcher/internal/config"
	"cc-launcher/internal/state"
)

// =============================================================================
// otel-endpoints-via-settings：OTel 环境准备
//
// 核心不变量：**ccl 不写 ~/.claude/settings.json** —— 端点接管全靠 --settings。
// =============================================================================

// clearOtelEnvs 清理 prepareOtelEnv 会 Setenv 的转发目标变量，避免测试间污染。
func clearOtelEnvs(t *testing.T) {
	t.Helper()
	for _, k := range []string{envOrigEndpoint, envForwardMetricsEndpoint} {
		t.Setenv(k, "")
	}
}

// newTestStateMgr 造一个 state.Manager（临时目录）。
func newTestStateMgr(t *testing.T) *state.Manager {
	t.Helper()
	return state.NewManager(t.TempDir())
}

// TestPrepareOtelEnv_DoesNotTouchSettingsFile 断言 ccl 不改用户的 settings 文件。
//
// 这是本 change 的核心不变量：旧版 ccl 会把端点改写进 settings.json，新版只读。
func TestPrepareOtelEnv_DoesNotTouchSettingsFile(t *testing.T) {
	clearOtelEnvs(t)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	cm := config.NewManager(settingsPath, filepath.Join(dir, "b"), 3)
	if err := cm.SaveSettings(map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
		"permissions": map[string]any{"allow": []any{"Bash(ls)"}},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	deliver := prepareOtelEnv(cm, newTestStateMgr(t))

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("settings.json 不得被 ccl 修改:\nbefore=%s\nafter=%s", before, after)
	}

	// 端点仍然由 --settings 接管
	if deliver[keyEndpoint] != defaultEndpoint {
		t.Errorf("交付的端点应为 ccl 默认值, got %q", deliver[keyEndpoint])
	}
}

// TestPrepareOtelEnv_DeliversTakeover 断言交付给子进程的 env 块内容。
//
// spec/claude-code-env-config §"ccl delivers the OTel env to Claude Code through
// --settings"：接管 2 个端点键、尊重用户写过的 exporter/开关、绝不交付 logs。
func TestPrepareOtelEnv_DeliversTakeover(t *testing.T) {
	clearOtelEnvs(t)
	dir := t.TempDir()
	cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
	if err := cm.SaveSettings(map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	}); err != nil {
		t.Fatal(err)
	}

	deliver := prepareOtelEnv(cm, newTestStateMgr(t))

	// 接管：两个端点键都是 ccl 的默认值
	if deliver[keyEndpoint] != defaultEndpoint {
		t.Errorf("%s 应被接管为 %q, got %q", keyEndpoint, defaultEndpoint, deliver[keyEndpoint])
	}
	if deliver[keyMetricsEndpoint] != defaultMetricsEndpoint {
		t.Errorf("%s 应被接管为 %q, got %q", keyMetricsEndpoint, defaultMetricsEndpoint, deliver[keyMetricsEndpoint])
	}
	// 用户没写 → 补默认值
	if deliver[keyMetricsExport] != defaultMetricsExporter {
		t.Errorf("%s 应补默认值 %q, got %q", keyMetricsExport, defaultMetricsExporter, deliver[keyMetricsExport])
	}
	if deliver[keyProtocol] != defaultProtocol {
		t.Errorf("%s 应补默认值 %q, got %q", keyProtocol, defaultProtocol, deliver[keyProtocol])
	}
	if deliver[keyEnableTelemetry] != defaultEnableTelemetry {
		t.Errorf("%s 应补默认值 %q, got %q", keyEnableTelemetry, defaultEnableTelemetry, deliver[keyEnableTelemetry])
	}
	// logs exporter 永不交付
	if _, ok := deliver[keyLogsExporter]; ok {
		t.Errorf("%s 不该被交付, got %q", keyLogsExporter, deliver[keyLogsExporter])
	}
}

// TestPrepareOtelEnv_RespectsUserChoice 断言用户自己写的 exporter 不被覆盖。
func TestPrepareOtelEnv_RespectsUserChoice(t *testing.T) {
	clearOtelEnvs(t)
	dir := t.TempDir()
	cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
	if err := cm.SaveSettings(map[string]any{
		"env": map[string]any{
			"OTEL_METRICS_EXPORTER":       "prometheus",
			"OTEL_LOGS_EXPORTER":          "otlp",
			"CLAUDE_CODE_ENABLE_TELEMETRY": "0",
		},
	}); err != nil {
		t.Fatal(err)
	}

	deliver := prepareOtelEnv(cm, newTestStateMgr(t))

	for _, k := range []string{keyMetricsExport, keyLogsExporter, keyEnableTelemetry} {
		if _, ok := deliver[k]; ok {
			t.Errorf("用户自己写过的 %s 不该被交付, got %q", k, deliver[k])
		}
	}
}

// TestPrepareOtelEnv_ForwardTarget 断言转发目标变量指向用户自己的端点。
//
// spec/claude-code-env-config §"ccl delivers the forwarding target to the daemon
// through its process environment"。
func TestPrepareOtelEnv_ForwardTarget(t *testing.T) {
	t.Run("fallback + per-signal 都投递", func(t *testing.T) {
		clearOtelEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		if err := cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "https://fallback:4317",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://metrics:4318/v1/metrics",
			},
		}); err != nil {
			t.Fatal(err)
		}

		prepareOtelEnv(cm, newTestStateMgr(t))

		if got := os.Getenv(envOrigEndpoint); got != "https://fallback:4317" {
			t.Errorf("%s = %q, want https://fallback:4317", envOrigEndpoint, got)
		}
		if got := os.Getenv(envForwardMetricsEndpoint); got != "https://metrics:4318/v1/metrics" {
			t.Errorf("%s = %q, want https://metrics:4318/v1/metrics", envForwardMetricsEndpoint, got)
		}
	})

	t.Run("用户没有端点 → 变量不设", func(t *testing.T) {
		clearOtelEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)

		prepareOtelEnv(cm, newTestStateMgr(t))

		if got := os.Getenv(envOrigEndpoint); got != "" {
			t.Errorf("%s 不该被设置, got %q", envOrigEndpoint, got)
		}
		if got := os.Getenv(envForwardMetricsEndpoint); got != "" {
			t.Errorf("%s 不该被设置, got %q", envForwardMetricsEndpoint, got)
		}
	})

	t.Run("遗留备份键优先于被改写的 live 键", func(t *testing.T) {
		clearOtelEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		if err := cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://localhost:4318",
				"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
			},
		}); err != nil {
			t.Fatal(err)
		}

		prepareOtelEnv(cm, newTestStateMgr(t))

		if got := os.Getenv(envOrigEndpoint); got != "https://company-otel:4317" {
			t.Errorf("%s = %q, want https://company-otel:4317（备份键优先）", envOrigEndpoint, got)
		}
	})
}

// TestPrepareOtelEnv_CleansLegacyArtifactsOnce 断言遗留清理：还原 + 只跑一次。
//
// spec/claude-code-env-config §"ccl cleans up legacy ccl artifacts from settings.json once"。
func TestPrepareOtelEnv_CleansLegacyArtifactsOnce(t *testing.T) {
	clearOtelEnvs(t)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	cm := config.NewManager(settingsPath, filepath.Join(dir, "b"), 3)
	if err := cm.SaveSettings(map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://localhost:4318",
			"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
			"ANTHROPIC_BASE_URL":                 "https://api.example.com",
		},
	}); err != nil {
		t.Fatal(err)
	}

	sm := newTestStateMgr(t)
	prepareOtelEnv(cm, sm)

	settings, err := cm.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	env := settings["env"].(map[string]any)
	if env[keyEndpoint] != "https://company-otel:4317" {
		t.Errorf("用户原值应被还原, got %v", env[keyEndpoint])
	}
	if _, ok := env["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"]; ok {
		t.Errorf("备份键应被删除, got %v", env)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.example.com" {
		t.Errorf("用户自有键不该被动, got %v", env)
	}

	// 第二次：不再动文件（标志已记）
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	prepareOtelEnv(cm, sm)
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("清理只应跑一次，第二次不该改文件:\nbefore=%s\nafter=%s", before, after)
	}
}

// TestPrepareOtelEnv_NoArtifactLeavesFileAlone 断言没有 ccl 痕迹时不动用户文件。
func TestPrepareOtelEnv_NoArtifactLeavesFileAlone(t *testing.T) {
	clearOtelEnvs(t)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	cm := config.NewManager(settingsPath, filepath.Join(dir, "b"), 3)
	if err := cm.SaveSettings(map[string]any{
		"env": map[string]any{
			"OTEL_METRICS_EXPORTER": "otlp", // 用户自己写的，不是 ccl 注入的
		},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	prepareOtelEnv(cm, newTestStateMgr(t))

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("没有 ccl 痕迹时不该改用户文件:\nbefore=%s\nafter=%s", before, after)
	}
}

// TestHasCclArtifact 断言"旧版 ccl 痕迹"的判定。
func TestHasCclArtifact(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]any
		want bool
	}{
		{"空 env", map[string]any{}, false},
		{"用户自己的 exporter", map[string]any{keyMetricsExport: "otlp"}, false},
		{"用户的远程端点", map[string]any{keyEndpoint: "https://company-otel:4317"}, false},
		{"旧版改写的端点", map[string]any{keyEndpoint: defaultEndpoint}, true},
		{"旧版的 CC_ORIG_ 备份键", map[string]any{backupPrefixOrig + keyEndpoint: "https://x"}, true},
		{"旧版的 FORWORD_ 备份键", map[string]any{backupPrefixForword + keyMetricsEndpoint: "https://x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasCclArtifact(tc.env); got != tc.want {
				t.Errorf("hasCclArtifact(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
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
//
// 决策（stats-time-buckets §D10）：--since 被 --day 取代,completion 候选同步更新。
func TestRunCompletion_StatsFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runCompletion([]string{"stats", ""}); err != nil {
			t.Errorf("runCompletion returned error: %v", err)
		}
	})
	assertContainsLines(t, out, []string{
		"--day", "--by-provider", "--by-project", "--project", "--json",
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

// §6.5.1: runShellInit("powershell") 输出一段以 Register-ArgumentCompleter
// 起始的 PowerShell 脚本（spec/powershell-completion §"__init powershell
// exit code" Scenario "stdout SHALL contain a valid PowerShell script
// (starts with Register-ArgumentCompleter or $ccl = ...)"）。
func TestRunShellInit_Powershell_OutputsScript(t *testing.T) {
	stdout := captureStdout(t, func() {
		if err := runShellInit("powershell"); err != nil {
			t.Errorf("runShellInit(powershell) returned error: %v", err)
		}
	})

	if !strings.Contains(stdout, "Register-ArgumentCompleter") {
		t.Errorf("powershell script should contain Register-ArgumentCompleter, got: %q", stdout)
	}
	if !strings.Contains(stdout, "-Native") {
		t.Errorf("powershell script should use -Native flag (ccl is native binary), got: %q", stdout)
	}
	if !strings.Contains(stdout, "CommandName 'ccl'") {
		t.Errorf("powershell script should register for CommandName 'ccl', got: %q", stdout)
	}
	if !strings.Contains(stdout, "StartsWith") {
		t.Errorf("powershell script should use StartsWith for literal prefix matching (spec §wordToComplete Scenario), got: %q", stdout)
	}
}

// §6.5.2: runShellInit("unknown") 走 default 分支：exitFn(1) + stderr 含
// "unknown shell" + 错误信息列出支持 shell（spec §"__init powershell unknown
// shell error" Scenario）。已存在 TestRunShellInit_FishUnknown 用 "fish" 覆盖
// 同路径；这里用 "nosuchshell" 确认错误信息包含 "powershell" 字样。
func TestRunShellInit_UnknownShell_ExitsAndMentionsPowershell(t *testing.T) {
	exitCode := -1
	exitCalled := false
	origExit := exitFn
	exitFn = func(code int) { exitCalled = true; exitCode = code }
	t.Cleanup(func() { exitFn = origExit })

	stderr := captureStderr(t, func() {
		if err := runShellInit("nosuchshell"); err != nil {
			t.Errorf("runShellInit returned error: %v", err)
		}
	})

	if !exitCalled {
		t.Error("unknown shell should trigger exitFn")
	}
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "powershell") {
		t.Errorf("stderr 应在支持列表中提到 powershell, got: %q", stderr)
	}
}

// 辅助：sort 一份字符串 slice（用来比较候选集时消除 fs 内部顺序影响）。
func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// ─── provider-setenv-cwd-binding §3: migrateSettingsJSON ───

// migrateTestRig 构造 migrate 测试所需的临时 cm + sm。
// snapshot 为 nil 表示不调 SetInjectedSnapshot（state.json 也不存在）。
// settingsEnv 可为 nil 表示不写 settings.json。
func migrateTestRig(t *testing.T, snapshot map[string]any, settingsEnv map[string]any) (*config.Manager, *state.Manager) {
	t.Helper()
	baseDir := t.TempDir()
	sm := state.NewManager(baseDir)
	if snapshot != nil {
		if err := sm.SetInjectedSnapshot(snapshot); err != nil {
			t.Fatalf("SetInjectedSnapshot: %v", err)
		}
	}
	settingsPath := filepath.Join(baseDir, "settings.json")
	if settingsEnv != nil {
		data, err := json.Marshal(map[string]any{"env": settingsEnv})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settingsPath, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	backupDir := filepath.Join(baseDir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 10)
	return cm, sm
}

// TestMigrate_SnapshotNil_NoOp 覆盖 spec/provider-setenv-migration §"snapshot
// is nil or empty" Scenario "migration returns early"——无 snapshot 跳过。
func TestMigrate_SnapshotNil_NoOp(t *testing.T) {
	cm, sm := migrateTestRig(t, nil, map[string]any{
		"ANTHROPIC_AUTH_TOKEN": "sk-user",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
	})

	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	loaded, err := cm.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	env := loaded["env"].(map[string]any)
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-user" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN should be untouched when snapshot is nil, got %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
		t.Error("OTel key should be untouched when snapshot is nil")
	}
	if snap := sm.GetInjectedSnapshot(); snap != nil {
		t.Errorf("snapshot should remain nil, got %v", snap)
	}
}

// TestMigrate_MatchesValue_RemovesAndBacksUp 覆盖 spec §"snapshot has
// ANTHROPIC_AUTH_TOKEN, settings.json matches"——匹配则删 + 备份 + 清空 snapshot。
func TestMigrate_MatchesValue_RemovesAndBacksUp(t *testing.T) {
	baseDir := t.TempDir()
	sm := state.NewManager(baseDir)
	if err := sm.SetInjectedSnapshot(map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote"},
	}); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(baseDir, "settings.json")
	data, _ := json.Marshal(map[string]any{"env": map[string]any{
		"ANTHROPIC_AUTH_TOKEN":          "sk-ccl-wrote", // 匹配 snapshot → 删
		"OTEL_EXPORTER_OTLP_ENDPOINT":   "http://localhost:4318", // 不在 snapshot → 保留
		"CLAUDE_CODE_ENABLE_TELEMETRY":  "1",            // 不在 snapshot → 保留
	}})
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(baseDir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 10)

	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	loaded, _ := cm.LoadSettings()
	env := loaded["env"].(map[string]any)
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("ANTHROPIC_AUTH_TOKEN should be removed after migration")
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
		t.Error("OTel key should be preserved (not in snapshot)")
	}
	if env["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" {
		t.Error("CLAUDE_CODE_ENABLE_TELEMETRY should be preserved (not in snapshot)")
	}

	if snap := sm.GetInjectedSnapshot(); snap != nil {
		t.Errorf("snapshot should be cleared after migration, got %v", snap)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 backup file, got %d", len(entries))
	}
}

// TestMigrate_ValueDiffers_PreservesKey 覆盖 spec §"user manually edited
// ANTHROPIC_AUTH_TOKEN"——值与 snapshot 不同则不删（保护用户手改）。
func TestMigrate_ValueDiffers_PreservesKey(t *testing.T) {
	cm, sm := migrateTestRig(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote"},
	}, map[string]any{
		"ANTHROPIC_AUTH_TOKEN": "sk-user-wrote", // 用户手改
	})

	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	loaded, _ := cm.LoadSettings()
	env := loaded["env"].(map[string]any)
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-user-wrote" {
		t.Errorf("user-edited value should be preserved, got %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if snap := sm.GetInjectedSnapshot(); snap != nil {
		t.Errorf("snapshot should still be cleared even when no key was deleted, got %v", snap)
	}
}

// TestMigrate_KeyNotInSettings_NoOp 覆盖 spec §"snapshot has key but settings.json
// does not"——不在 settings.json 就跳过。
func TestMigrate_KeyNotInSettings_NoOp(t *testing.T) {
	cm, sm := migrateTestRig(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote"},
	}, map[string]any{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318", // 无 ANTHROPIC_AUTH_TOKEN
	})

	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	loaded, _ := cm.LoadSettings()
	env := loaded["env"].(map[string]any)
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("ANTHROPIC_AUTH_TOKEN should not appear")
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
		t.Error("OTel key should be preserved")
	}
	if snap := sm.GetInjectedSnapshot(); snap != nil {
		t.Errorf("snapshot should still be cleared, got %v", snap)
	}
}

// TestMigrate_OtelKeysNotInSnapshot_Preserved 覆盖 spec §"OTel keys not in
// snapshot are preserved" + §"CC_ORIG_* keys not in snapshot are preserved"。
func TestMigrate_OtelKeysNotInSnapshot_Preserved(t *testing.T) {
	cm, sm := migrateTestRig(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote"},
	}, map[string]any{
		"OTEL_EXPORTER_OTLP_ENDPOINT":            "http://localhost:4318",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":    "http://localhost:4318/v1/metrics",
		"OTEL_METRICS_EXPORTER":                  "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":            "http/protobuf",
		"CLAUDE_CODE_ENABLE_TELEMETRY":           "1",
		"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT":    "http://remote:4318",
		"FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://remote:4318/v1/metrics",
		"ANTHROPIC_AUTH_TOKEN":                   "sk-ccl-wrote", // 匹配 → 删
	})

	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	loaded, _ := cm.LoadSettings()
	env := loaded["env"].(map[string]any)
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_METRICS_EXPORTER",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"CLAUDE_CODE_ENABLE_TELEMETRY",
		"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT",
		"FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	} {
		if _, ok := env[key]; !ok {
			t.Errorf("%s should be preserved (not in snapshot)", key)
		}
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("ANTHROPIC_AUTH_TOKEN should be removed")
	}
}

// TestMigrate_Idempotent 覆盖 spec §"second startup after migration"——第二次跑空操作。
func TestMigrate_Idempotent(t *testing.T) {
	cm, sm := migrateTestRig(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote"},
	}, map[string]any{
		"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote",
	})

	// 第一次跑
	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatal(err)
	}
	firstLoaded, _ := cm.LoadSettings()
	firstEnv := firstLoaded["env"].(map[string]any)
	if _, ok := firstEnv["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("first migrate should have removed ANTHROPIC_AUTH_TOKEN")
	}

	// 第二次跑：snapshot 已 nil，settings.json 已无 ANTHROPIC_AUTH_TOKEN → 无操作
	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	secondLoaded, _ := cm.LoadSettings()
	secondEnv := secondLoaded["env"].(map[string]any)
	if _, ok := secondEnv["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("second migrate should not re-add ANTHROPIC_AUTH_TOKEN")
	}
}

// TestMigrate_NoMatchNoBackup 覆盖 spec §"no backup when migration has nothing
// to remove"——无 key 被删时不调 Backup。
func TestMigrate_NoMatchNoBackup(t *testing.T) {
	baseDir := t.TempDir()
	sm := state.NewManager(baseDir)
	if err := sm.SetInjectedSnapshot(map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote"},
	}); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(baseDir, "settings.json")
	data, _ := json.Marshal(map[string]any{"env": map[string]any{
		"ANTHROPIC_AUTH_TOKEN": "sk-user-DIFFERENT", // 值不同 → 不删
	}})
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(baseDir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 10)

	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		// 目录不存在 = 0 backup files（spec §"no backup when migration has
		// nothing to remove" 允许"无操作"语义，包括不创建 backup dir）。
	} else if len(entries) != 0 {
		t.Errorf("expected 0 backup files when no key removed, got %d: %v", len(entries), entries)
	}
}

// ─── otel-endpoints-via-settings: E2E Backup 节奏回归 ───

// TestE2E_OldUserFirstRunTUI_BackupRhythm 覆盖老 ccl 用户升级后的备份节奏：
//   - migrate（provider 迁移）删 1 个匹配 key → Backup（1 次）
//   - 遗留清理（OTel）删掉旧版注入的端点 → Backup（1 次）
//   - 第二次启动：两个迁移都已是 no-op → 不再产生备份
//
// 关键不变量：**新流程不再为"控制权交接"备份** —— ccl 不写 settings.json，
// 没有交接这回事。
func TestE2E_OldUserFirstRunTUI_BackupRhythm(t *testing.T) {
	tmpHome := t.TempDir()

	// 构造 state.json 含 snapshot
	stateDir := filepath.Join(tmpHome, ".cc-launcher")
	sm := state.NewManager(stateDir)
	if err := sm.SetInjectedSnapshot(map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote"},
	}); err != nil {
		t.Fatalf("SetInjectedSnapshot: %v", err)
	}

	// 构造 settings.json 含匹配的 ANTHROPIC_* + 旧版 ccl 注入的 OTel 端点
	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings := map[string]any{"env": map[string]any{
		"ANTHROPIC_AUTH_TOKEN":        "sk-ccl-wrote",          // 匹配 snapshot → 删
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318", // 旧版 ccl 注入的端点 → 遗留清理删
	}}
	data, _ := json.Marshal(settings)
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(stateDir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 10)

	// 模拟 runTUI 的顺序：provider 迁移 → OTel 环境准备
	if err := migrateSettingsJSON(cm, sm); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prepareOtelEnv(cm, sm)

	// 第一次：migrate + 遗留清理各备份一次（备份文件名精确到秒，同秒会互相覆盖，
	// 所以这里只断言"有备份"+"旧版端点已被清掉"）
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("首次启动应产生备份")
	}

	// 旧版 ccl 注入的端点应被清掉
	after, err := cm.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	envAny, _ := after["env"].(map[string]any)
	if _, ok := envAny["OTEL_EXPORTER_OTLP_ENDPOINT"]; ok {
		t.Errorf("旧版注入的端点应被清理掉: %v", envAny)
	}
	if envAny["ANTHROPIC_AUTH_TOKEN"] != nil {
		t.Errorf("provider 迁移应删掉匹配的 key: %v", envAny)
	}

	// 第二次：都是 no-op → 不再产生备份
	before := map[string]bool{}
	for _, e := range entries {
		before[e.Name()] = true
	}
	prepareOtelEnv(cm, sm)
	entries, err = os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(before) {
		t.Errorf("第二次启动后 backups count = %d, want %d（不再新增）", len(entries), len(before))
	}
}

// TestMigrate_StderrLogOnChange 覆盖 spec §"migration prints message before
// write"——至少一个 key 被删时打 stderr 日志（包含 backup dir 路径与 key 数）。
func TestMigrate_StderrLogOnChange(t *testing.T) {
	cm, sm := migrateTestRig(t, map[string]any{
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote",
			"ANTHROPIC_BASE_URL":   "https://api.deepseek.com",
		},
	}, map[string]any{
		"ANTHROPIC_AUTH_TOKEN": "sk-ccl-wrote",
		"ANTHROPIC_BASE_URL":   "https://api.deepseek.com",
	})

	stderr := captureStderr(t, func() {
		if err := migrateSettingsJSON(cm, sm); err != nil {
			t.Errorf("migrate: %v", err)
		}
	})

	if !strings.Contains(stderr, "检测到 ccl 之前注入") {
		t.Errorf("stderr 应含 '检测到 ccl 之前注入', got: %q", stderr)
	}
	if !strings.Contains(stderr, cm.BackupDir()) {
		t.Errorf("stderr 应含 backup dir %q, got: %q", cm.BackupDir(), stderr)
	}
	if !strings.Contains(stderr, "2") { // 2 keys cleaned
		t.Errorf("stderr 应含 key 数 '2', got: %q", stderr)
	}
}