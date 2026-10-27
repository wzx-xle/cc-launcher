//go:build smoke

// web-usage-stats-ui 的 CLI 面 smoke test：
//   - `ccl --help` 的子命令段列出 web
//   - `ccl web --help` 打印三个 flag 的一行说明
//   - `ccl __complete ""` 顶层候选含 web
//   - `ccl __complete web ""` 输出 web 的 flag
//   - `ccl __complete stats --` 含 --provider
//
// 不依赖 daemon / SQLite / 浏览器。

package main

import (
	"strings"
	"testing"
)

// TestSmoke_HelpListsWeb 断言顶层帮助列出 web 子命令。
func TestSmoke_HelpListsWeb(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"--help"}); err != nil {
			t.Errorf("run(--help) returned error: %v", err)
		}
	})
	section := extractHelpSection(out, "子命令:", "选项:")
	if section == "" {
		t.Fatalf("--help 缺少 '子命令:' 段, got:\n%s", out)
	}
	if !strings.Contains(section, "web") {
		t.Errorf("--help 子命令段应含 web, got: %q", section)
	}
	// 隐藏子命令仍不应出现
	for _, hidden := range []string{"__complete", "__init"} {
		if strings.Contains(section, hidden) {
			t.Errorf("--help 子命令段不应列隐藏 subcommand %q", hidden)
		}
	}
}

// TestSmoke_WebHelp 断言 `ccl web --help` 打印用法 + 三个 flag 的一行说明。
func TestSmoke_WebHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"web", "--help"}); err != nil {
			t.Errorf("run(web --help) returned error: %v", err)
		}
	})
	if !strings.Contains(out, "ccl web") {
		t.Errorf("web --help 应含用法行, got:\n%s", out)
	}
	for _, flag := range []string{"--rotate", "--backfill", "--open"} {
		if !strings.Contains(out, flag) {
			t.Errorf("web --help 应列出 %q, got:\n%s", flag, out)
		}
	}
	if !strings.Contains(out, "URL") {
		t.Errorf("web --help 应说明会打印访问 URL, got:\n%s", out)
	}
}

// TestSmoke_CompletionTopLevelIncludesWeb 断言顶层补全候选含 web。
func TestSmoke_CompletionTopLevelIncludesWeb(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", ""}); err != nil {
			t.Errorf("run(__complete, '') returned error: %v", err)
		}
	})
	for _, want := range []string{"serve", "stats", "web", "provider", "mode", "-h", "--help", "help"} {
		if !strings.Contains(out, want) {
			t.Errorf("顶层候选应含 %q, got:\n%s", want, out)
		}
	}
}

// TestSmoke_CompletionWebFlags 断言 `ccl __complete web ""` 输出 web 的 flag。
func TestSmoke_CompletionWebFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", "web", ""}); err != nil {
			t.Errorf("run(__complete, web, '') returned error: %v", err)
		}
	})
	for _, want := range []string{"--rotate", "--backfill", "--open", "-h", "--help"} {
		if !strings.Contains(out, want) {
			t.Errorf("web 补全候选应含 %q, got:\n%s", want, out)
		}
	}
}

// TestSmoke_CompletionStatsIncludesProvider 断言 stats 补全候选含 --provider。
func TestSmoke_CompletionStatsIncludesProvider(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", "stats", "--"}); err != nil {
			t.Errorf("run(__complete, stats, --) returned error: %v", err)
		}
	})
	for _, want := range []string{"--provider", "--model", "--by-model"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats 补全候选应含 %q, got:\n%s", want, out)
		}
	}
}
