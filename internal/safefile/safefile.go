// Package safefile 提供"先备份、再原子写"的配置文件写入。
//
// 背景：同一批配置文件会被多个写入口碰到——ccl 的 CLI、web 控制台、
// Claude Code 自己（写 ~/.claude/settings.json）。裸 os.WriteFile 有两个问题：
//
//   - **不原子**：写到一半进程被杀会留下半个文件，配置半个 = 启动不了
//   - **没备份**：写坏了没法回退
//
// safefile.Write 把两件事一起做：先把旧内容备份进备份目录，再写临时文件 +
// rename。备份按文件分组轮转（每个文件保留最近 N 份），不会互相挤掉。
package safefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DefaultKeepBackups 是每个文件保留的备份份数上限。
//
// 与 internal/config.DefaultMaxBackups 取同一个值：settings.json 典型几十 KB，
// 30 份也就 1~2MB，可以忽略；而 30 份足够覆盖"手滑连改十几次"找回上一个版本。
const DefaultKeepBackups = 30

// tsLayout 是备份文件名里的时间戳格式。
//
// 用本地时间（备份是给人看的，"什么时候改的"按本地时间更直观），格式里
// **不能有冒号**（Windows 的文件名不允许）。毫秒精度是为了同一秒内两次写入
// 不撞名——撞名会让第二次的备份覆盖第一次的，丢一个中间版本。
//
// 字典序 == 时间序：格式的每一段都是定宽且高位在前，排序结果与时间顺序一致
// （"…-05.999" < "…-06.000"），所以轮转时按文件名排序即可。
const tsLayout = "2006-01-02T15-04-05.000"

// ErrEmptyPath 表示传入的路径为空。
var ErrEmptyPath = errors.New("路径不能为空")

// Write 把 data 原子地写入 path。
//
// 流程：
//  1. 若 path 已存在且有内容 → 先把它复制到 backupDir（文件名
//     `<base>-<时间戳>.bak`），并把该文件的备份裁剪到最近 keep 份
//  2. 同目录写临时文件 → fsync → chmod → rename 覆盖
//
// keep <= 0 时**不备份**（调用方明确表示不需要）。
//
// 权限：目标已存在时沿用它的权限位；不存在时用 0644。临时文件由
// os.CreateTemp 创建（0600），显式 chmod 之后不受 umask 影响——
// 这一点和 internal/web/auth.go 里写 token 的做法一致。
func Write(path string, data []byte, backupDir string, keep int) error {
	if path == "" {
		return ErrEmptyPath
	}

	if keep > 0 {
		if err := backup(path, backupDir, keep); err != nil {
			return err
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("建目录失败: %w", err)
	}

	// 沿用旧文件的权限；没有旧文件则 0644。
	mode := os.FileMode(0644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	// 失败路径上清掉临时文件（成功路径上 rename 之后它已不存在，Remove 报错忽略）。
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("刷临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("设置临时文件权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("重命名临时文件失败: %w", err)
	}
	return nil
}

// backup 把 path 的当前内容复制进 backupDir，并轮转该文件的备份。
//
// 文件不存在 / 内容为空 → 不备份（空文件备份出来只是占地方）。
func backup(path, backupDir string, keep int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 还没有这个文件 → 没有"旧内容"要备份
		}
		return fmt.Errorf("读旧文件失败: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if backupDir == "" {
		return errors.New("备份目录不能为空")
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("建备份目录失败: %w", err)
	}

	name := filepath.Base(path) + "-" + time.Now().Format(tsLayout) + ".bak"
	if err := os.WriteFile(filepath.Join(backupDir, name), data, 0644); err != nil {
		return fmt.Errorf("写备份失败: %w", err)
	}
	return prune(backupDir, filepath.Base(path), keep)
}

// prune 把某个文件的备份裁剪到最近 keep 份。
func prune(backupDir, base string, keep int) error {
	if keep <= 0 {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(backupDir, base+"-*.bak"))
	if err != nil {
		return err
	}
	if len(matches) <= keep {
		return nil
	}
	// 文件名里的时间戳定宽且高位在前 → 字典序 == 时间序 → 末尾 keep 份最新。
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-keep] {
		_ = os.Remove(old)
	}
	return nil
}
