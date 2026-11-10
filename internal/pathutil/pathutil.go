// Package pathutil 提供路径**展示**用的格式化辅助。
//
// 这里的函数只解决一件事：把绝对路径里的用户主目录前缀换成 `~`，让显示值
// 短一截，也顺带不把用户名暴露出去。它们的返回值**不能**拿去读写真实文件
// —— `~` 是展示形式，不是路径。
package pathutil

import (
	"os"
	"strings"
)

// Shorten 把绝对路径 p 的用户主目录前缀换成 `~`。
//
// 语义：
//
//   - os.UserHomeDir() 成功且 p 在 home 下 → 返回 "~" + 剩余路径
//   - home 取不到（HOME / USERPROFILE 都没设）→ 原样返回 p
//   - p 不在 home 下（如 /etc/hosts）→ 原样返回 p
//   - p 恰好等于 home → 返回 "~"
//
// **路径边界必须成立**：home 是 `/home/user` 时，`/home/userx` 不算 home 下。
// 纯字符串前缀匹配会把它误判成 home 下的路径（显示成 `~x`）——原先
// cmd/cc-launcher 里的 shortenHomePath 就有这个毛病，本包下沉时一并修掉。
// 现有调用点都是 home 的直接子目录（`~/.claude/...`、`~/.cc-launcher/...`），
// 修掉不影响它们的输出。
//
// **不做路径分隔符归一化**：Windows 上 `C:\Users\me\.claude\settings.json`
// → `~\.claude\settings.json`，反斜杠原样保留——这里只是截掉前缀，不是重拼
// 路径。
func Shorten(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == home {
		return "~"
	}
	sep := string(os.PathSeparator)
	// 补上分隔符再比前缀 —— 这样 home 之后的第一个字符必须是分隔符，
	// 路径边界才成立。
	if !strings.HasSuffix(home, sep) {
		home += sep
	}
	if rel, ok := strings.CutPrefix(p, home); ok {
		return "~" + sep + rel
	}
	return p
}
