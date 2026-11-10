package pathutil

import (
	"path/filepath"
	"testing"
)

// TestShorten 断言主目录前缀被换成 "~"，其余情况原样返回。
func TestShorten(t *testing.T) {
	home := t.TempDir()
	// os.UserHomeDir() 在 Unix 读 $HOME、在 Windows 读 %USERPROFILE%，两个都设上，
	// 免得测试只在某个平台上才走到"命中"分支。
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	outside := filepath.Join(t.TempDir(), "elsewhere.txt")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"主目录下的路径", filepath.Join(home, ".claude", "settings.json"), "~/.claude/settings.json"},
		{"主目录下的嵌套路径", filepath.Join(home, "a", "b", "c.txt"), "~/a/b/c.txt"},
		{"恰好是主目录", home, "~"},
		{"不在主目录下", outside, outside},
		{"空串", "", ""},
		{"主目录是前缀但不是路径边界", home + "x", home + "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Shorten(c.in); got != c.want {
				t.Errorf("Shorten(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

// TestShorten_HomeUnavailable 断言 home 取不到时原样返回（不报错、不 panic）。
func TestShorten_HomeUnavailable(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	const p = "/home/someone/.claude/settings.json"
	if got := Shorten(p); got != p {
		t.Errorf("home 不可用时 Shorten(%q) = %q，期望原样返回", p, got)
	}
}
