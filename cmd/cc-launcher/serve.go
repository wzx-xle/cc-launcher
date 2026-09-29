package main

import (
	"context"
	"flag"
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
// 决策（见 design.md §11-12, decision-lifecycle spec + flatten-stop-restore-into-serve §D1）：
//   - 启动：写 pidfile → 探 SQLite → 开 HTTP server → 阻塞
//   - SIGTERM：503 metrics handler → drain queue → checkpoint WAL → close db → 删 pidfile → exit 0
//   - 整个过程 ≤ 5s（hard cap；超时就靠 ccl serve --stop SIGKILL）
//   - 端口被占（EADDRINUSE）：报错退出（ccl 自动降级到无 daemon 的 Claude Code）
//   - flatten-stop-restore-into-serve：新加 --stop / --restore / --status 三个 bool flag；命中任一则
//     分发到对应动作并 `return`，不走 daemon 启动；未命中走原启动流程
func runServe(args []string) error {
	// flatten-stop-restore-into-serve §2.1-2.5: 顶部 flag 解析。
	// --stop / --restore / --status 三者互斥（§2.4 / §9.4）；未命中走 daemon 启动。

	// 帮助旗标短路：先于 fs.Parse 处理 -h / --help，避免 fs 自动打"Usage of serve:"覆盖我们的 serveHelp。
	// 只在 args 只含 help 旗标时短路；若同时给了 --stop / --restore / --status 则正常进入 fs.Parse 走互斥检查。
	wantsHelpOnly := len(args) > 0
	for _, a := range args {
		if a != "-h" && a != "--help" {
			wantsHelpOnly = false
			break
		}
	}
	if wantsHelpOnly {
		fmt.Println(serveHelp)
		return nil
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	stopFlag := fs.Bool("stop", false, "停 OTel daemon（等价于原 ccl stop）")
	restoreFlag := fs.Bool("restore", false, "还原 ~/.claude/settings.json 到 ccl 之前的状态")
	statusFlag := fs.Bool("status", false, "打印 daemon 状态摘要（pid / 端口可达 / db 文件大小 / session 数）")
	if err := fs.Parse(args); err != nil {
		// flag.ErrHelp 已被上面的 wantsHelpOnly 截走；到这里的 error 是真解析错误。
		return err
	}
	// 互斥检查（§2.4 / §9.4）：任何两个 flag 同时为 true 即报错并打用法提示。
	if (*stopFlag && *restoreFlag) || (*stopFlag && *statusFlag) || (*restoreFlag && *statusFlag) {
		fmt.Fprintln(os.Stderr, "用法: ccl serve [--stop | --restore | --status]（三者互斥）")
		fmt.Fprintln(os.Stderr, "详见 `ccl serve --help`")
		return fmt.Errorf("--stop / --restore / --status 互斥；不可同时给")
	}

	if *stopFlag {
		// §2.2：调 runStopInner 后直接返回（不走 daemon 启动）。
		return runStopInner(fs.Args())
	}
	if *restoreFlag {
		// §2.3：调 runRestoreInner 后直接返回（不走 daemon 启动）。
		return runRestoreInner(fs.Args())
	}
	if *statusFlag {
		// §9.3：调 runStatusInner 后直接返回（不走 daemon 启动）。
		return runStatusInner()
	}

	// §2.5：未命中 --stop / --restore / --status → 走原 daemon 启动流程。
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
		// 不返回 error——我们已经走到 graceful path；ccl serve --stop 会兜底 SIGKILL
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

const serveHelp = `用法: ccl serve [--stop | --restore | --status]

ccl 的 OTel daemon lifecycle and ccl settings restore 入口。

默认行为（不带 flag）= 启动 OTel daemon：
  监听 :4318 接收 Claude Code 推送的 OTel metrics；写入本地 SQLite
  (~/.cc-launcher/telemetry.db)。

设计要点（启动段）：
  - 由 ccl 自动 fork；通常不需手动跑。
  - 跨平台 graceful shutdown：SIGTERM → 5s 内关 HTTP + drain queue + 关 db。
  - 端口冲突即退出（提示设置 OTEL_EXPORTER_OTLP_ENDPOINT 覆盖）。
  - 永不自动 idle 退出；要关就用 ccl serve --stop。

--stop
  发 SIGTERM 给 ~/.cc-launcher/daemon.pid 里的 daemon。
  daemon 收到 SIGTERM 后 5s 内 graceful shutdown（503 metrics + drain queue + 关 db）。
  超时则 ccl serve --stop 发 SIGKILL。找不到 daemon 时打"daemon not running"并 exit 0。

--restore
  读 ~/.claude/settings.json，还原 ccl 注入的 OTel env：
    - CC_ORIG_<KEY> 还原到 <KEY>，删 CC_ORIG_*
    - 删除 ccl 注入的默认值（如 OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318）
    - 其它用户自有的 env 键不动
  不可逆：清掉后下次 ccl 启动会重新注入。
  操作前会先备份当前 settings.json 到 ~/.cc-launcher/backups/settings-<时间戳>.json。

--status
  打印 daemon 状态摘要（pid / 端口可达 / db 文件大小 / session 数）；
  db 缺失时不报错，输出 "db: missing"。与 --stop / --restore 互斥。

-h, --help
  显示本帮助并退出。`