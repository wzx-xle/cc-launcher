package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// LoadSettings 读取 settings.json，保持原始 JSON 字段顺序。
// 文件不存在时返回空 map。
func (m *Manager) LoadSettings() (map[string]any, error) {
	om, err := m.loadOrdered()
	if err != nil {
		return nil, err
	}
	return om.toMap(), nil
}

func (m *Manager) loadOrdered() (*orderedMap, error) {
	data, err := os.ReadFile(m.settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return newOrderedMap(), nil
		}
		return nil, err
	}
	om, err := decodeOrdered(data)
	if err != nil {
		return nil, fmt.Errorf("settings.json 解析失败: %w", err)
	}
	return om, nil
}

// SaveSettings 将配置写入 settings.json，保持原始字段顺序。
func (m *Manager) SaveSettings(settings map[string]any) error {
	om := mapToOrdered(settings)
	return m.saveOrdered(om)
}

func (m *Manager) saveOrdered(om *orderedMap) error {
	dir := filepath.Dir(m.settingsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := encodeOrdered(om)
	if err != nil {
		return err
	}
	// 美化输出：缩进 2 空格
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return os.WriteFile(m.settingsPath, data, 0644)
	}
	buf.WriteByte('\n')
	return os.WriteFile(m.settingsPath, buf.Bytes(), 0644)
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

// Merge 执行配置合并：备份 → 清理旧快照 → 清理认证冲突 → 注入新配置。
func (m *Manager) Merge(providerCfg, oldSnapshot map[string]any) error {
	if err := m.Backup(); err != nil {
		return err
	}

	settings, err := m.loadOrdered()
	if err != nil {
		return err
	}

	// 1. 清理旧快照
	if oldSnapshot != nil {
		snapshotOM := mapToOrdered(oldSnapshot)
		cleanSnapshotOrdered(settings, snapshotOM)
	}

	// 2. 清理认证冲突：provider 有 ANTHROPIC_API_KEY 时删除 AUTH_TOKEN，反之亦然
	providerOM := mapToOrdered(providerCfg)
	resolveAuthConflict(settings, providerOM)

	// 3. 注入新提供商配置
	deepMergeOrdered(settings, providerOM)

	return m.saveOrdered(settings)
}

// resolveAuthConflict 确保 ANTHROPIC_AUTH_TOKEN 和 ANTHROPIC_API_KEY 不会同时存在。
// 如果 provider 指定了其中一个，则从 settings 中删除另一个。
func resolveAuthConflict(settings, providerCfg *orderedMap) {
	provEnv, ok := providerCfg.get("env")
	if !ok {
		return
	}
	provEnvOM, ok := provEnv.(*orderedMap)
	if !ok {
		return
	}

	settEnv, ok := settings.get("env")
	if !ok {
		return
	}
	settEnvOM, ok := settEnv.(*orderedMap)
	if !ok {
		return
	}

	_, hasAPIKey := provEnvOM.get("ANTHROPIC_API_KEY")
	_, hasAuthToken := provEnvOM.get("ANTHROPIC_AUTH_TOKEN")

	if hasAPIKey {
		settEnvOM.del("ANTHROPIC_AUTH_TOKEN")
	}
	if hasAuthToken {
		settEnvOM.del("ANTHROPIC_API_KEY")
	}
}

// cleanSnapshot 递归遍历 snapshot，删除 settings 中值与 snapshot 相同的字段。
func cleanSnapshot(settings, snapshot map[string]any) {
	settingsOM := mapToOrdered(settings)
	snapshotOM := mapToOrdered(snapshot)
	cleanSnapshotOrdered(settingsOM, snapshotOM)
	// 全量替换 dst 内容
	for k := range settings {
		delete(settings, k)
	}
	for k, v := range settingsOM.toMap() {
		settings[k] = v
	}
}

// deepMerge 将 src 递归合并到 dst。
func deepMerge(dst, src map[string]any) {
	dstOM := mapToOrdered(dst)
	srcOM := mapToOrdered(src)
	deepMergeOrdered(dstOM, srcOM)
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range dstOM.toMap() {
		dst[k] = v
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
