package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State 记录 launcher 运行时状态。
//
// 字段演进：DirBindings（provider-setenv-cwd-binding §2）从 0 加入；
// OtelLegacyCleaned（otel-endpoints-via-settings §"ccl cleans up legacy ccl
// artifacts from settings.json once"）记录"旧版 ccl 遗留清理"是否已跑过。
// 旧 state.json 无该字段时，json.Unmarshal 忽略未知字段 —— 新字段的零值
// false 意味着"还没清理过"，升级后的第一次启动会补跑一次。
type State struct {
	ActiveProvider      string            `json:"active_provider"`
	InjectedSnapshot    map[string]any    `json:"injected_snapshot"`
	LastPermissionModes map[string]string `json:"last_permission_modes"`
	DirBindings         map[string]string `json:"dir_bindings"`
	OtelLegacyCleaned   bool              `json:"otel_legacy_cleaned"`
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
//
// 向后兼容：未知字段被 json.Unmarshal 忽略（provider-setenv-cwd-binding §2.1
// 要求"未知字段不报错"——json.Unmarshal 默认行为就是忽略）。老 state.json
// 无 DirBindings 字段 → loaded.DirBindings 为 nil → 本函数 nil-check 兜底
// 为空 map，保证调用方 GetProviderForDir 不会 panic。
func (m *Manager) Load() (*State, error) {
	data, err := os.ReadFile(m.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &State{
				LastPermissionModes: map[string]string{},
				DirBindings:         map[string]string{},
			}, nil
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
	if s.DirBindings == nil {
		s.DirBindings = map[string]string{}
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

// GetOtelLegacyCleaned 报告"旧版 ccl 遗留清理"是否已经跑过。
//
// 语义（otel-endpoints-via-settings）：清理**只跑一次**。规则里有"值等于 ccl
// 默认值就删"这种启发式，反复跑会把用户事后自己写进去的同名值也删掉。
func (m *Manager) GetOtelLegacyCleaned() bool {
	s, err := m.Load()
	if err != nil {
		return false
	}
	return s.OtelLegacyCleaned
}

// SetOtelLegacyCleaned 记录"旧版 ccl 遗留清理"已经跑过并持久化。
func (m *Manager) SetOtelLegacyCleaned(done bool) error {
	s, err := m.Load()
	if err != nil {
		return err
	}
	s.OtelLegacyCleaned = done
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

// GetProviderForDir 返回指定工作目录绑定的 provider 名称；无绑定返空字符串。
//
// provider-setenv-cwd-binding §2.2 + spec/provider-cwd-binding §"reading
// dir_bindings when none exist"。
func (m *Manager) GetProviderForDir(workDir string) string {
	s, err := m.Load()
	if err != nil {
		return ""
	}
	return s.DirBindings[workDir]
}

// SetProviderForDir 设置指定工作目录绑定的 provider 并持久化；同 wd 二次
// 写入覆盖旧值（spec/provider-cwd-binding §"writing a new binding" +
// §"updating an existing binding"）。
func (m *Manager) SetProviderForDir(workDir, name string) error {
	s, err := m.Load()
	if err != nil {
		return err
	}
	s.DirBindings[workDir] = name
	return m.Save(s)
}

// DefaultBaseDir 返回 ~/.cc-launcher 路径。
func DefaultBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cc-launcher"), nil
}
