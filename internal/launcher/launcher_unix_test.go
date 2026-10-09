//go:build !windows

package launcher

import (
	"os"
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

// TestLaunch_Unix_EnvForwarded 覆盖 spec/launcher-provider-env §"Unix Launch
// forwards Env to syscall.Exec" Scenario "opts.Env propagated verbatim"。
func TestLaunch_Unix_EnvForwarded(t *testing.T) {
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
