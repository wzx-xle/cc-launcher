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
    provider          管理提供商（详见 ccl provider --help）
    mode              管理当前目录的权限模式（详见 ccl mode --help）

选项:
    -h, --help        显示本帮助并退出

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

	fmt.Printf("正在启动 Claude Code (%s / %s / %s)...\n", result.Provider, result.Mode, sessionLabel)
	return launcher.Launch(opts)
}
