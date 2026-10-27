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
	// --backfill 已挪到 ccl serve（web-backfill-to-serve §D1）——web 只剩
	// --rotate / --open 两个关于看板本身的 flag。
	for _, want := range []string{"--rotate", "--open", "-h", "--help"} {
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

// TestSmoke_ServeBackfillStopMutex 断言 `ccl serve --backfill --stop` 报互斥错误。
//
// web-backfill-to-serve §D1：--backfill 进 serve 的互斥组（与另外四个一样是
// "对 daemon 数据 / 状态的维护动作"）。
func TestSmoke_ServeBackfillStopMutex(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--backfill", "--stop"})
	})
	if err == nil {
		t.Fatal("runServe --backfill --stop must return error (mutex violation)")
	}
}

// TestSmoke_WebBackfillRemoved 断言 `ccl web --backfill` 不再被识别。
//
// 回填已挪到 `ccl serve --backfill`（web-backfill-to-serve §D4：不做别名兼容）。
func TestSmoke_WebBackfillRemoved(t *testing.T) {
	sandboxHome(t)
	var err error
	_ = captureStderr(t, func() {
		err = runWeb([]string{"--backfill"})
	})
	if err == nil {
		t.Fatal("ccl web --backfill 应报 unknown flag（回填已挪到 ccl serve --backfill）")
	}
	if !strings.Contains(err.Error(), "not defined") {
		t.Errorf("错误信息应提到 flag 未定义, got %q", err.Error())
	}
}

// TestSmoke_CompletionWebHasNoBackfill 断言 web 的补全候选里没有 --backfill。
func TestSmoke_CompletionWebHasNoBackfill(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", "web", ""}); err != nil {
			t.Errorf("run(__complete, web, '') returned error: %v", err)
		}
	})
	if strings.Contains(out, "--backfill") {
		t.Errorf("web 补全候选不应含 --backfill, got:\n%s", out)
	}
	for _, want := range []string{"--rotate", "--open"} {
		if !strings.Contains(out, want) {
			t.Errorf("web 补全候选应含 %q, got:\n%s", want, out)
		}
	}
}

// TestSmoke_CompletionServeHasBackfill 断言 serve 的补全候选里有 --backfill。
func TestSmoke_CompletionServeHasBackfill(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run([]string{"__complete", "serve", ""}); err != nil {
			t.Errorf("run(__complete, serve, '') returned error: %v", err)
		}
	})
	if !strings.Contains(out, "--backfill") {
		t.Errorf("serve 补全候选应含 --backfill, got:\n%s", out)
	}
}
