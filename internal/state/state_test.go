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
