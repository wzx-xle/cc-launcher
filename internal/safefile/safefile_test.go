package safefile

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// swapNow 把包级时钟换成固定的，返回恢复函数。
//
// 用于把"同一时刻写两次"这种**撞不撞看运气**的场景变成确定性的。
func swapNow(fixed time.Time) func() {
	old := now
	now = func() time.Time { return fixed }
	return func() { now = old }
}

// TestWrite_SameInstantDoesNotLoseBackup 断言**同一时刻**连写多次不会丢备份。
//
// 回归：备份名只精确到毫秒 + os.WriteFile 是覆盖语义 → 同一毫秒内的两次写
// 产生同名文件 → 第二次覆盖第一次 → 丢一个中间版本。
//
// 这条**真的在 CI 上红过**：v0.21.0 的 CI 里 TestWrite_PruningIsPerFile 报
// "a.json 备份数 = 2，期望 3"（5 次写只落到 2 个不同的时间戳上）。本地撞不撞
// 看运气，所以用注入的固定时钟把它钉成必现。
func TestWrite_SameInstantDoesNotLoseBackup(t *testing.T) {
	defer swapNow(time.Date(2026, 11, 9, 1, 2, 3, 0, time.Local))()

	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}

	// 同一时刻写 5 次
	for i := range 5 {
		if err := Write(path, fmt.Appendf(nil, "v%d", i+1), backups, 3); err != nil {
			t.Fatal(err)
		}
	}

	matches, _ := filepath.Glob(filepath.Join(backups, "a.json-*.bak"))
	if len(matches) != 3 {
		t.Fatalf("备份数 = %d，期望 3（保留上限）", len(matches))
	}
}

// TestWrite_SameInstantKeepsAllBackups 断言同一时刻写的备份**一份都不少**。
//
// 保留上限给足（keep=10）时，5 次写应该留下 5 份互不覆盖的备份。
func TestWrite_SameInstantKeepsAllBackups(t *testing.T) {
	defer swapNow(time.Date(2026, 11, 9, 1, 2, 3, 0, time.Local))()

	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}

	for i := range 5 {
		if err := Write(path, fmt.Appendf(nil, "v%d", i+1), backups, 10); err != nil {
			t.Fatal(err)
		}
	}

	matches, _ := filepath.Glob(filepath.Join(backups, "a.json-*.bak"))
	if len(matches) != 5 {
		t.Fatalf("备份数 = %d，期望 5（同一时刻写 5 次就该有 5 份）", len(matches))
	}
	// 内容互不相同 → 证明没有被互相覆盖
	seen := map[string]bool{}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(data)] {
			t.Fatalf("有两份备份内容相同 → 被覆盖了: %s", m)
		}
		seen[string(data)] = true
	}
}

// TestBackupName_Ordering 断言备份名的**字典序 == 时间序**。
//
// prune 靠这个假设裁剪备份（末尾 = 最新，保留它，删掉更旧的）。一旦顺序反了，
// 它会把**最新**的当成最旧的删掉 —— 备份还在，但留的是错的版本。
func TestBackupName_Ordering(t *testing.T) {
	t1 := time.Date(2026, 11, 9, 1, 2, 3, 0, time.Local)
	t2 := t1.Add(time.Microsecond)
	t3 := t1.Add(time.Second)

	// 时间顺序：t1 的第 0 份 < t1 的第 1 份 < t1 的第 2 份 < t2 的第 0 份 < t3
	names := []string{
		backupName("a.json", t1, 2),
		backupName("a.json", t2, 0),
		backupName("a.json", t1, 0),
		backupName("a.json", t3, 0),
		backupName("a.json", t1, 1),
	}
	want := []string{
		backupName("a.json", t1, 0),
		backupName("a.json", t1, 1),
		backupName("a.json", t1, 2),
		backupName("a.json", t2, 0),
		backupName("a.json", t3, 0),
	}
	got := slices.Clone(names)
	slices.Sort(got)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("顺序不对:\n got %v\nwant %v", got, want)
		}
	}
}

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
	// 固定时钟：连写多次时"撞在同一毫秒"是真实存在的场景（v0.21.0 的 CI 上
	// 真撞过 —— 备份名撞名会互相覆盖，丢一个中间版本）。固定时钟让这个边界
	// **每次**都被覆盖，而不是靠机器速度碰运气。
	defer swapNow(time.Date(2026, 11, 9, 1, 2, 3, 0, time.Local))()
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
	// 固定时钟：连写多次时"撞在同一毫秒"是真实存在的场景（v0.21.0 的 CI 上
	// 真撞过 —— 备份名撞名会互相覆盖，丢一个中间版本）。固定时钟让这个边界
	// **每次**都被覆盖，而不是靠机器速度碰运气。
	defer swapNow(time.Date(2026, 11, 9, 1, 2, 3, 0, time.Local))()
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
