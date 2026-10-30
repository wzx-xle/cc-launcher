package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude 造一个假的 claude 可执行文件。
//
// helpText 是 `claude --help` 的输出；counterFile 记录被调用了几次（验证缓存）。
func fakeClaude(t *testing.T, dir, helpText, counterFile string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"echo \"$(date +%s%N)\" >> " + counterFile + "\n" +
		"cat <<'EOF'\n" + helpText + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEnsureSettingsSupported_Supported 断言：claude 支持 --settings → 不报错。
func TestEnsureSettingsSupported_Supported(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	counter := filepath.Join(t.TempDir(), "calls")
	fakeClaude(t, bin, "Usage: claude [options]\n  --settings <file>   settings file\n", counter)

	if err := ensureSettingsSupported(); err != nil {
		t.Fatalf("支持 --settings 时不该报错: %v", err)
	}
	if !fileExists(counter) {
		t.Fatal("应该执行过一次 claude --help")
	}
}

// TestEnsureSettingsSupported_NotSupported 断言：不支持 → 报错并说明原因。
func TestEnsureSettingsSupported_NotSupported(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	counter := filepath.Join(t.TempDir(), "calls")
	fakeClaude(t, bin, "Usage: claude [options]\n  --model <model>\n", counter)

	err := ensureSettingsSupported()
	if err == nil {
		t.Fatal("不支持 --settings 时应报错")
	}
	if !strings.Contains(err.Error(), "--settings") {
		t.Errorf("错误应提到 --settings: %v", err)
	}
}

// TestEnsureSettingsSupported_Cached 断言：第二次调用走缓存，不再执行 claude。
func TestEnsureSettingsSupported_Cached(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	counter := filepath.Join(t.TempDir(), "calls")
	fakeClaude(t, bin, "Usage: claude [options]\n  --settings <file>\n", counter)

	if err := ensureSettingsSupported(); err != nil {
		t.Fatal(err)
	}
	first := countLines(t, counter)

	if err := ensureSettingsSupported(); err != nil {
		t.Fatal(err)
	}
	second := countLines(t, counter)

	if second != first {
		t.Errorf("第二次应命中缓存，不再执行 claude（%d → %d）", first, second)
	}
}

// TestEnsureSettingsSupported_ClaudeChanged 断言：claude 换了（指纹变）→ 重新探测。
func TestEnsureSettingsSupported_ClaudeChanged(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	counter := filepath.Join(t.TempDir(), "calls")
	fakeClaude(t, bin, "Usage: claude [options]\n  --settings <file>\n", counter)

	if err := ensureSettingsSupported(); err != nil {
		t.Fatal(err)
	}
	first := countLines(t, counter)

	// 换成另一个版本的 claude（内容不同 → 大小/mtime 变）
	fakeClaude(t, bin, "Usage: claude [options]\n  --settings <file>\n  --model <model>\n", counter)
	if err := ensureSettingsSupported(); err != nil {
		t.Fatal(err)
	}
	second := countLines(t, counter)
	if second == first {
		t.Error("claude 换了之后应重新探测")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(data)))
}
