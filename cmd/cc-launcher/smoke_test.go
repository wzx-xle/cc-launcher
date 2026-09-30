//go:build smoke

// backup-on-control-handover §7.3-§7.6 sandbox smoke test +
// flatten-stop-restore-into-serve §7.3-§7.7 + §9.6 / §9.8 sandbox smoke test。
//
// 在 sandbox tmpdir 上模拟真实 HOME，验证 backup 触发语义 + serve flag 行为：
//   - §7.3 首跑 backup：pristine settings → ccl 启动 → 新增快照含原值
//   - §7.4 控制中跳过：第二次启动 → backups/ 文件数不变
//   - §7.5 第三方覆盖 backup：改 endpoint → ccl 启动 → 新增三方覆盖态快照
//   - §7.6 restore 不受影响：跑 restoreOTelEnv（`ccl serve --restore` 内部）→ settings.json 还原到接管前
//   - 7.3 serve --help 包含 --stop / --restore 两条 flag 说明
//   - 7.4 serve --stop daemon 不在 → "daemon not running"，exit 0
//   - 7.5 serve --restore settings.json 不在 → "no settings to restore"，exit 0
//   - 7.6 serve --stop --restore 互斥 → 非 0 exit
//   - 7.7 ccl stop / ccl restore 已砍 → 走兜底分支报"未知子命令"，非 0 exit
//   - 9.6 serve --status daemon 不在 → "daemon: not running" + "port :4318: unreachable"
//   - 9.8 serve --stop --status 互斥 → 非 0 exit
//
// 不通过 ccl 二进制的 TUI（避免交互阻塞），直接调 main 包函数；
// 端到端路径与 ccl runTUI / ccl serve 完全等价。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cc-launcher/internal/config"
	"cc-launcher/internal/stats"
)

// smokeSandbox 准备 sandbox HOME + ccl 配置目录，返回 settings / backups 路径。
func smokeSandbox(t *testing.T) (settingsPath, backupDir string) {
	t.Helper()
	dir := t.TempDir()
	settingsPath = filepath.Join(dir, ".claude", "settings.json")
	backupDir = filepath.Join(dir, ".cc-launcher", "backups")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatal(err)
	}
	return settingsPath, backupDir
}

func writeSettings(t *testing.T, path string, content map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSmoke_FirstRunBackup(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)

	// pristine settings.json 含用户原值（ccl 未接管）
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	})

	cm := config.NewManager(settingsPath, backupDir, 10)
	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	// §7.3 断言：backups/ 新增快照含原值
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("readDir backupDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup after first run, got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	var backup map[string]any
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatal(err)
	}
	env := backup["env"].(map[string]any)
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
		t.Errorf("first-run backup must capture user original endpoint, got %v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

func TestSmoke_InControlSkipsBackup(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)
	cm := config.NewManager(settingsPath, backupDir, 10)

	// 模拟"已注入"状态：endpoint 是 ccl 默认值
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		},
	})

	// 第一次 injectOTelEnv：应在控制下，不备份
	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("first injectOTelEnv: %v", err)
	}
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 0 {
		t.Errorf("in-control first run should NOT backup, got %d entries", len(entries))
	}

	// §7.4 第二次启动（仍控制中）→ 文件数应仍为 0
	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("second injectOTelEnv: %v", err)
	}
	entries, _ = os.ReadDir(backupDir)
	if len(entries) != 0 {
		t.Errorf("§7.4 in-control second run must NOT add backup, got %d entries", len(entries))
	}
}

func TestSmoke_ThirdPartyOverwriteTriggersBackup(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)
	cm := config.NewManager(settingsPath, backupDir, 10)

	// 模拟第三方覆盖：endpoint 是非 ccl 默认值 + CC_ORIG_* 残留
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://other-tool:4318",
			"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	})

	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	// §7.5 断言：backups/ 新增三方覆盖态快照
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("readDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup after third-party overwrite, got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	var backup map[string]any
	json.Unmarshal(data, &backup)
	bEnv := backup["env"].(map[string]any)
	if bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://other-tool:4318" {
		t.Errorf("backup must reflect pre-takeover state (third-party endpoint), got %v", bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}

	// ccl 接管后 CC_ORIG_* 仍在
	settings, _ := cm.LoadSettings()
	curEnv := settings["env"].(map[string]any)
	if curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
		t.Errorf("ccl must take over endpoint, got %v", curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	if curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
		t.Errorf("CC_ORIG_ must NOT be erased by injectOTelEnv, got %v", curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

func TestSmoke_RestoreUnaffected(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)
	cm := config.NewManager(settingsPath, backupDir, 10)

	// 模拟"已注入"状态：endpoint 是 ccl 默认值 + CC_ORIG_* 有原值
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
			"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT":  "https://company-otel:4317",
		},
	})

	// §7.6 调 restoreOTelEnv（等价于 `ccl serve --restore` 内部的 settings 重写）
	settings, err := cm.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	envAny := settings["env"].(map[string]any)
	envStr := make(map[string]string, len(envAny))
	for k, v := range envAny {
		if s, ok := v.(string); ok {
			envStr[k] = s
		}
	}
	restored := restoreOTelEnv(envStr)
	settings["env"] = restored
	if err := cm.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	// 验证 settings.json 等价于"首次 ccl 注入前 + restore 已清 CC_ORIG_ backup"
	final, _ := cm.LoadSettings()
	finalEnv := final["env"].(map[string]any)
	// restore 语义：CC_ORIG_<KEY> 还原到 <KEY>，删 CC_ORIG_*
	if finalEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
		t.Errorf("restore must restore user original to endpoint, got %v", finalEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	if _, ok := finalEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"]; ok {
		t.Errorf("CC_ORIG_ backup must be removed after restore, got %v", finalEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

// captureStdout 临时把 os.Stdout 接到 buffer，跑 fn，返回 buffer 内容。
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

// sandboxHome 把 HOME 改到 tmpdir（cclLauncherBaseDir 派生 HOME/.cc-launcher）。
func sandboxHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

// flatten-stop-restore-into-serve §7.3：`ccl serve --help` 包含 --stop / --restore。
func TestSmoke_ServeHelpMentionsFlags(t *testing.T) {
	sandboxHome(t)
	out := captureStdout(t, func() {
		if err := runServe([]string{"--help"}); err != nil {
			t.Fatalf("runServe --help: %v", err)
		}
	})
	if !strings.Contains(out, "--stop") {
		t.Errorf("ccl serve --help must mention --stop, got:\n%s", out)
	}
	if !strings.Contains(out, "--restore") {
		t.Errorf("ccl serve --help must mention --restore, got:\n%s", out)
	}
	if !strings.Contains(out, "--status") {
		t.Errorf("ccl serve --help must mention --status, got:\n%s", out)
	}
}

// §7.4：`ccl serve --stop` 在 daemon 没跑时输出 "daemon not running"，exit 0。
func TestSmoke_ServeStopNoDaemon(t *testing.T) {
	sandboxHome(t)
	var err error
	out := captureStdout(t, func() {
		err = runServe([]string{"--stop"})
	})
	if err != nil {
		t.Fatalf("runServe --stop must exit 0 when no daemon, got err: %v", err)
	}
	if !strings.Contains(out, "daemon not running") {
		t.Errorf("output must contain \"daemon not running\", got:\n%s", out)
	}
}

// §7.5：`ccl serve --restore` 在 settings.json 不存在时输出 "no settings to restore"，exit 0。
func TestSmoke_ServeRestoreNoSettings(t *testing.T) {
	sandboxHome(t)
	// HOME 改到 sandbox 后 ccl 会去 $HOME/.claude/settings.json——不存在，预期路径。
	var err error
	out := captureStdout(t, func() {
		err = runServe([]string{"--restore"})
	})
	if err != nil {
		t.Fatalf("runServe --restore must exit 0 when settings missing, got err: %v", err)
	}
	if !strings.Contains(out, "no settings to restore") {
		t.Errorf("output must contain \"no settings to restore\", got:\n%s", out)
	}
}

// §7.6：`ccl serve --stop --restore` 同时给两 flag → 非 0 exit。
func TestSmoke_ServeStopRestoreMutex(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--stop", "--restore"})
	})
	if err == nil {
		t.Fatalf("runServe --stop --restore must return error (mutex violation)")
	}
}

// §9.4 / §9.8：`ccl serve --stop --status` 同时给两 flag → 非 0 exit。
func TestSmoke_ServeStopStatusMutex(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--stop", "--status"})
	})
	if err == nil {
		t.Fatalf("runServe --stop --status must return error (mutex violation)")
	}
}

// §9.4：`ccl serve --restore --status` 同时给两 flag → 非 0 exit。
func TestSmoke_ServeRestoreStatusMutex(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--restore", "--status"})
	})
	if err == nil {
		t.Fatalf("runServe --restore --status must return error (mutex violation)")
	}
}

// §restart flag: --restart 与 --stop / --restore / --status 三个 flag 各自互斥。
func TestSmoke_ServeRestartStopMutex(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--restart", "--stop"})
	})
	if err == nil {
		t.Fatalf("runServe --restart --stop must return error (mutex violation)")
	}
}

func TestSmoke_ServeRestartRestoreMutex(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--restart", "--restore"})
	})
	if err == nil {
		t.Fatalf("runServe --restart --restore must return error (mutex violation)")
	}
}

func TestSmoke_ServeRestartStatusMutex(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--restart", "--status"})
	})
	if err == nil {
		t.Fatalf("runServe --restart --status must return error (mutex violation)")
	}
}

// §restart flag: --restart 无旧 daemon 时 → 等价于 fork 新 daemon（即使 fork 失败也不应 panic）。
//
// 环境依赖：fork 出去的是 fork 函数本身（不依赖：原 runServe 写 pidfile / 探端口）。在 sandbox HOME 下
// fork 可能因 PATH 等问题失败——只断言至少能调到退出（不 panic），不强求 fork 成功。
func TestSmoke_ServeRestartNoOldDaemon(t *testing.T) {
	sandboxHome(t)
	// 不预创建 pidfile → restartDaemon 应走"no running daemon"分支并尝试 fork。
	// fork 可能失败（sandbox 环境）→ 期望返回 error，但不应 panic。
	var err error
	out := captureStdout(t, func() {
		err = runServe([]string{"--restart"})
	})
	// 至少要看到 "no running daemon" 输出（说明正确进入 restart 分支）
	if !strings.Contains(out, "no running daemon") && !strings.Contains(out, "old daemon stopped") {
		t.Errorf("restart output must mention daemon state, got:\n%s", out)
	}
	// 无论 fork 成功与否（环境依赖）都不 panic；只验证 exit 行为可控。
	_ = err // err 可能非 nil（fork 失败）也可接受——sandbox 环境限制
}

// §7.7：`ccl stop` 已砍 → 走 main 路由的 cli.Run 兜底分支报"未知子命令"。
func TestSmoke_StopSubcommandRemoved(t *testing.T) {
	sandboxHome(t)
	// main 路由里没有 "stop" case → 落到 cli.Run → "未知子命令" 错误。
	if err := run([]string{"stop"}); err == nil {
		t.Fatalf("ccl stop must now error (subcommand removed)")
	}
}

// §7.7：`ccl restore` 已砍 → 同上。
func TestSmoke_RestoreSubcommandRemoved(t *testing.T) {
	sandboxHome(t)
	if err := run([]string{"restore"}); err == nil {
		t.Fatalf("ccl restore must now error (subcommand removed)")
	}
}

// §9.6：`ccl serve --status` 在 daemon 没跑时输出右对齐 label + 缩进子行 + db 行。
//
// 端口行内容依赖环境：本机 :4318 空 → "port :4318 unreachable"；被其它进程占 →
// "port :4318 in use by foreign service (no pidfile)"。两者都表示"daemon 没跑"，都是合法输出。
func TestSmoke_ServeStatusNoDaemon(t *testing.T) {
	sandboxHome(t)
	var err error
	out := captureStdout(t, func() {
		err = runServe([]string{"--status"})
	})
	if err != nil {
		t.Fatalf("runServe --status must exit 0 even when no daemon, got err: %v", err)
	}
	if !strings.Contains(out, "   daemon: not running") {
		t.Errorf("status must show \"   daemon: not running\" (10-char right-aligned label with colon), got:\n%s", out)
	}
	portOK := strings.Contains(out, "port :4318 unreachable") ||
		strings.Contains(out, "port :4318 in use by foreign service")
	if !portOK {
		t.Errorf("status must show an indented port :4318 <state> row, got:\n%s", out)
	}
	if !strings.Contains(out, "       db: missing") {
		t.Errorf("status must show \"       db: missing\" (right-aligned label with colon) when telemetry.db absent, got:\n%s", out)
	}
}

// §9.1 / §9.2a：humanBytes 单位换算（直接调，避免依赖 daemon 跑）。

// provider-flag-migration §11.1.c：smoke 验证短形式 runtime 真的接受（不依赖单元测试）。
// sandbox HOME → 走 main.run 完整路径 → cli.Run → runProvider → flag.NewFlagSet 接受 -x。
func TestSmoke_ProviderShortForms_Accepted(t *testing.T) {
	sandboxHome(t)

	// 验证 -list：等价 --list（空 providers dir → "暂无提供商配置"）
	var err error
	out := captureStdout(t, func() {
		err = run([]string{"provider", "-list"})
	})
	if err != nil {
		t.Fatalf("provider -list must succeed (flag.NewFlagSet accepts short form), got err: %v", err)
	}
	if !strings.Contains(out, "暂无提供商配置") {
		t.Errorf("-list should print '暂无提供商配置' for empty dir, got: %q", out)
	}

	// 验证 -T：等价 --template（无 type → 列全部 7 模板）
	out = captureStdout(t, func() {
		err = run([]string{"provider", "-T"})
	})
	if err != nil {
		t.Fatalf("provider -T must succeed, got err: %v", err)
	}
	for _, name := range []string{"anthropic", "deepseek", "minimax"} {
		if !strings.Contains(out, name) {
			t.Errorf("-T should list template %q, got: %q", name, out)
		}
	}

	// 验证 -h：等价 --help（打 providerHelp）
	out = captureStdout(t, func() {
		err = run([]string{"provider", "-h"})
	})
	if err != nil {
		t.Fatalf("provider -h must succeed, got err: %v", err)
	}
	if !strings.Contains(out, "--list") || !strings.Contains(out, "--template") {
		t.Errorf("-h should print providerHelp (含 --list / --template)，got: %q", out)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}
	for _, tc := range cases {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// §9.1：stats.CountSessionsByWindow 在 db 不存在时返回 (0, 0) + nil（status 友好）。
func TestStats_CountSessionsByWindow_Missing(t *testing.T) {
	dir := t.TempDir()
	total, today, err := stats.CountSessionsByWindow(filepath.Join(dir, "nope.db"), 0)
	if err != nil {
		t.Fatalf("CountSessionsByWindow on nonexistent db should be 0,0,nil, got %v", err)
	}
	if total != 0 || today != 0 {
		t.Errorf("CountSessionsByWindow should be (0,0), got (%d,%d)", total, today)
	}
}

// §9.7：模拟 daemon 在跑（写 pidfile + 占用 :4318），跑 status 应报"running"。
//
// 注意：本测试要 bind :4318 模拟端口可达。若本机 :4318 被其它进程占用（罕见但可能），
// 跳过以避免与并行测试干扰——主路径的 running 判定由 design.md §D8 矩阵决定。
func TestSmoke_ServeStatusRunning(t *testing.T) {
	home := sandboxHome(t)

	// 1. 占用 :4318（监听但不发 OTLP；probeDaemonAddr 只需端口可达即可）
	ln, err := net.Listen("tcp", "127.0.0.1:4318")
	if err != nil {
		t.Skipf("skip: cannot bind :4318 (likely occupied by other process): %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	// 2. 写 pidfile（用当前进程 PID——只是 probe 出来的"非零 pid"语义，非真 daemon）
	pidPath := filepath.Join(home, ".cc-launcher", "daemon.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pidPath) })

	var runErr error
	out := captureStdout(t, func() {
		runErr = runServe([]string{"--status"})
	})
	if runErr != nil {
		t.Fatalf("runServe --status must exit 0 when daemon simulated, got err: %v", runErr)
	}
	if !strings.Contains(out, "   daemon: running") {
		t.Errorf("status must show \"   daemon: running\" (right-aligned label with colon), got:\n%s", out)
	}
	if !strings.Contains(out, "port :4318 reachable") {
		t.Errorf("status must show \"port :4318 reachable\" (indented), got:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("pid=%d", os.Getpid())) {
		t.Errorf("status must show \"pid=%d\", got:\n%s", os.Getpid(), out)
	}
}

// §D8：端口可达 + 无 pidfile = "not running" + "in use by foreign service"。
// 跟 §9.7 共用端口（必须先 bind 再不写 pidfile）。
func TestSmoke_ServeStatusForeignService(t *testing.T) {
	sandboxHome(t)

	ln, err := net.Listen("tcp", "127.0.0.1:4318")
	if err != nil {
		t.Skipf("skip: cannot bind :4318: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	// 故意不写 pidfile → 走 foreign service 分支。

	var runErr error
	out := captureStdout(t, func() {
		runErr = runServe([]string{"--status"})
	})
	if runErr != nil {
		t.Fatalf("runServe --status must exit 0, got err: %v", runErr)
	}
	if !strings.Contains(out, "   daemon: not running") {
		t.Errorf("status must show \"   daemon: not running\" (right-aligned with colon), got:\n%s", out)
	}
	if !strings.Contains(out, "port :4318 in use by foreign service") {
		t.Errorf("status must show \"port :4318 in use by foreign service\" (no pidfile), got:\n%s", out)
	}
}

// §D8：端口不可达 + 有 pidfile = "not running" + "stale pidfile pid=N" + "port :4318 unreachable"。
// pidfile 存在但 daemon 已挂。
//
// 环境依赖：需要本机 :4318 不可达才能模拟"daemon 已挂"。若 4318 被其它进程占用，
// probe 会判断为 reachable → 走 foreign service 分支，无法验证 stale 路径——skip。
func TestSmoke_ServeStatusStalePidfile(t *testing.T) {
	home := sandboxHome(t)

	// 探 4318 是否空；空才继续，否则 skip。
	if probeDaemonAddr() == nil {
		t.Skipf("skip: :4318 is reachable (cannot simulate stale pidfile scenario)")
	}

	pidPath := filepath.Join(home, ".cc-launcher", "daemon.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", 999999)), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pidPath) })

	var runErr error
	out := captureStdout(t, func() {
		runErr = runServe([]string{"--status"})
	})
	if runErr != nil {
		t.Fatalf("runServe --status must exit 0, got err: %v", runErr)
	}
	if !strings.Contains(out, "   daemon: not running") {
		t.Errorf("status must show \"   daemon: not running\" (right-aligned with colon), got:\n%s", out)
	}
	if !strings.Contains(out, "stale pidfile pid=999999") {
		t.Errorf("status must show \"stale pidfile pid=999999\", got:\n%s", out)
	}
	if !strings.Contains(out, "port :4318 unreachable") {
		t.Errorf("status must show \"port :4318 unreachable\" (indented), got:\n%s", out)
	}
}
