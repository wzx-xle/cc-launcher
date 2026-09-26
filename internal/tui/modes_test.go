package tui

import (
	"sort"
	"strings"
	"testing"

	"cc-launcher/internal/cli"
)

// modeItems（TUI 展示）与 cli.ValidModes（命令行校验）是两份独立维护的列表，
// 历史上曾出现只改一处导致的漂移。这里锁死二者一致；
// 而 cli.ValidModes 与 Claude Code 本身的一致性由
// internal/cli/permission_contract_test.go（-tags contract）负责。
func TestModeItemsMatchCLIValidModes(t *testing.T) {
	got := make([]string, 0, len(modeItems))
	for _, it := range modeItems {
		got = append(got, it.mode)
	}
	want := append([]string(nil), cli.ValidModes...)

	sort.Strings(got)
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("TUI modeItems 与 cli.ValidModes 不一致\n  modeItems     : %v\n  cli.ValidModes: %v", got, want)
	}
}

// 每个模式都必须有非空中文说明——描述是用户做选择的唯一依据，空描述等于误导。
func TestModeItemsHaveDescriptions(t *testing.T) {
	for _, it := range modeItems {
		if strings.TrimSpace(it.desc) == "" {
			t.Errorf("模式 %q 缺少说明文案", it.mode)
		}
	}
}
