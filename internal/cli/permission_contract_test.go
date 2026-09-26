//go:build contract

// 契约测试：校验本项目的权限模式列表与本机 Claude Code CLI 实际接受的取值一致。
//
// 不参与常规 `go test ./...`，需显式启用：
//
//	go test -tags contract ./internal/cli/
//
// 本机没有 claude 命令时自动跳过，便于在无 Claude Code 的 CI 环境中安全执行。
package cli

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"cc-launcher/internal/modes"
)

var (
	errNoChoicesLine = errors.New("探测输出未找到 'Allowed choices are' 段")
)

// Claude Code 对未知 --permission-mode 值的错误信息形如：
//
//	error: option '--permission-mode <mode>' argument '<X>' is invalid. Allowed choices are A, B, C, ...
//
// 这是 CLI 解析器本身在运行时告诉我们合法值，比解析 --help 文本稳定得多：
// --help 文本会按终端宽度折行、可能改排版；而 "Allowed choices" 是单行、机器可读。
//
// 探测参数刻意用 '__cc_launcher_probe__' 这种绝不可能合法的字符串，确保触发错误路径；
// 不传其它旗标，避免触发无关校验（如 -p 需要 prompt、--resume 需要 session id 等）。
var choicesLineRe = regexp.MustCompile(`(?i)allowed choices are\s+(.+?)(?:\.\s*$|\.\s*\n)`)

// 提取 choices 后的逐项名称：逗号分隔、首尾允许空白。
var allowedItemRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*`)

func TestContract_PermissionModesMatchClaudeCLI(t *testing.T) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("本机未安装 claude，跳过契约测试")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 用一个绝不可能合法的模式名触发错误路径，从错误信息里拿真实 choices。
	// 不传其它旗标，避免触发无关校验。
	const probeMode = "__cc_launcher_probe__"
	out, err := exec.CommandContext(ctx, claudePath, "--permission-mode", probeMode).CombinedOutput()
	if err == nil {
		// 极不可能：claude 接受了我们的探针值。说明契约已破坏，列出我们有的让他验证。
		t.Fatalf("claude 接受了非法模式 %q，无法探测 choices；完整输出:\n%s", probeMode, out)
	}

	actual, err := parseAllowedChoices(string(out))
	if err != nil {
		t.Fatalf("解析 'Allowed choices' 失败: %v\n\n完整错误输出:\n%s", err, out)
	}

	expected := append([]string(nil), modes.Names()...)
	sort.Strings(expected)
	sort.Strings(actual)

	missing := diff(actual, expected) // CLI 有、项目没有
	extra := diff(expected, actual)   // 项目有、CLI 没有

	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("权限模式列表与 Claude Code CLI 不一致\n"+
			"  CLI  choices : %v\n"+
			"  项目 modes   : %v\n"+
			"  需要新增（CLI 有、项目无）: %v\n"+
			"  需要移除（项目有、CLI 无）: %v\n\n"+
			"修复位置：internal/modes/modes.go 的 all 切片（同时更新 Name 与 Description）。",
			actual, expected, missing, extra)
	}
}

// parseAllowedChoices 从 claude 探测输出中提取 'Allowed choices are X, Y, Z.'。
func parseAllowedChoices(out string) ([]string, error) {
	m := choicesLineRe.FindStringSubmatch(out)
	if m == nil {
		return nil, errNoChoicesLine
	}
	raw := strings.TrimSpace(m[1])
	// 掐掉可能的尾随句点
	raw = strings.TrimRight(raw, ".")

	seen := make(map[string]struct{})
	var out2 []string
	for _, tok := range allowedItemRe.FindAllString(raw, -1) {
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		out2 = append(out2, tok)
	}
	if len(out2) == 0 {
		return nil, errNoChoicesLine
	}
	return out2, nil
}

// diff 返回在 a 中但不在 b 中的元素。
func diff(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	var out []string
	for _, v := range a {
		if _, ok := set[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}