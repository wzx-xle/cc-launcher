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
		return nil
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

	sort.Strings(backups)
	for i := 0; i < len(backups)-m.maxBackups; i++ {
		os.Remove(filepath.Join(m.backupDir, backups[i]))
	}
	return nil
}

// Merge 执行配置合并：备份 → 清理旧快照 → 注入新配置。
func (m *Manager) Merge(providerCfg, oldSnapshot map[string]any) error {
	if err := m.Backup(); err != nil {
		return err
	}

	settings, err := m.LoadSettings()
	if err != nil {
		return err
	}

	if oldSnapshot != nil {
		cleanSnapshot(settings, oldSnapshot)
	}

	deepMerge(settings, providerCfg)

	return m.SaveSettings(settings)
}

// cleanSnapshot 递归遍历 snapshot，删除 settings 中值与 snapshot 相同的字段。
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

// DefaultSettingsPath 返回 ~/.claude/settings.json 路径。
func DefaultSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// DefaultBackupDir 返回 ~/.cc-launcher/backups 路径。
func DefaultBackupDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cc-launcher", "backups"), nil
}
