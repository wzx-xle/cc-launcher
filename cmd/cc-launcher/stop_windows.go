//go:build windows

package main

import (
	"fmt"
	"os"
	"time"
)

// stopDaemonWindows 在 Windows 上按 PID 终止 daemon。
//
// Windows 没有 PGID 概念，且 syscall.Kill 不可用；用 os.FindProcess + Kill（硬终止）。
// 个人 CLI 接受硬终止：进程退出后 SQLite WAL 自动恢复。
func stopDaemon(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("无效 pid: %d", pid)
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		// 进程不存在视作成功（幂等）。
		return nil
	}

	// 第一次发 Kill（Windows 上是 TerminateProcess，硬终止）。
	if err := p.Kill(); err != nil {
		return fmt.Errorf("kill pid %d: %w", pid, err)
	}

	// 等 ≤ 5s，期间探进程是否还在。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		// Windows 上 Process.Signal 不支持 0 信号；改用 FindProcess 错误判断。
		if _, err := os.FindProcess(pid); err != nil {
			return nil
		}
	}
	// 兜底再 Kill 一次（Windows 上 Kill 即硬终止，再发一次没坏处）
	_ = p.Kill()
	time.Sleep(200 * time.Millisecond)
	return nil
}