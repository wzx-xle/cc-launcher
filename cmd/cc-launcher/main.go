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

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	// 初始化各 Manager
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

	// 无参数 → TUI 启动流程
	if len(args) == 0 {
		return runTUI(providerMgr, stateMgr, configMgr)
	}

	// 有参数 → 路由到 CLI 子命令
	c := cli.New(providerMgr, stateMgr)
	return c.Run(args)
}

func runTUI(pm *provider.Manager, sm *state.Manager, cm *config.Manager) error {
	// 加载提供商列表
	providers, err := pm.List()
	if err != nil {
		return fmt.Errorf("加载提供商列表失败: %w", err)
	}

	// 获取默认值
	defaultProvider := sm.GetActiveProvider()
	wd, _ := os.Getwd()
	defaultMode, _ := sm.GetPermissionMode(wd)

	// TUI 选择
	selectedProvider, selectedMode, err := tui.Run(providers, defaultProvider, defaultMode)
	if err != nil {
		if err == tui.ErrCancelled {
			return nil
		}
		return err
	}

	// 提供商变更时合并配置
	if selectedProvider != defaultProvider {
		fmt.Print("正在切换提供商配置... ")

		providerCfg, err := pm.Get(selectedProvider)
		if err != nil {
			fmt.Println("✗")
			return err
		}

		oldSnapshot := sm.GetInjectedSnapshot()
		if err := cm.Merge(providerCfg, oldSnapshot); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("配置合并失败: %w", err)
		}

		sm.SetActiveProvider(selectedProvider)
		sm.SetInjectedSnapshot(providerCfg)
		fmt.Println("✓")
	}

	sm.SetPermissionMode(wd, selectedMode)

	fmt.Printf("正在启动 Claude Code (%s / %s)...\n", selectedProvider, selectedMode)
	return launcher.Launch(selectedMode)
}
