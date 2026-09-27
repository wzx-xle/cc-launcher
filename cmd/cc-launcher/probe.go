package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// probeDaemonAddr 探 :4318 是否可达（200ms timeout）。
//
// 返回 nil = 端口在用（daemon 可能活着）；返回 error = 端口空。
func probeDaemonAddr() error {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:4318", 200*time.Millisecond)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// waitForDaemonReady 阻塞等 :4318 可达，最多 3s。
//
// 每 100ms 探一次；端口可达立即返回。
func waitForDaemonReady() error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if probeDaemonAddr() == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon 在 3s 内未就绪")
}

// forkDaemon fork 当前 binary 为 ccl serve 子进程。
//
// 决策（见 design.md §3）：
//   - SysProcAttr.Setpgid = true → daemon 在新进程组（独立于 ccl / Claude Code）
//   - stdin/stdout/stderr = nil → disown
//   - Wait = nil（不等）；ccl 自己探端口就绪
func forkDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("找自己可执行文件失败: %w", err)
	}

	cmd := exec.Command(exe, "serve")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	configureSetpgid(cmd) // 跨平台：unix + windows 都设 best-effort

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork ccl serve 失败: %w", err)
	}
	// 不 Wait——disown
	go func() { _ = cmd.Wait() }()
	return nil
}

// ensureDaemonRunning 探活 + 按需 fork；超时/失败降级（不返回 error）。
//
// 决策（见 design.md §10）：fork 失败/超时 → ccl 仍 exec Claude Code，OTel 静默失败。
//
// 返回的 started=true 表示本次 fork 了新 daemon；started=false 表示本来就在或 fork 失败。
func ensureDaemonRunning() (started bool, err error) {
	if probeDaemonAddr() == nil {
		return false, nil // 端口在用 → 已有 daemon
	}

	// 用户若配了远程 endpoint，ccl 不应拉本地 daemon；本地 OTel 没意义。
	// 但 daemon 仍可作为转发 hub 起到作用？——决策 14：daemon 在
	// CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT 非空非 localhost 时启用 forwarder。
	// 所以即使有远程，也拉本地 daemon + forward；保留 stats 功能。
	if err := forkDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "OTel daemon 拉起失败: %v；usage stats 本次将不记录\n", err)
		return false, err
	}
	if err := waitForDaemonReady(); err != nil {
		fmt.Fprintf(os.Stderr, "OTel daemon 在 3s 内未就绪: %v\n", err)
		return true, err
	}
	return true, nil
}

// cclLauncherBaseDir 返回 ~/.cc-launcher 路径（cmd 公共）。
func cclLauncherBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// 极端情况：HOME 不可用。回退到 cwd（仅 debug）。
		return ".cc-launcher"
	}
	return filepath.Join(home, ".cc-launcher")
}