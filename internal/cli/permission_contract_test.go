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
)

var (
	errNoFlag          = errors.New("help 输出中未找到 --permission-mode 选项")
	errNoChoices       = errors.New("--permission-mode 描述中未找到 (choices: ...)")
	errUnclosedChoices = errors.New("(choices: ... 缺少右括号")
	errEmptyChoices    = errors.New("(choices: ...) 中未解析出任何取值")
)

// 终止续行收集：下一个选项行形如 "--foo" 或 "-p, --print"。
var optionLineRe = regexp.MustCompile(`^-{1,2}[a-zA-Z]`)

// 提取 choices 括号内的每一个带引号取值。
var quotedRe = regexp.MustCompile(`"([^"]+)"`)

func TestContract_PermissionModesMatchClaudeCLI(t *testing.T) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("本机未安装 claude，跳过契约测试")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// claude --help 在部分版本走 stderr，因此合并两路输出。
	out, err := exec.CommandContext(ctx, claudePath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("执行 %s --help 失败: %v\n输出:\n%s", claudePath, err, out)
	}

	actual, err := parsePermissionModeChoices(string(out))
	if err != nil {
		t.Fatalf("解析 --permission-mode choices 失败: %v\n\n完整 help 输出:\n%s", err, out)
	}

	expected := append([]string(nil), ValidModes...)
	sort.Strings(expected)
	sort.Strings(actual)

	missing := diff(actual, expected) // CLI 有、项目没有
	extra := diff(expected, actual)   // 项目有、CLI 没有

	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("权限模式列表与 Claude Code CLI 不一致\n"+
			"  CLI  choices : %v\n"+
			"  项目 ValidModes: %v\n"+
			"  需要新增（CLI 有、项目无）: %v\n"+
			"  需要移除（项目有、CLI 无）: %v\n\n"+
			"修复位置：internal/cli/cli.go 的 ValidModes 与 internal/tui/tui.go 的 modeItems（两处都要改，并补上中文说明）。",
			actual, expected, missing, extra)
	}
}

// parsePermissionModeChoices 从 claude --help 输出中提取 --permission-mode 的合法取值。
//
// help 输出是按终端宽度折行的，目标片段形如：
//
//	--permission-mode <mode>    Permission mode to use for the session
//	                            (choices: "acceptEdits", "auto",
//	                            "bypassPermissions", "manual",
//	                            "dontAsk", "plan")
//
// 因此先把 --permission-mode 及其所有续行拼成一段，再在段内定位 choices 括号，
// 避免误吞到下一个选项（如 --plugin-dir）的描述里。
func parsePermissionModeChoices(help string) ([]string, error) {
	var block strings.Builder
	found := false
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if !found {
			if strings.HasPrefix(trimmed, "--permission-mode") {
				found = true
				block.WriteString(trimmed)
			}
			continue
		}
		if optionLineRe.MatchString(trimmed) {
			break // 已进入下一个选项
		}
		block.WriteString(" " + trimmed)
	}
	if !found {
		return nil, errNoFlag
	}

	seg := block.String()
	start := strings.Index(seg, "(choices:")
	if start == -1 {
		return nil, errNoChoices
	}
	end := strings.Index(seg[start:], ")")
	if end == -1 {
		return nil, errUnclosedChoices
	}

	var modes []string
	for _, m := range quotedRe.FindAllStringSubmatch(seg[start:start+end], -1) {
		modes = append(modes, m[1])
	}
	if len(modes) == 0 {
		return nil, errEmptyChoices
	}
	return modes, nil
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
