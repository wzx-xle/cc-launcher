package main

import (
	"fmt"
	"os"
	"path/filepath"

	"cc-launcher/internal/cli"
	"cc-launcher/internal/config"
	"cc-launcher/internal/launcher"
	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
	"cc-launcher/internal/tui"
)

// topHelp 是顶层 ccl --help 输出的内容。
const topHelp = `用法: ccl [子命令] [选项]

子命令:
    (无)              启动 TUI 引导：选提供商 → 选权限模式 → 启动 Claude Code
    serve             OTel daemon 模式（ccl 自动 fork，通常不需手动跑）
    stats             显示 token / cost 聚合（直读 SQLite，无需 daemon 在跑）
    stop              停掉 OTel daemon
    restore           还原 settings.json 到 ccl 之前的状态
    provider          管理提供商（详见 ccl provider --help）
    mode              管理当前目录的权限模式（详见 ccl mode --help）

选项:
    -h, --help        显示本帮助并退出

用量监控：
  - ccl 首次跑会拉起 OTel daemon（监听 :4318）
  - 跑过 Claude Code session 后用 ccl stats 查看
  - 不要 daemon 时跑 ccl stop；要彻底清除注入用 ccl restore
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
	// 顶层 --help：只在 args[0] 是 help 旗标时触发（避免误吞 `ccl provider --help`）。
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Println(topHelp)
			return nil
		case "serve":
			return runServe(args[1:])
		case "stats":
			return runStats(args[1:])
		case "stop":
			return runStop(args[1:])
		case "restore":
			return runRestore(args[1:])
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
	configMgr := config.NewManager(settingsPath, backupDir, 10)

	if len(args) == 0 {
		return runTUI(providerMgr, stateMgr, configMgr)
	}

	c := cli.New(providerMgr, stateMgr)
	return c.Run(args)
}

func runTUI(pm *provider.Manager, sm *state.Manager, cm *config.Manager) error {
	providers, err := pm.List()
	if err != nil {
		return fmt.Errorf("加载提供商列表失败: %w", err)
	}

	defaultProvider := sm.GetActiveProvider()
	wd, _ := os.Getwd()
	defaultMode, _ := sm.GetPermissionMode(wd)

	result, err := tui.Run(providers, defaultProvider, defaultMode)
	if err != nil {
		if err == tui.ErrCancelled {
			return nil
		}
		return err
	}

	// 提供商变更时合并配置
	if result.Provider != defaultProvider {
		fmt.Print("正在切换提供商配置... ")

		providerCfg, err := pm.Get(result.Provider)
		if err != nil {
			fmt.Println("✗")
			return err
		}

		oldSnapshot := sm.GetInjectedSnapshot()
		if err := cm.Merge(providerCfg, oldSnapshot); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("配置合并失败: %w", err)
		}

		sm.SetActiveProvider(result.Provider)
		sm.SetInjectedSnapshot(providerCfg)
		fmt.Println("✓")
	}

	sm.SetPermissionMode(wd, result.Mode)

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
	return launcher.Launch(opts)
}

// injectOTelEnv 在 exec Claude Code 前把 OTel env 注入 ~/.claude/settings.json。
//
// 失败仅警告，不阻断 ccl 主流程（决策 10：daemon 是 optional）。
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
	if !changed {
		return nil // 无变更，不写盘
	}

	settings["env"] = updated
	return cm.SaveSettings(settings)
}