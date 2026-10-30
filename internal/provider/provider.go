package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"cc-launcher/internal/settingsschema"
)

// Info 提供商概要信息。
type Info struct {
	Name  string // 文件名去掉 .json
	Error string // JSON 解析失败时的错误信息
	// Note 是 provider 配置文件顶层 `note` 字段，缺省 ""。
	// 渲染层（TUI / CLI）用它显示一行备注。
	Note string
	// Order 是 provider 配置文件顶层 `order` 字段，缺省 0。
	// List() 返回前按 (Order asc, Name asc) 稳定排序。
	Order int
	// ValidationErrors 是配置的 schema 校验错误（settingsschema）。
	//
	// 非空表示**这个 provider 启动不了**：Claude Code 会拒绝整个 settings
	// 文件，provider 的凭证跟着一起消失（provider-settings-load-guard）。
	// 提前算出来，渲染层就能在选择之前把问题摆到用户眼前——不然用户选完
	// provider、走到启动那一步才看到报错。
	ValidationErrors []string
	// PassthroughKeys 是会被透传给 Claude Code 的顶层键数（env 以外的键）。
	// 用来告诉用户"这个 provider 还带了别的设置"。
	PassthroughKeys int
}

// configFile 是 provider JSON 文件的内部表示（仅用于 Unmarshal；List() 不导出）。
// 新增的 note / order 是可选字段，缺失时为零值。
type configFile struct {
	Env   map[string]any `json:"env"`
	Note  string         `json:"note,omitempty"`
	Order int            `json:"order,omitempty"`
}

// ReservedKeys 是 ccl 自己的 provider 元数据键。
//
// 它们**不是** Claude Code 的 settings 键（Claude Code 的顶层键里没有它们），
// 所以透传时必须排除：
//
//   - note：provider 列表里的备注行
//   - order：provider 列表的排序权重
//
// **维护约定**：新增 ccl 元数据键时必须同步加到这里，否则它会被当普通
// settings 键透传给 Claude Code（虽然会被静默忽略，但语义不对，且 `order`
// 这种通用词名有朝一日可能撞上真实键）。
var ReservedKeys = map[string]bool{
	"note":  true,
	"order": true,
}

// IsReservedKey 判断一个顶层键是不是 ccl 自己的元数据键。
func IsReservedKey(key string) bool { return ReservedKeys[key] }

// Manager 管理 ~/.cc-launcher/providers/ 目录。
type Manager struct {
	providersDir string
}

// NewManager 创建 Manager。
func NewManager(providersDir string) *Manager {
	return &Manager{providersDir: providersDir}
}

// ProvidersDir 返回 providers 目录路径（供 CLI 层显示用）。
func (m *Manager) ProvidersDir() string {
	return m.providersDir
}

// List 列出所有提供商。JSON 解析失败的在 Info.Error 中标记，不阻塞其他文件。
// 返回前按 (Order asc, Name asc) 稳定排序。
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
			var cfg configFile
			if err := json.Unmarshal(data, &cfg); err != nil {
				info.Error = fmt.Sprintf("JSON 解析失败: %s", err.Error())
			} else {
				info.Note = cfg.Note
				info.Order = cfg.Order
			}
			// 顶层键：算透传键数 + 跑一遍 schema 校验。
			// 校验失败不阻断 List()——列表要把所有 provider 都列出来，
			// 问题在渲染层标出来。
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err == nil {
				for k := range raw {
					if k == "env" || IsReservedKey(k) {
						continue
					}
					info.PassthroughKeys++
				}
				if res := settingsschema.Validate(raw); !res.OK() {
					info.ValidationErrors = res.Errors
				}
			}
		}
		result = append(result, info)
	}

	slices.SortStableFunc(result, func(a, b Info) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Name, b.Name)
	})
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

// Create 创建新的提供商配置文件，接受任意 env 字段集合。
// 旧签名 Create(name, baseURL, apiKey) 已删除，由 CreateWithEnv 取代。
func (m *Manager) CreateWithEnv(name string, env map[string]any) error {
	if name == "" {
		return fmt.Errorf("提供商名称不能为空")
	}
	if len(env) == 0 {
		return fmt.Errorf("env 不能为空")
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; !ok {
		return fmt.Errorf("env 必须包含 ANTHROPIC_AUTH_TOKEN")
	}
	if m.Exists(name) {
		return fmt.Errorf("提供商 %q 已存在", name)
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
