package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newSettingsCacheRig 造一个 HOME 隔离的测试环境，返回 cache 目录。
func newSettingsCacheRig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cc-launcher", "cache")
	return dir
}

// TestSettingsFileName_ContentHashed 断言文件名由内容哈希决定。
func TestSettingsFileName_ContentHashed(t *testing.T) {
	a := settingsFileName(`{"env":{"A":"1"}}`)
	b := settingsFileName(`{"env":{"A":"1"}}`)
	c := settingsFileName(`{"env":{"A":"2"}}`)

	if a != b {
		t.Errorf("同样内容应得到同样文件名: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("不同内容应得到不同文件名（并发不串）: %q", a)
	}
	if !strings.HasPrefix(a, "settings-") || !strings.HasSuffix(a, ".json") {
		t.Errorf("文件名形状不对: %q", a)
	}
	// 16 位 hex
	stem := strings.TrimSuffix(strings.TrimPrefix(a, "settings-"), ".json")
	if len(stem) != 16 {
		t.Errorf("哈希段长度 = %d, want 16 (%q)", len(stem), a)
	}
}

// TestWriteSettingsFile_ReusesSameFile 断言同内容复用同一个文件（幂等）。
func TestWriteSettingsFile_ReusesSameFile(t *testing.T) {
	newSettingsCacheRig(t)
	content := `{"env":{"A":"1"}}`

	p1, err := writeSettingsFile(content)
	if err != nil {
		t.Fatalf("writeSettingsFile: %v", err)
	}
	// 再写一次同样内容 → 同一个路径，且 mtime 不变（没重写）
	st1, err := os.Stat(p1)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	p2, err := writeSettingsFile(content)
	if err != nil {
		t.Fatalf("writeSettingsFile (2nd): %v", err)
	}
	if p1 != p2 {
		t.Errorf("同内容应复用同一文件: %q vs %q", p1, p2)
	}
	st2, err := os.Stat(p2)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("幂等写入不该重写文件（mtime 变了）")
	}
}

// TestWriteSettingsFile_Permissions 断言目录 0700、文件 0600。
func TestWriteSettingsFile_Permissions(t *testing.T) {
	dir := newSettingsCacheRig(t)
	p, err := writeSettingsFile(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-secret"}}`)
	if err != nil {
		t.Fatalf("writeSettingsFile: %v", err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0700 {
		t.Errorf("cache 目录权限 = %o, want 700", perm)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Errorf("settings 文件权限 = %o, want 600", perm)
	}
}

// TestWriteSettingsFile_ContentIsComplete 断言文件内容与交付内容一致（原子写）。
func TestWriteSettingsFile_ContentIsComplete(t *testing.T) {
	newSettingsCacheRig(t)
	content := `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-secret","X":"y"}}`
	p, err := writeSettingsFile(content)
	if err != nil {
		t.Fatalf("writeSettingsFile: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("文件内容不完整:\n got %q\nwant %q", got, content)
	}
}

// TestCleanupSettingsCache 断言只删超过 1 年的 settings-*.json。
func TestCleanupSettingsCache(t *testing.T) {
	dir := newSettingsCacheRig(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	old := filepath.Join(dir, "settings-aaaaaaaaaaaaaaaa.json")
	fresh := filepath.Join(dir, "settings-bbbbbbbbbbbbbbbb.json")
	other := filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, fresh, other} {
		if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// old 的 mtime 设成 2 年前
	twoYearsAgo := time.Now().Add(-2 * 365 * 24 * time.Hour)
	if err := os.Chtimes(old, twoYearsAgo, twoYearsAgo); err != nil {
		t.Fatal(err)
	}
	// fresh 的 mtime 设成 30 天前
	thirtyDaysAgo := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(fresh, thirtyDaysAgo, thirtyDaysAgo); err != nil {
		t.Fatal(err)
	}

	if err := cleanupSettingsCache(); err != nil {
		t.Fatalf("cleanupSettingsCache: %v", err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("超过 1 年的文件应被删除, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("1 年内的文件应保留: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("非 settings-* 文件不该动: %v", err)
	}
}

// TestCleanupSettingsCache_MissingDir 断言目录不存在时是 no-op。
func TestCleanupSettingsCache_MissingDir(t *testing.T) {
	newSettingsCacheRig(t)
	if err := cleanupSettingsCache(); err != nil {
		t.Errorf("目录不存在时不该报错: %v", err)
	}
}

// TestSettingsArgs_NoSecretOnCommandLine 断言 argv 里只有路径、没有内容。
func TestSettingsArgs_NoSecretOnCommandLine(t *testing.T) {
	newSettingsCacheRig(t)
	content := `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-secret-value"}}`
	args, _, err := settingsArgs(content)
	if err != nil {
		t.Fatalf("settingsArgs: %v", err)
	}
	if len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("参数形状不对: %v", args)
	}
	if strings.Contains(args[1], "sk-secret-value") {
		t.Errorf("argv 里不该出现明文 token: %q", args[1])
	}
	// 路径指向的文件里有内容
	got, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("文件内容不对: %q", got)
	}
}
