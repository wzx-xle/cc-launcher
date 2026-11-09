package web

import (
	"os"
	"path/filepath"
	"strings"

	"cc-launcher/internal/provider"
)

// validProviderName 判断 name 能不能当 provider 的配置文件名。
//
// 规则本身住在 internal/provider（它才是 provider 文件的所有者）——
// web 只是复用，避免两处各写一份正则、日后改一处忘一处。
func validProviderName(name string) bool {
	return provider.ValidName(name)
}

// providerPath 把 provider 名解析成 providers 目录内的配置文件路径。
//
// 第二道防线：拼完之后复核路径确实落在 providers 目录**之内**——
// 正则已经挡住了分隔符，但"拼完再验一次"不花成本，且能挡住将来
// 有人放宽正则时引入的穿越。
func providerPath(providersDir, name string) (string, bool) {
	if providersDir == "" || !validProviderName(name) {
		return "", false
	}
	path := filepath.Join(providersDir, name+".json")

	absDir, err := filepath.Abs(providersDir)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(absPath, absDir+string(os.PathSeparator)) {
		return "", false
	}
	return path, true
}

// Claude 配置文件的标识（URL 路径里的 {id}）。
const (
	// ClaudeFileSettings 是 ~/.claude/settings.json。
	ClaudeFileSettings = "settings"
	// ClaudeFileMemory 是 ~/.claude/CLAUDE.md。
	ClaudeFileMemory = "claude-md"
)

// claudeFilePath 把文件标识映射到固定路径。
//
// **不接受客户端传来的路径**：标识只有这两个，映射在服务端写死。
// 想加第三个文件（比如用户级 commands/）时，在这里加一个常量即可——
// 但那是"再开一个洞"，得先想清楚。
func claudeFilePath(id, settingsPath, memoryPath string) (string, bool) {
	switch id {
	case ClaudeFileSettings:
		return settingsPath, settingsPath != ""
	case ClaudeFileMemory:
		return memoryPath, memoryPath != ""
	}
	return "", false
}
