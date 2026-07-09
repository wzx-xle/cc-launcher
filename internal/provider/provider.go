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
