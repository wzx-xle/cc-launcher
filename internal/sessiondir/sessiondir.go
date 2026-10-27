// Package sessiondir 提供「session id → 工作目录」的反查能力。
//
// 背景（web-usage-stats-ui design.md §D6）：Claude Code 把每个会话的完整记录
// 写进 `~/.claude/projects/<编码cwd>/<session-id>.jsonl`，文件名就是 session id
// （与 telemetry.db 的 sessions.id 同一套 uuid），每条记录里带真实 `cwd` 字段。
//
// 纪律（spec/session-cwd-enrichment §"session cwd falls back to Claude Code
// session records"）：
//   - 只取顶层 `cwd` 字段；取到即停止解析
//   - **prompt / message 内容绝不落盘、落库或进内存**——本实现用
//     json.Decoder 的 token 流逐个跳过非 cwd 的值，不把整条记录反序列化进 map
//   - 目录名是编码后的 cwd，编码规则不唯一（`-` 与路径分隔符歧义），
//     **禁止**用目录名反推 cwd
//   - best-effort：文件缺失 / 读不到 / 无 cwd 字段 → 返回空串，不报错
package sessiondir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Resolver 按 session id 反查工作目录。
//
// 零值不可用——必须用 NewResolver 构造。
type Resolver struct {
	projectsDir string
}

// NewResolver 构造 Resolver，projectsDir 是 Claude Code 的会话目录
//（通常是 ~/.claude/projects）。
func NewResolver(projectsDir string) *Resolver {
	return &Resolver{projectsDir: projectsDir}
}

// DefaultProjectsDir 返回默认的 Claude Code 会话目录 ~/.claude/projects。
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// ProjectsDir 返回 resolver 使用的会话目录。
func (r *Resolver) ProjectsDir() string { return r.projectsDir }

// ResolveCwd 返回 sessionID 对应的工作目录；查不到返回空串。
//
// 定位方式：遍历 projectsDir 下的每个子目录，找 `<subdir>/<sessionID>.jsonl`。
// 命中后逐条解析该文件，**在第一个带顶层 `cwd` 字段的记录处停止**。
func (r *Resolver) ResolveCwd(sessionID string) string {
	if sessionID == "" || r == nil || r.projectsDir == "" {
		return ""
	}
	// session id 是 uuid；含路径分隔符 / 点号的一律拒绝（防目录穿越）。
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return ""
	}

	entries, err := os.ReadDir(r.projectsDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(r.projectsDir, e.Name(), sessionID+".jsonl")
		cwd, ok := readCwdFromSessionFile(path)
		if ok {
			return cwd
		}
	}
	return ""
}

// readCwdFromSessionFile 从会话记录文件里取第一条含顶层 cwd 的记录。
//
// 返回 (cwd, true) 表示命中；("", false) 表示文件缺失 / 解析失败 / 无 cwd。
func readCwdFromSessionFile(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	for {
		cwd, found, err := decodeObjectCwd(dec)
		if err != nil {
			// 文件读完（io.EOF）/ 损坏 / 非 JSON 行 → 到此为止。
			return "", false
		}
		if found {
			return cwd, true
		}
	}
}

// decodeObjectCwd 从 decoder 里读下一个顶层 JSON 对象，取其顶层 "cwd" 字段。
//
// 返回 (cwd, true, nil) 命中；("", false, nil) 该对象无 cwd 字段；
// (", false, err) 解析失败或已到 EOF。
//
// 关键点：非 cwd 的字段用 token 流**跳过**，不把值反序列化进内存——
// prompt 内容因此不会驻留在进程里。
func decodeObjectCwd(dec *json.Decoder) (string, bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", false, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return "", false, errNonObject
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", false, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return "", false, errNonObject
		}
		if key == "cwd" {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return "", false, err
			}
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				// cwd 不是字符串 → 视为无 cwd 字段，继续看下一条记录。
				continue
			}
			return s, true, nil
		}
		if err := skipValue(dec); err != nil {
			return "", false, err
		}
	}
	// 消费结尾的 '}'
	if _, err := dec.Token(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

// errNonObject 表示顶层不是 JSON 对象。
var errNonObject = &nonObjectError{}

type nonObjectError struct{}

func (e *nonObjectError) Error() string { return "顶层不是 JSON 对象" }

// skipValue 跳过 decoder 当前位置的一个 JSON 值（不解码其内容）。
//
// 用 token 流实现：标量读一个 token 即可；对象 / 数组按嵌套深度读到配对的
// 闭合符为止——全程不构造任何中间字符串或字节切片。
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // 标量
	}
	switch delim {
	case '{', '[':
		depth := 1
		for depth > 0 {
			t, err := dec.Token()
			if err != nil {
				return err
			}
			if d, ok := t.(json.Delim); ok {
				switch d {
				case '{', '[':
					depth++
				case '}', ']':
					depth--
				}
			}
		}
	}
	return nil
}
