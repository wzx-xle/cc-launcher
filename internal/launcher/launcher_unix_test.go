//go:build !windows

package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeExecCall 记录 fakeExec 收到的一次调用参数。
type fakeExecCall struct {
	Argv0 string
	Argv  []string
	Envv  []string
}

// fakeExecCalls 是测试期间 fakeExec 的累积调用记录。
var fakeExecCalls []fakeExecCall

// fakeExec 替换 execFunc，记录调用参数并返回 nil（不真的替换进程）。
func fakeExec(argv0 string, argv []string, envv []string) error {
	fakeExecCalls = append(fakeExecCalls, fakeExecCall{
		Argv0: argv0,
		Argv:  argv,
		Envv:  envv,
	})
	return nil
}

// withFakeExec 临时把 execFunc 替换为 fakeExec，返回 restore 函数。
func withFakeExec(t *testing.T) func() {
	t.Helper()
	orig := execFunc
	execFunc = fakeExec
	fakeExecCalls = nil
	return func() {
		execFunc = orig
	}
}

// withFakeClaude 在 PATH 前置一个假的 claude 可执行文件。
//
// 为什么需要：resolveClaude() 用 exec.LookPath("claude") 找命令。这两个测试
// 断言的是 **env 转发**，跟 claude 装没装无关，但此前直接依赖"测试机上恰好
// 装了 Claude Code"——开发者本机绿，CI 的干净 runner 上必红
// （2026-10-21 首次接 CI 时立刻炸出来："未找到 claude 命令"）。
//
// 放一个假的进 PATH，测试就与外部环境无关（hermetic）：任何机器、任何 CI
// 上跑结果一致。
//
// 用 t.Setenv 恢复 PATH（测试结束自动还原，且禁止与 t.Parallel 同用——
// 这两个测试本来就不是 parallel）。
func withFakeClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("写假 claude 失败: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestLaunch_Unix_EnvForwarded 覆盖 spec/launcher-provider-env §"Unix Launch
// forwards Env to syscall.Exec" Scenario "opts.Env propagated verbatim"。
func TestLaunch_Unix_EnvForwarded(t *testing.T) {
	withFakeClaude(t)
	defer withFakeExec(t)()

	customEnv := []string{
		"FOO=bar",
		"PATH=/usr/bin",
		"ANTHROPIC_AUTH_TOKEN=sk-test",
	}
	err := Launch(Options{
		PermissionMode: "plan",
		Env:            customEnv,
	})
	if err != nil {
		t.Fatalf("Launch() returned error: %v", err)
	}
	if len(fakeExecCalls) != 1 {
		t.Fatalf("expected 1 syscall.Exec call, got %d", len(fakeExecCalls))
	}
	got := fakeExecCalls[0]
	if got.Argv0 == "" {
		t.Errorf("argv0 is empty; resolveClaude should populate it")
	}
	// argv0 是 exec.LookPath("claude") 的绝对路径（test 环境可能装在
	// /home/.../.nvm/.../bin/claude 等位置），不强制等于 "claude"。
	wantArgv := []string{got.Argv0, "--permission-mode", "plan"}
	if !reflect.DeepEqual(got.Argv, wantArgv) {
		t.Errorf("argv mismatch:\n  got  %v\n  want %v", got.Argv, wantArgv)
	}
	if !reflect.DeepEqual(got.Envv, customEnv) {
		t.Errorf("envv mismatch:\n  got  %v\n  want %v", got.Envv, customEnv)
	}
}

// TestLaunch_Unix_NilEnvFallsBackToOsEnviron 覆盖 spec §"Caller omits Env field"
// ——opts.Env=nil 时 Unix 退化为 os.Environ()，保留 legacy 行为。
func TestLaunch_Unix_NilEnvFallsBackToOsEnviron(t *testing.T) {
	withFakeClaude(t)
	defer withFakeExec(t)()

	err := Launch(Options{
		PermissionMode: "plan",
		Env:            nil,
	})
	if err != nil {
		t.Fatalf("Launch() returned error: %v", err)
	}
	if len(fakeExecCalls) != 1 {
		t.Fatalf("expected 1 syscall.Exec call, got %d", len(fakeExecCalls))
	}
	if !reflect.DeepEqual(fakeExecCalls[0].Envv, os.Environ()) {
		t.Errorf("nil Env fallback mismatch:\n  got  %v\n  want %v",
			fakeExecCalls[0].Envv, os.Environ())
	}
}

// TestLaunch_ExtraArgs 断言 ExtraArgs 被追加到最终 argv（launch-context-settings-injection）。
//
// 承载 `--settings <json|file>`：launch 上下文走这个通道，而不是进程环境
// （settings.json 的 env 块优先级更高，会把进程环境里的同名变量顶掉）。
func TestLaunch_ExtraArgs(t *testing.T) {
	restore := withFakeExec(t)
	defer restore()
	withFakeClaude(t)

	settings := `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"team=platform,ccl.provider=minimax"}}`
	opts := Options{
		PermissionMode: "bypassPermissions",
		ExtraArgs:      []string{"--settings", settings},
	}
	if err := Launch(opts); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if len(fakeExecCalls) != 1 {
		t.Fatalf("want 1 exec call, got %d", len(fakeExecCalls))
	}
	argv := fakeExecCalls[0].Argv
	// argv[0] 是 claude 路径，后面是参数
	var got []string
	for _, a := range argv[1:] {
		got = append(got, a)
	}
	want := []string{"--permission-mode", "bypassPermissions", "--settings", settings}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv:\n got=%v\nwant=%v", got, want)
	}
}
