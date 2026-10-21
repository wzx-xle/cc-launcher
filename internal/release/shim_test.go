package release

import (
	"strings"
	"testing"
)

// TestWindowsShim_CRLF 守住 ccl.cmd 的行尾约定。
//
// .cmd 是 cmd.exe 读的批处理文件，Windows 惯例（也是 cmd.exe 自己写出来的）
// 是 **CRLF**。v0.14.0 及之前的 zip 里是 LF —— 那是 bash heredoc 的默认行为，
// 而且**从未在真 Windows 上跑过**（CI 只构建、不执行产物）。这条测试让
// "shim 的行尾"变成一个显式契约，而不是碰巧。
func TestWindowsShim_CRLF(t *testing.T) {
	if !strings.Contains(WindowsShim, "\r\n") {
		t.Fatalf("shim 必须用 CRLF 行尾,got %q", WindowsShim)
	}
	// 不能有裸 LF（每个 \n 前面都得有 \r）
	withoutCR := strings.ReplaceAll(WindowsShim, "\r\n", "")
	if strings.Contains(withoutCR, "\n") {
		t.Errorf("shim 里有裸 LF 行尾:%q", withoutCR)
	}
}

// TestWindowsShim_Content 守住 shim 的内容：转发给同目录的 cc-launcher.exe，
// 用 %~dp0 取自身目录（绝对路径，不依赖 cwd / PATH）。
//
// 这两条是 scoop manifest 的 `bin: ["ccl.cmd"]` 能工作的前提。
func TestWindowsShim_Content(t *testing.T) {
	want := "@echo off\r\n\"%~dp0cc-launcher.exe\" %*\r\n"
	if WindowsShim != want {
		t.Errorf("shim 内容变了:\n got %q\nwant %q", WindowsShim, want)
	}
}

// TestWindowsShim_OnlyTwoLines shim 必须只有两行 —— 多余的行会被 cmd.exe
// 当成命令执行（用户敲 ccl 时可能执行到意料之外的命令）。
func TestWindowsShim_OnlyTwoLines(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(WindowsShim, "\r\n"), "\r\n")
	if len(lines) != 2 {
		t.Fatalf("shim 应为 2 行,got %d: %q", len(lines), lines)
	}
	if lines[0] != "@echo off" {
		t.Errorf("第 1 行: want %q, got %q", "@echo off", lines[0])
	}
}
