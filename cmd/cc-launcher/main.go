package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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
  - 不要 daemon 时跑 ccl serve --stop；要彻底清除注入用 ccl serve --restore
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

	// 注入 OTel env（备份 + 写默认值）
	if err := injectOTelEnv(cm); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ 注入 OTel env 失败: %v\n", err)
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
//（基于 snapshot 比对），保留用户手写的 key + OTel / 备份键。
//
// 详细设计见 openspec/changes/provider-setenv-cwd-binding/design.md §Decision 3。
//
// 幂等性：每次 runTUI 开头调一次。无 snapshot → return；有 snapshot → 清完后
// 清空 snapshot，下次再跑无 snapshot → return。
//
// 备份策略：仅当至少一个 key 被删时调 cm.Backup()——无变更不浪费备份槽。
// "值匹配"原则：snapshot 值 == settings.json 当前值才删；用户手改过的 key
//（值与 snapshot 不等）保留（CLAUDE.md §"claudeCode 本身添加的配置要保留"）。
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

// injectOTelEnv 在 exec Claude Code 前把 OTel env 注入 ~/.claude/settings.json，
// 同时把备份键（CC_ORIG_* 与 FORWORD_* 两类）导出到当前进程 env（fork ccl serve 时子进程继承 →
// 触发 forwarder）。FORWORD_* 是 telemetry-config-compat 引入的 per-signal metrics 备份命名空间。
//
// 失败仅警告，不阻断 ccl 主流程（决策 10：daemon 是 optional）。
//
// 控制权交接语义（backup-on-control-handover change）：如果 settings.json 不在 ccl 当前控制下
// （即 OTel 端点不指向 ccl 默认值），调 Manager.Backup() 做一次文件级快照到 backups/，
// 目的：保留一份"ccl 接管前"的健康基线，便于日后回滚。控制中则跳过 Backup，
// 避免 10 个轮转被同状态瞬态版本耗光。
func injectOTelEnv(cm *config.Manager) error {
	settings, err := cm.LoadSettings()
	if err != nil {
		return err
	}
	envAny, _ := settings["env"].(map[string]any)
	env := make(map[string]string, len(envAny))
	for k, v := range envAny {
		if s, ok := v.(string); ok {
			env[k] = s
		}
	}

	// 控制权交接边界：若 settings.json 不在 ccl 当前控制下，备份一次作为健康基线。
	// 注意：调用发生在 backupAndInjectOTel 之前——此时 settings.json 仍是"接管前"状态，
	// 备份反映的是第三方/用户当前持有的版本，正是我们想保留的基线。
	if !isUnderCclControl(envAny) {
		if err := cm.Backup(); err != nil {
			log.Printf("injectOTelEnv: 备份 settings.json 失败: %v", err)
		}
	}

	updated := backupAndInjectOTel(env)

	// 比较是否真的改了
	changed := len(envAny) != len(updated)
	if !changed {
		for k, v := range updated {
			if cur, ok := envAny[k]; !ok || cur != v {
				changed = true
				break
			}
		}
	}
	if changed {
		settings["env"] = updated
		if err := cm.SaveSettings(settings); err != nil {
			return err
		}
	}

	// 把备份键同步到当前进程 env → fork ccl serve 时子进程继承。
	// 设计 14：daemon 读自己 env 的 CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT 决定是否启用 forwarder。
	// telemetry-config-compat §4：daemon 也读 FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT（per-signal 优先）。
	for k, v := range updated {
		if strings.HasPrefix(k, "CC_ORIG_") || strings.HasPrefix(k, "FORWORD_") {
			_ = os.Setenv(k, v)
		}
	}
	if auth := os.Getenv("CC_FORWARD_AUTH_HEADER"); auth != "" {
		_ = os.Setenv("CC_FORWARD_AUTH_HEADER", auth)
	}

	return nil
}

// isUnderCclControl 判断 settings.json 的 env map 是否仍处于 ccl 当前控制下。
//
// ccl 当前控制 = env 中至少一个端点字段还指向 ccl 默认地址：
//   - OTEL_EXPORTER_OTLP_ENDPOINT == "http://localhost:4318"（兜底端点）
//   - OTEL_EXPORTER_OTLP_METRICS_ENDPOINT == "http://localhost:4318/v1/metrics"（per-signal metrics）
//
// 都不指向 → ccl 当前不在控制 settings.json → 视为"控制权交接边界"。
//
// 注意：默认地址常量集中在 envconfig.go（keyEndpoint / defaultEndpoint /
// keyMetricsEndpoint / defaultMetricsEndpoint）。**若未来 ccl 默认值变化（如改端口、
// 加新端点），需同步更新 envconfig.go 的常量与本判断函数。**
//
// 入参 env 可为 nil 或缺字段；本函数只读、容忍 nil 和非 string 值。
func isUnderCclControl(env map[string]any) bool {
	if env == nil {
		return false
	}
	if v, ok := env[keyEndpoint].(string); ok && v == defaultEndpoint {
		return true
	}
	if v, ok := env[keyMetricsEndpoint].(string); ok && v == defaultMetricsEndpoint {
		return true
	}
	return false
}