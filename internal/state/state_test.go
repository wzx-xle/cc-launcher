package state

import (
	"os"
	"testing"
)

func TestLoad_NoFile(t *testing.T) {
	m := NewManager(t.TempDir())
	s, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if s.ActiveProvider != "" {
		t.Errorf("ActiveProvider = %q, want empty", s.ActiveProvider)
	}
	if s.InjectedSnapshot != nil {
		t.Errorf("InjectedSnapshot = %v, want nil", s.InjectedSnapshot)
	}
	if len(s.LastPermissionModes) != 0 {
		t.Errorf("LastPermissionModes len = %d, want 0", len(s.LastPermissionModes))
	}
	if s.DirBindings == nil {
		t.Error("DirBindings should be non-nil empty map for backward compat")
	}
	if len(s.DirBindings) != 0 {
		t.Errorf("DirBindings len = %d, want 0", len(s.DirBindings))
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	s := &State{
		ActiveProvider: "bedrock",
		InjectedSnapshot: map[string]any{
			"env":   map[string]any{"AWS_REGION": "us-east-1"},
			"model": "sonnet",
		},
		LastPermissionModes: map[string]string{
			"/home/user/project": "plan",
		},
	}

	if err := m.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ActiveProvider != "bedrock" {
		t.Errorf("ActiveProvider = %q, want %q", loaded.ActiveProvider, "bedrock")
	}
	if loaded.LastPermissionModes["/home/user/project"] != "plan" {
		t.Errorf("LastPermissionModes mismatch")
	}
}

func TestLoad_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	statePath := dir + "/state.json"
	if err := os.WriteFile(statePath, []byte("not json{{"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	_, err := m.Load()
	if err == nil {
		t.Fatal("Load() expected error for corrupt JSON, got nil")
	}
}

func TestGetSetActiveProvider(t *testing.T) {
	m := NewManager(t.TempDir())

	if got := m.GetActiveProvider(); got != "" {
		t.Errorf("GetActiveProvider() = %q, want empty", got)
	}

	if err := m.SetActiveProvider("anthropic"); err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}
	if got := m.GetActiveProvider(); got != "anthropic" {
		t.Errorf("GetActiveProvider() = %q, want %q", got, "anthropic")
	}
}

func TestGetSetPermissionMode(t *testing.T) {
	m := NewManager(t.TempDir())

	if mode, ok := m.GetPermissionMode("/some/dir"); ok || mode != "" {
		t.Errorf("GetPermissionMode() = (%q, %v), want (\"\", false)", mode, ok)
	}

	if err := m.SetPermissionMode("/some/dir", "plan"); err != nil {
		t.Fatalf("SetPermissionMode() error = %v", err)
	}

	mode, ok := m.GetPermissionMode("/some/dir")
	if !ok || mode != "plan" {
		t.Errorf("GetPermissionMode() = (%q, %v), want (%q, true)", mode, ok, "plan")
	}
}

func TestGetSetInjectedSnapshot(t *testing.T) {
	m := NewManager(t.TempDir())

	if snap := m.GetInjectedSnapshot(); snap != nil {
		t.Errorf("GetInjectedSnapshot() = %v, want nil", snap)
	}

	snap := map[string]any{"env": map[string]any{"KEY": "val"}}
	if err := m.SetInjectedSnapshot(snap); err != nil {
		t.Fatalf("SetInjectedSnapshot() error = %v", err)
	}

	m2 := NewManager(m.baseDir)
	loaded := m2.GetInjectedSnapshot()
	if loaded == nil {
		t.Fatal("GetInjectedSnapshot() returned nil after save")
	}
}

// ─── provider-setenv-cwd-binding §2: dir_bindings ───

// TestSaveAndLoad_DirBindings 覆盖 tasks.md §2.1："识别并保留 dir_bindings
// map[string]string 字段"。
func TestSaveAndLoad_DirBindings(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	s := &State{
		DirBindings: map[string]string{
			"/home/u/proj-a": "deepseek",
			"/home/u/proj-b": "anthropic",
		},
	}
	if err := m.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.DirBindings["/home/u/proj-a"]; got != "deepseek" {
		t.Errorf("DirBindings[/home/u/proj-a] = %q, want %q", got, "deepseek")
	}
	if got := loaded.DirBindings["/home/u/proj-b"]; got != "anthropic" {
		t.Errorf("DirBindings[/home/u/proj-b] = %q, want %q", got, "anthropic")
	}
}

// TestLoad_OldStateWithoutDirBindings 覆盖 tasks.md §2.1 "向前兼容旧 state.json"
// —— 老 state.json（无 dir_bindings 字段）load 应不报错，DirBindings 是空 map。
func TestLoad_OldStateWithoutDirBindings(t *testing.T) {
	dir := t.TempDir()
	statePath := dir + "/state.json"
	oldState := `{"active_provider": "anthropic", "last_permission_modes": {"/some/wd": "plan"}}`
	if err := os.WriteFile(statePath, []byte(oldState), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	loaded, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for legacy state.json", err)
	}
	if loaded.ActiveProvider != "anthropic" {
		t.Errorf("ActiveProvider = %q, want %q", loaded.ActiveProvider, "anthropic")
	}
	if loaded.LastPermissionModes["/some/wd"] != "plan" {
		t.Errorf("LastPermissionModes lost during legacy load")
	}
	if loaded.DirBindings == nil {
		t.Fatal("DirBindings should be non-nil empty map after loading legacy state.json")
	}
	if len(loaded.DirBindings) != 0 {
		t.Errorf("DirBindings should be empty for legacy state.json, got %v", loaded.DirBindings)
	}
}

// TestDirBindings_PreservedOnSetActiveProvider 覆盖 tasks.md §2.1
// "未知字段不报错，向前兼容" + spec/provider-cwd-binding §"dir_bindings field
// is preserved on other state writes"——写 active_provider 不丢 dir_bindings。
func TestDirBindings_PreservedOnSetActiveProvider(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if err := m.SetProviderForDir("/wd", "deepseek"); err != nil {
		t.Fatalf("SetProviderForDir() error = %v", err)
	}
	if err := m.SetActiveProvider("anthropic"); err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}

	m2 := NewManager(dir)
	if got := m2.GetProviderForDir("/wd"); got != "deepseek" {
		t.Errorf("DirBindings[/wd] lost after SetActiveProvider: got %q, want deepseek", got)
	}
}

// TestGetProviderForDir_NotFound 覆盖 spec/provider-cwd-binding §"reading
// dir_bindings when none exist"——读不存在 wd 返空字符串。
func TestGetProviderForDir_NotFound(t *testing.T) {
	m := NewManager(t.TempDir())
	if got := m.GetProviderForDir("/nonexistent"); got != "" {
		t.Errorf("GetProviderForDir(/nonexistent) = %q, want empty", got)
	}
}

// TestSetProviderForDir_NewBinding 覆盖 spec §"writing a new binding"。
func TestSetProviderForDir_NewBinding(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if err := m.SetProviderForDir("/new-wd", "deepseek"); err != nil {
		t.Fatalf("SetProviderForDir() error = %v", err)
	}
	if got := m.GetProviderForDir("/new-wd"); got != "deepseek" {
		t.Errorf("GetProviderForDir(/new-wd) = %q, want deepseek", got)
	}
	// 持久化验证：新建 Manager 读同样目录
	m2 := NewManager(dir)
	if got := m2.GetProviderForDir("/new-wd"); got != "deepseek" {
		t.Errorf("after reload: GetProviderForDir(/new-wd) = %q, want deepseek", got)
	}
}

// TestSetProviderForDir_OverwriteExisting 覆盖 spec §"updating an existing
// binding"——同 wd 二次写入覆盖。
func TestSetProviderForDir_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if err := m.SetProviderForDir("/wd", "deepseek"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetProviderForDir("/wd", "anthropic"); err != nil {
		t.Fatal(err)
	}
	if got := m.GetProviderForDir("/wd"); got != "anthropic" {
		t.Errorf("GetProviderForDir(/wd) = %q, want %q (overwrite)", got, "anthropic")
	}
}
