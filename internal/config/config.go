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

// DefaultMaxBackups 是 backups/ 目录保留的最大备份数（轮转阈值）。
//
// 调高原因：merge-on-rotate 频繁切 provider 的用户每次 Merge 都会触发 Backup，
// 旧值 10 在高频切换下会让"健康基线"快照很快被轮转挤掉；
// 30 = 3× 增长，足以覆盖"每天切 ~10 次"的边缘场景，且单目录 30 个文件肉眼浏览仍友好。
//
// 调高代价：磁盘占用线性增长（settings.json 典型 ~50KB × 30 ≈ 1.5MB），可忽略。
const DefaultMaxBackups = 30

// maxBackupSeq 是同一秒内备份文件名的序号上限。
//
// 同一秒内写 100 份备份不可能（每份都要落盘 + 序列化）；到了就是有 bug，
// 宁可报错也不覆盖已有备份。
const maxBackupSeq = 100

// now 取当前时间。
//
// 抽成包级变量是为了测试能注入固定时钟 —— "同一秒内备份两次"这个场景撞不撞
// **看运气**（IO 快慢、调度抖动）：撞上了 CI 红、撞不上本地绿，没法当回归
// 测试守。
var now = time.Now

// NewManager 创建 Manager。
func NewManager(settingsPath, backupDir string, maxBackups int) *Manager {
	return &Manager{
		settingsPath: settingsPath,
		backupDir:    backupDir,
		maxBackups:   maxBackups,
	}
}

// BackupDir 返回备份目录路径（供 migrate 等需要报告备份位置的调用方使用）。
func (m *Manager) BackupDir() string {
	return m.backupDir
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

	timestamp := now().Format("2006-01-02T15-04-05")
	// **O_EXCL 原子占位**：同一秒内两次备份会算出**同一个文件名** → 覆盖 →
	// 丢一份备份。频繁切 provider 的用户每次 Merge 都触发一次 Backup，
	// 一秒内切两次不是不可能（脚本切 provider、或者 Merge 与切 provider
	// 同时发生）。
	//
	// 撞名就加 "_NN" 定宽序号，**绝不覆盖**已有的备份。
	//
	// 序号分隔符用 "_"（0x5F）而不是 "-"（0x2D）：它比扩展名前的 "."（0x2E）
	// **大** → 字典序上"无序号的"排在"带序号的"**前面** → 同一秒内序号递增
	// == 时间递增 → rotateBackups 那个"末尾最新"的假设仍然成立。
	//
	// 时间戳**保持秒级**（不改精度）：改成微秒会让新旧两种文件名混排时排序
	// 颠倒（"…-03.000000.json" 排在 "…-03.json" **前面** → 刚写的新备份被
	// rotate 当成最旧先删掉）。精度不是防撞名的关键，序号才是。
	for seq := range maxBackupSeq {
		name := "settings-" + timestamp + ".json"
		if seq > 0 {
			name = "settings-" + timestamp + "_" + fmt.Sprintf("%02d", seq) + ".json"
		}
		f, err := os.OpenFile(filepath.Join(m.backupDir, name),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue // 撞名 → 换下一个序号
			}
			return fmt.Errorf("写入备份失败: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return fmt.Errorf("写入备份失败: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("写入备份失败: %w", err)
		}
		break
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

// DefaultMemoryPath 返回 ~/.claude/CLAUDE.md 路径。
//
// **只管用户级**：项目级的 CLAUDE.md 跟着 git 工作区走，在工具里改它
// 容易和 git 打架（而且工具是全局的，不知道"当前项目"是哪个）。
func DefaultMemoryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "CLAUDE.md"), nil
}
