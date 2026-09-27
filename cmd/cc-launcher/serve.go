package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cc-launcher/internal/collector"
)

// runServe 是 `ccl serve` 的入口。
//
// 决策（见 design.md §11-12, decision-lifecycle spec）：
//   - 启动：写 pidfile → 探 SQLite → 开 HTTP server → 阻塞
//   - SIGTERM：503 metrics handler → drain queue → checkpoint WAL → close db → 删 pidfile → exit 0
//   - 整个过程 ≤ 5s（hard cap；超时就靠 ccl stop SIGKILL）
//   - 端口被占（EADDRINUSE）：报错退出（ccl 自动降级到无 daemon 的 Claude Code）
func runServe(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(serveHelp)
		return nil
	}

	stateBaseDir, err := stateDirOrError()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateBaseDir, 0755); err != nil {
		return fmt.Errorf("建 state 目录失败: %w", err)
	}

	pidPath := filepath.Join(stateBaseDir, "daemon.pid")
	dbPath := filepath.Join(stateBaseDir, "telemetry.db")

	// 写 pidfile（写 .tmp 再 rename 防半写状态）。
	if err := writePidfile(pidPath); err != nil {
		return fmt.Errorf("写 pidfile 失败: %w", err)
	}
	// 任何错误路径都先删 pidfile；不依赖 defer（signal handler 也要能删）。
	pidfileRemove := func() { _ = os.Remove(pidPath) }
	defer pidfileRemove()

	// 探 :4318（提前 EADDRINUSE 检测；ccl main 在 fork 后也会探，但 daemon 自己再确认）。
	ln, err := net.Listen("tcp", ":4318")
	if err != nil {
		return fmt.Errorf("port 4318 already in use; either stop the conflicting service or set OTEL_EXPORTER_OTLP_ENDPOINT to a different port and re-run claude with that env var: %w", err)
	}
	_ = ln.Close()

	// 开 SQLite。
	store, err := collector.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("开 db 失败: %w", err)
	}

	// 配 forwarder（auto-detect CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT）。
	fwd := collector.NewForwarderFromEnv()

	// 组装 HTTP server。
	srv := collector.NewServer(collector.ServerOptions{
		Addr:    ":4318",
		Store:   store,
		Forward: fwd,
	})

	// Signal handler：SIGTERM → graceful shutdown；SIGINT 同样。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("ccl serve: 收到信号 %v，开始 graceful shutdown...", sig)
		srv.SetShuttingDown()
		cancel()
	}()

	log.Printf("ccl serve: 监听 :4318（pid=%d），db=%s", os.Getpid(), dbPath)
	if err := srv.Start(ctx); err != nil && err != context.Canceled {
		// Server 异常退出（端口阻塞等不该走到这里——前面已探）。
		return fmt.Errorf("server 异常: %w", err)
	}

	// Drain + 关闭 store（≤ 5s 整体预算）。
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	if err := store.Close(shutCtx); err != nil {
		log.Printf("ccl serve: store 关闭未在 5s 内完成: %v", err)
		// 不返回 error——我们已经走到 graceful path；ccl stop 会兜底 SIGKILL
	}

	log.Printf("ccl serve: 关闭完成")
	return nil
}

// writePidfile 原子写 pidfile：写 .tmp 再 rename。
func writePidfile(path string) error {
	tmp := path + ".tmp"
	pid := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(tmp, []byte(pid), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readPidfile 读 pidfile；不存在 / 解析失败 → 返回 0。
func readPidfile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pid int
	_, _ = fmt.Sscanf(string(data), "%d", &pid)
	return pid
}

// stateDirOrError 返回 ~/.cc-launcher 路径；home 不可用则报错。
func stateDirOrError() (string, error) {
	dir := cclLauncherBaseDir()
	if dir == "" || dir == ".cc-launcher" {
		return "", fmt.Errorf("无法获取 home 目录")
	}
	return dir, nil
}

const serveHelp = `用法: ccl serve

ccl 的 OTel daemon 模式。

监听 :4318 接收 Claude Code 推送的 OTel metrics；写入本地 SQLite
(~/.cc-launcher/telemetry.db)。

设计要点：
  - 由 ccl 自动 fork；通常不需手动跑。
  - 跨平台 graceful shutdown：SIGTERM → 5s 内关 HTTP + drain queue + 关 db。
  - 端口冲突即退出（提示设置 OTEL_EXPORTER_OTLP_ENDPOINT 覆盖）。
  - 永不自动 idle 退出；要关就用 ccl stop。`