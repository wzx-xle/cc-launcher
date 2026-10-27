package main

// completion.go: ccl shell tab completion 实现。
//
// 协议（design §D1）：`ccl __complete <args...>` 把位置解析后，**只**输出候选 token
// （每行一个），bash 用 `compgen -W` / zsh 用 `compadd` 直接消费。wrapper script 由
// `ccl __init <shell>` 输出，用户 `source <(ccl __init bash)` 完成启用。
//
// wrapper 约定（spec protocol note）：shell wrapper 在 spawn 二进制前去掉 `ccl` 自身，
// 所以 `__complete` 收到的 args 是从 subcommand 开始——`ccl __complete serve "--"`
// 即"在 `ccl serve` 后待补全"。args 末尾是 ""（空字符串）表示待补全位置。

import (
	"flag"
	"fmt"
	"os"

	"cc-launcher/internal/cli"
	"cc-launcher/internal/modes"
	"cc-launcher/internal/templates"
)

// bashCompletionScript 是 `ccl __init bash` 的输出。
//
// `complete -F _ccl_complete -o default ccl`：bash 把 `<TAB>` 触发 `_ccl_complete`，
// 后者从 `COMP_WORDS` 拿当前命令 token，调 `ccl __complete` 拿候选行，最后用
// `compgen -W` 按 cur 前缀过滤。`-o default` 让非匹配项也能 fall back 到 filename 补全。
const bashCompletionScript = `_ccl_complete() {
    local cur words cword
    _init_completion || return
    COMPREPLY=( $(compgen -W "$(ccl __complete "${COMP_WORDS[@]:1:$COMP_CWORD}")" -- "$cur") )
}
complete -F _ccl_complete -o default ccl
`

// zshCompletionScript 是 `ccl __init zsh` 的输出。
//
// `#compdef ccl` 让 zsh 在加载 /caring 时自动注册。`${(@f)$(...)}` 按行 split stdout；
// `compadd -- "${completions[@]}"` 把候选加入当前上下文。
const zshCompletionScript = `#compdef ccl
_ccl() {
    local -a completions
    completions=("${(@f)$(ccl __complete "${words[@]:2:$CURRENT-2}")}")
    compadd -- "${completions[@]}"
}
compdef _ccl ccl
`

// runCompletion 是 `ccl __complete <args...>` 入口。
//
// args[0] 决定分发目标（subcommand 或 "" 表示顶层）：
//   - "" → completeAtTopLevel（用户在 `ccl <TAB>`）
//   - "serve" / "stats" / "provider" / "mode" → 对应 completeAtX
//   - 其它（"-h" / "help" / "bogus"）→ 输出空（shell fall back 到默认行为）
//
// 所有错误吞掉 + exit 0（design §D6）——completion 是 shell 辅助，不应让用户感受到 panic。
func runCompletion(args []string) error {
	if len(args) == 0 || args[0] == "" {
		completeAtTopLevel()
		return nil
	}
	switch args[0] {
	case "serve":
		completeAtServe(args[1:])
	case "stats":
		completeAtStats(args[1:])
	case "web":
		completeAtWeb(args[1:])
	case "provider":
		completeAtProvider(args[1:])
	case "mode":
		completeAtMode(args[1:])
	default:
		// unknown subcommand（"help" / "bogus" 等）→ 输出空（spec §3.10）
	}
	return nil
}

// runShellInit 是 `ccl __init <shell>` 入口。
//
// shell ∈ {bash, zsh}：stdout 输出对应 wrapper script（用户 `source <(ccl __init bash)`
// 即可启用）。其它：stderr 打 "unknown shell" + exit 1（design §D6）。
//
// 注意：未知 shell 走 exitFn(1) 直接退出——不走 return error（让 main 误以为进程
// 正常退出，错误码变 0）。
//
// exitFn 是可注入点：测试覆盖为 no-op 避免 os.Exit 把整个 test 进程杀掉。
var exitFn = func(code int) { os.Exit(code) }

// powershellCompletionScript 是 `ccl __init powershell` 输出的 PowerShell
// Register-ArgumentCompleter 脚本。
//
// spec/powershell-completion 关键约束：
//   - 自包含：脚本独立可粘贴到 $PROFILE 通过 Invoke-Expression 工作
//   - 用 -Native（ccl 是 native binary，不是 cmdlet）
//   - 通过 `ccl __complete <args>` 协议与 bash/zsh 复用候选
//   - 过滤用 StartsWith（OrdinalIgnoreCase）做字面前缀匹配，避免 -like
//     通配符膨胀候选（spec §"wordToComplete contains wildcard metacharacters"）
//
// 用户接入（任选其一）：
//
//	Invoke-Expression -Command (ccl __init powershell)
//	ccl __init powershell | Out-String | Invoke-Expression
const powershellCompletionScript = `$ccl = Get-Command ccl | Select-Object -ExpandProperty Source

Register-ArgumentCompleter -Native -CommandName 'ccl' -ScriptBlock {
	param($wordToComplete, $commandAst)

	$tokens = @($commandAst.ToString() -split '\s+')
	$candidates = & $ccl __complete @tokens 2>$null

	foreach ($line in @($candidates)) {
		if ($line.StartsWith($wordToComplete, [System.StringComparison]::OrdinalIgnoreCase)) {
			[System.Management.Automation.CompletionResult]::new(
				$line, $line, 'ParameterName', $line
			)
		}
	}
}
`

func runShellInit(shell string) error {
	switch shell {
	case "bash":
		fmt.Print(bashCompletionScript)
		return nil
	case "zsh":
		fmt.Print(zshCompletionScript)
		return nil
	case "powershell":
		fmt.Print(powershellCompletionScript)
		return nil
	default:
		// fish 当前未实现（spec/powershell-completion Scenario 列表仅
		// bash/zsh/powershell；fish 在错误信息里标注"已规划"，让用户
		// 知道未来会加）
		fmt.Fprintf(os.Stderr, "unknown shell: %s（支持: bash / zsh / powershell）\n", shell)
		exitFn(1)
		return nil // 满足编译器；exitFn 调用决定是否真的退出
	}
}

// extractFlagNames 反射提取 FlagSet 里所有 flag 的候选名（design §D2）。
//
// 实现走 fs.VisitAll + len(*Flag.Name) 判定长 / 短别名：
//   - 长度 1 → 短别名，输出 "-x"
//   - 长度 > 1 → 长名，输出 "--name"
//
// 不用 flag.PrintDefaults 解析字符串——后者依赖 stdlib 输出格式稳定性；
// fs.VisitAll 是 stdlib 暴露的稳定 API，跨 Go 版本一致。
//
// 返回顺序按 *Flag 内部 map 遍历顺序（go 1.21+ 是字典序）。
func extractFlagNames(fs *flag.FlagSet) []string {
	var names []string
	seen := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true
		if len(f.Name) == 1 {
			names = append(names, "-"+f.Name)
		} else {
			names = append(names, "--"+f.Name)
		}
	})
	return names
}

// trimTrailingEmpty 去掉 args 末尾的 ""（待补全占位符），返回有效 token 列表。
//
// 为什么：wrapper 把待补全位传成 `""`，ccl 解析时要去掉它才能判断"前一个 token 是
// 什么"。但 shell-side 也可能传非空 cur（用户已输入字符），那就不去掉——保留原样判断。
func trimTrailingEmpty(args []string) []string {
	if len(args) > 0 && args[len(args)-1] == "" {
		return args[:len(args)-1]
	}
	return args
}

// lastToken 返回 args 最后一个非空 token；没有则空字符串。
//
// 用于 mutex 防御：completeAtServe / completeAtProvider 看到前一个 token 是 mutex
// flag 之一时，输出空（避免误导用户选两个互斥 flag）。
func lastToken(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

// printLines 把多行候选以 "\n" 结尾输出到 stdout。
func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}

// completeAtTopLevel 输出 `ccl <TAB>` 的候选：5 个子命令 + 3 个 help 旗标。
func completeAtTopLevel() {
	printLines([]string{"serve", "stats", "web", "provider", "mode", "-h", "--help", "help"})
}

// completeAtServe 输出 `ccl serve <TAB>` 的候选 flag。
//
// §3.7 mutex 防御：前一个 token 已是 --stop / --restore / --status / --restart
// 之一时输出空（4 个互斥）。
//
// -h / --help 手动追加：runServe 走 wantsHelpOnly 短路处理 help，不在 fs 注册（spec §3.2
// 要求补全候选含 -h / --help）。若未来 runServe 改用 fs.ErrHelp 处理 help，需同步把
// help / h 加进 registerServeFlags 并删掉这段手动追加——否则会出现"runtime 接受 -
// h / --help 但 completion 漏提示"的小脱节。
func completeAtServe(args []string) {
	effective := trimTrailingEmpty(args)
	serveMutex := map[string]bool{
		"--stop":     true,
		"--restore":  true,
		"--status":   true,
		"--restart":  true,
		"--backfill": true,
	}
	if serveMutex[lastToken(effective)] {
		return
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	registerServeFlags(fs)
	candidates := extractFlagNames(fs)
	candidates = append(candidates, "-h", "--help")
	printLines(candidates)
}

// completeAtStats 输出 `ccl stats <TAB>` 的候选 flag。
//
// -h / --help 手动追加：runStats 走 fs.Args() 后处理 help，不在 fs 注册（spec §3.3 要求）。
// 若未来 runStats 改用 fs.ErrHelp 处理 help，同步把 help / h 加进 registerStatsFlags
// 并删掉这段手动追加。
func completeAtStats(args []string) {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	registerStatsFlags(fs)
	candidates := extractFlagNames(fs)
	candidates = append(candidates, "-h", "--help")
	printLines(candidates)
}

// completeAtWeb 输出 `ccl web <TAB>` 的候选 flag。
//
// 与 runWeb 共用 registerWebFlags（flag 改动自动同步，cli-shell-completion §D2）。
// -h / --help 手动追加：runWeb 走 fs.Args() 后处理 help，不在 fs 注册。
func completeAtWeb(args []string) {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	registerWebFlags(fs)
	candidates := extractFlagNames(fs)
	candidates = append(candidates, "-h", "--help")
	printLines(candidates)
}

// completeAtProvider 输出 `ccl provider <TAB>` / 后续位置的候选。
//
// 候选分发（按 effective 末尾 token）：
//   - "--template" 或 "--type" → 7 个模板名（从 internal/templates.All 提）
//   - "--new" → --type / -T / --template / -h / --help（让用户接 --type 走引导流）
//   - "--delete" / "--list" → 输出空（mutex 已触发；--delete 后位置是用户文件名）
//   - 其它 → 全部 flag（用 cli.RegisterProviderFlags 反射提取）
func completeAtProvider(args []string) {
	effective := trimTrailingEmpty(args)
	providerMutex := map[string]bool{
		"--delete": true,
		"--list":   true,
		"-h":       true,
		"--help":   true,
	}
	if providerMutex[lastToken(effective)] {
		return
	}

	switch lastToken(effective) {
	case "--template", "--type":
		// --template "" 或 --new --type "" → 补 7 个模板名（spec §3.5）
		var names []string
		for _, t := range templates.All {
			names = append(names, t.Name)
		}
		printLines(names)
		return
	case "--new":
		// --new "" → 补 --type / -T / --template / -h / --help（spec §3.9）
		printLines([]string{"--type", "-T", "--template", "-h", "--help"})
		return
	}

	// 默认：补全部 flag（用 RegisterProviderFlags 反射提取，与 runProvider 共享一份注册）
	fs := flag.NewFlagSet("provider", flag.ContinueOnError)
	cli.RegisterProviderFlags(fs)
	printLines(extractFlagNames(fs))
}

// completeAtMode 输出 `ccl mode <TAB>` 的候选：6 个权限模式名（无 flag）。
//
// spec §3.6：从 internal/modes.Names() 提取（design §D3 公开 API）。
func completeAtMode(args []string) {
	printLines(modes.Names())
}
