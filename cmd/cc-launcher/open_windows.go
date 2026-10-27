//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

// openBrowser 用系统默认浏览器打开 URL。
//
// Windows：cmd /c start "" <url>——空串是窗口标题占位，避免 URL 被当成标题。
// 只启动浏览器进程，不等待它退出。
func openBrowser(url string) error {
	if err := exec.Command("cmd", "/c", "start", "", url).Start(); err != nil {
		return fmt.Errorf("start 失败: %w", err)
	}
	return nil
}
