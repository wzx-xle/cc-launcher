package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
)

// 合法的权限模式列表。
var validModes = []string{"manual", "acceptEdits", "auto", "bypassPermissions", "dontAsk", "plan"}

// CLI 处理 cc 子命令路由。
type CLI struct {
	providers *provider.Manager
	state     *state.Manager
}

// New 创建 CLI 路由器。
func New(pm *provider.Manager, sm *state.Manager) *CLI {
	return &CLI{providers: pm, state: sm}
}

// Run 根据 args 路由到对应子命令。args[0] 应为子命令名（"provider"、"mode"）。
func (c *CLI) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("缺少子命令")
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

func (c *CLI) runProvider(args []string) error {
	if len(args) == 0 {
		return c.listProviders()
	}

	switch args[0] {
	case "--list":
		return c.listProviders()
	case "--new":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		return c.newProvider(name)
	case "--delete":
		if len(args) < 2 {
			return fmt.Errorf("用法: cc provider --delete <name>")
		}
		return c.deleteProvider(args[1])
	default:
		if strings.HasPrefix(args[0], "-") {
			return fmt.Errorf("未知选项: %s", args[0])
		}
		return c.switchProvider(args[0])
	}
}

func (c *CLI) runMode(args []string) error {
	if len(args) == 0 {
		return c.listModes()
	}

	mode := args[0]
	if strings.HasPrefix(mode, "-") {
		return fmt.Errorf("未知选项: %s", mode)
	}

	for _, m := range validModes {
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
	return fmt.Errorf("无效的权限模式 %q，合法值: %s", mode, strings.Join(validModes, ", "))
}

func (c *CLI) listProviders() error {
	list, err := c.providers.List()
	if err != nil {
		return err
	}

	active := c.state.GetActiveProvider()
	if len(list) == 0 {
		fmt.Println("暂无提供商配置")
		return nil
	}

	for _, info := range list {
		marker := "  "
		if info.Name == active {
			marker = "▶ "
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
	fmt.Printf("已切换到 %s\n", name)
	return nil
}

func (c *CLI) newProvider(name string) error {
	if name == "" {
		fmt.Print("提供商名称: ")
		n, ok := readLine()
		if !ok {
			return fmt.Errorf("已取消")
		}
		name = strings.TrimSpace(n)
		if name == "" {
			return fmt.Errorf("提供商名称不能为空")
		}
	}

	if c.providers.Exists(name) {
		return fmt.Errorf("提供商 %q 已存在", name)
	}

	fmt.Print("base_url: ")
	baseURL, _ := readLine()
	baseURL = strings.TrimSpace(baseURL)

	fmt.Print("auth_token: ")
	apiKey, ok := readLine()
	if !ok {
		return fmt.Errorf("已取消")
	}
	apiKey = strings.TrimSpace(apiKey)

	if err := c.providers.Create(name, baseURL, apiKey); err != nil {
		return err
	}

	fmt.Printf("已创建提供商 %s\n", name)
	return nil
}

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

	for _, mode := range validModes {
		marker := "  "
		if mode == currentMode {
			marker = "▶ "
		}
		fmt.Printf("%s%s\n", marker, mode)
	}
	return nil
}

var stdinReader = bufio.NewReader(os.Stdin)

// readLine 从 stdin 读取一行（含空行）。返回 ("", false) 表示 EOF。
func readLine() (string, bool) {
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}
