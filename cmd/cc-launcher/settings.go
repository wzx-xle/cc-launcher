package main

// settingsArgs 把 additional settings 的 JSON 变成 Claude Code 的 `--settings` 参数。
//
// **两个平台同一套策略**（provider-env-via-settings）：
//   - 内容写到 `~/.cc-launcher/cache/settings-<sha256[:16]>.json`（内容哈希命名 →
//     复用 + 并发不串 + 写入幂等）
//   - 参数是**文件路径**，不是内联 JSON：
//     - Unix 上内联 JSON 会明文进 argv（/proc/<pid>/cmdline 是 0444，同机可读）
//     - Windows 上 cmd.exe 会剥掉 JSON 的双引号
//   - **不删除**（cache 文件常驻，启动时按 mtime 清 1 年前的）
//
// 返回：
//   - args：追加给 claude 的参数（`--settings <path>`）
//   - cleanup：清理动作（**恒为 no-op** —— 文件常驻不删；保留字段是为了不改动
//     launcher.Options 的结构）
//   - err：构造失败
func settingsArgs(json string) (args []string, cleanup func(), err error) {
	path, err := writeSettingsFile(json)
	if err != nil {
		return nil, nil, err
	}
	return []string{"--settings", path}, func() {}, nil
}
