package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// settings 载体：Claude Code 的 `--settings` 参数指向的文件。
//
// **为什么用文件而不是内联 JSON**（provider-env-via-settings design §调研结论）：
//   - Unix 上 `--settings <JSON>` 是命令行参数 → /proc/<pid>/cmdline 权限 0444
//     （同机任何用户可读）→ provider 的 ANTHROPIC_AUTH_TOKEN 会明文进 argv
//   - 内联 JSON 还有 cmd.exe 剥引号的问题（Windows）
//
// **为什么用内容哈希命名**（同上 §二）：
//   - 同样的内容 → 同一个文件 → **复用**（不是每次启动留一个文件）
//   - 不同内容 → 不同文件 → 两个并发启动**各读各的**，不会串
//     （固定名会串：实测 A 写 → B 写 → A 读到 B 的 provider → 用错账号）
//   - 同名即同内容 → **写入幂等**：已存在且内容一致就直接复用，不重写
//     （Windows 上也就不会撞上"文件被 claude 占用 → 覆盖失败"）
//
// **为什么不删除**：文件常驻（是 cache，不是 per-launch 临时文件）→ 启动时按
// mtime 清理 1 年前的即可。Unix 的 launcher 用 syscall.Exec 替换进程、**没有
// "退出"那一刻**，所以"退出时删文件"在 Unix 上从来就没实现过（opts.Cleanup 在
// Unix 上是 no-op）——常驻反而让两个平台的语义一致。
const (
	settingsCacheDirName = "cache"
	settingsFilePrefix   = "settings-"
	settingsFileSuffix   = ".json"
	// settingsFileMaxAge 是缓存文件的最长保留期（1 年）。
	settingsFileMaxAge = 365 * 24 * time.Hour
)

// settingsCacheDir 返回 settings 缓存目录（~/.cc-launcher/cache）。
//
// 目录权限 0700 —— 内容可能含 provider 凭证。
func settingsCacheDir() (string, error) {
	base := cclLauncherBaseDir()
	if base == "" {
		return "", fmt.Errorf("无法定位 ~/.cc-launcher 目录")
	}
	return filepath.Join(base, settingsCacheDirName), nil
}

// settingsFileName 返回内容的哈希文件名：settings-<sha256 前 16 位>.json。
func settingsFileName(content string) string {
	sum := sha256.Sum256([]byte(content))
	return settingsFilePrefix + hex.EncodeToString(sum[:])[:16] + settingsFileSuffix
}

// writeSettingsFile 把 settings 内容写进缓存目录，返回文件路径。
//
// 幂等：目标已存在且内容一致 → 直接复用（不重写）。
// 否则原子写：同目录临时文件 + rename（并发读者不会看到半个文件）。
func writeSettingsFile(content string) (string, error) {
	dir, err := settingsCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("创建 settings 缓存目录失败: %w", err)
	}
	// 目录已存在时收紧权限（可能被 umask 放宽过）
	_ = os.Chmod(dir, 0700)

	path := filepath.Join(dir, settingsFileName(content))
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return path, nil // 幂等：同内容 → 复用
	}

	tmp, err := os.CreateTemp(dir, settingsFilePrefix+"*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建 settings 临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	// rename 成功后这个 Remove 会因"文件不存在"而失败 → 忽略
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("设置 settings 文件权限失败: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("写 settings 文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("关闭 settings 文件失败: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// Windows：目标已存在时 rename 失败（ERROR_ALREADY_EXISTS / 被占用）
		// → 删掉目标再试一次
		_ = os.Remove(path)
		if err2 := os.Rename(tmpName, path); err2 != nil {
			// 兜底：直接写目标（失去原子性，但内容完整）
			if err3 := os.WriteFile(path, []byte(content), 0600); err3 != nil {
				return "", fmt.Errorf("写 settings 文件失败: %w", err3)
			}
			return path, nil
		}
	}
	return path, nil
}

// cleanupSettingsCache 清理缓存目录里 mtime 超过 1 年的 settings 文件。
//
// 只删自己命名前缀的文件（settings-*.json），其余一律不动。
// 失败只返回错误，由调用方决定是否警告——不阻断 ccl 启动。
func cleanupSettingsCache() error {
	dir, err := settingsCacheDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	cutoff := time.Now().Add(-settingsFileMaxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, settingsFilePrefix) || !strings.HasSuffix(name, settingsFileSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	return nil
}
