package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestCleanSnapshot_RemoveUnchanged(t *testing.T) {
	settings := map[string]any{
		"env":   map[string]any{"AWS_REGION": "us-east-1", "CUSTOM_KEY": "keep"},
		"model": "sonnet",
	}
	snapshot := map[string]any{
		"env":   map[string]any{"AWS_REGION": "us-east-1"},
		"model": "sonnet",
	}

	cleanSnapshot(settings, snapshot)

	if _, ok := settings["model"]; ok {
		t.Error("model should be removed (unchanged)")
	}
	env := settings["env"].(map[string]any)
	if _, ok := env["AWS_REGION"]; ok {
		t.Error("AWS_REGION should be removed (unchanged)")
	}
	if _, ok := env["CUSTOM_KEY"]; !ok {
		t.Error("CUSTOM_KEY should be kept (not in snapshot)")
	}
}

func TestCleanSnapshot_KeepModified(t *testing.T) {
	settings := map[string]any{
		"env":   map[string]any{"AWS_REGION": "us-west-2"},
		"model": "opus",
	}
	snapshot := map[string]any{
		"env":   map[string]any{"AWS_REGION": "us-east-1"},
		"model": "sonnet",
	}

	cleanSnapshot(settings, snapshot)

	env := settings["env"].(map[string]any)
	if env["AWS_REGION"] != "us-west-2" {
		t.Error("user-modified AWS_REGION should be kept")
	}
	if settings["model"] != "opus" {
		t.Error("user-modified model should be kept")
	}
}

func TestCleanSnapshot_KeyNotInSettings(t *testing.T) {
	settings := map[string]any{
		"env": map[string]any{"EXISTING": "val"},
	}
	snapshot := map[string]any{
		"env":            map[string]any{"EXISTING": "val", "GONE": "was-deleted"},
		"removedSection": map[string]any{"foo": "bar"},
	}

	cleanSnapshot(settings, snapshot)

	// env 中唯一的快照匹配 key 被清理后，env 整个被删除
	if _, ok := settings["env"]; ok {
		t.Error("env should be removed (all snapshot-matching keys cleaned)")
	}
}

func TestDeepMerge(t *testing.T) {
	dst := map[string]any{
		"env":   map[string]any{"EXISTING": "old"},
		"model": "haiku",
	}
	src := map[string]any{
		"env": map[string]any{"NEW_KEY": "new", "EXISTING": "overwritten"},
		"modelOverrides": map[string]any{
			"claude-opus-4-8": "arn:...",
		},
	}

	deepMerge(dst, src)

	env := dst["env"].(map[string]any)
	if env["EXISTING"] != "overwritten" {
		t.Error("EXISTING should be overwritten")
	}
	if env["NEW_KEY"] != "new" {
		t.Error("NEW_KEY should be added")
	}
	if _, ok := dst["modelOverrides"]; !ok {
		t.Error("modelOverrides should be added")
	}
	if dst["model"] != "haiku" {
		t.Error("model should be kept (not in src)")
	}
}

func TestDeepMerge_NestedObject(t *testing.T) {
	dst := map[string]any{
		"env": map[string]any{"nested": map[string]any{"a": 1, "b": 2}},
	}
	src := map[string]any{
		"env": map[string]any{"nested": map[string]any{"b": 3, "c": 4}},
	}

	deepMerge(dst, src)

	env := dst["env"].(map[string]any)
	nested := env["nested"].(map[string]any)
	if nested["a"] != 1 {
		t.Error("a should be kept from dst")
	}
	if nested["b"] != 3 {
		t.Error("b should be overwritten by src")
	}
	if nested["c"] != 4 {
		t.Error("c should be added from src")
	}
}

func TestMerge_FullFlow(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 10)

	existing := map[string]any{
		"env": map[string]any{
			"AWS_REGION":              "us-east-1",
			"CLAUDE_CODE_USE_BEDROCK": "1",
			"USER_CUSTOM":             "my-value",
		},
		"model": "sonnet",
	}
	m.SaveSettings(existing)

	oldSnapshot := map[string]any{
		"env": map[string]any{
			"AWS_REGION":              "us-east-1",
			"CLAUDE_CODE_USE_BEDROCK": "1",
		},
		"model": "sonnet",
	}

	newProvider := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_API_KEY": "sk-new",
		},
		"model": "opus",
	}

	if err := m.Merge(newProvider, oldSnapshot); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	result, _ := m.LoadSettings()

	env := result["env"].(map[string]any)
	if _, ok := env["AWS_REGION"]; ok {
		t.Error("AWS_REGION should be cleaned")
	}
	if _, ok := env["CLAUDE_CODE_USE_BEDROCK"]; ok {
		t.Error("CLAUDE_CODE_USE_BEDROCK should be cleaned")
	}
	if env["USER_CUSTOM"] != "my-value" {
		t.Error("USER_CUSTOM should be kept")
	}
	if env["ANTHROPIC_API_KEY"] != "sk-new" {
		t.Error("ANTHROPIC_API_KEY should be injected")
	}
	if result["model"] != "opus" {
		t.Error("model should be overwritten by new provider")
	}

	backups, _ := filepath.Glob(filepath.Join(backupDir, "settings-*.json"))
	if len(backups) != 1 {
		t.Errorf("expected 1 backup, got %d", len(backups))
	}
}

func TestMerge_AuthConflict(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 10)

	// settings.json 中有 OAuth 登录设置的 AUTH_TOKEN
	m.SaveSettings(map[string]any{
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN": "oauth-token",
		},
	})

	// provider 配置含 API_KEY
	newProvider := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_API_KEY": "sk-key",
			"ANTHROPIC_MODEL":   "sonnet",
		},
	}

	if err := m.Merge(newProvider, nil); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	result, _ := m.LoadSettings()
	env := result["env"].(map[string]any)

	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("ANTHROPIC_AUTH_TOKEN should be removed when API_KEY is injected")
	}
	if env["ANTHROPIC_API_KEY"] != "sk-key" {
		t.Error("ANTHROPIC_API_KEY should be injected")
	}
	if env["ANTHROPIC_MODEL"] != "sonnet" {
		t.Error("ANTHROPIC_MODEL should be injected")
	}
}

func TestMerge_AuthConflict_Reverse(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 10)

	m.SaveSettings(map[string]any{
		"env": map[string]any{
			"ANTHROPIC_API_KEY": "old-key",
		},
	})

	// provider 含 AUTH_TOKEN
	newProvider := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN": "new-token",
		},
	}

	if err := m.Merge(newProvider, nil); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	result, _ := m.LoadSettings()
	env := result["env"].(map[string]any)

	if _, ok := env["ANTHROPIC_API_KEY"]; ok {
		t.Error("ANTHROPIC_API_KEY should be removed when AUTH_TOKEN is injected")
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "new-token" {
		t.Error("ANTHROPIC_AUTH_TOKEN should be injected")
	}
}

func TestSaveSettings_PreservesOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	m := NewManager(path, filepath.Join(dir, "backups"), 10)

	// 模拟 Claude Code 自身的 settings.json 初始顺序
	original := `{
  "env": {
    "KEY_FIRST": "1"
  },
  "model": "sonnet",
  "tipsHistory": {
    "some-tip": 99
  }
}
`
	os.WriteFile(path, []byte(original), 0644)

	// 合并新配置
	m.Merge(
		map[string]any{
			"env": map[string]any{"NEW_KEY": "val"},
		},
		nil,
	)

	// 读取输出
	data, _ := os.ReadFile(path)
	result := string(data)

	// 验证 top-level key 顺序：env 应在 model 前面（因为原文件 env 在前）
	envIdx := strings.Index(result, "\"env\"")
	modelIdx := strings.Index(result, "\"model\"")
	tipsIdx := strings.Index(result, "\"tipsHistory\"")
	if envIdx < 0 || modelIdx < 0 || tipsIdx < 0 {
		t.Fatal("missing expected keys in output")
	}
	if envIdx >= modelIdx {
		t.Error("env should appear before model (original order preserved)")
	}
	if modelIdx >= tipsIdx {
		t.Error("model should appear before tipsHistory (original order preserved)")
	}
	// NEW_KEY 应该在 KEY_FIRST 后面（注入的新 key 追加在后）
	firstIdx := strings.Index(result, "\"KEY_FIRST\"")
	newIdx := strings.Index(result, "\"NEW_KEY\"")
	if newIdx < firstIdx {
		t.Error("NEW_KEY should appear after KEY_FIRST (new keys appended)")
	}
}
