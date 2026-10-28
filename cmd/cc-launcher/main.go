package main

import (
	"fmt"
	"os"
	"path/filepath"

	"cc-launcher/internal/cli"
	"cc-launcher/internal/config"
	"cc-launcher/internal/envutil"
	"cc-launcher/internal/launcher"
	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
	"cc-launcher/internal/tui"
)

// topHelp 是顶层 ccl --help 输出的内容。
const topHelp = `用法: ccl [子命令] [选项]

子命令:
    (无)              启动 TUI 引导：选提供商 → 选权限模式 → 启动 Claude Code
    serve             OTel daemon 模式（启动 / 停 daemon 或还原 settings.json；详见 ccl serve --help）
    stats             显示 token / cost 聚合（直读 SQLite，无需 daemon 在跑）
    web               打印用量看板的访问 URL（浏览器里看 token / cost；详见 ccl web --help）
    provider          管理提供商（详见 ccl provider --help）
    mode              管理当前目录的权限模式（详见 ccl mode --help）

选项:
    -h, --help        显示本帮助并退出

Tab completion:
  bash 用户：在 ~/.bashrc 加一行   source <(ccl __init bash)
  zsh 用户：在 ~/.zshrc 加一行    source <(ccl __init zsh)
  powershell 用户：在 $PROFILE 加一行   Invoke-Expression -Command (ccl __init powershell)
  启用后 <Tab> 自动补全子命令 / flag / 模板名 / 权限模式名。不启用也能用 ccl，只是不补全。

用量监控：
  - ccl 首次跑会拉起 OTel daemon（监听 :4318）
  - 跑过 Claude Code session 后用 ccl stats 查看
  - 想在浏览器里看用 ccl web（会打印带 token 的看板 URL）
  - 不要 daemon 时跑 ccl serve --stop；要清理旧版 ccl 留在 settings.json 的痕迹用 ccl serve --restore
  - ccl 不改写 ~/.claude/settings.json：OTel 配置通过 --settings 参数交付
  - 隐私：daemon 不解析 logs/traces；本地 SQLite 永远没有 prompt 内容

想看具体怎么配置 deepseek / MiniMax / 智谱 / 阿里云？
    ccl provider --template        # 列出全部 7 种支持的模板
    ccl provider --template deepseek   # 看 deepseek 的 JSON 模板`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	// 顶层 --help / --version：只在 args[0] 是 help/version 旗标时触发（避免误吞子命令的同名旗标）。
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Println(topHelp)
			return nil
		case "-V", "--version", "version":
			printVersion()
			return nil
		case "__complete":
			// 隐藏子命令：shell completion 协议端点（cli-shell-completion）。
			// 收到 args[1:] 作为"待补全位置上下文"，输出候选 token（每行一个）。
			// 不进 topHelp 子命令列表（spec §"hidden subcommands"）——避免干扰 `ccl --help`。
			return runCompletion(args[1:])
		case "__init":
			// 隐藏子命令：输出 shell wrapper script。args[1] 是 shell 名（bash / zsh）。
			// 未知 shell → stderr + exit 1。
			return runShellInit(args[1])
		case "serve":
			return runServe(args[1:])
		case "stats":
			return runStats(args[1:])
		case "web":
			return runWeb(args[1:])
		}
	}

	stateBaseDir, err := state.DefaultBaseDir()
	if err != nil {
		return fmt.Errorf("无法获取 home 目录: %w", err)
	}
	stateMgr := state.NewManager(stateBaseDir)
	providersDir := filepath.Join(stateBaseDir, "providers")
	providerMgr := provider.NewManager(providersDir)

	settingsPath, err := config.DefaultSettingsPath()
	if err != nil {
		return fmt.Errorf("无法获取 settings.json 路径: %w", err)
	}
	backupDir, err := config.DefaultBackupDir()
	if err != nil {
		return fmt.Errorf("无法获取备份目录路径: %w", err)
	}
	configMgr := config.NewManager(settingsPath, backupDir, config.DefaultMaxBackups)

	if len(args) == 0 {
		return runTUI(providerMgr, stateMgr, configMgr)
	}

	c := cli.New(providerMgr, stateMgr)
	return c.Run(args)
}

func runTUI(pm *provider.Manager, sm *state.Manager, cm *config.Manager) error {
	// provider-setenv-cwd-binding §3.3：runTUI 开头调 migrateSettingsJSON
	// 一次性清理 settings.json 历史注入；失败仅警告，不阻断 ccl 主流程
	//（与 injectOTelEnv 失败的降级策略一致）。
	if err := migrateSettingsJSON(cm, sm); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ 迁移 settings.json 失败: %v\n", err)
	}

	providers, err := pm.List()
	if err != nil {
		return fmt.Errorf("加载提供商列表失败: %w", err)
	}

	defaultProvider := sm.GetActiveProvider()
	wd, _ := os.Getwd()
	defaultMode, _ := sm.GetPermissionMode(wd)

	// provider-setenv-cwd-binding §4.3：按 dir_bindings[wd] > active_provider
	// 顺序解析 preSelected provider，传给 tui.Run 作为预选 cursor。
	cwdDefaultProvider := sm.GetProviderForDir(wd)
	preSelected := cwdDefaultProvider
	if preSelected == "" {
		preSelected = defaultProvider
	}

	result, err := tui.Run(providers, preSelected, defaultMode, cwdDefaultProvider, wd)
	if err != nil {
		if err == tui.ErrCancelled {
			return nil
		}
		return err
	}

	// provider-setenv-cwd-binding §4.4：删除 cm.Merge 调用——provider
	// 配置不再写 settings.json（migrate 已清理 ccl 历史注入），改用
	// setenv 注入到子进程环境（§4.5）。
	if result.Provider != defaultProvider {
		sm.SetActiveProvider(result.Provider)
	}

	// provider-setenv-cwd-binding §4.2：TUI 末尾"记住 cwd binding"——Y 时
	// 调 sm.SetProviderForDir；n 时不动；TUI 取消（result.RememberBinding=false
	// 且 result.Provider=""）由上面 tui.Run 的 ErrCancelled 分支处理。
	if result.RememberBinding && result.Provider != "" {
		if err := sm.SetProviderForDir(wd, result.Provider); err != nil {
			return fmt.Errorf("保存 cwd binding 失败: %w", err)
		}
	}

	sm.SetPermissionMode(wd, result.Mode)

	// provider-setenv-cwd-binding §4.5：准备 opts.Env——从 os.Environ() 开始，
	// 用 envutil.Upsert 替换/追加 providerCfg.env 里的所有 key，赋给
	// launcher.Options.Env。launcher 包在 Unix 透传给 syscall.Exec 第三参数，
	// Windows 写到 cmd.Env（见 launcher-provider-env spec）。
	var optsEnv []string
	providerBaseURL := ""
	if result.Provider != "" {
		providerCfg, err := pm.Get(result.Provider)
		if err != nil {
			return fmt.Errorf("加载提供商 %q 配置失败: %w", result.Provider, err)
		}
		if providerEnv, ok := providerCfg["env"].(map[string]any); ok && len(providerEnv) > 0 {
			optsEnv = os.Environ()
			for k, v := range providerEnv {
				s, ok := v.(string)
				if !ok {
					continue
				}
				optsEnv = envutil.Upsert(optsEnv, k, s)
				if k == "ANTHROPIC_BASE_URL" {
					providerBaseURL = s
				}
			}
		}
	}

	sessionLabel := "新会话"
	opts := launcher.Options{PermissionMode: result.Mode}
	switch result.SessionKind {
	case "continue":
		opts.Continue = true
		sessionLabel = "继续上次会话"
	case "resume":
		opts.Resume = true
		sessionLabel = "从会话列表选择"
	}

	// 准备 OTel 环境（otel-endpoints-via-settings）：
	//   - 一次性清理旧版 ccl 留在 settings.json 里的痕迹
	//   - 读用户自己的端点 → 投递到 ccl 进程环境（fork daemon 时继承 → 转发）
	//   - 算出要通过 --settings 交付给子进程的 env 块
	//
	// ccl **不再写** ~/.claude/settings.json。
	otelEnv := prepareOtelEnv(cm, sm)

	// 注入 launch 上下文（provider / cwd / 端点 host → OTEL_RESOURCE_ATTRIBUTES）。
	// 必须在 provider 选定之后、launcher.Launch 之前——上下文描述的是"这次启动"。
	//
	// 主通道是 Claude Code 的 `--settings` 参数（优先级高于 settings.json 与进程
	// 环境）；进程环境那条是冗余第二通道。详见 launchctx.go 的注释。
	optsEnv, launchSettingsJSON := injectLaunchContext(cm, result.Provider, wd, providerBaseURL, optsEnv, otelEnv)

	// additional settings（--settings）——承载 launch 上下文的主通道。
	if launchSettingsJSON != "" {
		args, cleanup, err := settingsArgs(launchSettingsJSON)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ 构造 --settings 参数失败（provider / 项目维度将为空）: %v\n", err)
		} else {
			opts.ExtraArgs = args
			opts.Cleanup = cleanup
		}
	}

	// 拉起 OTel daemon（按需；端口已占 → skip）
	started, _ := ensureDaemonRunning()
	if started {
		fmt.Println("✓ OTel daemon 拉起成功（监听 :4318）")
	}

	fmt.Printf("正在启动 Claude Code (%s / %s / %s)...\n", result.Provider, result.Mode, sessionLabel)
	if optsEnv != nil {
		opts.Env = optsEnv
	}
	return launcher.Launch(opts)
}

// migrateSettingsJSON 一次性清理 settings.json 中 ccl 历史注入的 provider env
// （基于 snapshot 比对），保留用户手写的 key + OTel / 备份键。
//
// 详细设计见 openspec/changes/provider-setenv-cwd-binding/design.md §Decision 3。
//
// 幂等性：每次 runTUI 开头调一次。无 snapshot → return；有 snapshot → 清完后
// 清空 snapshot，下次再跑无 snapshot → return。
//
// 备份策略：仅当至少一个 key 被删时调 cm.Backup()——无变更不浪费备份槽。
// "值匹配"原则：snapshot 值 == settings.json 当前值才删；用户手改过的 key
// （值与 snapshot 不等）保留（CLAUDE.md §"claudeCode 本身添加的配置要保留"）。
func migrateSettingsJSON(cm *config.Manager, sm *state.Manager) error {
	snapshot := sm.GetInjectedSnapshot()
	if snapshot == nil {
		return nil
	}
	snapshotEnv, _ := snapshot["env"].(map[string]any)
	if len(snapshotEnv) == 0 {
		// snapshot 没有 env 字段（老 ccl 写入但空 map / 损坏结构）——清空以
		// 标记"已考虑"，下次启动不重跑。
		return sm.SetInjectedSnapshot(nil)
	}

	settings, err := cm.LoadSettings()
	if err != nil {
		return err
	}
	envAny, _ := settings["env"].(map[string]any)

	cleaned := []string{}
	for k, snapVal := range snapshotEnv {
		cur, ok := envAny[k]
		if !ok {
			continue // 已不在 settings.json
		}
		if cur != snapVal {
			continue // 用户手改过——不删（spec/provider-setenv-migration §
			// "Migration preserves user-modified keys"）
		}
		delete(envAny, k)
		cleaned = append(cleaned, k)
	}

	if len(cleaned) == 0 {
		// 无 key 匹配——snapshot 仍清空（spec §"second startup after
		// migration" Scenario "migration SHALL return early without touching
		// settings.json"）。
		return sm.SetInjectedSnapshot(nil)
	}

	// 有 key 要删——先备份再写
	if err := cm.Backup(); err != nil {
		return fmt.Errorf("migrate 前备份 settings.json 失败: %w", err)
	}

	settings["env"] = envAny
	if err := cm.SaveSettings(settings); err != nil {
		return err
	}

	// stderr 日志（spec §"Migration reports to user" Scenario）
	fmt.Fprintf(os.Stderr,
		"⚙ 检测到 ccl 之前注入的 provider env，正在清理 settings.json（备份到 %s）... ✓ (%d 个 key)\n",
		cm.BackupDir(), len(cleaned))

	return sm.SetInjectedSnapshot(nil)
}

// prepareOtelEnv 准备本次启动的 OTel 环境。
//
// 三件事（otel-endpoints-via-settings）：
//
//  1. 一次性清理旧版 ccl 留在 settings.json 里的痕迹（cleanupLegacyOtelSettings）
//  2. 读**用户自己的**端点 → 写进 ccl 的进程环境变量（fork daemon 时被继承 →
//     daemon 的 forwarder 转发到用户的远程 collector）
//  3. 算出要通过 `--settings` 交付给子进程的 env 块（接管端点 + 补默认值）
//
// ccl **不再写** ~/.claude/settings.json。
//
// 返回交付用的 env 块（map[string]string）。
func prepareOtelEnv(cm *config.Manager, sm *state.Manager) map[string]string {
	cleanupLegacyOtelSettings(cm, sm)

	userEnv := settingsEnvMap(cm)

	// 转发目标：用户自己的端点 → daemon 的进程环境变量。
	// daemon 侧（internal/collector/forwarder.go）读自己 env 的
	// FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT（per-signal，优先）与
	// CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT（fallback，兜底）。
	//
	// 没有就显式 unset：变量必须"要么是真值、要么不存在"，不能留占位值。
	fallback, perSignal := resolveForwardTarget(userEnv)
	if fallback != "" {
		_ = os.Setenv(envOrigEndpoint, fallback)
	} else {
		_ = os.Unsetenv(envOrigEndpoint)
	}
	if perSignal != "" {
		_ = os.Setenv(envForwardMetricsEndpoint, perSignal)
	} else {
		_ = os.Unsetenv(envForwardMetricsEndpoint)
	}

	return buildChildOtelEnv(userEnv)
}

// cleanupLegacyOtelSettings 一次性清理旧版 ccl 留在 settings.json 里的痕迹。
//
// 只跑一次（标志记在 state.json）：清理规则里有"值等于 ccl 默认值就删"这种启发式，
// 反复跑会把用户事后自己写进去的同名值也删掉。
//
// 没有 ccl 痕迹 → **不碰用户的文件**（spec/claude-code-env-config §"ccl cleans up
// legacy ccl artifacts from settings.json once"）。
//
// 失败（读不了 / 写不了）仅警告，不阻断 ccl 主流程。
func cleanupLegacyOtelSettings(cm *config.Manager, sm *state.Manager) {
	if sm.GetOtelLegacyCleaned() {
		return // 只跑一次
	}

	settings, err := cm.LoadSettings()
	if err != nil {
		return // 读不了 → 这轮跳过，下次启动再看
	}
	envAny, _ := settings["env"].(map[string]any)
	if envAny == nil || !hasCclArtifact(envAny) {
		// 没有 ccl 痕迹 → 标记"已处理"，此后不再检查用户的文件。
		_ = sm.SetOtelLegacyCleaned(true)
		return
	}

	cleaned, changed := cleanupLegacyOtelEnv(envAny)
	if !changed {
		_ = sm.SetOtelLegacyCleaned(true)
		return
	}

	// 先备份再写（与 provider 迁移一致）
	if err := cm.Backup(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ 清理旧版 ccl 遗留前备份 settings.json 失败: %v\n", err)
	}
	settings["env"] = cleaned
	if err := cm.SaveSettings(settings); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ 清理旧版 ccl 遗留失败: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "⚙ 已清理旧版 ccl 留在 settings.json 的 OTel 注入痕迹（备份到 %s）\n", cm.BackupDir())
	_ = sm.SetOtelLegacyCleaned(true)
}

// settingsEnvMap 读 ~/.claude/settings.json 的 env 块。
//
// 文件不存在 / 没有 env 字段 / 解析失败 → 空 map（只读，不报错）。
func settingsEnvMap(cm *config.Manager) map[string]any {
	if cm == nil {
		return map[string]any{}
	}
	settings, err := cm.LoadSettings()
	if err != nil {
		return map[string]any{}
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		return map[string]any{}
	}
	return env
}
