//go:build !windows

package main

// settingsArgs 把 additional settings 的 JSON 变成 Claude Code 的 `--settings` 参数。
//
// Unix 上 `syscall.Exec` 直传 argv（不经 shell）→ JSON 里的双引号是普通字符，
// 内联 JSON 字符串安全 ✓，且**不产生临时文件**（exec 后 ccl 进程被替换成
// claude，没有任何代码能回头删文件）。
//
// Windows 上必须走文件（cmd.exe 会剥掉 JSON 的双引号）——见 settings_windows.go。
//
// 参数：
//   - json：additional settings 的 JSON 文本
//
// 返回：
//   - args：追加给 claude 的参数（`--settings <json>`）
//   - cleanup：清理动作（Unix 是 no-op）
//   - err：构造失败
func settingsArgs(json string) (args []string, cleanup func(), err error) {
	return []string{"--settings", json}, func() {}, nil
}
