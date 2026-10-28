//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// settingsArgs 把 additional settings 的 JSON 变成 Claude Code 的 `--settings` 参数。
//
// **Windows 必须用临时文件，不能用内联 JSON 字符串**：`launcher_windows.go` 走
// `exec.Command("cmd", "/c", claudePath, args...)` → cmd.exe 会把 JSON 里的双引号
// 当作参数分隔符剥掉 → 传过去的 JSON 已经不是合法 JSON。
//
// 临时文件：
//   - 位置 os.TempDir()（路径不含空格 → 不触发 cmd.exe 的引号问题）
//   - 权限 0600（内容含 settings.json 里的用户原值，可能敏感）
//   - 清理：Windows 上 launcher 是 `cmd.Run()`（等子进程结束）→ claude 退出后
//     ccl 的代码还在 → 可以删（Unix 上 exec 后 ccl 没了，所以 Unix 不用文件）
//
// 参数：
//   - json：additional settings 的 JSON 文本
//
// 返回：
//   - args：追加给 claude 的参数（`--settings <path>`）
//   - cleanup：删除临时文件
//   - err：写文件失败
func settingsArgs(json string) (args []string, cleanup func(), err error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("ccl-settings-%d.json", os.Getpid()))
	if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
		return nil, nil, fmt.Errorf("写 additional settings 临时文件失败: %w", err)
	}
	return []string{"--settings", path}, func() { _ = os.Remove(path) }, nil
}
