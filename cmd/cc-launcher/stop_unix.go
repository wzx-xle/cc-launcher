//go:build !windows

package main

import (
	"fmt"
	"syscall"
	"time"
)

// stopDaemonUnix 在 Unix 上 SIGTERM daemon 的 PGID，等 ≤ 5s，超时发 SIGKILL。
//
// 设计（见 design.md §13 + daemon-lifecycle spec "stop kills daemon gracefully (Unix)"）：
//   - daemon 是 ccl 在新进程组 fork 的；PGID == daemon PID
//   - 用 kill(-PID, SIGTERM) 杀整个 PGID，避免漏杀（daemon 不会 fork 子进程，但稳一点）
//   - 不用 SIGKILL 是因为 daemon 自己有 graceful shutdown
func stopDaemon(pid int) error {
	return stopDaemonWithin(pid, 5*time.Second)
}

// stopDaemonWithin 与 stopDaemon 相同，但等待死线可调。
//
// 死线为什么要能调：daemon 自己的关闭预算是 10s（HTTP server 的 5s + serve.go
// 的 5s），而 SIGKILL 兜底会**打断 Close 里的 wal_checkpoint(TRUNCATE)** → 留下
// 残缺 WAL。`ccl serve --compact` 走这条路径时用 15s。
func stopDaemonWithin(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("无效 pid: %d", pid)
	}

	// 发 SIGTERM 给 PGID（负 PID）。
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// ESRCH = 进程已不在；视作成功。
		if err == syscall.ESRCH {
			return nil
		}
		return fmt.Errorf("kill -%d SIGTERM: %w", pid, err)
	}

	// 等到死线，期间每隔 100ms 探一次。
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		// 发 0 信号探测；ESRCH = 进程已退。
		if err := syscall.Kill(-pid, 0); err != nil {
			if err == syscall.ESRCH {
				return nil
			}
			return err
		}
	}

	// 超时：发 SIGKILL 给 PGID。
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("kill -%d SIGKILL: %w", pid, err)
	}
	// 再给一点时间让内核回收。
	time.Sleep(200 * time.Millisecond)
	return nil
}
