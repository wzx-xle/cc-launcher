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
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-test"},
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
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-test"},
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

	err := m.CreateWithEnv("custom", map[string]any{
		"ANTHROPIC_BASE_URL":   "https://api.example.com",
		"ANTHROPIC_AUTH_TOKEN": "sk-custom",
	})
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
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-custom" {
		t.Errorf("api_key = %v, want sk-custom", env["ANTHROPIC_AUTH_TOKEN"])
	}
}

func TestCreate_MissingAuthToken(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// env 中没有 ANTHROPIC_AUTH_TOKEN 必须失败
	err := m.CreateWithEnv("custom", map[string]any{
		"ANTHROPIC_BASE_URL": "https://api.example.com",
	})
	if err == nil {
		t.Fatal("Create() expected error when ANTHROPIC_AUTH_TOKEN missing")
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "existing.json", map[string]any{})
	err := m.CreateWithEnv("existing", map[string]any{
		"ANTHROPIC_AUTH_TOKEN": "key",
	})
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

// writeProviderRaw 直接写原始字节（用于构造非 JSON 兼容字段如 order="ten"）。
func writeProviderRaw(t *testing.T, dir, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// ─── provider-list-rendering §note / order 解析与排序 ───

// TestList_ParsesNoteAndOrder 覆盖 spec/provider-list-rendering §"Provider
// config supports optional note and order fields" Scenarios "Config without
// note or order loads successfully" / "Config with note loads into Info.Note"
// / "Config with order loads into Info.Order"——三个 provider JSON
// （带/不带 note、order 不同值）各自正确填充 Info。
func TestList_ParsesNoteAndOrder(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "alpha.json", map[string]any{
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-a"},
		"order": 5,
	})
	writeProvider(t, dir, "bravo.json", map[string]any{
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-b"},
		"note":  "国内 / 按量计费",
		"order": 10,
	})
	writeProvider(t, dir, "charlie.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-c"},
		// 无 note / order
	})

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List() len = %d, want 3", len(list))
	}

	byName := map[string]Info{}
	for _, info := range list {
		byName[info.Name] = info
	}

	if got := byName["alpha"]; got.Note != "" || got.Order != 5 {
		t.Errorf("alpha = Note=%q Order=%d, want Note=%q Order=5", got.Note, got.Order, "")
	}
	if got := byName["bravo"]; got.Note != "国内 / 按量计费" || got.Order != 10 {
		t.Errorf("bravo = Note=%q Order=%d, want Note=%q Order=10", got.Note, got.Order, "国内 / 按量计费")
	}
	if got := byName["charlie"]; got.Note != "" || got.Order != 0 {
		t.Errorf("charlie = Note=%q Order=%d, want Note=%q Order=0 (defaults)", got.Note, got.Order, "")
	}
}

// TestList_SortedByOrderThenName 覆盖 spec/provider-list-rendering §"Manager
// .List() returns providers sorted by Order then Name" Scenario "Three
// providers with distinct order"——order=20/5/10 排成 5/10/20。
func TestList_SortedByOrderThenName(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "alpha.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-a"}, "order": 20,
	})
	writeProvider(t, dir, "bravo.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-b"}, "order": 5,
	})
	writeProvider(t, dir, "charlie.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-c"}, "order": 10,
	})

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	got := []string{list[0].Name, list[1].Name, list[2].Name}
	want := []string{"bravo", "charlie", "alpha"}
	if !slicesEqual(got, want) {
		t.Errorf("List() order = %v, want %v", got, want)
	}
}

// TestList_SameOrderFallsBackToName 覆盖 spec Scenario "Providers with same
// order fall back to name"——两个 provider 同 order=5 时按 Name 字典序。
func TestList_SameOrderFallsBackToName(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "bravo.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-b"}, "order": 5,
	})
	writeProvider(t, dir, "alpha.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-a"}, "order": 5,
	})

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	got := []string{list[0].Name, list[1].Name}
	want := []string{"alpha", "bravo"}
	if !slicesEqual(got, want) {
		t.Errorf("List() order = %v, want %v", got, want)
	}
}

// TestList_NoOrderSortsByName 覆盖 spec Scenario "Providers without order
// sort by name"——无 order 字段（默认 0）按 Name 字典序。
func TestList_NoOrderSortsByName(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	writeProvider(t, dir, "charlie.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-c"},
	})
	writeProvider(t, dir, "alpha.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-a"},
	})

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	got := []string{list[0].Name, list[1].Name}
	want := []string{"alpha", "charlie"}
	if !slicesEqual(got, want) {
		t.Errorf("List() order = %v, want %v", got, want)
	}
}

// TestList_NonNumericOrderMarksOnlyThatProviderAsError 覆盖 spec Scenario
// "Config with non-numeric order marks only that provider as Error"——
// order="ten" 的 provider 单独 Error，其它不受影响。
func TestList_NonNumericOrderMarksOnlyThatProviderAsError(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// 手写：order 字段是字符串，json.Unmarshal configFile 会失败
	writeProviderRaw(t, dir, "alpha.json", []byte(`{
  "env": {"ANTHROPIC_AUTH_TOKEN": "sk-a"},
  "order": "ten"
}`))
	writeProvider(t, dir, "bravo.json", map[string]any{
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-b"},
		"note":  "国内",
		"order": 7,
	})

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}

	byName := map[string]Info{}
	for _, info := range list {
		byName[info.Name] = info
	}

	if got := byName["alpha"]; got.Error == "" {
		t.Errorf("alpha (order=\"ten\") should have Error set, got empty; Note=%q Order=%d", got.Note, got.Order)
	}
	if got := byName["bravo"]; got.Error != "" {
		t.Errorf("bravo should have no Error, got %q", got.Error)
	} else if got.Note != "国内" || got.Order != 7 {
		t.Errorf("bravo Note=%q Order=%d, want Note=%q Order=7", got.Note, got.Order, "国内")
	}
}

// TestList_ErroredProviderIncludedAndSorted 覆盖 spec Scenario "Errored
// providers are included and sorted normally"——无效 JSON + 有效 provider
// 同时返回；order 都为 0，按 Name 字典序，alpha 在前 bravo 在后。
func TestList_ErroredProviderIncludedAndSorted(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// bravo 是无效 JSON（Order 默认为 0；Error != ""）
	writeProviderRaw(t, dir, "bravo.json", []byte("not json{{{"))
	// alpha 是有效 JSON，Order 默认为 0
	writeProvider(t, dir, "alpha.json", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-a"},
	})

	list, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}

	if list[0].Name != "alpha" {
		t.Errorf("list[0] = %q, want alpha (alpha < bravo by name)", list[0].Name)
	}
	if list[1].Name != "bravo" || list[1].Error == "" {
		t.Errorf("list[1] = %q (Error=%q), want bravo with Error set", list[1].Name, list[1].Error)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestList_FlagsInvalidPassthroughConfig 断言：List() 把"已知键类型写错"的
// provider 标出来（ValidationErrors），并统计顶层透传键（PassthroughKeys）。
//
// 提前标出来的意义：Claude Code 会拒绝整个 settings 文件（provider 的凭证
// 跟着一起消失），用户应该在**选择之前**就看到这个问题，而不是选完 provider
// 才在启动那一步报错。
func TestList_FlagsInvalidPassthroughConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, cfg string) {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(cfg), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("broken", `{"model": {"name": "x"}, "env": {"A": "B"}}`)
	write("good", `{"model": "m", "permissions": {"allow": ["Bash(ls:*)"]}, "env": {"A": "B"}}`)

	m := NewManager(dir)
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Info{}
	for _, info := range list {
		byName[info.Name] = info
	}

	broken := byName["broken"]
	if len(broken.ValidationErrors) == 0 {
		t.Error("类型写错的 provider 应被标出校验错误")
	}
	if broken.Error != "" {
		t.Errorf("类型写错不是 JSON 错误，Error 应为空: %q", broken.Error)
	}

	good := byName["good"]
	if len(good.ValidationErrors) != 0 {
		t.Errorf("合法 provider 不该有校验错误: %+v", good.ValidationErrors)
	}
	// model + permissions 两个顶层键（env 不算，note/order 不算）
	if good.PassthroughKeys != 2 {
		t.Errorf("PassthroughKeys = %d, want 2", good.PassthroughKeys)
	}
}
