package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettings_NoFile(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "settings.json"), t.TempDir(), 10)
	settings, err := m.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	if len(settings) != 0 {
		t.Errorf("LoadSettings() len = %d, want 0", len(settings))
	}
}

// TestDefaultMaxBackups 锁定备份轮转默认值。改这个值要同步改 README + statscmd 帮助文案，
// 所以加测试显式锚定。merge-on-rotate：调高原因见 config.go DefaultMaxBackups 注释。
func TestDefaultMaxBackups(t *testing.T) {
	if DefaultMaxBackups != 30 {
		t.Errorf("DefaultMaxBackups = %d, want 30 (merge-on-rotate rationale)", DefaultMaxBackups)
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	m := NewManager(path, t.TempDir(), 10)

	settings := map[string]any{
		"env":   map[string]any{"KEY": "val"},
		"model": "sonnet",
	}
	if err := m.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}

	loaded, err := m.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}

	loadedJSON, _ := json.Marshal(loaded)
	wantJSON, _ := json.Marshal(settings)
	if string(loadedJSON) != string(wantJSON) {
		t.Errorf("roundtrip mismatch: got %s, want %s", loadedJSON, wantJSON)
	}
}

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 3)

	settings := map[string]any{"model": "sonnet"}
	if err := m.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	if err := m.Backup(); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}

	backups, err := filepath.Glob(filepath.Join(backupDir, "settings-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups count = %d, want 1", len(backups))
	}

	data, _ := os.ReadFile(backups[0])
	var backup map[string]any
	json.Unmarshal(data, &backup)
	if backup["model"] != "sonnet" {
		t.Error("backup content mismatch")
	}
}

func TestBackup_Rotation(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 2)

	for i := 0; i < 3; i++ {
		settings := map[string]any{"seq": i}
		m.SaveSettings(settings)
		m.Backup()
	}

	backups, _ := filepath.Glob(filepath.Join(backupDir, "settings-*.json"))
	if len(backups) > 2 {
		t.Errorf("backups count = %d, want <= 2 (rotation not working)", len(backups))
	}
}

