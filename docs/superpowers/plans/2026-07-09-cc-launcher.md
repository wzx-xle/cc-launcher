# Claude Code Launcher 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建 `cc` 终端启动器，在启动 Claude Code 时提供 AI 提供商配置切换和权限模式选择的 TUI 界面。

**Architecture:** Go + bubbletea TUI 框架，通过 CLI 子命令管理提供商和权限模式配置。配置合并采用「快照比对」机制——切换提供商时只清理未被用户手动修改的字段，保留用户自定义修改。

**Tech Stack:** Go 1.22+, bubbletea (TUI), bubbles (list 组件), lipgloss (样式), 标准库 os/exec (启动 Claude Code)

## Global Constraints

- 只操作 `~/.claude/settings.json`，不碰 `~/.claude.json`
- 提供商配置文件结构与 Claude Code settings.json 结构一致（支持 env、model、modelOverrides 等所有字段）
- 切换提供商时：值比对清理，用户手动修改的配置不丢失
- 每步操作可取消（Esc）
- `exec` 替换进程，不残留后台进程
- 写前备份 settings.json，保留最近 10 个

---

### Task 1: 项目脚手架

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `cmd/cc-launcher/main.go` (stub)
- Create: `internal/state/state.go` (stub)
- Create: `internal/provider/provider.go` (stub)
- Create: `internal/config/config.go` (stub)
- Create: `internal/tui/tui.go` (stub)
- Create: `internal/launcher/launcher.go` (stub)

**Interfaces:**
- Produces: 模块路径 `cc-launcher`，所有包可直接引用

- [ ] **Step 1: 初始化 Go module**

```bash
cd /home/zxwang/workspace/golang/cc-launcher
go mod init cc-launcher
```

Expected: 创建 `go.mod`，module 名为 `cc-launcher`，go version 为当前安装版本。

- [ ] **Step 2: 创建 .gitignore**

```bash
cat > .gitignore << 'EOF'
/cc-launcher
/.claude/
*.swp
*.swo
*~
EOF
```

- [ ] **Step 3: 创建目录结构和 stub 文件**

```bash
mkdir -p cmd/cc-launcher
mkdir -p internal/{state,provider,config,tui,launcher}
```

- [ ] **Step 4: 编写入口 stub**

写入 `cmd/cc-launcher/main.go`：

```go
package main

import "fmt"

func main() {
	fmt.Println("cc-launcher")
}
```

- [ ] **Step 5: 编写各包 stub 文件**

写入 `internal/state/state.go`：

```go
package state
```

写入 `internal/provider/provider.go`：

```go
package provider
```

写入 `internal/config/config.go`：

```go
package config
```

写入 `internal/tui/tui.go`：

```go
package tui
```

写入 `internal/launcher/launcher.go`：

```go
package launcher
```

- [ ] **Step 6: 验证编译**

```bash
go build ./cmd/cc-launcher/
```

Expected: 编译成功，生成 `cc-launcher` 二进制。

- [ ] **Step 7: 安装依赖**

```bash
go get github.com/charmbracelet/bubbletea github.com/charmbracelet/bubbles github.com/charmbracelet/lipgloss
```

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore: init Go module and project scaffold"
```

---

### Task 2: 状态管理 (state.Manager)

**Files:**
- Create: `internal/state/state.go`
- Create: `internal/state/state_test.go`

**Interfaces:**
- Produces:
  - `type State struct { ActiveProvider string; InjectedSnapshot map[string]any; LastPermissionModes map[string]string }`
  - `type Manager struct { ... }`
  - `func NewManager(baseDir string) *Manager`
  - `func (m *Manager) Load() (*State, error)` — 文件不存在返回空 State，JSON 损坏返回 error
  - `func (m *Manager) Save(s *State) error` — 自动创建目录
  - `func (m *Manager) GetActiveProvider() string`
  - `func (m *Manager) SetActiveProvider(name string) error`
  - `func (m *Manager) GetInjectedSnapshot() map[string]any`
  - `func (m *Manager) SetInjectedSnapshot(snapshot map[string]any) error`
  - `func (m *Manager) GetPermissionMode(workDir string) (string, bool)`
  - `func (m *Manager) SetPermissionMode(workDir, mode string) error`

- [ ] **Step 1: 编写 state.Manager 测试**

写入 `internal/state/state_test.go`：

```go
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
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

	// 验证文件存在
	statePath := filepath.Join(dir, "state.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("state.json was not created")
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
	statePath := filepath.Join(dir, "state.json")
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

	// 初始为空
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

	// 无记录
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

	// 从文件重新加载验证持久化
	m2 := NewManager(m.baseDir)
	loaded := m2.GetInjectedSnapshot()
	loadedJSON, _ := json.Marshal(loaded)
	snapJSON, _ := json.Marshal(snap)
	if string(loadedJSON) != string(snapJSON) {
		t.Errorf("snapshot mismatch: got %s, want %s", loadedJSON, snapJSON)
	}
}
```

- [ ] **Step 2: 运行测试，验证全部失败**

```bash
go test ./internal/state/ -v
```

Expected: 所有测试 FAIL，因为 `state.go` 尚未实现。

- [ ] **Step 3: 实现 state.Manager**

写入 `internal/state/state.go`：

```go
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State 记录 launcher 运行时状态。
type State struct {
	ActiveProvider      string                 `json:"active_provider"`
	InjectedSnapshot    map[string]any         `json:"injected_snapshot"`
	LastPermissionModes map[string]string      `json:"last_permission_modes"`
}

// Manager 管理 state.json 的读写。
type Manager struct {
	baseDir string
}

// NewManager 创建 Manager，baseDir 为 ~/.cc-launcher 目录。
func NewManager(baseDir string) *Manager {
	return &Manager{baseDir: baseDir}
}

func (m *Manager) statePath() string {
	return filepath.Join(m.baseDir, "state.json")
}

// Load 加载 state.json。文件不存在时返回空 State，JSON 损坏时返回 error。
func (m *Manager) Load() (*State, error) {
	data, err := os.ReadFile(m.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &State{LastPermissionModes: map[string]string{}}, nil
		}
		return nil, err
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.LastPermissionModes == nil {
		s.LastPermissionModes = map[string]string{}
	}
	return &s, nil
}

// Save 将状态写入 state.json，自动创建目录。
func (m *Manager) Save(s *State) error {
	if err := os.MkdirAll(m.baseDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.statePath(), data, 0644)
}

// GetActiveProvider 返回当前激活的提供商名称。
func (m *Manager) GetActiveProvider() string {
	s, err := m.Load()
	if err != nil {
		return ""
	}
	return s.ActiveProvider
}

// SetActiveProvider 设置当前激活的提供商并持久化。
func (m *Manager) SetActiveProvider(name string) error {
	s, err := m.Load()
	if err != nil {
		return err
	}
	s.ActiveProvider = name
	return m.Save(s)
}

// GetInjectedSnapshot 返回上次注入的快照。
func (m *Manager) GetInjectedSnapshot() map[string]any {
	s, err := m.Load()
	if err != nil {
		return nil
	}
	return s.InjectedSnapshot
}

// SetInjectedSnapshot 更新注入快照并持久化。
func (m *Manager) SetInjectedSnapshot(snapshot map[string]any) error {
	s, err := m.Load()
	if err != nil {
		return err
	}
	s.InjectedSnapshot = snapshot
	return m.Save(s)
}

// GetPermissionMode 返回指定工作目录上次使用的权限模式。
func (m *Manager) GetPermissionMode(workDir string) (string, bool) {
	s, err := m.Load()
	if err != nil {
		return "", false
	}
	mode, ok := s.LastPermissionModes[workDir]
	return mode, ok
}

// SetPermissionMode 设置指定工作目录的权限模式并持久化。
func (m *Manager) SetPermissionMode(workDir, mode string) error {
	s, err := m.Load()
	if err != nil {
		return err
	}
	s.LastPermissionModes[workDir] = mode
	return m.Save(s)
}
```

- [ ] **Step 4: 运行测试验证通过**

```bash
go test ./internal/state/ -v
```

Expected: 所有测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/state/
git commit -m "feat: add state management for state.json"
```

---

### Task 3: 提供商管理 (provider.Manager)

**Files:**
- Create: `internal/provider/provider.go`
- Create: `internal/provider/provider_test.go`

**Interfaces:**
- Produces:
  - `type Info struct { Name string; IsActive bool; Error string }`
  - `type Manager struct { ... }`
  - `func NewManager(providersDir string) *Manager`
  - `func (m *Manager) List() ([]Info, error)` — 扫描目录下所有 .json 文件
  - `func (m *Manager) Get(name string) (map[string]any, error)` — 读取并解析单个提供商配置
  - `func (m *Manager) Create(name, baseURL, apiKey string) error` — 创建新提供商文件
  - `func (m *Manager) Delete(name string) error` — 删除提供商文件
  - `func (m *Manager) Exists(name string) bool`

- [ ] **Step 1: 编写 provider.Manager 测试**

写入 `internal/provider/provider_test.go`：

```go
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

	// 创建两个提供商文件
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

// helpers

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
```

- [ ] **Step 2: 运行测试，验证全部失败**

```bash
go test ./internal/provider/ -v
```

Expected: 所有测试 FAIL（编译错误，类型未定义）。

- [ ] **Step 3: 实现 provider.Manager**

写入 `internal/provider/provider.go`：

```go
package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Info 提供商概要信息。
type Info struct {
	Name  string // 文件名去掉 .json
	Error string // JSON 解析失败时的错误信息
}

// Manager 管理 ~/.cc-launcher/providers/ 目录。
type Manager struct {
	providersDir string
}

// NewManager 创建 Manager。
func NewManager(providersDir string) *Manager {
	return &Manager{providersDir: providersDir}
}

// List 列出所有提供商。JSON 解析失败的在 Info.Error 中标记，不阻塞其他文件。
func (m *Manager) List() ([]Info, error) {
	if err := os.MkdirAll(m.providersDir, 0755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(m.providersDir)
	if err != nil {
		return nil, err
	}

	var result []Info
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		info := Info{Name: strings.TrimSuffix(name, ".json")}

		data, err := os.ReadFile(filepath.Join(m.providersDir, name))
		if err != nil {
			info.Error = err.Error()
		} else {
			var v any
			if err := json.Unmarshal(data, &v); err != nil {
				info.Error = fmt.Sprintf("JSON 解析失败: %s", err.Error())
			}
		}
		result = append(result, info)
	}
	return result, nil
}

// Get 读取并解析指定提供商的完整配置。
func (m *Manager) Get(name string) (map[string]any, error) {
	path := filepath.Join(m.providersDir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("提供商 %q 不存在", name)
		}
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("提供商 %q 配置解析失败: %w", name, err)
	}
	return cfg, nil
}

// Create 创建新的提供商配置文件。baseURL 为空时不写入 ANTHROPIC_BASE_URL。
func (m *Manager) Create(name, baseURL, apiKey string) error {
	if name == "" {
		return fmt.Errorf("提供商名称不能为空")
	}
	if apiKey == "" {
		return fmt.Errorf("api_key 不能为空")
	}
	if m.Exists(name) {
		return fmt.Errorf("提供商 %q 已存在", name)
	}

	env := map[string]any{
		"ANTHROPIC_API_KEY": apiKey,
	}
	if baseURL != "" {
		env["ANTHROPIC_BASE_URL"] = baseURL
	}

	cfg := map[string]any{"env": env}

	if err := os.MkdirAll(m.providersDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.providersDir, name+".json"), data, 0644)
}

// Delete 删除指定提供商配置文件。
func (m *Manager) Delete(name string) error {
	path := filepath.Join(m.providersDir, name+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("提供商 %q 不存在", name)
	}
	return os.Remove(path)
}

// Exists 检查提供商配置文件是否存在。
func (m *Manager) Exists(name string) bool {
	path := filepath.Join(m.providersDir, name+".json")
	_, err := os.Stat(path)
	return err == nil
}
```

- [ ] **Step 4: 运行测试验证通过**

```bash
go test ./internal/provider/ -v
```

Expected: 所有测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/provider/
git commit -m "feat: add provider CRUD management"
```

---

### Task 4: 配置合并引擎 (config.Manager)

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Manager struct { ... }`
  - `func NewManager(settingsPath, backupDir string, maxBackups int) *Manager`
  - `func (m *Manager) LoadSettings() (map[string]any, error)` — 文件不存在返回空 map
  - `func (m *Manager) SaveSettings(settings map[string]any) error` — 自动创建父目录
  - `func (m *Manager) Backup() error` — 备份 settings.json 到 backupDir，自动轮转
  - `func (m *Manager) Merge(providerCfg, oldSnapshot map[string]any) error` — 备份 → 清理快照 → 注入新配置
  - `func cleanSnapshot(settings, snapshot map[string]any)` — 递归比对清理
  - `func deepMerge(dst, src map[string]any)` — 递归合并

- [ ] **Step 1: 编写 config.Manager 测试**

写入 `internal/config/config_test.go`：

```go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

	// 写入 settings.json
	settings := map[string]any{"model": "sonnet"}
	if err := m.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	// 执行备份
	if err := m.Backup(); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}

	// 验证备份文件存在
	backups, err := filepath.Glob(filepath.Join(backupDir, "settings-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups count = %d, want 1", len(backups))
	}

	// 验证备份内容一致
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
	m := NewManager(settingsPath, backupDir, 2) // 只保留 2 个

	// 创建 3 次备份
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

	// 用户改过的都应该保留
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

	env := settings["env"].(map[string]any)
	if _, ok := env["EXISTING"]; ok {
		t.Error("EXISTING should be removed (unchanged)")
	}
	// GONE not in settings → skip (no panic)
	// removedSection not in settings → skip (no panic)
}

func TestDeepMerge(t *testing.T) {
	dst := map[string]any{
		"env":   map[string]any{"EXISTING": "old"},
		"model": "haiku",
	}
	src := map[string]any{
		"env":   map[string]any{"NEW_KEY": "new", "EXISTING": "overwritten"},
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
	// model 不在 src 中，应保留原值
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
	if nested["a"] != float64(1) { // JSON numbers → float64
		t.Error("a should be kept from dst")
	}
	if nested["b"] != float64(3) {
		t.Error("b should be overwritten by src")
	}
	if nested["c"] != float64(4) {
		t.Error("c should be added from src")
	}
}

func TestMerge_FullFlow(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	m := NewManager(settingsPath, backupDir, 10)

	// 模拟已有 settings.json
	existing := map[string]any{
		"env": map[string]any{
			"AWS_REGION":                "us-east-1", // 上次注入的
			"CLAUDE_CODE_USE_BEDROCK":   "1",          // 上次注入的
			"USER_CUSTOM":               "my-value",   // 用户手动加的
		},
		"model": "sonnet", // 上次注入的
	}
	m.SaveSettings(existing)

	// 旧快照
	oldSnapshot := map[string]any{
		"env": map[string]any{
			"AWS_REGION":              "us-east-1",
			"CLAUDE_CODE_USE_BEDROCK": "1",
		},
		"model": "sonnet",
	}

	// 新提供商配置
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

	// 旧注入字段应被清理
	env := result["env"].(map[string]any)
	if _, ok := env["AWS_REGION"]; ok {
		t.Error("AWS_REGION should be cleaned")
	}
	if _, ok := env["CLAUDE_CODE_USE_BEDROCK"]; ok {
		t.Error("CLAUDE_CODE_USE_BEDROCK should be cleaned")
	}

	// 用户自定义字段应保留
	if env["USER_CUSTOM"] != "my-value" {
		t.Error("USER_CUSTOM should be kept")
	}

	// 新提供商字段应注入
	if env["ANTHROPIC_API_KEY"] != "sk-new" {
		t.Error("ANTHROPIC_API_KEY should be injected")
	}
	if result["model"] != "opus" {
		t.Error("model should be overwritten by new provider")
	}

	// 备份应存在
	backups, _ := filepath.Glob(filepath.Join(backupDir, "settings-*.json"))
	if len(backups) != 1 {
		t.Errorf("expected 1 backup, got %d", len(backups))
	}
}

// helpers

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func init() {
	// suppress unused import warning in test if reflect not used elsewhere
	_ = reflect.DeepEqual
}
```

- [ ] **Step 2: 运行测试，验证全部失败**

```bash
go test ./internal/config/ -v
```

Expected: 所有测试 FAIL。

- [ ] **Step 3: 实现 config.Manager**

写入 `internal/config/config.go`：

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Manager 管理 ~/.claude/settings.json 的读写与合并。
type Manager struct {
	settingsPath string
	backupDir    string
	maxBackups   int
}

// NewManager 创建 Manager。
func NewManager(settingsPath, backupDir string, maxBackups int) *Manager {
	return &Manager{
		settingsPath: settingsPath,
		backupDir:    backupDir,
		maxBackups:   maxBackups,
	}
}

// LoadSettings 读取 settings.json。文件不存在时返回空 map。
func (m *Manager) LoadSettings() (map[string]any, error) {
	data, err := os.ReadFile(m.settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("settings.json 解析失败: %w", err)
	}
	return settings, nil
}

// SaveSettings 将配置写入 settings.json。
func (m *Manager) SaveSettings(settings map[string]any) error {
	dir := filepath.Dir(m.settingsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.settingsPath, data, 0644)
}

// Backup 备份当前 settings.json 到 backupDir，自动轮转保留最近 maxBackups 个。
func (m *Manager) Backup() error {
	if _, err := os.Stat(m.settingsPath); os.IsNotExist(err) {
		return nil // 没有 settings.json，无需备份
	}

	if err := os.MkdirAll(m.backupDir, 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(m.settingsPath)
	if err != nil {
		return fmt.Errorf("读取 settings.json 备份失败: %w", err)
	}

	timestamp := time.Now().Format("2006-01-02T15-04-05")
	backupPath := filepath.Join(m.backupDir, "settings-"+timestamp+".json")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("写入备份失败: %w", err)
	}

	// 轮转：保留最近 maxBackups 个
	return m.rotateBackups()
}

func (m *Manager) rotateBackups() error {
	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		return err
	}

	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "settings-") && strings.HasSuffix(e.Name(), ".json") {
			backups = append(backups, e.Name())
		}
	}

	if len(backups) <= m.maxBackups {
		return nil
	}

	// 按名称排序（时间戳格式自带字典序），删除最旧的
	sort.Strings(backups)
	for i := 0; i < len(backups)-m.maxBackups; i++ {
		os.Remove(filepath.Join(m.backupDir, backups[i]))
	}
	return nil
}

// Merge 执行配置合并：备份 → 清理旧快照 → 注入新配置。
func (m *Manager) Merge(providerCfg, oldSnapshot map[string]any) error {
	// 1. 备份
	if err := m.Backup(); err != nil {
		return err
	}

	// 2. 加载当前配置
	settings, err := m.LoadSettings()
	if err != nil {
		return err
	}

	// 3. 清理旧快照中未修改的字段
	if oldSnapshot != nil {
		cleanSnapshot(settings, oldSnapshot)
	}

	// 4. 注入新提供商配置
	deepMerge(settings, providerCfg)

	// 5. 写回
	return m.SaveSettings(settings)
}

// cleanSnapshot 递归遍历 snapshot，删除 settings 中值与 snapshot 相同的字段。
// 当前值 == 快照值 → 删除（用户未修改）；当前值 != 快照值 → 保留（用户改过）。
func cleanSnapshot(settings, snapshot map[string]any) {
	for key, snapVal := range snapshot {
		curVal, exists := settings[key]
		if !exists {
			continue
		}

		if reflect.DeepEqual(curVal, snapVal) {
			delete(settings, key)
			continue
		}

		// 值不同时，检查是否为嵌套 map 需要递归处理
		snapMap, snapIsMap := snapVal.(map[string]any)
		curMap, curIsMap := curVal.(map[string]any)
		if snapIsMap && curIsMap {
			cleanSnapshot(curMap, snapMap)
			if len(curMap) == 0 {
				delete(settings, key)
			}
		}
	}
}

// deepMerge 将 src 递归合并到 dst。src 中的值覆盖 dst 同名 key，嵌套 map 递归合并。
func deepMerge(dst, src map[string]any) {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			srcMap, srcIsMap := srcVal.(map[string]any)
			dstMap, dstIsMap := dstVal.(map[string]any)
			if srcIsMap && dstIsMap {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
}
```

- [ ] **Step 4: 运行测试验证通过**

```bash
go test ./internal/config/ -v
```

Expected: 所有测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add config merge engine with snapshot cleanup and backup"
```

---

### Task 5: 启动器 (launcher.Launcher)

**Files:**
- Create: `internal/launcher/launcher.go`

**Interfaces:**
- Produces:
  - `func Launch(permissionMode string) error` — 用 `--permission-mode` 参数 exec 启动 claude，失败返回 error

- [ ] **Step 1: 实现 launcher.Launch**

写入 `internal/launcher/launcher.go`：

```go
package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Launch 用 exec 替换当前进程启动 Claude Code。
// permissionMode 直接传给 claude --permission-mode 参数。
func Launch(permissionMode string) error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("未找到 claude 命令，请确认已安装 Claude Code")
	}

	args := []string{"claude"}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}

	// exec: 用 claude 进程替换当前进程
	argv := append([]string{claudePath}, args[1:]...)
	return syscall.Exec(claudePath, argv, os.Environ())
}
```

- [ ] **Step 2: 验证编译**

```bash
go build ./internal/launcher/
```

Expected: 编译成功。

- [ ] **Step 3: Commit**

```bash
git add internal/launcher/
git commit -m "feat: add Claude Code launcher with exec"
```

---

### Task 6: CLI 子命令 (cc provider / cc mode)

**Files:**
- Create: `internal/cli/cli.go`
- Create: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes:
  - `provider.Manager` (List, Get, Create, Delete, Exists)
  - `state.Manager` (GetActiveProvider, SetActiveProvider, GetPermissionMode, SetPermissionMode)
- Produces:
  - `type CLI struct { ... }`
  - `func New(providerMgr *provider.Manager, stateMgr *state.Manager) *CLI`
  - `func (c *CLI) Run(args []string) error` — 路由到 provider/mode 子命令

- [ ] **Step 1: 编写 CLI 测试**

写入 `internal/cli/cli_test.go`：

```go
package cli

import (
	"strings"
	"testing"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
)

func setupCLI(t *testing.T) (*CLI, *provider.Manager, *state.Manager) {
	t.Helper()
	providersDir := t.TempDir()
	stateDir := t.TempDir()

	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)

	// 预置一个提供商
	pm.Create("anthropic", "", "sk-test")

	return New(pm, sm), pm, sm
}

func captureOutput(fn func() error) (string, error) {
	// 测试不捕获输出，直接验证状态变更
	return "", fn()
}

func TestProviderList(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "--list"})
	if err != nil {
		t.Fatalf("provider --list error = %v", err)
	}
}

func TestProviderSwitch(t *testing.T) {
	c, pm, sm := setupCLI(t)

	// 创建第二个提供商
	pm.Create("bedrock", "", "sk-bedrock")

	err := c.Run([]string{"provider", "bedrock"})
	if err != nil {
		t.Fatalf("provider bedrock error = %v", err)
	}

	if got := sm.GetActiveProvider(); got != "bedrock" {
		t.Errorf("active provider = %q, want bedrock", got)
	}
}

func TestProviderSwitch_NotFound(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestProviderNew(t *testing.T) {
	// --new 需要交互输入，通过 stdin 模拟
	// 此处跳过测试（需要 stdin），在集成测试中覆盖
}

func TestProviderDelete(t *testing.T) {
	c, pm, _ := setupCLI(t)

	pm.Create("temp", "", "key")
	err := c.Run([]string{"provider", "--delete", "temp"})
	if err != nil {
		t.Fatalf("provider --delete error = %v", err)
	}
	if pm.Exists("temp") {
		t.Error("provider temp should be deleted")
	}
}

func TestProviderDelete_Active(t *testing.T) {
	c, pm, sm := setupCLI(t)

	pm.Create("active-prov", "", "key")
	sm.SetActiveProvider("active-prov")

	// 删除激活的提供商（非交互模式不确认 → 应拒绝）
	err := c.Run([]string{"provider", "--delete", "active-prov"})
	if err == nil {
		t.Fatal("expected error when deleting active provider")
	}
	if !pm.Exists("active-prov") {
		t.Error("active provider should not be deleted without confirmation")
	}
}

func TestProviderDelete_NotFound(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "--delete", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestModeList(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"mode"})
	if err != nil {
		t.Fatalf("mode error = %v", err)
	}
}

func TestModeSet(t *testing.T) {
	c, _, sm := setupCLI(t)

	err := c.Run([]string{"mode", "plan"})
	if err != nil {
		t.Fatalf("mode plan error = %v", err)
	}

	mode, ok := sm.GetPermissionMode(".")
	if !ok || mode != "plan" {
		t.Errorf("permission mode = (%q, %v), want (plan, true)", mode, ok)
	}
}

func TestModeSet_Invalid(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"mode", "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "合法值") {
		t.Errorf("error should mention valid values, got: %v", err)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"unknown"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}
```

- [ ] **Step 2: 运行测试，验证全部失败**

```bash
go test ./internal/cli/ -v
```

Expected: 编译失败（包不存在）。

- [ ] **Step 3: 实现 CLI 路由和子命令**

写入 `internal/cli/cli.go`：

```go
package cli

import (
	"fmt"
	"os"
	"strings"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
)

// 合法的权限模式列表。
var validModes = []string{"manual", "acceptEdits", "auto", "bypassPermissions", "dontAsk", "plan"}

// CLI 处理 cc 子命令路由。
type CLI struct {
	providers *provider.Manager
	state     *state.Manager
}

// New 创建 CLI 路由器。
func New(pm *provider.Manager, sm *state.Manager) *CLI {
	return &CLI{providers: pm, state: sm}
}

// Run 根据 args 路由到对应子命令。args[0] 应为子命令名（"provider"、"mode"）。
func (c *CLI) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("缺少子命令")
	}

	switch args[0] {
	case "provider":
		return c.runProvider(args[1:])
	case "mode":
		return c.runMode(args[1:])
	default:
		return fmt.Errorf("未知子命令: %s，可用: provider, mode", args[0])
	}
}

func (c *CLI) runProvider(args []string) error {
	// 无参数 → 列出
	if len(args) == 0 {
		return c.listProviders()
	}

	switch args[0] {
	case "--list":
		return c.listProviders()
	case "--new":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		return c.newProvider(name)
	case "--delete":
		if len(args) < 2 {
			return fmt.Errorf("用法: cc provider --delete <name>")
		}
		return c.deleteProvider(args[1])
	default:
		// 位置参数 → 切换提供商
		if strings.HasPrefix(args[0], "-") {
			return fmt.Errorf("未知选项: %s", args[0])
		}
		return c.switchProvider(args[0])
	}
}

func (c *CLI) runMode(args []string) error {
	if len(args) == 0 {
		return c.listModes()
	}

	mode := args[0]
	if strings.HasPrefix(mode, "-") {
		return fmt.Errorf("未知选项: %s", mode)
	}

	// 验证合法性
	for _, m := range validModes {
		if m == mode {
			// 获取当前目录
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("无法获取当前目录: %w", err)
			}
			if err := c.state.SetPermissionMode(wd, mode); err != nil {
				return err
			}
			fmt.Printf("当前目录权限模式已设为 %s\n", mode)
			return nil
		}
	}
	return fmt.Errorf("无效的权限模式 %q，合法值: %s", mode, strings.Join(validModes, ", "))
}

func (c *CLI) listProviders() error {
	list, err := c.providers.List()
	if err != nil {
		return err
	}

	active := c.state.GetActiveProvider()
	if len(list) == 0 {
		fmt.Println("暂无提供商配置")
		return nil
	}

	for _, info := range list {
		marker := "  "
		if info.Name == active {
			marker = "▶ "
		}
		if info.Error != "" {
			fmt.Printf("⚠️  %s — %s\n", info.Name, info.Error)
		} else {
			fmt.Printf("%s%s\n", marker, info.Name)
		}
	}
	return nil
}

func (c *CLI) switchProvider(name string) error {
	if !c.providers.Exists(name) {
		return fmt.Errorf("提供商 %q 不存在", name)
	}
	if err := c.state.SetActiveProvider(name); err != nil {
		return err
	}
	fmt.Printf("已切换到 %s\n", name)
	return nil
}

func (c *CLI) newProvider(name string) error {
	scanner := &inputScanner{}

	if name == "" {
		fmt.Print("提供商名称: ")
		n, ok := scanner.read()
		if !ok {
			return fmt.Errorf("已取消")
		}
		name = strings.TrimSpace(n)
		if name == "" {
			return fmt.Errorf("提供商名称不能为空")
		}
	}

	if c.providers.Exists(name) {
		return fmt.Errorf("提供商 %q 已存在", name)
	}

	fmt.Print("base_url: ")
	baseURL, _ := scanner.read()
	baseURL = strings.TrimSpace(baseURL)

	fmt.Print("api_key: ")
	apiKey, ok := scanner.read()
	if !ok {
		return fmt.Errorf("已取消")
	}
	apiKey = strings.TrimSpace(apiKey)

	if err := c.providers.Create(name, baseURL, apiKey); err != nil {
		return err
	}

	fmt.Printf("已创建提供商 %s\n", name)
	return nil
}

func (c *CLI) deleteProvider(name string) error {
	if !c.providers.Exists(name) {
		return fmt.Errorf("提供商 %q 不存在", name)
	}

	// 检查是否为当前激活的提供商
	if c.state.GetActiveProvider() == name {
		fmt.Printf("⚠️  %s 是当前激活的提供商，确定删除？[y/N] ", name)
		scanner := &inputScanner{}
		answer, _ := scanner.read()
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			return fmt.Errorf("已取消")
		}
	}

	if err := c.providers.Delete(name); err != nil {
		return err
	}
	fmt.Printf("已删除提供商 %s\n", name)
	return nil
}

func (c *CLI) listModes() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	currentMode, _ := c.state.GetPermissionMode(wd)

	for _, mode := range validModes {
		marker := "  "
		if mode == currentMode {
			marker = "▶ "
		}
		fmt.Printf("%s%s\n", marker, mode)
	}
	return nil
}

// inputScanner 封装 fmt.Scan 用于交互式输入。
type inputScanner struct{}

func (s *inputScanner) read() (string, bool) {
	var input string
	n, err := fmt.Scanln(&input)
	if err != nil {
		if n == 0 {
			return "", false // EOF
		}
	}
	return input, true
}
```

- [ ] **Step 4: 运行测试验证通过**

```bash
go test ./internal/cli/ -v
```

Expected: 包含 `fmt.Scanln` 的测试（`TestProviderNew`）跳过，其余 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat: add CLI subcommands for provider and mode"
```

---

### Task 7: TUI (bubbletea 提供商 + 模式选择)

**Files:**
- Create: `internal/tui/tui.go`

**Interfaces:**
- Consumes:
  - `provider.Info` (Name, Error)
- Produces:
  - `func Run(providers []provider.Info, defaultProvider, defaultMode string) (selectedProvider, selectedMode string, err error)`
  - 如果用户在第一阶段 Esc → 返回 `("", "", ErrCancelled)`

- [ ] **Step 1: 实现 TUI**

写入 `internal/tui/tui.go`：

```go
package tui

import (
	"fmt"
	"strings"

	"cc-launcher/internal/provider"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ErrCancelled 表示用户取消操作。
var ErrCancelled = fmt.Errorf("已取消")

// 权限模式及其描述。
type modeItem struct {
	mode string
	desc string
}

var modeItems = []modeItem{
	{"manual", "每次操作询问确认"},
	{"acceptEdits", "自动接受文件编辑"},
	{"auto", "自动执行非破坏性操作"},
	{"bypassPermissions", "跳过所有权限检查"},
	{"dontAsk", "不询问，直接执行"},
	{"plan", "只读，仅分析不执行"},
}

// ─── bubbletea list item 实现 ───

type providerListItem struct {
	info     provider.Info
	isActive bool
}

func (i providerListItem) Title() string {
	if i.info.Error != "" {
		return fmt.Sprintf("⚠️  %s — 解析失败", i.info.Name)
	}
	if i.isActive {
		return fmt.Sprintf("%s  ← 上次使用", i.info.Name)
	}
	return i.info.Name
}
func (i providerListItem) Description() string { return "" }
func (i providerListItem) FilterValue() string  { return i.info.Name }

type modeListItem struct {
	item modeItem
}

func (m modeListItem) Title() string       { return m.item.mode }
func (m modeListItem) Description() string { return m.item.desc }
func (m modeListItem) FilterValue() string { return m.item.mode }

// ─── 主模型 ───

type stage int

const (
	stageProvider stage = iota
	stageMode
	stageDone
)

type model struct {
	stage       stage
	providerList list.Model
	modeList     list.Model

	selectedProvider string
	selectedMode     string
	quitting         bool

	width  int
	height int
}

func newModel(providers []provider.Info, defaultProvider, defaultMode string) model {
	// ── 提供商列表 ──
	providerItems := make([]list.Item, len(providers))
	defaultProviderIdx := 0
	for i, p := range providers {
		providerItems[i] = providerListItem{
			info:     p,
			isActive: p.Name == defaultProvider,
		}
		if p.Name == defaultProvider {
			defaultProviderIdx = i
		}
	}

	providerDelegate := list.NewDefaultDelegate()
	providerDelegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6")).
		BorderLeft(true).
		PaddingLeft(1)
	providerDelegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6"))

	providerList := list.New(providerItems, providerDelegate, 0, 0)
	providerList.Title = "选择提供商"
	providerList.SetShowStatusBar(false)
	providerList.SetFilteringEnabled(false)
	providerList.SetShowHelp(false)
	providerList.Select(defaultProviderIdx)

	// ── 模式列表 ──
	modeListItems := make([]list.Item, len(modeItems))
	defaultModeIdx := 0
	for i, m := range modeItems {
		modeListItems[i] = modeListItem{item: m}
		if m.mode == defaultMode {
			defaultModeIdx = i
		}
	}

	modeDelegate := list.NewDefaultDelegate()
	modeDelegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6")).
		BorderLeft(true).
		PaddingLeft(1)
	modeDelegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6"))

	modeList := list.New(modeListItems, modeDelegate, 0, 0)
	modeList.Title = "选择权限模式"
	modeList.SetShowStatusBar(false)
	modeList.SetFilteringEnabled(false)
	modeList.SetShowHelp(false)
	modeList.Select(defaultModeIdx)

	return model{
		stage:        stageProvider,
		providerList: providerList,
		modeList:     modeList,
		selectedMode: defaultMode, // fallback
	}
}

func (m model) Init() tea.Cmd {
	return tea.SetWindowTitle("Claude Code Launcher")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		h, v := headerStyle.GetFrameSize()
		f, _ := footerStyle.GetFrameSize()
		availableHeight := m.height - h - v - f - 4
		// half for list
		m.providerList.SetSize(m.width-4, availableHeight/2)
		m.modeList.SetSize(m.width-4, availableHeight/2)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "esc":
			if m.stage == stageProvider {
				m.quitting = true
				return m, tea.Quit
			}
			// 从模式选择退回提供商选择
			m.stage = stageProvider
			return m, nil

		case "enter":
			if m.stage == stageProvider {
				if item, ok := m.providerList.SelectedItem().(providerListItem); ok {
					if item.info.Error != "" {
						return m, nil // 跳过解析失败的
					}
					m.selectedProvider = item.info.Name
					m.stage = stageMode
					return m, nil
				}
			} else {
				if item, ok := m.modeList.SelectedItem().(modeListItem); ok {
					m.selectedMode = item.item.mode
					m.stage = stageDone
					return m, tea.Quit
				}
			}
		}
	}

	var cmd tea.Cmd
	if m.stage == stageProvider {
		m.providerList, cmd = m.providerList.Update(msg)
	} else {
		m.modeList, cmd = m.modeList.Update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	if m.stage == stageDone {
		return ""
	}

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	if m.stage == stageProvider {
		content := headerStyle.Render("Claude Code Launcher") + "\n\n"
		content += m.providerList.View() + "\n\n"
		content += footerStyle.Render("Enter 确认  Esc 退出  ↑↓ 移动")
		return borderStyle.Render(content)
	}

	// stageMode
	content := headerStyle.Render("Claude Code Launcher") + "\n\n"
	content += lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		fmt.Sprintf("提供商: %s\n\n", m.selectedProvider),
	)
	content += m.modeList.View() + "\n\n"
	content += footerStyle.Render("Enter 确认  Esc 返回  ↑↓ 移动")
	return borderStyle.Render(content)
}

// ─── 样式 ───

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	headerStyle = lipgloss.NewStyle().Bold(true).PaddingLeft(1)
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).PaddingLeft(1)
)

// ─── 公开入口 ───

// Run 启动 TUI，依次让用户选择提供商和权限模式。
// defaultProvider 和 defaultMode 用于设置列表默认光标位置。
func Run(providers []provider.Info, defaultProvider, defaultMode string) (string, string, error) {
	if len(providers) == 0 {
		return "", "", fmt.Errorf("暂无提供商配置，请在 ~/.cc-launcher/providers/ 下添加 JSON 文件")
	}

	if defaultMode == "" {
		defaultMode = "manual"
	}

	m := newModel(providers, defaultProvider, defaultMode)
	finalModel, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return "", "", err
	}

	result := finalModel.(model)
	if result.quitting || result.selectedProvider == "" {
		return "", "", ErrCancelled
	}

	return result.selectedProvider, result.selectedMode, nil
}

func init() {
	_ = strings.TrimSpace // suppress unused import
}
```

- [ ] **Step 2: 验证编译**

```bash
go build ./internal/tui/
```

Expected: 编译成功。

- [ ] **Step 3: Commit**

```bash
git add internal/tui/
git commit -m "feat: add bubbletea TUI for provider and mode selection"
```

---

### Task 8: 主入口与完整连线

**Files:**
- Modify: `cmd/cc-launcher/main.go` (替换 stub)

**Interfaces:**
- Consumes: 所有 internal 包
- Produces: 完整的 `cc-launcher` 二进制

- [ ] **Step 1: 检查是否存在 `~/.cc-launcher` 路径与 `~/.claude` 路径的工具函数**

在此之前，先确认 `state.Manager` 和 `config.Manager` 的路径常量已定义。在 `internal/state/state.go` 末尾追加：

```go
// DefaultBaseDir 返回 ~/.cc-launcher 路径。
func DefaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".cc-launcher")
	}
	return filepath.Join(home, ".cc-launcher")
}
```

在 `internal/config/config.go` 中追加：

```go
// DefaultSettingsPath 返回 ~/.claude/settings.json 路径。
func DefaultSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// DefaultBackupDir 返回 ~/.cc-launcher/backups 路径。
func DefaultBackupDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cc-launcher", "backups")
}
```

- [ ] **Step 2: 编写 main.go**

写入 `cmd/cc-launcher/main.go`（替换 stub）：

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"cc-launcher/internal/cli"
	"cc-launcher/internal/config"
	"cc-launcher/internal/launcher"
	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
	"cc-launcher/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	// 初始化各 Manager
	stateMgr := state.NewManager(state.DefaultBaseDir())
	providersDir := filepath.Join(state.DefaultBaseDir(), "providers")
	providerMgr := provider.NewManager(providersDir)
	configMgr := config.NewManager(
		config.DefaultSettingsPath(),
		config.DefaultBackupDir(),
		10,
	)

	// 无参数 → TUI 启动流程
	if len(args) == 0 {
		return runTUI(providerMgr, stateMgr, configMgr)
	}

	// 有参数 → 路由到 CLI 子命令
	c := cli.New(providerMgr, stateMgr)
	return c.Run(args)
}

func runTUI(pm *provider.Manager, sm *state.Manager, cm *config.Manager) error {
	// 加载提供商列表
	providers, err := pm.List()
	if err != nil {
		return fmt.Errorf("加载提供商列表失败: %w", err)
	}

	// 获取默认值
	defaultProvider := sm.GetActiveProvider()
	wd, _ := os.Getwd()
	defaultMode, _ := sm.GetPermissionMode(wd)

	// TUI 选择
	selectedProvider, selectedMode, err := tui.Run(providers, defaultProvider, defaultMode)
	if err != nil {
		if err == tui.ErrCancelled {
			return nil // 用户取消，静默退出
		}
		return err
	}

	// 提供商变更时合并配置
	if selectedProvider != defaultProvider {
		fmt.Print("正在切换提供商配置... ")

		providerCfg, err := pm.Get(selectedProvider)
		if err != nil {
			fmt.Println("✗")
			return err
		}

		oldSnapshot := sm.GetInjectedSnapshot()
		if err := cm.Merge(providerCfg, oldSnapshot); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("配置合并失败: %w", err)
		}

		// 更新 state
		sm.SetActiveProvider(selectedProvider)
		sm.SetInjectedSnapshot(providerCfg)
		fmt.Println("✓")
	}

	// 更新权限模式记录
	sm.SetPermissionMode(wd, selectedMode)

	fmt.Printf("正在启动 Claude Code (%s / %s)...\n", selectedProvider, selectedMode)
	return launcher.Launch(selectedMode)
}
```

- [ ] **Step 3: 验证编译**

```bash
go build -o cc-launcher ./cmd/cc-launcher/
```

Expected: 编译成功。

- [ ] **Step 4: 运行所有单元测试**

```bash
go test ./internal/... -v
```

Expected: 所有测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: wire main entry point with TUI + config merge + launch"
```

---

### Task 9: 构建、安装与端到端验证

**Files:**
- Create: `Makefile` (optional)

- [ ] **Step 1: 编译 release 二进制**

```bash
go build -ldflags="-s -w" -o cc-launcher ./cmd/cc-launcher/
```

Expected: 生成 stripped `cc-launcher` 二进制。

- [ ] **Step 2: 创建 cc 命令别名**

```bash
# 方案 A: 符号链接到 PATH 中的目录
ln -sf "$(pwd)/cc-launcher" /usr/local/bin/cc 2>/dev/null || \
  echo "提示: 将 cc-launcher 添加到 PATH，或创建 shell alias: alias cc='$(pwd)/cc-launcher'"

# 方案 B: shell alias（添加到 ~/.bashrc 或 ~/.zshrc）
# alias cc='/home/zxwang/workspace/golang/cc-launcher/cc-launcher'
```

- [ ] **Step 3: 创建测试提供商配置**

```bash
mkdir -p ~/.cc-launcher/providers

cat > ~/.cc-launcher/providers/anthropic.json << 'EOF'
{
  "env": {
    "ANTHROPIC_API_KEY": "sk-ant-your-key-here"
  },
  "model": "sonnet"
}
EOF

cat > ~/.cc-launcher/providers/bedrock.json << 'EOF'
{
  "env": {
    "CLAUDE_CODE_USE_BEDROCK": "1",
    "AWS_REGION": "us-east-1"
  },
  "model": "sonnet"
}
EOF
```

- [ ] **Step 4: 验证 cc provider 子命令**

```bash
./cc-launcher provider --list
```

Expected:
```
  anthropic
  bedrock
```

```bash
./cc-launcher provider anthropic
```

Expected: `已切换到 anthropic`

```bash
./cc-launcher provider --new test-provider
# 输入 base_url（回车跳过）和 api_key
```

```bash
./cc-launcher provider --delete test-provider
```

- [ ] **Step 5: 验证 cc mode 子命令**

```bash
./cc-launcher mode
```

Expected: 列出 6 种模式。

```bash
./cc-launcher mode plan
```

Expected: `当前目录权限模式已设为 plan`

- [ ] **Step 6: 验证 TUI 启动（如果环境中有 claude 命令）**

```bash
./cc-launcher
```

Expected: 进入 TUI 界面，可选择提供商和模式。按 Esc 可退出。

- [ ] **Step 7: 验证配置合并**

```bash
# 检查 settings.json 是否只被修改 env 及相关字段，不动其他字段
cat ~/.claude/settings.json
```

- [ ] **Step 8: 验证备份**

```bash
ls ~/.cc-launcher/backups/
```

Expected: 有 `settings-*.json` 文件。

- [ ] **Step 9: Commit + tag**

```bash
git add -A
git commit -m "chore: add Makefile and install instructions"
git tag v0.1.0
```

---

## 实现顺序依赖

```
Task 1 (脚手架)
   ↓
Task 2 (state)  Task 3 (provider)  Task 4 (config)  Task 5 (launcher)
   ↓               ↓                   ↓
Task 6 (CLI) ←─────┘                   │
   ↓                                   │
Task 7 (TUI) ←── Task 3 (provider) ────┘
   ↓
Task 8 (main 连线) ←── 依赖所有上述
   ↓
Task 9 (构建验证)
```

Task 2–5 无相互依赖，可并行实现。Task 6 依赖 Task 2 和 3。Task 7 依赖 Task 3。Task 8 依赖所有。
