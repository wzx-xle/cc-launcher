package web

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidProviderName(t *testing.T) {
	ok := []string{"iflytek", "my-provider", "a.b", "A_1", "x"}
	for _, name := range ok {
		if !validProviderName(name) {
			t.Errorf("%q 应该合法", name)
		}
	}

	bad := []string{
		"",
		".",
		"..",
		"../etc",
		"foo/bar",
		`foo\bar`,
		"..%2F..%2Fetc%2Fpasswd",
		"has space",
		"emoji😀",
		strings.Repeat("x", 65),
	}
	for _, name := range bad {
		if validProviderName(name) {
			t.Errorf("%q 应该被拒绝", name)
		}
	}
}

func TestProviderPath_InsideDir(t *testing.T) {
	dir := t.TempDir()
	path, ok := providerPath(dir, "iflytek")
	if !ok {
		t.Fatal("合法名字应该通过")
	}
	want := filepath.Join(dir, "iflytek.json")
	if path != want {
		t.Fatalf("路径 = %q，期望 %q", path, want)
	}
}

func TestProviderPath_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"..", "../evil", "foo/bar", `..\evil`} {
		if _, ok := providerPath(dir, name); ok {
			t.Errorf("%q 应该被拒绝", name)
		}
	}
}

func TestProviderPath_EmptyDir(t *testing.T) {
	if _, ok := providerPath("", "iflytek"); ok {
		t.Fatal("空目录应该被拒绝")
	}
}

func TestClaudeFilePath_KnownIDs(t *testing.T) {
	settings := "/home/u/.claude/settings.json"
	memory := "/home/u/.claude/CLAUDE.md"

	got, ok := claudeFilePath(ClaudeFileSettings, settings, memory)
	if !ok || got != settings {
		t.Fatalf("settings 映射 = %q (%v)", got, ok)
	}
	got, ok = claudeFilePath(ClaudeFileMemory, settings, memory)
	if !ok || got != memory {
		t.Fatalf("claude-md 映射 = %q (%v)", got, ok)
	}
}

func TestClaudeFilePath_UnknownID(t *testing.T) {
	for _, id := range []string{"", "..", "settings.json", "other", "../../etc/passwd"} {
		if _, ok := claudeFilePath(id, "/s", "/m"); ok {
			t.Errorf("%q 应该被拒绝", id)
		}
	}
}

func TestClaudeFilePath_UnconfiguredPath(t *testing.T) {
	if _, ok := claudeFilePath(ClaudeFileSettings, "", "/m"); ok {
		t.Fatal("未配置的路径应该被拒绝")
	}
}

func TestEtagOf_StableAndSensitive(t *testing.T) {
	a := etagOf([]byte(`{"x":1}`))
	b := etagOf([]byte(`{"x":1}`))
	if a != b {
		t.Fatalf("同样内容应得到同样 etag: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("etag 长度 = %d，期望 16", len(a))
	}
	if a == etagOf([]byte(`{"x":2}`)) {
		t.Fatal("不同内容不该得到同样 etag")
	}
}

func TestEtagMatches(t *testing.T) {
	cur := etagOf([]byte("x"))
	cases := []struct {
		in   string
		want bool
	}{
		{cur, true},
		{`"` + cur + `"`, true},
		{"", false},
		{"deadbeefdeadbeef", false},
	}
	for _, c := range cases {
		if got := etagMatches(c.in, cur); got != c.want {
			t.Errorf("etagMatches(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}
