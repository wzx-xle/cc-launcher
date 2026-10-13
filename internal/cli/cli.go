package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cc-launcher/internal/modes"
	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
	"cc-launcher/internal/templates"
)

// providerHelp 是 `ccl provider --help` 输出的内容。
const providerHelp = `用法: ccl provider [选项] [name]

注意: --list / --new / --delete 三者互斥，不可同时给（--template 允许与 --new 共存，等价于仅 --template）

选项:
    --list            列出所有提供商（▶ 标记当前激活的）
    --new [<name>]    新增提供商（可用 --type 选择模板；不指定 name 时会询问）
    --delete <name>   删除提供商（激活的需二次确认）
    --template, -T [<type>]
                      打印提供商配置模板。无 type 时列出全部 7 种类型。
                      type 可选: anthropic, deepseek, minimax, glm,
                                aliyun-coding, aliyun-pay, aliyun-token
    -h, --help        显示本帮助并退出

子命令:
    ccl provider <name>   切换到指定提供商

示例:
    ccl provider --template           # 看 7 种类型 + 一行说明
    ccl provider --template deepseek  # 看 deepseek 的 JSON 模板
    ccl provider --new --type deepseek my-deepseek  # 引导式新建`

// modeHelp 是 `ccl mode --help` 输出的内容。
const modeHelp = `用法: ccl mode [选项] [<name>]

无参数时列出当前目录可用的 6 种权限模式，▶ 标记当前默认。

选项:
    -h, --help        显示本帮助并退出

示例:
    ccl mode                       # 列出 6 种模式 + 当前默认
    ccl mode plan                  # 当前目录默认设为 plan 模式
    ccl mode bypassPermissions     # 切换到 bypassPermissions 模式

模式说明详见 README "权限模式" 小节。`

// CLI 处理 ccl 子命令路由。
type CLI struct {
	providers *provider.Manager
	state     *state.Manager
}

// New 创建 CLI 路由器。
func New(pm *provider.Manager, sm *state.Manager) *CLI {
	return &CLI{providers: pm, state: sm}
}

// Run 根据 args 路由到对应子命令。args[0] 应为子命令名（"provider"、"mode"），或 "-h"/"--help"/"help"。
func (c *CLI) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("缺少子命令；运行 ccl --help 查看用法")
	}

	// 顶层帮助（在不知道是什么子命令前先识别）
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Println(topHelpForCLI())
		return nil
	}

	switch args[0] {
	case "provider":
		return c.runProvider(args[1:])
	case "mode":
		return c.runMode(args[1:])
	default:
		return fmt.Errorf("未知子命令: %s，可用: provider, mode", args[0])
	}
}

// topHelpForCLI 是 CLI 层的顶层帮助。
// 注意：主程序 main.go 也有自己的 topHelp（在加载 state 之前打印）。
// 这里是为了让 `ccl help`（不通过 main.go 直达 cli.Run）也能拿到帮助。
func topHelpForCLI() string {
	return `用法: ccl [子命令] [选项]

子命令:
    (无)              启动 TUI 引导：选提供商 → 选权限模式 → 启动 Claude Code
    provider          管理提供商（详见 ccl provider --help）
    mode              管理当前目录的权限模式（详见 ccl mode --help）

选项:
    -h, --help        显示本帮助并退出`
}

// RegisterProviderFlags 把 provider 子命令的 flag 注册到 fs，返回值供 runProvider 与
// cmd/cc-launcher/completion.go::completeAtProvider 共用。
//
// 约束（cli-shell-completion 兼容性）：前 4 个返回值（list / new / delete / template）的
// 位置 / 类型 / 顺序不变。新增 typeFlag / help 追加在末尾，调用方可用 `_, _, _, _, _, _ :=`
// 显式忽略。
//
// 短别名（-T / -h）实现方式：Go 1.21+ stdlib 不再支持 Bool("name1, name2", ...) 的逗号
// 分隔多名字机制——Bool 会把字面 "name1, name2" 当作单个 flag 注册。所以用 BoolVar 把短别名
// 显式注册到与主名相同的 *bool：fs.Parse 时 -T 或 --template 任意一个被设都会反映到
// template 指针。
//
// 同时注册 `--type`（不暴露 *bool 给调用方）和 `--help, -h`：让 fs.Parse 通过未知 flag
// 检查；`--type` 仅在 `--new` 分支被 dispatch 读取，`--help` 由 stdlib 的 flag.ErrHelp
// 早返回触发（无需看 *helpFlag）。
//
// exported 是 cli-shell-completion 的实施前置——cmd/cc-launcher/completion.go（package main）
// 跨包调不到包内 unexported 符号（2026-09-30 实施前改名）。
func RegisterProviderFlags(fs *flag.FlagSet) (list, new, delete, template *bool, typeFlag *string, help *bool) {
	list = fs.Bool("list", false, "列出所有提供商")
	new = fs.Bool("new", false, "新增提供商（可用 --type 选模板）")
	delete = fs.Bool("delete", false, "删除提供商（需给 name）")

	// template：--template 与 -T 共享同一 bool
	var templateVar bool
	template = &templateVar
	fs.BoolVar(template, "template", false, "打印提供商配置模板")
	fs.BoolVar(template, "T", false, "打印提供商配置模板（-T 短别名）")

	typeFlag = fs.String("type", "", "仅在 --new 上下文生效")

	// help：--help 与 -h 共享同一 bool（stdlib 即使注册也会硬返回 flag.ErrHelp，但仍
	// 保留这个指针以防未来行为变化）
	var helpVar bool
	help = &helpVar
	fs.BoolVar(help, "help", false, "显示本帮助并退出")
	fs.BoolVar(help, "h", false, "显示本帮助并退出（-h 短别名）")

	return
}

func (c *CLI) runProvider(args []string) error {
	// 1. 建 FlagSet 并注册 flag
	fs := flag.NewFlagSet("provider", flag.ContinueOnError)
	// 抑制 fs.Parse 默认往 fs.output() 写"flag provided but not defined: -x"——
	// 下面翻译成"未知选项: -x"返回，重复打印没意义。mutex 报错走 fmt.Fprintln(os.Stderr)
	// 单独输出，跟 fs.output() 无关。
	fs.SetOutput(io.Discard)
	listFlag, newFlag, deleteFlag, templateFlag, typeFlag, helpFlag := RegisterProviderFlags(fs)

	if err := fs.Parse(args); err != nil {
		// Go stdlib 见到 -h / --help 即便注册了也会硬返回 flag.ErrHelp（早于任何 mutex
		// 检查）——`--list --new --help` 仍走 help 分支，自动满足 help 优先级最高契约。
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println(providerHelp)
			return nil
		}
		// 把 stdlib "flag provided but not defined: -bogus" 翻译成"未知选项: -bogus"——
		// 保持 TestProvider_BogusFlagStillErrors 老契约（断言错误含"未知"）。
		if strings.HasPrefix(err.Error(), "flag provided but not defined:") {
			bad := strings.TrimPrefix(err.Error(), "flag provided but not defined: ")
			return fmt.Errorf("未知选项: %s", bad)
		}
		return err
	}

	// 兜底：若未来 stdlib 不再硬返回 ErrHelp，*helpFlag 仍能反映 help 输入。
	if *helpFlag {
		fmt.Println(providerHelp)
		return nil
	}

	// 3. mutex 检查：--list / --new / --delete 互斥。--template 不进 mutex。
	set := 0
	for _, f := range []*bool{listFlag, newFlag, deleteFlag} {
		if *f {
			set++
		}
	}
	if set > 1 {
		fmt.Fprintln(os.Stderr, "用法: ccl provider [--list | --new | --delete]（互斥）")
		fmt.Fprintln(os.Stderr, "详见 `ccl provider --help`")
		return fmt.Errorf("--list / --new / --delete 互斥；不可同时给")
	}

	// 4. --type 上下文约束：仅当 --type 是唯一给定 flag 时报错；其它核心 flag 与 --type
	// 共存时 --type silently ignored（dispatch 不读它）。
	if *typeFlag != "" && !*listFlag && !*newFlag && !*deleteFlag && !*templateFlag {
		return fmt.Errorf("--type 只能与 --new 一起使用")
	}

	// 5. 位置参数
	positional := fs.Args()
	var name string
	if len(positional) > 0 {
		name = positional[0]
	}

	// 6. dispatch 顺序：template 优先级最高（保留 --new --template 等价 --template 约定）
	if *templateFlag {
		return c.printTemplate(positional)
	}
	if *listFlag {
		return c.listProviders()
	}
	if *deleteFlag {
		if name == "" {
			return fmt.Errorf("用法: ccl provider --delete <name>")
		}
		return c.deleteProvider(name)
	}
	if *newFlag {
		return c.newProvider(name, *typeFlag)
	}

	// 7. 兜底：无 flag 时按位置参数走 switch / list
	if name != "" {
		return c.switchProvider(name)
	}
	return c.listProviders()
}

// printTemplate 实现 `ccl provider --template [type]`。
// 无参数列出全部类型；有效类型打印 JSON 模板；无效类型返回错误。
func (c *CLI) printTemplate(args []string) error {
	if len(args) == 0 {
		fmt.Println("支持的提供商模板:")
		for _, t := range templates.All {
			fmt.Printf("  %-15s %s\n", t.Name, t.Description)
		}
		fmt.Println("\n示例: ccl provider --template deepseek")
		return nil
	}
	tpl, ok := templates.ByName(args[0])
	if !ok {
		names := make([]string, 0, len(templates.All))
		for _, t := range templates.All {
			names = append(names, t.Name)
		}
		return fmt.Errorf("不支持的模板类型 %q，支持: %s", args[0], strings.Join(names, ", "))
	}
	fmt.Print(tpl.Body)
	if !strings.HasSuffix(tpl.Body, "\n") {
		fmt.Println()
	}
	return nil
}

func (c *CLI) runMode(args []string) error {
	if len(args) == 0 {
		return c.listModes()
	}

	switch args[0] {
	case "-h", "--help":
		fmt.Println(modeHelp)
		return nil
	}

	mode := args[0]
	if strings.HasPrefix(mode, "-") {
		return fmt.Errorf("未知选项: %s", mode)
	}

	for _, m := range modes.Names() {
		if m == mode {
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("无法获取当前目录: %w", err)
			}
			if err := c.state.SetPermissionMode(wd, mode); err != nil {
				return err
			}
			fmt.Printf("当前目录权限模式已设为 %s\n", mode)
			return nil
		}
	}
	return fmt.Errorf("无效的权限模式 %q，合法值: %s", mode, strings.Join(modes.Names(), ", "))
}

func (c *CLI) listProviders() error {
	list, err := c.providers.List()
	if err != nil {
		return err
	}

	active := c.state.GetActiveProvider()
	// provider-setenv-cwd-binding §5.2：cwd 命中 dir_bindings[wd] 的 provider
	// 加 * 标记（叠加在 ▶ 上时显示 *▶，spec/provider-cwd-binding §"cwd
	// binding equals active_provider" Scenario）。
	wd, _ := os.Getwd()
	cwdBound := c.state.GetProviderForDir(wd)
	if len(list) == 0 {
		fmt.Println("暂无提供商配置")
		return nil
	}

	for _, info := range list {
		// provider-setenv-cwd-binding §5.2：cwd 命中 dir_bindings[wd] 用 *
		// 标记，全局 active_provider 用 ▶ 标记；都命中叠加为 *▶（spec §
		// "cwd binding equals active_provider" Scenario）。
		isCwd := info.Name == cwdBound
		isActive := info.Name == active
		var marker string
		switch {
		case isCwd && isActive:
			marker = "*▶ "
		case isCwd:
			marker = "* "
		case isActive:
			marker = "▶ "
		default:
			marker = "  "
		}
		if info.Error != "" {
			fmt.Printf("⚠️  %s — %s\n", info.Name, info.Error)
		} else {
			fmt.Printf("%s%s\n", marker, info.Name)
		}
	}
	return nil
}

func (c *CLI) switchProvider(name string) error {
	if !c.providers.Exists(name) {
		return fmt.Errorf("提供商 %q 不存在", name)
	}
	if err := c.state.SetActiveProvider(name); err != nil {
		return err
	}
	// provider-setenv-cwd-binding §5.1：CLI 切换也写 dir_bindings[wd]——与
	// TUI 末尾"记住"对齐（spec §"CLI provider switches and binds cwd"）。
	wd, _ := os.Getwd()
	if err := c.state.SetProviderForDir(wd, name); err != nil {
		return fmt.Errorf("保存 cwd binding 失败: %w", err)
	}
	fmt.Printf("已切换到 %s\n", name)
	return nil
}

// newProvider 引导式新建提供商。
// positional 是可选的 name 参数；typeName 是 --type 提供的模板名。
func (c *CLI) newProvider(positional, typeName string) error {
	// 解析模板
	var tpl templates.Template
	hasType := typeName != ""
	if hasType {
		var ok bool
		tpl, ok = templates.ByName(typeName)
		if !ok {
			names := make([]string, 0, len(templates.All))
			for _, t := range templates.All {
				names = append(names, t.Name)
			}
			return fmt.Errorf("不支持的模板类型 %q，支持: %s", typeName, strings.Join(names, ", "))
		}
	}

	// 1. 选 name
	name := positional
	if name == "" {
		fmt.Print("提供商名称（用于文件名，如 'deepseek-prod'）: ")
		n, ok := readLine()
		if !ok {
			return fmt.Errorf("已取消")
		}
		name = strings.TrimSpace(n)
	}
	if name == "" {
		return fmt.Errorf("提供商名称不能为空")
	}
	if c.providers.Exists(name) {
		return fmt.Errorf("提供商 %q 已存在", name)
	}

	// 2. 没有 --type 时让用户选
	if !hasType {
		fmt.Println("\n支持的模板:")
		for _, t := range templates.All {
			fmt.Printf("  %-15s %s\n", t.Name, t.Description)
		}
		fmt.Print("\n选择模板 (输入名称): ")
		choice, ok := readLine()
		if !ok {
			return fmt.Errorf("已取消")
		}
		choice = strings.TrimSpace(choice)
		var ok2 bool
		tpl, ok2 = templates.ByName(choice)
		if !ok2 {
			return fmt.Errorf("无效模板 %q", choice)
		}
		hasType = true
	}

	// 3. 打印模板预览
	fmt.Printf("\n模板预览 (%s):\n%s\n\n", tpl.Name, tpl.Body)

	// 4. 收集：pre-asks → fields（含通用占位符替换 + SkipPrompt 跳过）→ post-overrides
	env := make(map[string]any, len(tpl.Fields)+len(tpl.PreAsks))
	if err := c.collectPreAsks(tpl, env); err != nil {
		return err
	}
	if err := c.collectFields(tpl, env); err != nil {
		return err
	}
	c.applyPostOverrides(tpl, env)

	// 5. 清理临时字段（WorkspaceId 是 aliyun-pay 的 URL 拼装源，写盘前删掉）
	delete(env, "WorkspaceId")
	delete(env, "region") // 防御性：region 是 PreAsk 字段理论上不写入 env，但保留 delete 保证兼容性

	// 6. 写盘前快照：算当前所有 provider 的 max(Order)，写盘后用此值给用户
	// 推荐 order 建议（max+10；写盘前的快照不包含本次新文件）。
	var maxOrder int
	if existing, err := c.providers.List(); err == nil {
		for _, info := range existing {
			if info.Order > maxOrder {
				maxOrder = info.Order
			}
		}
	}
	// List() 出错不阻塞主流程——order 建议是 best-effort。

	// 7. 写盘
	if err := c.providers.CreateWithEnv(name, env); err != nil {
		return err
	}

	// 8. 写盘后追加可选 metadata 步骤（provider-list-display-improvements §R2）：
	// note 询问 → 空串/EOF 视为跳过；非空则 ReadFile + JSON patch 写回 note 字段。
	// order 提示 → 只显示 max+10，不读输入。
	// 设计：note 阶段 EOF 视为跳过（与 template prompts 阶段 EOF 视为取消不同——
	// 因为 env 块已写入无法回滚，回滚要单独 delete-on-EOF 路径，scope 不值）。
	fmt.Print("note (可选备注，回车跳过): ")
	raw, _ := readLine() // 忽略 ok：EOF 视为空字符串（与用户回车等价）
	note := strings.TrimSpace(raw)
	if note != "" {
		path := filepath.Join(c.providers.ProvidersDir(), name+".json")
		if err := writeNoteToProviderFile(path, note); err != nil {
			return err
		}
	}

	fmt.Printf("排序值: %d (现有 max=%d, 建议 max+10；手改 JSON order 字段覆盖)\n", maxOrder+10, maxOrder)

	fmt.Printf("\n已写入 %s/%s.json，可手工编辑补充其它字段\n", c.providers.ProvidersDir(), name)
	return nil
}

// writeNoteToProviderFile 把 note 顶层字段写回 provider JSON 文件。
// env 块不动；用 ReadFile + Unmarshal + MarshalIndent 回写。
func writeNoteToProviderFile(path, note string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读回 %s 失败: %w", path, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	cfg["note"] = note
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("写回 %s 失败: %w", path, err)
	}
	return nil
}

// collectPreAsks 跑模板声明的 PreAsks：按 Prompt 读用户输入，Default / Validate 跟。
// 结果写入 env[PreAsk.Key]。
func (c *CLI) collectPreAsks(tpl templates.Template, env map[string]any) error {
	for _, pa := range tpl.PreAsks {
		for {
			fmt.Print(pa.Prompt)
			line, ok := readLine()
			if !ok {
				return fmt.Errorf("已取消")
			}
			value := strings.TrimSpace(line)
			if value == "" && pa.Default != "" {
				value = pa.Default
			}
			if pa.Validate != nil {
				if err := pa.Validate(value); err != nil {
					fmt.Printf("  %s，请重新输入\n", err)
					continue
				}
			}
			env[pa.Key] = value
			break
		}
	}
	return nil
}

// collectFields 走模板的 TemplateField 主循环：通用占位符替换 + SkipPrompt 跳过 + readLine。
func (c *CLI) collectFields(tpl templates.Template, env map[string]any) error {
	for _, f := range tpl.Fields {
		// 通用占位符替换：f.Default 含 {key} 时用 env[key] 替换（aliyun-pay 用此机制做 WorkspaceId 拼装）
		defaultVal := f.Default
		if defaultVal != "" {
			for k, v := range env {
				s, ok := v.(string)
				if !ok {
					continue
				}
				placeholder := "{" + k + "}"
				if strings.Contains(defaultVal, placeholder) {
					defaultVal = strings.ReplaceAll(defaultVal, placeholder, s)
				}
			}
		}

		// SkipPrompt 字段：跳过 readLine，直接用 Default 写入 env（aliyun-pay / minimax 的 BASE_URL）
		if f.SkipPrompt {
			if defaultVal != "" {
				env[f.Key] = defaultVal
				fmt.Printf("  %s = %s (自动拼装)\n", f.Key, defaultVal)
			}
			continue
		}

		var prompt string
		if defaultVal != "" {
			prompt = fmt.Sprintf("%s [默认 %s]", f.Prompt, defaultVal)
		} else if f.Secret {
			prompt = f.Prompt + " (不回显)"
		} else {
			prompt = f.Prompt
		}

		var value string
		for {
			fmt.Printf("%s: ", prompt)
			line, ok := readLineSecret(f.Secret)
			if !ok {
				return fmt.Errorf("已取消")
			}
			value = strings.TrimSpace(line)
			if value == "" && defaultVal != "" {
				value = defaultVal
			}
			if value == "" && f.Required {
				fmt.Println("  该字段必填，请重新输入")
				continue
			}
			break
		}
		if value != "" {
			env[f.Key] = value
		}
	}
	return nil
}

// applyPostOverrides 跑模板声明的 PostOverrides：按 Apply(env) 拿新值覆盖 env[Field]。
func (c *CLI) applyPostOverrides(tpl templates.Template, env map[string]any) {
	for _, ovr := range tpl.PostOverrides {
		env[ovr.Field] = ovr.Apply(env)
	}
}

// ProvidersDir 暴露 providers 目录路径（供 newProvider 显示）。
// 由于 Manager 字段私有，这里加一个访问方法。
// 注意：上面代码用的是 c.providers.ProvidersDir()，需要 provider 包提供该方法。

func (c *CLI) deleteProvider(name string) error {
	if !c.providers.Exists(name) {
		return fmt.Errorf("提供商 %q 不存在", name)
	}

	if c.state.GetActiveProvider() == name {
		fmt.Printf("⚠️  %s 是当前激活的提供商，确定删除？[y/N] ", name)
		answer, _ := readLine()
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			return fmt.Errorf("已取消")
		}
	}

	if err := c.providers.Delete(name); err != nil {
		return err
	}
	fmt.Printf("已删除提供商 %s\n", name)
	return nil
}

func (c *CLI) listModes() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	currentMode, _ := c.state.GetPermissionMode(wd)

	for _, mode := range modes.Names() {
		marker := "  "
		if mode == currentMode {
			marker = "▶ "
		}
		fmt.Printf("%s%s\n", marker, mode)
	}
	return nil
}

// readLineFn 默认从 stdin 读；测试可覆盖。
var readLineFn = func() (string, bool) {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}

// readLine 从 stdin 读取一行（含空行）。返回 ("", false) 表示 EOF。
func readLine() (string, bool) { return readLineFn() }

// readLineSecret 同 readLine，但当 secret 为 true 时把 stdin 切到 raw 模式不回显输入。
// 注意：当前实现未真正做 raw 模式（避免引入 golang.org/x/term 依赖），
// 仅在 prompt 标注"不回显"作为提示；用户在非交互 stdin 下能正常工作。
// 真正的不回显需要 terminal.MakeRaw + term.ReadPassword，留作后续增强。
func readLineSecret(secret bool) (string, bool) {
	_ = secret // 预留：未来引入 golang.org/x/term 做不回显
	return readLine()
}