package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestBackup_SameSecondDoesNotLoseBackup 断言**同一秒内**连备份多次不丢备份。
//
// 回归：备份名只精确到秒 + os.WriteFile 是覆盖语义 → 同一秒内的两次备份
// 算出同一个文件名 → 第二次覆盖第一次 → 丢一份备份。
//
// 频繁切 provider 的用户每次 Merge 都触发一次 Backup，一秒内两次不是不可能
// （脚本切 provider、或 Merge 与切 provider 同时发生）。
//
// 撞不撞**看运气**（IO 快慢、调度抖动），所以用注入的固定时钟把它钉成必现。
func TestBackup_SameSecondDoesNotLoseBackup(t *testing.T) {
	old := now
	now = func() time.Time { return time.Date(2026, 11, 9, 1, 2, 3, 0, time.Local) }
	defer func() { now = old }()

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 10)

	if err := os.WriteFile(settingsPath, []byte(`{"model":"sonnet"}`), 0644); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := m.Backup(); err != nil {
			t.Fatal(err)
		}
	}

	backups, _ := filepath.Glob(filepath.Join(backupDir, "settings-*.json"))
	if len(backups) != 3 {
		t.Fatalf("备份数 = %d，期望 3（同一秒内备份 3 次就该有 3 份）", len(backups))
	}
}

// TestBackup_RotationKeepsNewestInSameSecond 断言轮转留下的是**最新的**那几份。
//
// 同一秒内的多份备份靠 "_NN" 序号区分新旧；rotateBackups 靠字典序判断新旧
// （末尾最新）。序号分隔符选错（用 "-" 而不是 "_"）这里就会红。
func TestBackup_RotationKeepsNewestInSameSecond(t *testing.T) {
	old := now
	now = func() time.Time { return time.Date(2026, 11, 9, 1, 2, 3, 0, time.Local) }
	defer func() { now = old }()

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 2) // 只留 2 份

	// 每次备份前改内容 → 各份备份内容不同 → 能验证留下的是哪几份
	for i := range 3 {
		content := `{"model":"m` + string(rune('0'+i)) + `"}`
		if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := m.Backup(); err != nil {
			t.Fatal(err)
		}
	}

	backups, _ := filepath.Glob(filepath.Join(backupDir, "settings-*.json"))
	if len(backups) != 2 {
		t.Fatalf("备份数 = %d，期望 2（保留上限）", len(backups))
	}
	// 留下的应该是后两次（m1、m2），最早的那份（m0）被裁掉
	kept := map[string]bool{}
	for _, b := range backups {
		data, _ := os.ReadFile(b)
		var v map[string]any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatal(err)
		}
		kept[v["model"].(string)] = true
	}
	if !kept["m1"] || !kept["m2"] {
		t.Fatalf("留下的不是最新的两份: %v", kept)
	}
	if kept["m0"] {
		t.Fatalf("最旧的那份没被裁掉: %v", kept)
	}
}
