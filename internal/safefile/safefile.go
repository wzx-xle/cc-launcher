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
// **不能有冒号**（Windows 的文件名不允许）。
//
// **微秒精度**：毫秒挡不住同一毫秒内的两次写入 —— 名字一样就是覆盖，会丢一个
// 中间版本。网页控制台连点两次"保存"、或者 CLI 与 daemon 同时写，就可能撞。
//
// 字典序 == 时间序：格式的每一段都是定宽且高位在前，排序结果与时间顺序一致
// （"…-05.999999" < "…-06.000000"），所以轮转时按文件名排序即可。
const tsLayout = "2006-01-02T15-04-05.000000"

// maxBackupSeq 是同一时刻内备份文件名的序号上限。
//
// 同一微秒内写 100 次备份不可能（每份都要落盘）；到了就是有 bug，宁可报错
// 也不覆盖已有备份。
const maxBackupSeq = 100

// now 取当前时间。
//
// 抽成包级变量是为了测试能注入固定时钟："同一时刻写两次"这种撞名场景在真实
// 机器上撞不撞**看运气**（IO 快慢、调度抖动）—— 撞上了 CI 红、撞不上本地绿，
// 没法当回归测试守。
var now = time.Now

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

	if _, err := writeBackup(backupDir, filepath.Base(path), data, now()); err != nil {
		return err
	}
	return prune(backupDir, filepath.Base(path), keep)
}

// writeBackup 把 data 写成一份备份，返回写入的文件名。
//
// **绝不覆盖已存在的备份**：用 O_EXCL 原子地"占位"，撞名（同一时刻两次写）
// 就换名重试。覆盖会丢一个中间版本 —— 而 safefile 的全部意义就是"写坏了能
// 回退到上一个版本"，丢一份备份等于这个承诺在最需要的时候失效。
func writeBackup(dir, base string, data []byte, t time.Time) (string, error) {
	for seq := range maxBackupSeq {
		name := backupName(base, t, seq)
		f, err := os.OpenFile(filepath.Join(dir, name),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue // 撞名 → 换下一个序号
			}
			return "", fmt.Errorf("写备份失败: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("写备份失败: %w", err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("写备份失败: %w", err)
		}
		return name, nil
	}
	return "", fmt.Errorf("备份文件名序号用尽（同一时刻写了 %d 份备份）", maxBackupSeq)
}

// backupName 生成 base 在时刻 t 的第 seq 个备份文件名。
//
// 同一个时刻的第 0 份没有后缀，第 1 份起追加 "_NN" 定宽序号。
//
// **分隔符用 "_"（0x5F）而不是 "-"（0x2D）**：它比时间戳与扩展名之间的
// "."（0x2E）**大** → 字典序上"无后缀的"排在"带后缀的"**前面** → 同一时刻内
// 序号递增 == 时间递增 → prune 那个"末尾最新"的假设仍然成立。
// 用 "-" 的话 "_" 比 "." 小，带后缀的会排到前面，prune 会把**最新**的当成
// 最旧的删掉。
func backupName(base string, t time.Time, seq int) string {
	stamp := t.Format(tsLayout)
	if seq <= 0 {
		return base + "-" + stamp + ".bak"
	}
	return base + "-" + stamp + "_" + fmt.Sprintf("%02d", seq) + ".bak"
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
