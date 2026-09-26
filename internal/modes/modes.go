// Package modes 定义 Claude Code --permission-mode 的合法取值与中文描述，
// 是 cli 与 tui 共享的单一事实源。
//
// 漂移由两层契约测试阻止：
//   - 本包 modes_test.go 的常规单测（锁 names / descriptions 自身一致且非空）
//   - internal/cli/permission_contract_test.go（-tags contract，对表本机 claude CLI）
//
// 新增 / 删除 / 重命名权限模式时：只改本文件的 all 切片，然后跑
// `go test ./... && ./build.sh contract` 校验两侧同步。
package modes

// Mode 一个权限模式。
type Mode struct {
	Name        string
	Description string
}

// Names 按官方 choices 顺序返回所有合法权限模式的名称。
func Names() []string {
	out := make([]string, len(all))
	for i, m := range all {
		out[i] = m.Name
	}
	return out
}

// All 返回所有权限模式（含描述）。TUI 展示与 README 文档都用这份。
func All() []Mode {
	out := make([]Mode, len(all))
	copy(out, all)
	return out
}

// ByName 按名称查找；找不到时返回零值与 false。
func ByName(name string) (Mode, bool) {
	for _, m := range all {
		if m.Name == name {
			return m, true
		}
	}
	return Mode{}, false
}

// all 是单一事实源。顺序与 Claude Code --help 中 (choices: ...) 列出顺序一致。
var all = []Mode{
	{"manual", "标准行为，危险操作逐次询问确认（Claude Code 内部 default 的别名）"},
	{"acceptEdits", "自动接受文件编辑操作，其余操作仍询问"},
	{"auto", "由模型分类器自动批准/拒绝权限请求"},
	{"bypassPermissions", "跳过所有权限检查（危险，仅建议在隔离沙箱使用）"},
	{"dontAsk", "不弹权限询问；未被 allow 规则预先授权的操作直接拒绝"},
	{"plan", "计划模式，只分析不实际执行工具"},
}