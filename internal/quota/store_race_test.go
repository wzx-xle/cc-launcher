package quota

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSave_RenameRetriesWithoutRemovingTarget 断言：rename 失败时**先重试**，
// 且**不删目标文件**。
//
// 为什么不能先删（Windows 专属的坑）：Go 的 os.ReadFile 打开文件时**不带
// FILE_SHARE_DELETE** → 状态栏正在读 quota.json 时，daemon 的 os.Rename
// 会失败 → 旧实现走"删掉目标再改名" → 目标**短暂不存在** → 状态栏这一刻读到
// ErrNotExist → 误判成"没有额度记录" → 退出码非 0 → 状态栏回落到 Claude Code
// 自己的 rate_limits（用户看到"原始的限额内容"）。
func TestSave_RenameRetriesWithoutRemovingTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota.json")
	st := NewStore(dir)

	// 写一个已存在的文件（Save 的目标）
	if err := os.WriteFile(path, []byte(`{"version":1,"entries":{}}`), 0600); err != nil {
		t.Fatal(err)
	}

	// 注入：前两次 rename 失败（模拟"目标被占用"），第三次成功
	origRename := renameFile
	defer func() { renameFile = origRename }()
	var calls int
	renameFile = func(oldpath, newpath string) error {
		calls++
		if calls <= 2 {
			return os.ErrPermission // 模拟 sharing violation
		}
		return origRename(oldpath, newpath)
	}

	f := &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	if err := st.Save(f); err != nil {
		t.Fatalf("Save 应重试后成功: %v", err)
	}
	if calls != 3 {
		t.Errorf("rename 应被调用 3 次（2 次失败 + 1 次成功）, got %d", calls)
	}
	// **关键**：目标文件在重试期间不能被删 —— 删了就有"文件不存在"的窗口
	if _, err := os.Stat(path); err != nil {
		t.Errorf("重试期间不该删掉目标文件: %v", err)
	}
	// 内容应是新写的
	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != quotaFileVersion {
		t.Errorf("重试后写入的内容不对: %+v", got)
	}
}

// TestSave_RenameFailsThenRemoveFallback 断言：重试**全部**失败时，才走
// "删掉目标再改名"的兜底（目标被长期占用时的唯一出路）。
func TestSave_RenameFailsThenRemoveFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota.json")
	st := NewStore(dir)
	if err := os.WriteFile(path, []byte(`{"version":1,"entries":{}}`), 0600); err != nil {
		t.Fatal(err)
	}

	origRename := renameFile
	defer func() { renameFile = origRename }()
	var calls int
	renameFile = func(oldpath, newpath string) error {
		calls++
		if calls <= renameRetries {
			return os.ErrPermission
		}
		return origRename(oldpath, newpath)
	}
	f := &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	if err := st.Save(f); err != nil {
		t.Fatalf("Save 兜底路径应成功: %v", err)
	}
	if calls != renameRetries+1 {
		t.Errorf("rename 应被调用 %d 次（%d 次重试 + 1 次兜底）, got %d", renameRetries+1, renameRetries, calls)
	}
}

// TestLoad_RetriesWhenFileVanished 断言：文件**短暂消失**（正在被替换）时，
// Load 会重试并拿到**后来的内容**，而不是误判成"没有记录"。
//
// 这条正是用户看到的现象：状态栏撞上替换窗口 → ccl quota 退出码 1 → 回落到
// Claude Code 自己的 rate_limits（"原始的限额内容"）。
//
// 用注入点做**确定性**测试：第一次读返回 ErrNotExist（模拟文件刚被删掉），
// 第二次返回真实内容（模拟 rename 回来）。
func TestLoad_RetriesWhenFileVanished(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)

	f := &File{Version: quotaFileVersion, Entries: map[string]Entry{
		"p1": {Source: "newapi", Pool: "pool-1", FetchedAt: time.Now().Unix()},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}

	origRead := readFile
	defer func() { readFile = origRead }()
	var calls int
	readFile = func(p string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, os.ErrNotExist // 文件正被替换（短暂不存在）
		}
		return origRead(p)
	}

	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("读失败后应重试一次, readFile 被调用 %d 次", calls)
	}
	if _, ok := got.EntryFor("p1"); !ok {
		t.Error("文件短暂消失后，Load 应重试并拿到内容，而不是当成空数据")
	}
}

// TestLoad_StillEmptyWhenFileReallyMissing 断言：文件**真的**不存在时（daemon
// 从没写过），仍返回空 File 而不报错（首次运行不该是错误）。
func TestLoad_StillEmptyWhenFileReallyMissing(t *testing.T) {
	st := NewStore(t.TempDir())
	got, err := st.Load()
	if err != nil {
		t.Fatalf("文件不存在不该报错: %v", err)
	}
	if len(got.Entries) != 0 {
		t.Errorf("该返回空数据, got %+v", got.Entries)
	}
}
