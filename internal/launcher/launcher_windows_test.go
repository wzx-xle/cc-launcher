//go:build windows

package launcher

import (
	"os/exec"
	"reflect"
	"testing"
)

// withInspectingConfigureCmd 临时把 configureCmd 替换为 capture hook，capture
// cmd 引用并 panic 中断 cmd.Run()。test 用 recover 验证 cmd.Env 字段。
func withInspectingConfigureCmd(t *testing.T) func() {
	t.Helper()
	orig := configureCmd
	return func() { configureCmd = orig }
}

// TestLaunch_Windows_EnvSetOnCmd 覆盖 spec/launcher-provider-env §"Windows
// Launch sets cmd.Env from Env" Scenario "opts.Env assigned to cmd.Env"。
// 用 hook 捕获 cmd.Env（task 1.5 spec："用 exec.Command 不可达 fake path
// 验证 cmd.Env 被设置"——hook 在 cmd.Run 之前执行，无需真的启动 Claude Code）。
func TestLaunch_Windows_EnvSetOnCmd(t *testing.T) {
	defer withInspectingConfigureCmd(t)()

	var captured *exec.Cmd
	customEnv := []string{
		"FOO=bar",
		"PATH=C:\\Windows",
		"ANTHROPIC_AUTH_TOKEN=sk-test",
	}
	configureCmd = func(cmd *exec.Cmd, opts Options) {
		captured = cmd
		panic("test: stop before cmd.Run to inspect cmd.Env")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from configureCmd hook (proves cmd.Run was prevented)")
		}
		if captured == nil {
			t.Fatal("configureCmd was not called")
		}
		if !reflect.DeepEqual(captured.Env, customEnv) {
			t.Errorf("cmd.Env mismatch:\n  got  %v\n  want %v", captured.Env, customEnv)
		}
	}()

	_ = Launch(Options{
		PermissionMode: "plan",
		Env:            customEnv,
	})
}

// TestLaunch_Windows_NilEnvLeavesCmdEnvUnset 覆盖 spec §"opts.Env is nil leaves
// cmd.Env unset on Windows"——cmd.Env 应保持零值（nil），子进程继承父进程 env。
func TestLaunch_Windows_NilEnvLeavesCmdEnvUnset(t *testing.T) {
	defer withInspectingConfigureCmd(t)()

	var captured *exec.Cmd
	configureCmd = func(cmd *exec.Cmd, opts Options) {
		captured = cmd
		panic("test: stop")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from configureCmd hook")
		}
		if captured == nil {
			t.Fatal("configureCmd was not called")
		}
		if captured.Env != nil {
			t.Errorf("cmd.Env should be nil when opts.Env is nil, got %v", captured.Env)
		}
	}()

	_ = Launch(Options{
		PermissionMode: "plan",
		Env:            nil,
	})
}
