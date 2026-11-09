package safefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrite_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")

	if err := Write(path, []byte(`{"x":1}`), filepath.Join(dir, "backups"), 3); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if string(data) != `{"x":1}` {
		t.Fatalf("内容不对: %q", data)
	}
}

func TestWrite_EmptyPath(t *testing.T) {
	if err := Write("", []byte("x"), t.TempDir(), 3); err == nil {
		t.Fatal("空路径应该报错")
	}
}

func TestWrite_BacksUpPreviousContent(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "a.json")

	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), backups, 3); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// 新内容在位
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("新内容不对: %q", data)
	}

	// 旧内容进了备份目录
	matches, err := filepath.Glob(filepath.Join(backups, "a.json-*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("备份数量 = %d，期望 1", len(matches))
	}
	old, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "old" {
		t.Fatalf("备份内容不对: %q", old)
	}
}

func TestWrite_NoBackupWhenNoOldFile(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")

	if err := Write(filepath.Join(dir, "new.json"), []byte("x"), backups, 3); err != nil {
		t.Fatalf("Write: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(backups, "*.bak"))
	if len(matches) != 0 {
		t.Fatalf("不该有备份，却有 %d 份", len(matches))
	}
}

func TestWrite_PrunesOldBackups(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "a.json")

	// 写 5 次，每次内容不同 → 5 份备份
	for i := range 5 {
		if err := Write(path, []byte(strings.Repeat("x", i+1)), backups, 3); err != nil {
			t.Fatalf("第 %d 次 Write: %v", i, err)
		}
	}

	matches, _ := filepath.Glob(filepath.Join(backups, "a.json-*.bak"))
	if len(matches) != 3 {
		t.Fatalf("备份数量 = %d，期望 3（保留上限）", len(matches))
	}
}

func TestWrite_PruningIsPerFile(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")

	// a.json 写 5 次（保留 3），b.json 写 2 次（各留 1 份备份）
	for range 5 {
		if err := Write(filepath.Join(dir, "a.json"), []byte("a"), backups, 3); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := Write(filepath.Join(dir, "b.json"), []byte("b"), backups, 3); err != nil {
			t.Fatal(err)
		}
	}

	a, _ := filepath.Glob(filepath.Join(backups, "a.json-*.bak"))
	b, _ := filepath.Glob(filepath.Join(backups, "b.json-*.bak"))
	if len(a) != 3 {
		t.Fatalf("a.json 备份数 = %d，期望 3", len(a))
	}
	if len(b) != 1 {
		t.Fatalf("b.json 备份数 = %d，期望 1（不该被 a.json 挤掉）", len(b))
	}
}

func TestWrite_KeepZeroSkipsBackup(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "a.json")

	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), backups, 0); err != nil {
		t.Fatalf("Write: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(backups, "*.bak"))
	if len(matches) != 0 {
		t.Fatalf("keep=0 时不该备份，却有 %d 份", len(matches))
	}
}

func TestWrite_PreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), filepath.Join(dir, "b"), 3); err != nil {
		t.Fatalf("Write: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("权限 = %v，期望 0600（沿用旧文件）", fi.Mode().Perm())
	}
}

func TestWrite_NoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := Write(path, []byte("x"), filepath.Join(dir, "b"), 3); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}
