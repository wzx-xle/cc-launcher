//go:build smoke

// telemetry-db-compaction 的 CLI 面 smoke test：
//   - `ccl serve --help` 列出 --compact
//   - `ccl __complete serve ""` 的候选含 --compact
//   - `ccl __complete serve --compact ""` 输出为空（互斥防御）
//   - `ccl serve --compact --backfill` 报互斥错误
//
// 不依赖 daemon 运行中 / SQLite / 磁盘空间。

package main

import (
	"strings"
	"testing"
)

// TestSmoke_ServeHelpMentionsCompact 断言 `ccl serve --help` 列出 --compact。
func TestSmoke_ServeHelpMentionsCompact(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runServe([]string{"--help"}); err != nil {
			t.Errorf("runServe(--help) returned error: %v", err)
		}
	})
	if !strings.Contains(out, "--compact") {
		t.Errorf("serve --help 应列出 --compact, got:\n%s", out)
	}
	// 用法行也要把它算进互斥组
	if !strings.Contains(out, "--backfill | --compact") {
		t.Errorf("serve --help 的用法行应包含 --compact, got:\n%s", out)
	}
}

// TestSmoke_CompletionServeHasCompact 断言 serve 的补全候选里有 --compact。
func TestSmoke_CompletionServeHasCompact(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", "serve", ""}); err != nil {
			t.Errorf("run(__complete, serve, '') returned error: %v", err)
		}
	})
	if !strings.Contains(out, "--compact") {
		t.Errorf("serve 补全候选应含 --compact, got:\n%s", out)
	}
	for _, want := range []string{"--stop", "--restore", "--status", "--restart", "--backfill"} {
		if !strings.Contains(out, want) {
			t.Errorf("serve 补全候选应含 %q, got:\n%s", want, out)
		}
	}
}

// TestSmoke_CompletionServeCompactMutex 断言 --compact 之后的补全为空
// （互斥防御——serveMutex 是**硬编码 map**，不随 registerServeFlags 自动跟随）。
func TestSmoke_CompletionServeCompactMutex(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", "serve", "--compact", ""}); err != nil {
			t.Errorf("run(__complete, serve, --compact, '') returned error: %v", err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("--compact 之后的补全应为空, got:\n%s", out)
	}
}

// TestSmoke_ServeCompactBackfillMutex 断言 `ccl serve --compact --backfill` 报互斥。
func TestSmoke_ServeCompactBackfillMutex(t *testing.T) {
	err := runServe([]string{"--compact", "--backfill"})
	if err == nil {
		t.Fatal("runServe --compact --backfill 应报互斥错误")
	}
	if !strings.Contains(err.Error(), "互斥") {
		t.Errorf("错误信息应说明互斥, got %q", err.Error())
	}
}

// TestSmoke_ServeCompactStopMutex 断言 `ccl serve --compact --stop` 也报互斥。
func TestSmoke_ServeCompactStopMutex(t *testing.T) {
	err := runServe([]string{"--compact", "--stop"})
	if err == nil {
		t.Fatal("runServe --compact --stop 应报互斥错误")
	}
	if !strings.Contains(err.Error(), "互斥") {
		t.Errorf("错误信息应说明互斥, got %q", err.Error())
	}
}
