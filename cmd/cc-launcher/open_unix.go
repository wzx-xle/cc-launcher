//go:build !windows

package main

import (
	"fmt"
	"os/exec"
)

// openBrowser 用系统默认浏览器打开 URL。
//
// Unix：优先 xdg-open（Linux），没有则试 open（macOS）。
// 只启动浏览器进程，不等待它退出。
func openBrowser(url string) error {
	for _, cmd := range []string{"xdg-open", "open"} {
		if _, err := exec.LookPath(cmd); err != nil {
			continue
		}
		if err := exec.Command(cmd, url).Start(); err != nil {
			return fmt.Errorf("%s 启动失败: %w", cmd, err)
		}
		return nil
	}
	return fmt.Errorf("找不到 xdg-open / open，无法打开浏览器")
}
