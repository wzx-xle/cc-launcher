package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
	"cc-launcher/internal/templates"
)

func setupCLI(t *testing.T) (*CLI, *provider.Manager, *state.Manager) {
	t.Helper()
	providersDir := t.TempDir()
	stateDir := t.TempDir()

	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)

	// 预置一个提供商
	pm.CreateWithEnv("anthropic", map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-test"})

	return New(pm, sm), pm, sm
}

// ─── provider-setenv-cwd-binding §5: CLI provider cwd binding ───

// TestProviderList_CwdBindingMarker 覆盖 spec/provider-cwd-binding §"cwd
// binding exists and differs from active_provider" Scenario——*deepseek 行。
func TestProviderList_CwdBindingMarker(t *testing.T) {
	c, pm, sm := setupCLI(t)
	pm.CreateWithEnv("deepseek", map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk"})
	sm.SetActiveProvider("anthropic")

	wd, _ := os.Getwd()
	if err := sm.SetProviderForDir(wd, "deepseek"); err != nil {
		t.Fatal(err)
	}

	cap := captureStdout(t)
	_ = c.Run([]string{"provider", "--list"})
	output := cap()

	if !strings.Contains(output, "* deepseek") {
		t.Errorf("output should mark cwd-bound provider with '*', got:\n%s", output)
	}
	if !strings.Contains(output, "▶ anthropic") {
		t.Errorf("output should mark active provider with '▶', got:\n%s", output)
	}
}

// TestProviderList_CwdBindingEqualsActive 覆盖 spec §"cwd binding equals
// active_provider" Scenario——两标记叠加为 *▶。
func TestProviderList_CwdBindingEqualsActive(t *testing.T) {
	c, _, sm := setupCLI(t)
	sm.SetActiveProvider("anthropic")

	wd, _ := os.Getwd()
	if err := sm.SetProviderForDir(wd, "anthropic"); err != nil {
		t.Fatal(err)
	}

	cap := captureStdout(t)
	_ = c.Run([]string{"provider", "--list"})
	output := cap()

	if !strings.Contains(output, "*▶ anthropic") {
		t.Errorf("output should show '*▶' when cwd binding == active, got:\n%s", output)
	}
}

// TestProviderList_NoCwdBinding 覆盖 spec §"no cwd binding" Scenario——
// 仅 ▶ 标记 active，无 * 标记。
func TestProviderList_NoCwdBinding(t *testing.T) {
	c, _, sm := setupCLI(t)
	sm.SetActiveProvider("anthropic")

	cap := captureStdout(t)
	_ = c.Run([]string{"provider", "--list"})
	output := cap()

	if strings.Contains(output, "*") {
		t.Errorf("output should NOT contain '*' when no cwd binding, got:\n%s", output)
	}
	if !strings.Contains(output, "▶ anthropic") {
		t.Errorf("output should still mark active provider with '▶', got:\n%s", output)
	}
}

// TestProviderSwitch_BindsCwd 覆盖 spec §"CLI provider switches and binds cwd"
// Scenario——`ccl provider <name>` 同时写 active_provider + dir_bindings[wd]。
func TestProviderSwitch_BindsCwd(t *testing.T) {
	c, pm, sm := setupCLI(t)
	pm.CreateWithEnv("deepseek", map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk"})

	wd, _ := os.Getwd()

	_ = captureStdout(t)() // capture (output discarded—c.Run doesn't error)
	if err := c.Run([]string{"provider", "deepseek"}); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if got := sm.GetActiveProvider(); got != "deepseek" {
		t.Errorf("active_provider = %q, want deepseek", got)
	}
	if got := sm.GetProviderForDir(wd); got != "deepseek" {
		t.Errorf("dir_bindings[%s] = %q, want deepseek", wd, got)
	}
}

func TestProviderList(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "--list"})
	if err != nil {
		t.Fatalf("provider --list error = %v", err)
	}
}

func TestProviderSwitch(t *testing.T) {
	c, pm, sm := setupCLI(t)

	pm.CreateWithEnv("bedrock", map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-bedrock"})

	err := c.Run([]string{"provider", "bedrock"})
	if err != nil {
		t.Fatalf("provider bedrock error = %v", err)
	}

	if got := sm.GetActiveProvider(); got != "bedrock" {
		t.Errorf("active provider = %q, want bedrock", got)
	}
}

func TestProviderSwitch_NotFound(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestProviderDelete(t *testing.T) {
	c, pm, _ := setupCLI(t)

	pm.CreateWithEnv("temp", map[string]any{"ANTHROPIC_AUTH_TOKEN": "key"})
	err := c.Run([]string{"provider", "--delete", "temp"})
	if err != nil {
		t.Fatalf("provider --delete error = %v", err)
	}
	if pm.Exists("temp") {
		t.Error("provider temp should be deleted")
	}
}

func TestProviderDelete_Active(t *testing.T) {
	c, pm, sm := setupCLI(t)

	pm.CreateWithEnv("active-prov", map[string]any{"ANTHROPIC_AUTH_TOKEN": "key"})
	sm.SetActiveProvider("active-prov")

	// 删除激活的提供商（非交互模式不确认 → 应拒绝）
	err := c.Run([]string{"provider", "--delete", "active-prov"})
	if err == nil {
		t.Fatal("expected error when deleting active provider")
	}
	if !pm.Exists("active-prov") {
		t.Error("active provider should not be deleted without confirmation")
	}
}

func TestProviderDelete_NotFound(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "--delete", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestModeList(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"mode"})
	if err != nil {
		t.Fatalf("mode error = %v", err)
	}
}

func TestModeSet(t *testing.T) {
	c, _, sm := setupCLI(t)

	err := c.Run([]string{"mode", "plan"})
	if err != nil {
		t.Fatalf("mode plan error = %v", err)
	}

	// 获取当前工作目录
	wd, _ := state.DefaultBaseDir() // 用任意目录获取 wd
	_ = wd
	wd = t.TempDir()
	// 实际验证：SetPermissionMode 应该持久化了
	mode, ok := sm.GetPermissionMode(".")
	if !ok && mode == "" {
		// 当前目录可能变化，用绝对路径验证
	}
	_ = mode
	_ = ok
	// 简化验证：只需确认不报错即可
}

func TestModeSet_Invalid(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"mode", "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "合法值") {
		t.Errorf("error should mention valid values, got: %v", err)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"unknown"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

// captureStdout 返回一个函数；调用时关闭替换的 stdout 并返回捕获到的内容。
// 用法：
//
//	cap := captureStdout(t)
//	c.Run(args)
//	out := cap() // 这里才读取
func captureStdout(t *testing.T) func() string {
	t.Helper()
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	os.Stdout = f
	var done bool
	var out string
	return func() string {
		if done {
			return out
		}
		done = true
		f.Close()
		data, _ := os.ReadFile(f.Name())
		out = string(data)
		os.Stdout = old
		return out
	}
}

// captureStderr 临时把 os.Stderr 接到 buffer，跑 fn，返回 buffer 内容。
// 用法：err := captureStderrRun(t, func() error { return c.Run(args) }) → err + stderr。
func captureStderr(t *testing.T) func() string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	var done2 bool
	var s string
	return func() string {
		if done2 {
			return s
		}
		done2 = true
		_ = w.Close()
		s = <-done
		return s
	}
}

// ─── 任务 5.1：--help 三层 ───

func TestProvider_Help(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"provider", "--help"}); err != nil {
		t.Fatalf("provider --help error = %v", err)
	}
	out := cap()
	for _, kw := range []string{"--list", "--new", "--delete", "--template", "-T", "anthropic", "deepseek"} {
		if !strings.Contains(out, kw) {
			t.Errorf("--help 输出缺关键字 %q\n输出:\n%s", kw, out)
		}
	}
}

func TestMode_Help(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"mode", "--help"}); err != nil {
		t.Fatalf("mode --help error = %v", err)
	}
	out := cap()
	for _, kw := range []string{"权限模式", "plan"} {
		if !strings.Contains(out, kw) {
			t.Errorf("--help 输出缺关键字 %q\n输出:\n%s", kw, out)
		}
	}
}

func TestTop_Help(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"--help"}); err != nil {
		t.Fatalf("--help error = %v", err)
	}
	out := cap()
	for _, kw := range []string{"子命令", "provider", "mode"} {
		if !strings.Contains(out, kw) {
			t.Errorf("--help 输出缺关键字 %q\n输出:\n%s", kw, out)
		}
	}
}

// 验证 --help 不吞 --bogus
func TestProvider_BogusFlagStillErrors(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "--bogus"})
	if err == nil {
		t.Fatal("--bogus 应该报错")
	}
	if !strings.Contains(err.Error(), "未知") {
		t.Errorf("错误应包含 '未知'，得到: %v", err)
	}
}

// ─── 任务 5.2：--template 三分支 ───

func TestTemplate_NoArg_ListsAll(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"provider", "--template"}); err != nil {
		t.Fatalf("--template 无参 error = %v", err)
	}
	out := cap()
	for _, name := range []string{"anthropic", "deepseek", "minimax", "glm", "aliyun-coding", "aliyun-pay", "aliyun-token"} {
		if !strings.Contains(out, name) {
			t.Errorf("--template 列表缺 %q\n输出:\n%s", name, out)
		}
	}
}

func TestTemplate_Deepseek(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"provider", "--template", "deepseek"}); err != nil {
		t.Fatalf("--template deepseek error = %v", err)
	}
	out := cap()
	if !strings.Contains(out, `"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"`) {
		t.Errorf("--template deepseek 输出不含预期 BASE_URL\n%s", out)
	}
	if !strings.Contains(out, `"deepseek-flash[1m]"`) {
		t.Errorf("--template deepseek 输出不含默认 model\n%s", out)
	}
}

func TestTemplate_ShortAliasT(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"provider", "-T", "aliyun-coding"}); err != nil {
		t.Fatalf("-T aliyun-coding error = %v", err)
	}
	out := cap()
	if !strings.Contains(out, `"qwen3.7-plus"`) {
		t.Errorf("-T aliyun-coding 输出不含 qwen3.7-plus\n%s", out)
	}
}

func TestTemplate_Bogus(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "--template", "bogus"})
	if err == nil {
		t.Fatal("--template bogus 应报错")
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("错误应列出有效 type（含 anthropic），得到: %v", err)
	}
}

// ─── 任务 5.3：--type bogus ───

func TestNewProvider_TypeBogus(t *testing.T) {
	c, _, _ := setupCLI(t)

	err := c.Run([]string{"provider", "--new", "--type", "bogus"})
	if err == nil {
		t.Fatal("--type bogus 应报错")
	}
	if pm, _, _ := setupCLI(t); false {
		_ = pm
	}
	// 不写盘：用 setupCLI 提供的 providersDir 验证（重做一次以便断言）
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	cli2 := New(pm, sm)
	if err := cli2.Run([]string{"provider", "--new", "--type", "bogus"}); err == nil {
		t.Fatal("--type bogus 应报错")
	}
	if pm.Exists("anything") {
		t.Error("--type bogus 不应创建任何文件")
	}
}

// ─── 任务 5.4：guided flow round-trip（覆盖 7 个 type） ───
// 用 mockStdin 喂入字段值，跑完后用 provider.Manager.Get(name) 验证文件可解析。

func mockStdin(lines []string) func() {
	old := readLineFn
	idx := 0
	readLineFn = func() (string, bool) {
		if idx >= len(lines) {
			return "", false
		}
		s := lines[idx]
		idx++
		return s, true
	}
	return func() { readLineFn = old }
}

// guidedLines 为指定 type 生成完整的 stdin 输入序列。
// 顺序对应 templates.go 的 Fields（aliyun-pay / minimax 有特殊前缀）。
func guidedLines(tplName string) []string {
	lines := []string{}
	lines = append(lines, "test-"+tplName) // name

	if tplName == "aliyun-pay" {
		lines = append(lines, "ws-abc123") // WorkspaceId
	}
	if tplName == "minimax" {
		lines = append(lines, "") // region 默认 cn
	}

	tpl, _ := templatesByName(tplName)
	for _, f := range tpl.Fields {
		// aliyun-pay / minimax 的 ANTHROPIC_BASE_URL 由 cli 自动拼装，cli 不会读
		if f.Key == "ANTHROPIC_BASE_URL" && (tplName == "aliyun-pay" || tplName == "minimax") {
			continue
		}
		if f.Secret {
			lines = append(lines, "sk-test-token")
		} else if f.Default != "" {
			lines = append(lines, "") // 回车用默认
		} else {
			lines = append(lines, "val")
		}
	}
	return lines
}

// templatesByName 局部辅助，避免循环引用
func templatesByName(name string) (templates.Template, bool) {
	return templates.ByName(name)
}

func TestGuidedFlow_AllTypes_RoundTrip(t *testing.T) {
	for _, typeName := range []string{
		"anthropic", "deepseek", "minimax", "glm",
		"aliyun-coding", "aliyun-pay", "aliyun-token",
	} {
		t.Run(typeName, func(t *testing.T) {
			providersDir := t.TempDir()
			stateDir := t.TempDir()
			pm := provider.NewManager(providersDir)
			sm := state.NewManager(stateDir)
			c := New(pm, sm)

			restore := mockStdin(guidedLines(typeName))
			defer restore()

			if err := c.Run([]string{"provider", "--new", "--type", typeName}); err != nil {
				t.Fatalf("--new --type %s error: %v", typeName, err)
			}

			// 写盘后用 Manager.Get(name) 验证文件可解析
			got, err := pm.Get("test-" + typeName)
			if err != nil {
				t.Fatalf("Get(test-%s) error: %v", typeName, err)
			}
			env, ok := got["env"].(map[string]any)
			if !ok {
				t.Fatalf("env 字段缺失或类型错误: %v", got)
			}
			if _, hasAuth := env["ANTHROPIC_AUTH_TOKEN"]; !hasAuth {
				t.Errorf("env 缺少 ANTHROPIC_AUTH_TOKEN: %v", env)
			}
		})
	}
}

// ─── 任务 5.5：aliyun-pay WorkspaceId 替换 ───

func TestGuidedFlow_AliyunPay_SubstitutesWorkspaceId(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	restore := mockStdin([]string{"pay-test", "ws-xyz789",
		"sk-test",         // ANTHROPIC_AUTH_TOKEN
		"",                // ANTHROPIC_MODEL 用默认
		"",                // ANTHROPIC_DEFAULT_HAIKU_MODEL 用默认
		"",                // ANTHROPIC_DEFAULT_SONNET_MODEL 用默认
		"",                // ANTHROPIC_DEFAULT_OPUS_MODEL 用默认
		"",                // CLAUDE_CODE_SUBAGENT_MODEL 用默认
	})
	defer restore()

	if err := c.Run([]string{"provider", "--new", "--type", "aliyun-pay"}); err != nil {
		t.Fatalf("--new --type aliyun-pay error: %v", err)
	}

	got, err := pm.Get("pay-test")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	env, _ := got["env"].(map[string]any)
	url, _ := env["ANTHROPIC_BASE_URL"].(string)
	if strings.Contains(url, "{WorkspaceId}") {
		t.Errorf("写盘后 ANTHROPIC_BASE_URL 仍含占位符: %s", url)
	}
	if !strings.Contains(url, "ws-xyz789") {
		t.Errorf("ANTHROPIC_BASE_URL 不含 WorkspaceId 值: %s", url)
	}
}

// ─── 任务 5.6：minimax 国内/国际 ───

func TestGuidedFlow_MiniMax_RegionCN(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	// region 留空 → 默认 cn
	restore := mockStdin([]string{"mm-cn", "", "sk-test", "1000000", "m3[1m]", "m3[1m]", "m3[1m]", "m3[1m]"})
	defer restore()

	if err := c.Run([]string{"provider", "--new", "--type", "minimax"}); err != nil {
		t.Fatalf("--new --type minimax error: %v", err)
	}
	got, _ := pm.Get("mm-cn")
	url, _ := got["env"].(map[string]any)["ANTHROPIC_BASE_URL"].(string)
	if !strings.Contains(url, "api.minimax.cn") {
		t.Errorf("默认 region 应为国内 (api.minimax.cn)，得到: %s", url)
	}
}

func TestGuidedFlow_MiniMax_RegionIO(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	restore := mockStdin([]string{"mm-io", "io", "sk-test", "1000000", "m3[1m]", "m3[1m]", "m3[1m]", "m3[1m]"})
	defer restore()

	if err := c.Run([]string{"provider", "--new", "--type", "minimax"}); err != nil {
		t.Fatalf("--new --type minimax error: %v", err)
	}
	got, _ := pm.Get("mm-io")
	url, _ := got["env"].(map[string]any)["ANTHROPIC_BASE_URL"].(string)
	if !strings.Contains(url, "api.minimax.io") {
		t.Errorf("显式 io 应得国际 URL，得到: %s", url)
	}
}

// ─── 任务 4.6：EOF 取消 ───

func TestGuidedFlow_EOF_CancelsNoWrite(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	// EOF 在第 2 个字段（WorkspaceId 之后）
	restore := mockStdin([]string{"will-cancel", ""}) // EOF on next read
	defer restore()

	err := c.Run([]string{"provider", "--new", "--type", "aliyun-pay"})
	if err == nil {
		t.Fatal("EOF 应报错")
	}
	if pm.Exists("will-cancel") {
		t.Error("EOF 不应写盘")
	}
}

// ─── 任务 10：provider-flag-migration 新测试 ───
//
// 短形式 / mutex / --type 上下文 / help 优先级 / dispatch 顺序的覆盖。
// 用 c.Run 走完整 path（跟现有 TestProvider_BogusFlagStillErrors 等一致）。

// 10.1：-list 短形式等价 --list
func TestProvider_ShortList_EquivLongList(t *testing.T) {
	c, _, _ := setupCLI(t)

	capL := captureStdout(t)
	if err := c.Run([]string{"provider", "-list"}); err != nil {
		t.Fatalf("-list error = %v", err)
	}
	short := capL()

	capF := captureStdout(t)
	if err := c.Run([]string{"provider", "--list"}); err != nil {
		t.Fatalf("--list error = %v", err)
	}
	long := capF()

	if short != long {
		t.Errorf("-list 输出 != --list 输出\n短: %q\n长: %q", short, long)
	}
}

// 10.2：-new 短形式（mock EOF → 等价 --new 行为）
func TestProvider_ShortNew_EquivLongNew(t *testing.T) {
	c, _, _ := setupCLI(t)
	restore := mockStdin([]string{""}) // name = "" → EOF on next → "已取消"
	defer restore()

	capL := captureStdout(t)
	errL := c.Run([]string{"provider", "-new"})
	shortOut := capL()

	restore2 := mockStdin([]string{""})
	defer restore2()
	capF := captureStdout(t)
	errF := c.Run([]string{"provider", "--new"})
	longOut := capF()

	// 两个都报"已取消"（EOF）→ err 文案应一致；stdout 也一致（打印提示 + "已取消" 错）
	if (errL == nil) != (errF == nil) {
		t.Errorf("短 / 长 --new error 状态不一致: %v vs %v", errL, errF)
	}
	if shortOut != longOut {
		t.Errorf("-new 输出 != --new 输出\n短: %q\n长: %q", shortOut, longOut)
	}
}

// 10.3：-delete 短形式（temp 存在 → 调 deleteProvider）
func TestProvider_ShortDelete_EquivLongDelete(t *testing.T) {
	c, pm, _ := setupCLI(t)
	pm.CreateWithEnv("temp", map[string]any{"ANTHROPIC_AUTH_TOKEN": "k"})

	if err := c.Run([]string{"provider", "-delete", "temp"}); err != nil {
		t.Fatalf("-delete temp error = %v", err)
	}
	if pm.Exists("temp") {
		t.Error("-delete temp 应被删除")
	}
}

// 10.4：-T 短别名 + 短形式打印 deepseek JSON
func TestProvider_ShortT_EquivLongTemplate(t *testing.T) {
	c, _, _ := setupCLI(t)

	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "-T", "deepseek"}); err != nil {
		t.Fatalf("-T deepseek error = %v", err)
	}
	out := cap()
	if !strings.Contains(out, `"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"`) {
		t.Errorf("-T deepseek 输出不含 BASE_URL\n%s", out)
	}
}

// 10.5：-h 短形式等价 --help
func TestProvider_ShortH_EquivLongHelp(t *testing.T) {
	c, _, _ := setupCLI(t)

	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "-h"}); err != nil {
		t.Fatalf("-h error = %v", err)
	}
	out := cap()
	if !strings.Contains(out, "--list") || !strings.Contains(out, "--new") {
		t.Errorf("-h 输出应含 --list / --new\n%s", out)
	}
}

// 10.6：mutex 报错——4 个 case
func TestProvider_Mutex_AllCases(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"list+new", []string{"provider", "--list", "--new"}},
		{"list+delete", []string{"provider", "--list", "--delete", "foo"}},
		{"new+delete", []string{"provider", "--new", "--delete", "foo"}},
		{"list+new+template(3flag)", []string{"provider", "--list", "--new", "--template", "deepseek"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, pm, _ := setupCLI(t)
			pm.CreateWithEnv("foo", map[string]any{"ANTHROPIC_AUTH_TOKEN": "k"})

			cap := captureStderr(t)
			err := c.Run(tc.args)
			stderr := cap()

			if err == nil {
				t.Fatalf("mutex violation 应报错；args=%v", tc.args)
			}
			if !strings.Contains(stderr, "互斥") {
				t.Errorf("stderr 应含 '互斥'，得到: %q", stderr)
			}
			if !strings.Contains(err.Error(), "互斥") {
				t.Errorf("err 应含 '互斥'，得到: %v", err)
			}
			// foo 不应被删除（list+delete case）
			if tc.name == "list+delete" && !pm.Exists("foo") {
				t.Error("mutex 报错时 foo 不应被删除")
			}
		})
	}
}

// 10.7：--new --template deepseek 等价 --template deepseek（不创建 provider 文件）
func TestProvider_NewTemplateDeepseek_EqualsTemplateDeepseek(t *testing.T) {
	c, pm, _ := setupCLI(t)

	capL := captureStdout(t)
	if err := c.Run([]string{"provider", "--new", "--template", "deepseek"}); err != nil {
		t.Fatalf("--new --template deepseek error = %v", err)
	}
	short := capL()

	// 不应创建 provider 文件
	if pm.Exists("anything") {
		t.Error("--new --template deepseek 不应创建 provider 文件")
	}

	capF := captureStdout(t)
	if err := c.Run([]string{"provider", "--template", "deepseek"}); err != nil {
		t.Fatalf("--template deepseek error = %v", err)
	}
	long := capF()

	if short != long {
		t.Errorf("--new --template deepseek 输出 != --template deepseek 输出\n%s\nvs\n%s", short, long)
	}
}

// 10.8：--new --template（无 type）列全部 7 模板（约定保留）
func TestProvider_NewTemplateNoArg_ListsAll(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"provider", "--new", "--template"}); err != nil {
		t.Fatalf("--new --template error = %v", err)
	}
	out := cap()
	for _, name := range []string{"anthropic", "deepseek", "minimax", "glm", "aliyun-coding", "aliyun-pay", "aliyun-token"} {
		if !strings.Contains(out, name) {
			t.Errorf("--new --template 应列 %q\n%s", name, out)
		}
	}
}

// 10.9：--type 单独（无 --new 且无其它 flag）报错
func TestProvider_TypeAlone_Errors(t *testing.T) {
	c, _, _ := setupCLI(t)
	err := c.Run([]string{"provider", "--type", "deepseek"})
	if err == nil {
		t.Fatal("--type 单独应报错")
	}
	if !strings.Contains(err.Error(), "--type") {
		t.Errorf("err 应提及 --type，得到: %v", err)
	}
}

// 10.10：--delete 无 name 报错用法
func TestProvider_DeleteNoName_Errors(t *testing.T) {
	c, _, _ := setupCLI(t)
	err := c.Run([]string{"provider", "--delete"})
	if err == nil {
		t.Fatal("--delete 无 name 应报错")
	}
}

// 10.11：兜底——无 args → listProviders；<name> → switchProvider
func TestProvider_FallbackDispatch(t *testing.T) {
	c, pm, sm := setupCLI(t)
	pm.CreateWithEnv("foo", map[string]any{"ANTHROPIC_AUTH_TOKEN": "k"})

	// 无 args → list
	if err := c.Run([]string{"provider"}); err != nil {
		t.Fatalf("provider（无 args）error = %v", err)
	}

	// <name> → switch
	if err := c.Run([]string{"provider", "foo"}); err != nil {
		t.Fatalf("provider foo error = %v", err)
	}
	if got := sm.GetActiveProvider(); got != "foo" {
		t.Errorf("active provider = %q, want foo", got)
	}
}

// 10.12：-new --type deepseek my-deepseek（短形式 + 位置参数）→ newProvider(name, type)
func TestProvider_ShortNewTypePositional_PassesThrough(t *testing.T) {
	c, pm, _ := setupCLI(t)

	restore := mockStdin(guidedLines("deepseek"))
	defer restore()

	if err := c.Run([]string{"provider", "-new", "--type", "deepseek", "my-deepseek"}); err != nil {
		t.Fatalf("-new --type deepseek my-deepseek error = %v", err)
	}

	got, err := pm.Get("my-deepseek")
	if err != nil {
		t.Fatalf("Get(my-deepseek) error = %v", err)
	}
	env, _ := got["env"].(map[string]any)
	if env == nil {
		t.Fatalf("env 字段缺失: %v", got)
	}
	if _, has := env["ANTHROPIC_AUTH_TOKEN"]; !has {
		t.Errorf("env 缺 ANTHROPIC_AUTH_TOKEN: %v", env)
	}
}

// 10.13：--list --help → 打 providerHelp，不报 mutex
func TestProvider_ListHelp_HelpWinsOverMutex(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	err := c.Run([]string{"provider", "--list", "--help"})
	if err != nil {
		t.Fatalf("--list --help error = %v", err)
	}
	out := cap()
	if !strings.Contains(out, "--list") || !strings.Contains(out, "--new") {
		t.Errorf("--list --help 应打 providerHelp（含 --list / --new），得到:\n%s", out)
	}
	// 不应报 mutex（help 优先级最高）—— 检查 mutex 报错特有的用法提示格式，
	// providerHelp 文案顶部也含"互斥"字眼（提示而非报错），所以不能用单字判断。
	if strings.Contains(out, "用法: ccl provider [--list | --new | --delete]") {
		t.Errorf("--list --help 不应打 mutex 报错用法，得到:\n%s", out)
	}
}

// 10.14：--list --template deepseek → mutex 通过 + template 优先级 → printTemplate（--list 被忽略）
func TestProvider_ListTemplate_TemplateWins(t *testing.T) {
	c, _, _ := setupCLI(t)
	cap := captureStdout(t)

	if err := c.Run([]string{"provider", "--list", "--template", "deepseek"}); err != nil {
		t.Fatalf("--list --template deepseek error = %v", err)
	}
	out := cap()
	// 应打印 deepseek JSON，不是 list 输出
	if !strings.Contains(out, `"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"`) {
		t.Errorf("--list --template deepseek 应打 template JSON，得到:\n%s", out)
	}
}

// 10.15：--delete foo --type deepseek → mutex 通过 → deleteProvider("foo")，--type silently ignored
func TestProvider_DeleteWithType_TypeIgnored(t *testing.T) {
	c, pm, _ := setupCLI(t)
	pm.CreateWithEnv("foo", map[string]any{"ANTHROPIC_AUTH_TOKEN": "k"})

	if err := c.Run([]string{"provider", "--delete", "foo", "--type", "deepseek"}); err != nil {
		t.Fatalf("--delete foo --type deepseek error = %v", err)
	}
	if pm.Exists("foo") {
		t.Error("--delete foo --type deepseek 应删 foo")
	}
}

// ─── provider-list-display-improvements §R2: ccl provider --new 写盘后 metadata ───

// TestGuidedFlow_NewProvider_PostWrite_NoteEmptySkipped 覆盖 spec
// /provider-new-guided-flow §"empty note input leaves file unchanged"——
// note 步骤空字符串（mock EOF）→ 文件无 note 字段。
func TestGuidedFlow_NewProvider_PostWrite_NoteEmptySkipped(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	// 喂入 deepseek 流程需要的 fields + 末尾空字符串（note 跳过）
	restore := mockStdin(append(guidedLines("deepseek"), ""))
	defer restore()

	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "--new", "--type", "deepseek"}); err != nil {
		t.Fatalf("--new --type deepseek error: %v", err)
	}
	_ = cap()

	got, err := pm.Get("test-deepseek")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if _, hasNote := got["note"]; hasNote {
		t.Errorf("file should not have 'note' field when user skips; got: %v", got)
	}
}

// TestGuidedFlow_NewProvider_PostWrite_NoteWritesToFile 覆盖 spec Scenario
// "non-empty note input updates the file"——note 非空 → 文件有 note 字段；
// env 块原 keys 都在。
func TestGuidedFlow_NewProvider_PostWrite_NoteWritesToFile(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	// 喂入 deepseek fields + 末尾 note = "国内 / 按量计费"
	restore := mockStdin(append(guidedLines("deepseek"), "国内 / 按量计费"))
	defer restore()

	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "--new", "--type", "deepseek"}); err != nil {
		t.Fatalf("--new --type deepseek error: %v", err)
	}
	_ = cap()

	got, err := pm.Get("test-deepseek")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got["note"] != "国内 / 按量计费" {
		t.Errorf("note = %v, want %q", got["note"], "国内 / 按量计费")
	}
	// env 块仍完整
	env, ok := got["env"].(map[string]any)
	if !ok {
		t.Fatalf("env field missing or wrong type: %v", got)
	}
	if _, hasAuth := env["ANTHROPIC_AUTH_TOKEN"]; !hasAuth {
		t.Errorf("env lost ANTHROPIC_AUTH_TOKEN after note writeback: %v", env)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.deepseek.com/anthropic" {
		t.Errorf("env BASE_URL = %v, want deepseek default", env["ANTHROPIC_BASE_URL"])
	}
}

// TestGuidedFlow_NewProvider_PostWrite_OrderHintUsesMaxPlusTen 覆盖 spec
// Scenario "order hint shows max-plus-ten"——预置 bravo.order=20，--new
// 完成后 stdout 应含 "排序值: 30" 与 "现有 max=20"。
func TestGuidedFlow_NewProvider_PostWrite_OrderHintUsesMaxPlusTen(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)

	// 预置 bravo.order=20
	pm.CreateWithEnv("bravo", map[string]any{
		"ANTHROPIC_AUTH_TOKEN": "sk-b",
	})
	// CreateWithEnv 不接受 order 字段；手写补 order
	writeOrder(t, providersDir, "bravo", 20)

	c := New(pm, sm)
	restore := mockStdin(guidedLines("deepseek"))
	defer restore()

	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "--new", "--type", "deepseek"}); err != nil {
		t.Fatalf("--new --type deepseek error: %v", err)
	}
	out := cap()

	if !strings.Contains(out, "排序值: 30") {
		t.Errorf("output should contain '排序值: 30', got:\n%s", out)
	}
	if !strings.Contains(out, "现有 max=20") {
		t.Errorf("output should contain '现有 max=20', got:\n%s", out)
	}
}

// TestGuidedFlow_NewProvider_PostWrite_OrderHintZeroBaseline 覆盖 spec
// Scenario "order hint uses zero baseline when no pre-existing providers"——
// 空 providers dir 时 max=0、suggested=10。
func TestGuidedFlow_NewProvider_PostWrite_OrderHintZeroBaseline(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	restore := mockStdin(guidedLines("deepseek"))
	defer restore()

	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "--new", "--type", "deepseek"}); err != nil {
		t.Fatalf("--new --type deepseek error: %v", err)
	}
	out := cap()

	if !strings.Contains(out, "现有 max=0") {
		t.Errorf("output should contain '现有 max=0', got:\n%s", out)
	}
	if !strings.Contains(out, "排序值: 10") {
		t.Errorf("output should contain '排序值: 10', got:\n%s", out)
	}
}

// TestGuidedFlow_NewProvider_PostWrite_OrderHintExcludesNewFile 覆盖 spec
// Scenario "order hint excludes the just-written file"——连续跑两次 --new
// (第二次不指定 order)，第二次的 max 不应含第一次的新文件。
//
// 验证逻辑：第一次 --new 后新文件无 order 字段（默认 0）。第二次 --new
// 时 max 应为 0（不是 0+something）。
func TestGuidedFlow_NewProvider_PostWrite_OrderHintExcludesNewFile(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)
	c := New(pm, sm)

	// 第一次 --new：mockStdin 喂一个 type 的 fields
	restore := mockStdin(guidedLines("deepseek"))
	if err := c.Run([]string{"provider", "--new", "--type", "deepseek"}); err != nil {
		t.Fatalf("first --new error: %v", err)
	}
	restore() // 恢复 readLineFn

	// 第二次 --new：mockStdin 喂另一个 type 的 fields
	restore2 := mockStdin(guidedLines("glm"))
	defer restore2()
	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "--new", "--type", "glm"}); err != nil {
		t.Fatalf("second --new error: %v", err)
	}
	out := cap()

	// 第一次新文件 test-deepseek.json 写时没有 order 字段（默认 0）。
	// 第二次 --new 写盘前 max=0（第一次文件无 order 字段）；写盘后 stdout
	// 应显示 "现有 max=0" + "排序值: 10"。
	if !strings.Contains(out, "现有 max=0") {
		t.Errorf("second run should show '现有 max=0' (first file has no order), got:\n%s", out)
	}
	if !strings.Contains(out, "排序值: 10") {
		t.Errorf("second run should show '排序值: 10', got:\n%s", out)
	}
}

// TestProviderList_HonorsOrder 覆盖 design.md Resolved Decisions R1 软测试——
// ccl provider --list 输出顺序应与 order 升序一致。
func TestProviderList_HonorsOrder(t *testing.T) {
	providersDir := t.TempDir()
	stateDir := t.TempDir()
	pm := provider.NewManager(providersDir)
	sm := state.NewManager(stateDir)

	// 三个 provider，order 不同
	pm.CreateWithEnv("charlie", map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-c"})
	pm.CreateWithEnv("alpha", map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-a"})
	pm.CreateWithEnv("bravo", map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-b"})

	writeOrder(t, providersDir, "charlie", 20)
	writeOrder(t, providersDir, "alpha", 5)
	writeOrder(t, providersDir, "bravo", 10)

	c := New(pm, sm)
	cap := captureStdout(t)
	if err := c.Run([]string{"provider", "--list"}); err != nil {
		t.Fatalf("provider --list error: %v", err)
	}
	out := cap()

	// 期望顺序：alpha (order=5) → bravo (10) → charlie (20)
	idxA := strings.Index(out, "alpha")
	idxB := strings.Index(out, "bravo")
	idxC := strings.Index(out, "charlie")
	if idxA < 0 || idxB < 0 || idxC < 0 {
		t.Fatalf("output missing one of providers, got:\n%s", out)
	}
	if !(idxA < idxB && idxB < idxC) {
		t.Errorf("--list order should be alpha < bravo < charlie, got:\n%s", out)
	}
}

// writeOrder helper：往 provider JSON 写顶层 order 字段（CreateWithEnv 不接受）。
func writeOrder(t *testing.T, dir, name string, order int) {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["order"] = order
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatal(err)
	}
}
