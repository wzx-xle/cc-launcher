package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cc-launcher/internal/collector"
	"cc-launcher/internal/web"
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
//   - daemon 热重启：新增 --restart flag，SIGTERM 旧 daemon + fork 新 binary daemon，Claude Code session 不受影响
func runServe(args []string) error {
	// flatten-stop-restore-into-serve §2.1-2.5 + restart flag: 顶部 flag 解析。
	// --stop / --restore / --status / --restart 四者互斥（§2.4 / §9.4）；未命中走 daemon 启动。

	// 帮助旗标短路：先于 fs.Parse 处理 -h / --help，避免 fs 自动打"Usage of serve:"覆盖我们的 serveHelp。
	// 只在 args 只含 help 旗标时短路；若同时给了 --stop / --restore / --status / --restart 则正常进入 fs.Parse 走互斥检查。
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
	stopFlag, restoreFlag, statusFlag, restartFlag, backfillFlag := registerServeFlags(fs)
	if err := fs.Parse(args); err != nil {
		// flag.ErrHelp 已被上面的 wantsHelpOnly 截走；到这里的 error 是真解析错误。
		return err
	}
	// 互斥检查：--stop / --restore / --status / --restart / --backfill 任意两个
	// 同时为 true 即报错并打用法提示。
	//
	// --backfill 也在互斥组里（web-backfill-to-serve §D1）：它与另外四个一样是
	// "对 daemon 数据 / 状态的维护动作"，都是一次性动作后立即返回。
	flags := []*bool{stopFlag, restoreFlag, statusFlag, restartFlag, backfillFlag}
	set := 0
	for _, f := range flags {
		if *f {
			set++
		}
	}
	if set > 1 {
		fmt.Fprintln(os.Stderr, "用法: ccl serve [--stop | --restore | --status | --restart | --backfill]（互斥）")
		fmt.Fprintln(os.Stderr, "详见 `ccl serve --help`")
		return fmt.Errorf("--stop / --restore / --status / --restart / --backfill 互斥；不可同时给")
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
	if *restartFlag {
		// restart flag: 热重启 daemon（见 restartDaemon 注释）。
		return restartDaemon()
	}
	if *backfillFlag {
		// backfill flag: 不限量的历史 cwd 回填（见 runBackfillInner 注释）。
		return runBackfillInner()
	}

	// §2.5：未命中 --stop / --restore / --status / --restart → 走原 daemon 启动流程。
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

	// 开 SQLite。注入 cwd resolver：payload 没带 ccl.cwd 时用 Claude Code 的
	// 会话记录反查（session-cwd-enrichment）。
	resolver, resolverErr := newCwdResolver()
	var storeOpts []collector.OpenStoreOption
	if resolverErr == nil {
		storeOpts = append(storeOpts, collector.WithCwdResolver(resolver))
	} else {
		log.Printf("ccl serve: 定位 Claude Code 会话目录失败，cwd 兜底反查不可用: %v", resolverErr)
	}
	store, err := collector.OpenStore(dbPath, storeOpts...)
	if err != nil {
		return fmt.Errorf("开 db 失败: %w", err)
	}

	// 配 forwarder（auto-detect CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT）。
	fwd := collector.NewForwarderFromEnv()

	// 组装 HTTP server。看板路由由 cmd 层挂载（collector 不反向依赖 web，
	// 见 collector.ServerOptions.Mount 与 design.md §D2）。
	webSrv, err := web.NewServer(web.Options{
		DBPath:    dbPath,
		TokenPath: filepath.Join(stateBaseDir, web.TokenFileName),
	})
	if err != nil {
		// 看板不可用不该阻断 daemon（OTel 采集是主路径）。
		log.Printf("ccl serve: 看板初始化失败（/ui 不可用）: %v", err)
	}
	srv := collector.NewServer(collector.ServerOptions{
		Addr:    ":4318",
		Store:   store,
		Forward: fwd,
		Mount: func(mux *http.ServeMux) {
			if webSrv != nil {
				webSrv.Mount(mux)
			}
		},
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

	// 后台回填历史 session 的 cwd：等 listener 就绪后再跑，**不阻塞**端口绑定
	// （页面立即可用，回填逐步生效）。每次启动最多 500 行；剩余的留给下次
	// 启动，或让用户跑 `ccl web --backfill` 显式补齐。
	go func() {
		if err := waitForDaemonReady(); err != nil {
			log.Printf("ccl serve: 等 listener 就绪超时，跳过本次 cwd 回填: %v", err)
			return
		}
		resolved, failed, err := store.BackfillCwd(500)
		if err != nil {
			log.Printf("ccl serve: cwd 回填失败: %v", err)
			return
		}
		log.Printf("ccl serve: cwd 回填完成: resolved=%d failed=%d", resolved, failed)
	}()

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

// registerServeFlags 把 serve 子命令的 flag 注册到 fs，返回值供 runServe 与
// cmd/cc-launcher/completion.go::completeAtServe 共用。
//
// 实施前置（flatten-stop-restore-into-serve change）：4 个 flag（--stop / --restore / --status / --restart）
// 在 runServe 内做 mutex 检查（互斥），返回的指针即 mutex 检查 / dispatch 用。
//
// 为什么必须抽 helper：cli-shell-completion §D2 要求"flag 改动自动同步"——
// 加新 flag 时（仅改 registerServeFlags）completion 自动跟随；若不抽，
// `runServe` 加 flag 而 `completeAtServe` 漏改 → runtime 接受 / completion 提示脱节。
func registerServeFlags(fs *flag.FlagSet) (stop, restore, status, restart, backfill *bool) {
	stop = fs.Bool("stop", false, "停 OTel daemon（等价于原 ccl stop）")
	restore = fs.Bool("restore", false, "清理旧版 ccl 留在 ~/.claude/settings.json 里的 OTel 注入痕迹")
	status = fs.Bool("status", false, "打印 daemon 状态摘要（pid / 端口可达 / db 文件大小 / session 数）")
	restart = fs.Bool("restart", false, "热重启 daemon（SIGTERM 旧 daemon + fork 新 binary daemon）")
	backfill = fs.Bool("backfill", false, "回填历史 session 的 cwd（不限量；查不到的保持未知项目）")
	return
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

// restartDaemon 热重启 daemon：SIGTERM 旧 daemon + fork 新 binary daemon，等 :4318 端口就绪。
//
// 用途（升级 ccl 后）：旧 daemon 进程仍在跑旧 binary 的内存映射；直接 fork 出来的也是旧 binary
// （磁盘 inode 替换不影响已映射进程）。必须先 SIGTERM 旧 daemon，让位端口，然后 fork 出去的新
// 子进程才会用新 binary（fork 那一刻 os.Executable() 拿的是已替换的磁盘路径）。
//
// Claude Code session 不受影响：daemon 重启期间 :4318 短暂不可用，OTel SDK 自动重连；
// session 本身（对话、状态）跟 daemon 完全解耦，不丢。
//
// 流程：
//  1. 读 pidfile 拿旧 pid（不存在 → 视为 not running，仅 fork 新 daemon）
//  2. SIGTERM 旧 daemon（graceful shutdown ≤ 5s），best-effort 删 pidfile
//  3. forkDaemon fork 当前 binary 为新 ccl serve 子进程（新 PGID，disown）
//  4. waitForDaemonReady 阻塞等 :4318 可达（≤ 3s）；超时则失败（fork 出来但未 ready）
//
// 失败语义：
//   - 旧 daemon 不在 → 跳过 SIGTERM，仅 fork
//   - SIGTERM 失败 → 仍继续 fork（让新 daemon 强占端口；旧 daemon 残留 → 用户可见的 port 冲突）
//   - fork 失败 → 返回 error，新 daemon 未起来
//   - 端口未就绪 → 返回 error，新 daemon 已 fork 但 3s 内未 bind 端口（孤儿进程）
func restartDaemon() error {
	stateBaseDir, err := stateDirOrError()
	if err != nil {
		return err
	}
	pidPath := filepath.Join(stateBaseDir, "daemon.pid")

	// 1. 旧 daemon pid
	oldPid := readPidfile(pidPath)
	if oldPid > 0 {
		fmt.Printf("stopping old daemon pid=%d...\n", oldPid)
		if err := stopDaemon(oldPid); err != nil {
			// SIGTERM 失败不阻断——继续 fork；旧 daemon 残留会引发 :4310 EADDRINUSE，
			// 新 fork 出来的子进程会自己探到然后本 logging.error 进入 .tmp 路径。
			log.Printf("ccl serve --restart: SIGTERM 旧 daemon 失败: %v（继续 fork 新 daemon）", err)
		}
		// best-effort 删 pidfile——旧 daemon graceful shutdown 时通常会自己删，但超时 SIGKILL 不会。
		_ = os.Remove(pidPath)
		fmt.Println("old daemon stopped")
	} else {
		fmt.Println("no running daemon; starting fresh")
	}

	// 2. fork 新 daemon
	if err := forkDaemon(); err != nil {
		return err
	}
	fmt.Println("forked new daemon")

	// 3. 等端口就绪
	if err := waitForDaemonReady(); err != nil {
		return fmt.Errorf("新 daemon fork 成功但 3s 内未就绪: %w", err)
	}
	fmt.Println("daemon ready (port :4318 reachable)")
	return nil
}

// runBackfillInner 是 `ccl serve --backfill` 的实现。
//
// 行为（web-backfill-to-serve §D1 / spec/daemon-lifecycle §"ccl serve
// --backfill runs the historical cwd backfill"）：
//   - **不限量**回填（daemon 启动时的自动回填每次最多 500 行）
//   - 幂等：只处理 cwd = ” 的行，不改 schema
//   - 打印 resolved / failed 计数；查不到的行是正常状态（不是错误）
//
// 独立进程直接操作 db 文件（WAL 模式下与 daemon 的写入并发安全）。
func runBackfillInner() error {
	stateBaseDir, err := stateDirOrError()
	if err != nil {
		return err
	}
	dbPath := filepath.Join(stateBaseDir, "telemetry.db")

	resolver, err := newCwdResolver()
	if err != nil {
		return fmt.Errorf("定位 Claude Code 会话目录失败: %w", err)
	}
	resolved, failed, err := collector.BackfillCwdInFile(dbPath, resolver, 0)
	if err != nil {
		return fmt.Errorf("cwd 回填失败: %w", err)
	}
	fmt.Printf("cwd 回填完成: resolved=%d failed=%d\n", resolved, failed)
	if failed > 0 {
		fmt.Println("  failed 的行是查不到会话记录的历史会话（Claude Code 已清理），保持「未知项目」。")
	}
	return nil
}

const serveHelp = `用法: ccl serve [--stop | --restore | --status | --restart | --backfill]

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
  清理**旧版 ccl** 留在 ~/.claude/settings.json 里的 OTel 注入痕迹：
    - CC_ORIG_<KEY> / FORWORD_<KEY> 还原到 <KEY>，删掉备份键
    - 删除旧版 ccl 注入的默认值（如 OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318）
    - 其它用户自有的 env 键不动
  新版 ccl 通过 Claude Code 的 --settings 参数交付 OTel 配置，**不再改写
  settings.json**——这里没有"当前接管"可撤销，只有旧版留下的痕迹要清理。
  升级后第一次跑 ccl 会自动清理一次（幂等）。
  操作前会先备份当前 settings.json 到 ~/.cc-launcher/backups/settings-<时间戳>.json。

--status
  打印 daemon 状态摘要（pid / 端口可达 / db 文件大小 / session 数）；
  db 缺失时不报错，输出 "db: missing"。与 --stop / --restore 互斥。

--restart
  热重启 daemon：SIGTERM 旧 daemon（graceful shutdown ≤ 5s）+ fork 新 binary daemon + 等端口就绪。
  升级 ccl 二进制后用：先 ./build.sh local，再 ccl serve --restart，新版本 daemon 上位，
  Claude Code session 不受影响（OTel SDK 自动重连）。
  无旧 daemon 时等同于 fork 新 daemon。
  与 --stop / --restore / --status 互斥。

--backfill
  回填历史 session 的 cwd：把 sessions 表里 cwd 为空的行，用 Claude Code 的
  会话记录（~/.claude/projects/*/<session-id>.jsonl 里的顶层 cwd 字段）补上。
  与 daemon 启动时的自动回填不同，这里的回填**不限量**（自动回填每次启动
  最多 500 行）。
  幂等：只处理 cwd 为空的行，跑两次结果一致；查不到的行（Claude Code 已清理
  的旧会话）保持「未知项目」，不是错误。
  与 --stop / --restore / --status / --restart 互斥。

-h, --help
  显示本帮助并退出。`
