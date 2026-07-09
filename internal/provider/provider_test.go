package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestList_Empty(t *testing.T) {
	m := NewManager(t.TempDir())
	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List() len = %d, want 0", len(list))
	}
}

func TestList_WithProviders(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "anthropic.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_API_KEY": "sk-test"},
	})
	writeProvider(t, dir, "bedrock.json", map[string]any{
		"env": map[string]any{"CLAUDE_CODE_USE_BEDROCK": "1"},
	})

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}

	names := map[string]bool{}
	for _, info := range list {
		names[info.Name] = true
		if info.Error != "" {
			t.Errorf("info.Error = %q for %s, want empty", info.Error, info.Name)
		}
	}
	if !names["anthropic"] || !names["bedrock"] {
		t.Errorf("List() names = %v, want anthropic and bedrock", names)
	}
}

func TestList_IgnoresNonJSON(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "anthropic.json", map[string]any{})
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("docs"), 0644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0644)

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List() len = %d, want 1", len(list))
	}
}

func TestList_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "good.json", map[string]any{})
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json{{{"), 0644)

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	for _, info := range list {
		if info.Name == "bad" && info.Error == "" {
			t.Error("bad.json should have Error set")
		}
		if info.Name == "good" && info.Error != "" {
			t.Error("good.json should not have Error set")
		}
	}
}

func TestGet(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	cfg := map[string]any{
		"env":   map[string]any{"ANTHROPIC_API_KEY": "sk-test"},
		"model": "sonnet",
	}
	writeProvider(t, dir, "anthropic.json", cfg)

	got, err := m.Get("anthropic")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(cfg)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("Get() = %s, want %s", gotJSON, wantJSON)
	}
}

func TestGet_NotFound(t *testing.T) {
	m := NewManager(t.TempDir())
	_, err := m.Get("nonexistent")
	if err == nil {
		t.Fatal("Get() expected error for nonexistent provider")
	}
}

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	err := m.Create("custom", "https://api.example.com", "sk-custom")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := m.Get("custom")
	if err != nil {
		t.Fatalf("Get() after Create error = %v", err)
	}

	env, ok := got["env"].(map[string]any)
	if !ok {
		t.Fatal("env field missing or wrong type")
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.example.com" {
		t.Errorf("base_url = %v, want https://api.example.com", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_API_KEY"] != "sk-custom" {
		t.Errorf("api_key = %v, want sk-custom", env["ANTHROPIC_API_KEY"])
	}
}

func TestCreate_EmptyBaseURL(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	err := m.Create("custom", "", "sk-custom")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := m.Get("custom")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	env := got["env"].(map[string]any)
	if _, exists := env["ANTHROPIC_BASE_URL"]; exists {
		t.Error("ANTHROPIC_BASE_URL should be omitted when empty")
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "existing.json", map[string]any{})
	err := m.Create("existing", "", "key")
	if err == nil {
		t.Fatal("Create() expected error for duplicate name")
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "temp.json", map[string]any{})
	if err := m.Delete("temp"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if m.Exists("temp") {
		t.Error("Exists() = true after Delete, want false")
	}
}

func TestDelete_NotFound(t *testing.T) {
	m := NewManager(t.TempDir())
	err := m.Delete("nonexistent")
	if err == nil {
		t.Fatal("Delete() expected error for nonexistent provider")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if m.Exists("any") {
		t.Error("Exists() = true for nonexistent provider")
	}

	writeProvider(t, dir, "any.json", map[string]any{})
	if !m.Exists("any") {
		t.Error("Exists() = false for existing provider")
	}
}

func writeProvider(t *testing.T, dir, filename string, cfg map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0644); err != nil {
		t.Fatal(err)
	}
}
