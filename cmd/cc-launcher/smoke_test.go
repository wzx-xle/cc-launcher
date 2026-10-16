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
	"time"

	"cc-launcher/internal/collector"
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

// captureStdout / captureStderr 在 main_test.go 定义（同 package 共享）。

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

// =============================================================================
// cli-shell-completion change: smoke 测试（§8.5-§8.6）
//
// 走 `go test -tags smoke ./cmd/cc-launcher/...` 触发；不依赖 daemon / SQLite，
// 仅端到端验证 `ccl --help` 不含隐藏子命令 + `ccl __complete ""` 顶层候选正确。
// =============================================================================

// §8.5: 端到端 `ccl --help` 不暴露 __complete / __init 隐藏子命令（spec scenario "top-level
// help does not list internal subcommands"）。
//
// 范围：仅 "子命令:" 段（spec 原文："subcommand list"）。topHelp 的 "Tab completion"
// prose 段落里会出现 `ccl __init bash` / `ccl __init zsh` 等引导文本——那是用法提示，
// 不是列出 subcommand（spec 没要求 prose 不出现）。所以断言限定在子命令段。
func TestSmoke_HelpDoesNotListHiddenSubcommands(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"--help"}); err != nil {
			t.Errorf("run(--help) returned error: %v", err)
		}
	})
	if !strings.Contains(out, "用法: ccl") {
		t.Errorf("--help 应含 '用法: ccl' header, got:\n%s", out)
	}
	subcommandSection := extractHelpSection(out, "子命令:", "选项:")
	if subcommandSection == "" {
		t.Fatalf("--help 缺少 '子命令:' 段, got:\n%s", out)
	}
	for _, hidden := range []string{"__complete", "__init"} {
		if strings.Contains(subcommandSection, hidden) {
			t.Errorf("--help 子命令段不应列隐藏 subcommand %q, got section: %q", hidden, subcommandSection)
		}
	}
	// 正向 sanity：4 个用户面子命令应出现
	for _, sub := range []string{"serve", "stats", "provider", "mode"} {
		if !strings.Contains(subcommandSection, sub) {
			t.Errorf("--help 子命令段应包含子命令 %q, got section: %q", sub, subcommandSection)
		}
	}
}

// extractHelpSection 截取 `out` 中 [start, end) 之间的内容。任一 marker 缺失返回空。
func extractHelpSection(out, start, end string) string {
	s := strings.Index(out, start)
	if s < 0 {
		return ""
	}
	rest := out[s+len(start):]
	e := strings.Index(rest, end)
	if e < 0 {
		return out[s:]
	}
	return out[s : s+len(start)+e]
}

// §8.6: 端到端 `ccl __complete ""` 顶层候选正确（spec scenario "completing the
// top-level subcommand"）。
func TestSmoke_CompletionTopLevel(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", ""}); err != nil {
			t.Errorf("run(__complete, '') returned error: %v", err)
		}
	})
	for _, want := range []string{"serve", "stats", "provider", "mode", "-h", "--help", "help"} {
		if !strings.Contains(out, want) {
			t.Errorf("顶层候选应含 %q, got:\n%s", want, out)
		}
	}
}

// stats-time-buckets §3.2: 端到端 smoke test 覆盖 spec 关键 scenario。
//
// 测试结构：
//   - sandbox HOME → 写 telemetry.db（注入固定时钟 17:30 这一刻）
//   - 调 runStats(args) + captureStdout / captureStderr
//   - statsExitFn 覆盖为 no-op（避免真 os.Exit 把 test 进程杀掉）
//
// 覆盖 spec scenarios:
//   (a) --day 7 文本含 7 daily label + TOTAL_TOKENS + TOTAL
//   (b) --day 1 文本含 24 hourly + partial note + 当前小时 *
//   (c) --day 缺省 文本不含 TOTAL_TOKENS / BUCKET
//   (d) --json --day 7 JSON 含 bucket_rows(7 个)+ bucket_totals + bucket_iso
//   (e) --json 缺省 JSON 无 bucket_rows / bucket_totals
//   (f) --since 报 "flag provided but not defined: -since"
//   (g) --day 0 / 91 exit 非 0 + stderr 含 "--day 必须在 1 到 90 之间, got:"
//   (h) --json --day 1 当前小时桶含 is_current: true
//   (i) 80-col 终端宽度下柱状图右边界对齐
func TestStatsTimeBuckets(t *testing.T) {
	home := sandboxHome(t)
	baseDir := cclLauncherBaseDir()
	dbPath := filepath.Join(baseDir, "telemetry.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}

	// 注入固定时钟 2026-09-30 17:30 本地时区
	now := time.Date(2026, 9, 30, 17, 30, 0, 0, time.Local)
	store, err := collector.OpenStore(dbPath, collector.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	// 写 1 个 session @ 17:00（input=100, output=20, cacheRead=5, cost=0.01）
	if err := store.WriteSession(collector.Session{
		ID: "smoke1", StartedAt: now.Add(-30 * time.Minute),
		Provider: "anthropic", Cwd: "/tmp/smoke", Model: "claude-opus",
	}); err != nil {
		t.Fatal(err)
	}
	metrics := []collector.Metric{
		{SessionID: "smoke1", Name: "claude_code.token.usage", Value: 100, Labels: map[string]string{"type": "input"}, CapturedAt: now},
		{SessionID: "smoke1", Name: "claude_code.token.usage", Value: 20, Labels: map[string]string{"type": "output"}, CapturedAt: now},
		{SessionID: "smoke1", Name: "claude_code.token.usage", Value: 5, Labels: map[string]string{"type": "cacheRead"}, CapturedAt: now},
		{SessionID: "smoke1", Name: "claude_code.cost.usage", Value: 0.01, CapturedAt: now},
	}
	for _, m := range metrics {
		if err := store.WriteMetric(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = home // 占位以防未来用 home 路径

	// 覆盖 statsExitFn：避免真 os.Exit 杀掉测试进程
	origExit := statsExitFn
	t.Cleanup(func() { statsExitFn = origExit })
	exitCode := -1
	exitCalled := false
	statsExitFn = func(code int) { exitCode = code; exitCalled = true }

	t.Run("a_day7_text_has_7_buckets_and_total", func(t *testing.T) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = runStats([]string{"--day", "7"})
		})
		if runErr != nil {
			t.Fatalf("runStats --day 7: %v", runErr)
		}
		// 7 daily label (09-24 .. 09-30) 应都出现
		for _, d := range []string{"09-24", "09-25", "09-26", "09-27", "09-28", "09-29", "09-30"} {
			if !strings.Contains(out, d) {
				t.Errorf("--day 7 output 应含 %q, got:\n%s", d, out)
			}
		}
		// TOTAL_TOKENS 列头 + TOTAL 行
		if !strings.Contains(out, "TOTAL_TOKENS") {
			t.Errorf("--day 7 应含 TOTAL_TOKENS 列头, got:\n%s", out)
		}
		if !strings.Contains(out, "TOTAL") {
			t.Errorf("--day 7 应含 TOTAL 行, got:\n%s", out)
		}
	})

	t.Run("b_day1_text_has_hourly_with_partial_note", func(t *testing.T) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = runStats([]string{"--day", "1"})
		})
		if runErr != nil {
			t.Fatalf("runStats --day 1: %v", runErr)
		}
		// 0..currentHour hourly label 都应出现(currentHour 由 time.Now() 决定,smoke test 不注入)
		currentHour := time.Now().Hour()
		for h := 0; h <= currentHour; h++ {
			want := fmt.Sprintf("%02d:00", h)
			if !strings.Contains(out, want) {
				t.Errorf("--day 1 应含 hourly label %q, got:\n%s", want, out)
			}
		}
		// partial note
		if !strings.Contains(out, "(* = 当前小时,数据仍在累计)") {
			t.Errorf("--day 1 应含 partial note, got:\n%s", out)
		}
		// 当前小时带 *
		currentLabel := fmt.Sprintf("%02d:00*", currentHour)
		if !strings.Contains(out, currentLabel) {
			t.Errorf("--day 1 当前小时 label 应为 %q, got:\n%s", currentLabel, out)
		}
	})

	t.Run("c_no_day_text_has_no_bucket_table", func(t *testing.T) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = runStats([]string{})
		})
		if runErr != nil {
			t.Fatalf("runStats (no --day): %v", runErr)
		}
		if strings.Contains(out, "TOTAL_TOKENS") || strings.Contains(out, "BUCKET") {
			t.Errorf("缺省 --day 时不应含 BUCKET/TOTAL_TOKENS 列头, got:\n%s", out)
		}
	})

	t.Run("d_json_day7_has_bucket_rows_and_totals", func(t *testing.T) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = runStats([]string{"--json", "--day", "7"})
		})
		if runErr != nil {
			t.Fatalf("runStats --json --day 7: %v", runErr)
		}
		var r stats.Result
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if len(r.BucketRows) != 7 {
			t.Errorf("bucket_rows want 7, got %d", len(r.BucketRows))
		}
		if r.BucketTotals == nil {
			t.Error("bucket_totals should not be nil")
		}
		for i, br := range r.BucketRows {
			if br.BucketISO == "" {
				t.Errorf("bucket_rows[%d] missing bucket_iso", i)
			}
			if br.BucketUnixMs == 0 {
				t.Errorf("bucket_rows[%d] missing bucket_unix_ms", i)
			}
			if br.Label == "" {
				t.Errorf("bucket_rows[%d] missing bucket label (spec scenario 4)", i)
			}
		}
	})

	t.Run("e_json_no_day_omits_bucket_fields", func(t *testing.T) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = runStats([]string{"--json"})
		})
		if runErr != nil {
			t.Fatalf("runStats --json (no --day): %v", runErr)
		}
		var r stats.Result
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if len(r.BucketRows) != 0 {
			t.Errorf("--json 缺省 --day 应无 bucket_rows, got %d rows", len(r.BucketRows))
		}
		if r.BucketTotals != nil {
			t.Error("--json 缺省 --day 应无 bucket_totals")
		}
		// 同时校验 raw JSON 文本无 "bucket_rows" / "bucket_totals" / "bucket" key
		// (omitempty 严格保证 JSON 字节级一致)
		if strings.Contains(out, "bucket_rows") {
			t.Errorf("--json 缺省 --day 输出不应含 'bucket_rows' 字串, got:\n%s", out)
		}
		if strings.Contains(out, "bucket_totals") {
			t.Errorf("--json 缺省 --day 输出不应含 'bucket_totals' 字串, got:\n%s", out)
		}
	})

	t.Run("f_since_removed", func(t *testing.T) {
		var runErr error
		stderr := captureStderr(t, func() {
			runErr = runStats([]string{"--since", "2026-09-20"})
		})
		// Go flag package 会返回 fs.Parse 错误 → runStats return err
		if runErr == nil {
			t.Error("--since 应报错, got nil")
		}
		if !strings.Contains(stderr, "flag provided but not defined") && !strings.Contains(runErr.Error(), "flag provided but not defined") {
			t.Errorf("--since 应报 'flag provided but not defined', got stderr=%q err=%v", stderr, runErr)
		}
	})

	t.Run("g_day_out_of_range", func(t *testing.T) {
		// --day 0
		exitCode = -1
		exitCalled = false
		stderr0 := captureStderr(t, func() {
			_ = runStats([]string{"--day", "0"})
		})
		if !exitCalled {
			t.Error("--day 0 应触发 statsExitFn")
		}
		if exitCode != 2 {
			t.Errorf("--day 0 exit code 应为 2, got %d", exitCode)
		}
		if !strings.Contains(stderr0, "--day 必须在 1 到 90 之间, got: 0") {
			t.Errorf("--day 0 stderr 应含校验消息, got: %q", stderr0)
		}

		// --day 91
		exitCode = -1
		exitCalled = false
		stderr91 := captureStderr(t, func() {
			_ = runStats([]string{"--day", "91"})
		})
		if !exitCalled {
			t.Error("--day 91 应触发 statsExitFn")
		}
		if exitCode != 2 {
			t.Errorf("--day 91 exit code 应为 2, got %d", exitCode)
		}
		if !strings.Contains(stderr91, "--day 必须在 1 到 90 之间, got: 91") {
			t.Errorf("--day 91 stderr 应含校验消息, got: %q", stderr91)
		}
	})

	t.Run("h_json_day1_marks_current_hour", func(t *testing.T) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = runStats([]string{"--json", "--day", "1"})
		})
		if runErr != nil {
			t.Fatalf("runStats --json --day 1: %v", runErr)
		}
		var r stats.Result
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		currentHour := time.Now().Hour()
		wantRows := currentHour + 1
		if len(r.BucketRows) != wantRows {
			t.Fatalf("--day 1 应 %d rows (00..%d), got %d", wantRows, currentHour, len(r.BucketRows))
		}
		foundCurrent := false
		for _, br := range r.BucketRows {
			if br.IsCurrent {
				foundCurrent = true
				// Label 字段 json:"-" 不进 JSON;通过 BucketUnixMs 推 hour 验证
				hour := time.UnixMilli(br.BucketUnixMs).Hour()
				if hour != currentHour {
					t.Errorf("IsCurrent bucket hour: want %d, got %d (unix_ms=%d)",
						currentHour, hour, br.BucketUnixMs)
				}
			}
		}
		if !foundCurrent {
			t.Error("--json --day 1 应有 IsCurrent=true 桶")
		}
	})

	t.Run("i_no_bar_glyphs_in_table", func(t *testing.T) {
		// 决策(stats-bucket-tokens-column-numeric §Decision 1 + §Decision 3):
		//   TOTAL_TOKENS 列从 20 字符 Unicode 柱改为 humanizeTokens 数字合计。
		//   spec 改 "TOTAL_TOKENS column shows the humanized per-bucket sum"。
		//
		// 本测试覆盖:FormatBucketTable 输出不含任何柱字符 / 半块字符集 /
		// 占位符 / 横线 separator;data 行 + TOTAL 行末列是 humanize 输出形态;
		// hourly note 仍保留(* partial 标记不走列内,语义不变)。
		rows := []stats.BucketRow{
			{Label: "00:00", Sessions: 1, InputTokens: 100, OutputTokens: 20, CacheRead: 5, CostUSD: 0.01},
			{Label: "11:00*", IsCurrent: true, Sessions: 2, InputTokens: 1000, OutputTokens: 500, CacheCreate: 50, CacheRead: 200, CostUSD: 1.5},
		}
		totals := &stats.BucketRow{Sessions: 3, InputTokens: 1100, OutputTokens: 520, CacheCreate: 50, CacheRead: 205, CostUSD: 1.51}
		var sb strings.Builder
		stats.FormatBucketTable(&sb, rows, totals)
		out := sb.String()

		// 旧版柱字符集 + 占位符 + 横线 separator 全部禁止出现
		// (data 行 + TOTAL 行 —— hourly note `(*` 不在表内,跟列内字符无关)
		forbidden := []string{
			"█", "▏", "▎", "▍", "▌", "▋", "▊", "▉", // 半块字符集
			"⏳", "·", "─", " (total)", // partial / zero / separator 后缀
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		tableLines := 0
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "(*") {
				// hourly partial note 行允许保留 `(*`,但禁止其它列内字符
				continue
			}
			tableLines++
			for _, f := range forbidden {
				if strings.Contains(line, f) {
					t.Errorf("table line should not contain %q (removed bar glyph/separator):\n%s", f, line)
				}
			}
		}
		if tableLines < 4 { // header + 2 data + TOTAL → 至少 4 行
			t.Errorf("expected >=4 table lines (header+data+TOTAL), got %d:\n%s", tableLines, out)
		}

		// 末列应是 humanize 形态:匹配正则 ^[0-9]+(\.[0-9]+)?[KMB]?$ 或 ^0$
		// (覆盖 spec scenario:零值桶 = literal "0",非零值 humanize)
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "BUCKET") || strings.HasPrefix(trim, "(*") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}
			last := fields[len(fields)-1]
			if last != "0" {
				// humanize 后的形态:数字 + 可选 .xx + K/M/B
				matched := false
				for _, suffix := range []string{"", "K", "M", "B"} {
					if suffix == "" {
						if last != "" && !strings.ContainsAny(last, "KMB.") {
							// 纯整数(可选小数)
							dotCount := strings.Count(last, ".")
							if dotCount <= 1 {
								matched = true
							}
						}
					} else if strings.HasSuffix(last, suffix) {
						matched = true
					}
				}
				if !matched {
					t.Errorf("末列应为 humanize 数字形态, got %q (line=%q)", last, line)
				}
			}
		}
	})

	// j_text_and_json_consistency 覆盖 spec scenario 4 末尾:
	// "bucket_rows numeric fields match the human-readable table" —
	// 对同一 db 跑 text + JSON,逐桶断言 numeric 字段相等。
	//
	// 注意:db 已在 TestStatsTimeBuckets 顶层建好,这里只需各跑一次 + 解析比对。
	t.Run("j_text_and_json_consistency", func(t *testing.T) {
		// 跑 text 拿 stdout
		var textOut string
		captureStdout(t, func() {
			if err := runStats([]string{"--day", "7"}); err != nil {
				t.Fatalf("runStats --day 7: %v", err)
			}
		})
		// captureStdout 不能直接取返回值,改用 io.Writer 写 file
		// 简化:用 captureStdoutNoCheck 风格
		_ = textOut

		// 直接用临时 buffer 重跑
		textOut = runStatsCapture(t, []string{"--day", "7"})
		jsonOut := runStatsCapture(t, []string{"--json", "--day", "7"})

		// parse text:提取每桶 sessions / input_tokens / output_tokens / cache_create / cache_read / cost_usd
		type textBucket struct {
			label    string
			sessions int64
			in, out, cre, read int64
			cost     float64
		}
		var textBuckets []textBucket
		for _, line := range strings.Split(textOut, "\n") {
			// 跳过 header / note / TOTAL 行 / 窗口 header / 空行
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "BUCKET") || strings.HasPrefix(trim, "(*") ||
				strings.HasPrefix(trim, "TOTAL") || strings.HasPrefix(trim, "Window") ||
				strings.HasPrefix(trim, "(no data") || strings.HasPrefix(trim, "STARTED") {
				continue
			}
			// 数据行:tab-separated,字段顺序:BUCKET SESSIONS IN OUT CACHE_CRE CACHE_READ COST_USD TOTAL_TOKENS
			// (8 个 field;TOTAL_TOKENS 是新增的 humanize 数字合计列,不参与 numeric 比对)
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}
			// fields[0] = "MM-DD"
			var sess, in, out, cre, read int64
			var cost float64
			if _, err := fmt.Sscanf(fields[1], "%d", &sess); err != nil {
				continue
			}
			if _, err := fmt.Sscanf(fields[2], "%d", &in); err != nil {
				continue
			}
			if _, err := fmt.Sscanf(fields[3], "%d", &out); err != nil {
				continue
			}
			if _, err := fmt.Sscanf(fields[4], "%d", &cre); err != nil {
				continue
			}
			if _, err := fmt.Sscanf(fields[5], "%d", &read); err != nil {
				continue
			}
			if _, err := fmt.Sscanf(fields[6], "%f", &cost); err != nil {
				continue
			}
			textBuckets = append(textBuckets, textBucket{
				label: fields[0], sessions: sess, in: in, out: out, cre: cre, read: read, cost: cost,
			})
		}

		// parse JSON
		var r stats.Result
		if err := json.Unmarshal([]byte(jsonOut), &r); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, jsonOut)
		}

		// 逐桶断言 numeric 字段一致
		if len(textBuckets) != len(r.BucketRows) {
			t.Fatalf("bucket count mismatch: text=%d, json=%d\ntext:\n%s\njson:\n%s",
				len(textBuckets), len(r.BucketRows), textOut, jsonOut)
		}
		for i, tb := range textBuckets {
			br := r.BucketRows[i]
			if tb.sessions != br.Sessions {
				t.Errorf("bucket[%d] sessions: text=%d, json=%d", i, tb.sessions, br.Sessions)
			}
			if tb.in != br.InputTokens {
				t.Errorf("bucket[%d] input_tokens: text=%d, json=%d", i, tb.in, br.InputTokens)
			}
			if tb.out != br.OutputTokens {
				t.Errorf("bucket[%d] output_tokens: text=%d, json=%d", i, tb.out, br.OutputTokens)
			}
			if tb.cre != br.CacheCreate {
				t.Errorf("bucket[%d] cache_creation_tokens: text=%d, json=%d", i, tb.cre, br.CacheCreate)
			}
			if tb.read != br.CacheRead {
				t.Errorf("bucket[%d] cache_read_tokens: text=%d, json=%d", i, tb.read, br.CacheRead)
			}
			if tb.cost != br.CostUSD {
				t.Errorf("bucket[%d] cost_usd: text=%f, json=%f", i, tb.cost, br.CostUSD)
			}
		}
	})
}

// runStatsCapture 跑 runStats 并捕获 stdout,返回捕获到的内容。
//
// 比 captureStdout(t, fn) 多一步把 buffer 内容作为 string 返回。
func runStatsCapture(t *testing.T, args []string) string {
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

	if err := runStats(args); err != nil {
		_ = w.Close()
		t.Fatalf("runStats %v: %v", args, err)
	}
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// abs 整数绝对值(smoke test 本地辅助,避免引 strconv 仅为一个函数)
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
