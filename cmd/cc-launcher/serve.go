package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cc-launcher/internal/collector"
	"cc-launcher/internal/config"
	"cc-launcher/internal/provider"
	"cc-launcher/internal/quota"
	"cc-launcher/internal/state"
	"cc-launcher/internal/web"

	_ "modernc.org/sqlite"
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
	stopFlag, restoreFlag, statusFlag, restartFlag, backfillFlag, compactFlag := registerServeFlags(fs)
	if err := fs.Parse(args); err != nil {
		// flag.ErrHelp 已被上面的 wantsHelpOnly 截走；到这里的 error 是真解析错误。
		return err
	}
	// 互斥检查：--stop / --restore / --status / --restart / --backfill / --compact
	// 任意两个同时为 true 即报错并打用法提示。
	//
	// --backfill 与 --compact 也在互斥组里（web-backfill-to-serve §D1 /
	// telemetry-db-compaction §D7）：它们与另外四个一样是"对 daemon 数据 / 状态
	// 的维护动作"，都是一次性动作后立即返回。
	flags := []*bool{stopFlag, restoreFlag, statusFlag, restartFlag, backfillFlag, compactFlag}
	set := 0
	for _, f := range flags {
		if *f {
			set++
		}
	}
	if set > 1 {
		fmt.Fprintln(os.Stderr, "用法: ccl serve [--stop | --restore | --status | --restart | --backfill | --compact]（互斥）")
		fmt.Fprintln(os.Stderr, "详见 `ccl serve --help`")
		return fmt.Errorf("--stop / --restore / --status / --restart / --backfill / --compact 互斥；不可同时给")
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
	if *compactFlag {
		// compact flag: 收缩 telemetry.db（见 runCompactInner 注释）。
		return runCompactInner()
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
	//
	// 控制台（provider / Claude 配置 / 额度）需要注入三个管理器：
	// provider.Manager（providers 目录）、Claude 两个文件路径、quota.Store。
	// 全部来自 daemon 已经在用的那几个目录——不新增路径、不新增进程。
	providersDir := filepath.Join(stateBaseDir, "providers")
	backupDir := filepath.Join(stateBaseDir, "backups")
	providersMgr := provider.NewManager(providersDir)
	providersMgr.SetBackupDir(backupDir)
	claudeSettingsPath, _ := config.DefaultSettingsPath()
	claudeMemoryPath, _ := config.DefaultMemoryPath()
	webSrv, err := web.NewServer(web.Options{
		DBPath:             dbPath,
		TokenPath:          filepath.Join(stateBaseDir, web.TokenFileName),
		Providers:          providersMgr,
		ClaudeSettingsPath: claudeSettingsPath,
		ClaudeMemoryPath:   claudeMemoryPath,
		BackupDir:          backupDir,
		QuotaStore:         quota.NewStore(stateBaseDir),
	})
	if err != nil {
		// 看板不可用不该阻断 daemon（OTel 采集是主路径）。
		log.Printf("ccl serve: 看板初始化失败（/ui 不可用）: %v", err)
	}
	// 激活通道：ccl 启动 Claude Code 前会声明"本次用 provider X" → daemon
	// 立刻把它标记为活跃并刷新一轮（不必等 Claude Code 起来推 OTel）。
	//
	// 缓冲 8 个：一次 ccl 启动只发一条，8 足够；满了就丢弃（下一轮 ticker
	// 会补上），**绝不能阻塞 HTTP handler**。
	activateCh := make(chan string, 8)

	srv := collector.NewServer(collector.ServerOptions{
		Addr:    ":4318",
		Store:   store,
		Forward: fwd,
		Mount: func(mux *http.ServeMux) {
			if webSrv != nil {
				webSrv.Mount(mux)
			}
		},
		OnActivate: func(provider string) error {
			return notifyActivate(activateCh, provider, filepath.Join(stateBaseDir, "providers"))
		},
		// 只读额度端点（GET /ccl/quota?provider=X）：状态栏用它**零进程**读额度
		//（Windows 上起进程 p50 170ms，状态栏的预算只有 250ms）。
		OnQuota: func(name string) (any, int) {
			return serveQuota(stateBaseDir, name)
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

	// 额度刷新（provider-quota）：周期刷新**最近有活动的** provider 的额度，
	// 写进 ~/.cc-launcher/quota.json。
	//
	// **独立 goroutine + ctx 取消**：不阻塞端口绑定、不干扰 metrics 服务，
	// graceful shutdown 仍在 5s 预算内完成（ctx 取消会中断在途的 HTTP 请求）。
	//
	// **启动即刷一轮**（不等一个周期）→ 用户跑完一次 ccl（它会拉起 serve）
	// 就能读到数据。
	//
	// **启动兜底**：daemon 的活跃度是纯内存状态，刚起来时是空的 → 那一轮
	// 会一个 provider 都不查（实测 serve.log：「查了 0 / 不活跃跳过 6」，且
	// 整整 60 秒才恢复）。把 state.json 里记录的 active provider 标为活跃，
	// 启动那轮就有池可刷。
	markRecordedActiveProvider(srv.Activity(), stateBaseDir)

	go quotaRefreshLoop(ctx, stateBaseDir, srv.Activity(), activateCh)

	// 保留期清理（telemetry-db-compaction §D6）：启动即清一次，之后每 24h 一次。
	go retentionPruneLoop(ctx, store)

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

// retentionDays 是遥测数据的保留期（天）。
//
// 90 天覆盖"回头看上个月用量"这类常见需求；配合 labels 白名单瘦身，稳态体积
// 约为当前的 1/5。
const retentionDays = 90

// retentionPruneLoop 定期清理超过保留期的遥测数据。
//
// 行为：
//   - **启动即清一次**（不等一个周期）——daemon 长期不重启时靠 ticker 兜底
//   - 之后每 24h 清一次
//   - **清理失败只进 serve 日志，不阻断 daemon**——db 出问题不能让 ccl 完全不能用
//
// 清理只删数据，**不做 VACUUM**：删除产生的空闲页被后续写入复用 → 文件不再涨
// （但不缩）。把空闲页还给文件系统是 `ccl serve --compact` 的职责。
func retentionPruneLoop(ctx context.Context, store *collector.Store) {
	prune := func() {
		cutoff := time.Now().Add(-retentionDays * 24 * time.Hour)
		sessions, metrics, err := store.PruneOlderThan(cutoff)
		if err != nil {
			log.Printf("ccl serve: 清理过期遥测数据失败: %v", err)
			return
		}
		if sessions > 0 || metrics > 0 {
			log.Printf("ccl serve: 已清理超过 %d 天的遥测数据（sessions=%d metrics=%d）",
				retentionDays, sessions, metrics)
		}
	}
	prune() // 启动即清一次
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

// quotaRefreshLoop 是 `ccl serve` 的额度刷新循环（provider-quota §7）。
//
// **为什么刷新搭在 serve 上**：额度数据的变化速度是分钟级，而消费它的状态栏
// 命令每秒被调 3 次多——刷新频率与调用频率必须解耦。serve 已经是 ccl 自动
// 拉起的常驻进程（daemon-lifecycle spec：每次 ccl 启动 Claude Code 前都会
// 探 :4318 并 fork 它，且它永不自动 idle 退出）→ 不需要新建轮询体系。
//
// 行为：
//   - **启动即刷一轮**（不等一个周期）
//   - ticker 周期 = 所有待刷 provider 的 refreshInterval 的**最小值**；
//     每轮遍历 provider，**距上次刷新不足其自身周期的跳过这一轮**
//     （时间从数据文件的 fetched_at 读，见 quota.RefreshAll）
//   - **收到激活声明 → 立刻刷一轮**（不等 ticker）：ccl 启动 Claude Code 前
//     会声明本次用的 provider → 那次会话的额度当场可用
//   - **刷新失败只进 serve 日志，不写数据文件** → 旧条目保留，fetched_at
//     越来越旧 → stale 自动生效（状态栏不会因为一次网络抖动就从数字跳回 --）
//   - ctx 取消即退出（SIGTERM → graceful shutdown 不被拖慢）
func quotaRefreshLoop(ctx context.Context, baseDir string, activity *collector.Activity, activateCh chan string) {
	providersDir := filepath.Join(baseDir, "providers")
	st := quota.NewStore(baseDir)

	// **接口级节流器跨轮复用**（quota-per-vendor-rate-limit §D1）：节流状态记
	// 在内存里，必须由常驻的 daemon 持有同一个实例 —— 否则"上一轮末尾的请求"与
	// "下一轮开头的请求"之间不受约束，而实测两个同厂商 provider 的 429 时刻
	// 只隔 8-20 秒，正是跨轮形态。
	throttle := quota.NewEndpointThrottle()

	// 刷一轮并返回下一次的 ticker 周期（provider 配置可能变了 → 周期跟着变）。
	refresh := func() time.Duration {
		refs, err := quota.Enumerate(providersDir)
		if err != nil {
			log.Printf("ccl serve: 枚举 provider 失败: %v", err)
			return quota.DefaultRefreshInterval
		}
		if len(refs) == 0 {
			return quota.DefaultRefreshInterval
		}
		// **只刷新最近有活动的 provider**：额度数据的唯一消费者是状态栏，
		// 而状态栏只显示当前会话那个 provider 的额度 → 其余 provider 的额度
		// 数据当前根本没人看（查它们 = 纯浪费 + 触发厂商限流）。
		//
		// 注意：**refs 仍是全部**（Prune 的依据）——"这轮没刷它" ≠ "provider 被
		// 删了"，混在一起会误删它的额度条目。
		active := activeProviders(refs, activity)
		if err := quota.RefreshAll(ctx, refs, st, func(format string, args ...any) {
			// **[quota] 前缀**：让"额度相关"的行可以被 ASCII 关键词精确筛出来
			// （Windows 上日志里��中文可能显示成乱码，但标记、时间戳、provider
			// 名、HTTP 状态码都是 ASCII → 照样能解析）。
			log.Printf("ccl serve: [quota] "+format, args...)
		}, active, quota.DefaultFreshnessThreshold, throttle); err != nil && ctx.Err() == nil {
			log.Printf("ccl serve: 额度刷新失败: %v", err)
		}
		// ticker 周期必须**细于相位分辨率**（quota.TickInterval 的注释）——
		// 否则多个 provider 会在同一个 tick 上一起到期，相位错开白做。
		return quota.TickInterval(refs)
	}

	interval := refresh() // 启动即刷一轮
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// syncTicker 把 ticker 周期对齐到最新的刷新周期（provider 的周期或数量
	// 被改了 → 重算）。
	syncTicker := func(iv time.Duration) {
		if iv != interval {
			interval = iv
			ticker.Reset(interval)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case provider := <-activateCh:
			// **激活声明**：立刻刷一轮，不等 ticker。
			//
			// collector 侧已经把该 provider 标记为活跃 → 它所在的额度池会被刷
			// （同池的其他 provider 也一起拿到同一份数据）。
			log.Printf("ccl serve: [quota] provider %q 激活，立即刷新一轮", provider)
			syncTicker(refresh())
		case <-ticker.C:
			syncTicker(refresh())
		}
	}
}

// notifyActivate 校验 provider 名并把它送进激活通道。
//
// **白名单校验是必须的**：名字来自 HTTP 查询串 → 必须确认它是 providers/
// 目录里真实存在的 provider。否则这个端点就成了"让 daemon 去访问任意 URL"
// 的放大器（daemon 会带着用户的 API key 发起请求）。
//
// 通道满了就**丢弃**（不阻塞 HTTP handler）：丢掉的后果只是这一轮没立即刷，
// 下一个 ticker 会补上。
func notifyActivate(ch chan string, provider, providersDir string) error {
	if !providerExists(providersDir, provider) {
		return fmt.Errorf("未知的 provider %q", provider)
	}
	select {
	case ch <- provider:
	default:
		log.Printf("ccl serve: 激活通道已满，丢弃 %q 的激活声明", provider)
	}
	return nil
}

// providerExists 判断 providers 目录里有没有这个名字的 provider 配置。
//
// **名字必须能安全地拼进路径**：provider 名来自配置文件名，正常不含分隔符，
// 但这是 HTTP 输入 → 显式挡住路径穿越。
func providerExists(providersDir, name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	_, err := os.Stat(filepath.Join(providersDir, name+".json"))
	return err == nil
}

// serveQuota 实现 daemon 的只读额度端点（GET /ccl/quota?provider=X）。
//
// **为什么要它**：状态栏读额度时**起进程太慢**——Windows 上 `ccl quota --json`
// 的 p50 是 170ms，而状态栏的调用预算只有 250ms（忙的时候必然超时 → 静默
// 回落成 Claude Code 自己的 rate_limits）。走 daemon 的回环端点只要几 ms。
//
// 状态码：
//   - 200 有额度（文档与 `ccl quota --json` **逐字段同构**）
//   - 404 provider 未配置 / 没有额度记录
//   - 422 provider 没有额度接口（与"没记录"分开，消费方才分得清"这厂商没
//     额度"与"数据还没准备好"）
//
// **白名单校验**：provider 名来自 HTTP 查询串 → 先过 providerExists（挡路径
// 穿越），再读它的配置。
//
// **读数据文件必须走 quota.Store.Load()**（不能裸 os.ReadFile）：Windows 上
// daemon 写文件用 tmp+rename，替换瞬间目标可能短暂不存在 → Store 里有 5ms
// 重试兜住它。
func serveQuota(baseDir, name string) (any, int) {
	providersDir := filepath.Join(baseDir, "providers")
	if !providerExists(providersDir, name) {
		return fmt.Sprintf("提供商 %q 不存在", name), http.StatusNotFound
	}
	ref, hasQuota, err := quota.LoadProviderRef(providersDir, name)
	if err != nil {
		return fmt.Sprintf("读提供商 %q 的配置失败: %v", name, err), http.StatusNotFound
	}
	if !hasQuota {
		return fmt.Sprintf("提供商 %q 没有额度接口", name), http.StatusUnprocessableEntity
	}
	file, err := quota.NewStore(baseDir).Load()
	if err != nil {
		return fmt.Sprintf("读额度数据失败: %v", err), http.StatusInternalServerError
	}
	entry, ok := file.EntryFor(name)
	if !ok {
		return fmt.Sprintf("还没有 %q 的额度记录", name), http.StatusNotFound
	}
	out, ok := quota.BuildOutput(ref, entry, time.Now())
	if !ok {
		return fmt.Sprintf("还没有 %q 的额度记录", name), http.StatusNotFound
	}
	return out, http.StatusOK
}

// markRecordedActiveProvider 把 state.json 里记录的 active provider 标记为活跃。
//
// **为什么需要**：daemon 的 provider 活跃度是**纯内存**状态，daemon 一起来
// 就是空的 → 启动那一轮刷新会一个 provider 都不查（实测 serve.log：
// 「查了 0 / 不活跃跳过 6」，且整整 60 秒才等到第一条 OTel 恢复）。而 ccl 在
// 拉起 daemon **之前**就把本次选中的 provider 写进了 state.json
// （SetActiveProvider 在 ensureDaemonRunning 之前）→ 启动那轮就能刷它。
//
// **这是兜底，不是主路径**：主路径是 ccl 启动后直接向 daemon 声明
// （见 collector.ServerOptions.OnActivate）。兜底覆盖的是"daemon 独立启动
// （不经 ccl）"和"ccl 启动时没写 state.json"（选中的正好是 default provider
// 时不写）这两种情况。
func markRecordedActiveProvider(activity *collector.Activity, baseDir string) {
	if activity == nil {
		return
	}
	name := state.NewManager(baseDir).GetActiveProvider()
	if name == "" {
		return
	}
	activity.Mark(name, time.Now())
	log.Printf("ccl serve: 启动兜底：把 state.json 记录的 active provider %q 标记为活跃", name)
}

// quotaActiveWindow 是"最近有 OTel 活动"的判定窗口。
//
// **为什么是 5 分钟**：实测 Claude Code **每 60 秒**推一次 OTel metrics
// （2026-11-06，本机 telemetry.db：相邻两条间隔 60000/60001 秒）→ 窗口必须
// > 60 秒，否则两次推送之间会被误判成"没人用"（额度无故停更）。
//
// 5 分钟 = 4 个 OTel 周期 → 容忍 3 次连续丢失（网络抖动、SDK 暂停导出）。
// 更长 → 用户离开半小时后还在查他的额度（回到"全查"的毛病）。
const quotaActiveWindow = 5 * time.Minute

// activeProviders 返回"最近有 OTel 活动"的 provider 名集合。
//
// **返回 nil 的含义是"不过滤"**（全部都要刷）；返回空集合的含义是"一个都不
// 刷"（没人用 → 一个请求都不发）。两者语义不同，不能混。
//
// 活动信号来自 collector（它解析每条 metrics 时就知道是哪个 provider——ccl
// 注入的 ccl.provider resource attribute），详见 internal/collector/activity.go。
//
// activity 为 nil（拿不到活动信息）→ **不过滤**：宁可多查几个，也不要在信息缺失
// 时一个都不查。
func activeProviders(refs []quota.ProviderRef, activity *collector.Activity) map[string]bool {
	if activity == nil {
		return nil
	}
	recent := activity.Recent(quotaActiveWindow)
	out := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if recent[ref.Name] {
			out[ref.Name] = true
		}
	}
	return out
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
func registerServeFlags(fs *flag.FlagSet) (stop, restore, status, restart, backfill, compact *bool) {
	stop = fs.Bool("stop", false, "停 OTel daemon（等价于原 ccl stop）")
	restore = fs.Bool("restore", false, "清理旧版 ccl 留在 ~/.claude/settings.json 里的 OTel 注入痕迹")
	status = fs.Bool("status", false, "打印 daemon 状态摘要（pid / 端口可达 / db 文件大小 / session 数）")
	restart = fs.Bool("restart", false, "热重启 daemon（SIGTERM 旧 daemon + fork 新 binary daemon）")
	backfill = fs.Bool("backfill", false, "回填历史 session 的 cwd（不限量；查不到的保持未知项目）")
	compact = fs.Bool("compact", false, "收缩 telemetry.db（VACUUM 回收空闲空间；daemon 在跑则先停后拉起）")
	return
}

// runCompactInner 收缩 telemetry.db（VACUUM 回收空闲空间）。
//
// 为什么必须先停 daemon（telemetry-db-compaction §D7）：VACUUM 要排除并发**写**
// ——只读连接与活跃读事务在 WAL 模式下**不**阻塞它（实测），而 daemon 是唯一的
// 写者 → 停掉它。
//
// 为什么 VACUUM 之后还要 wal_checkpoint(TRUNCATE)（§D6）：WAL 模式下 VACUUM 把
// 新内容写进 -wal，**主文件要等下一次 checkpoint 才被覆盖**。实测 db 2.4MB →
// VACUUM 后主文件仍是 2437120 字节（-wal 反而涨到 4.9MB）→ 用户 ls -l 看到
// "没变小"，整个命令等于空跑；再 checkpoint 后主文件才降到 1224704。
//
// 为什么执行完按"原状态"拉起 daemon：daemon 停了用户的 Claude Code 就推不上遥测
// 了。原本没在跑则不拉起（保持原状态）。
func runCompactInner() error {
	stateBaseDir, err := stateDirOrError()
	if err != nil {
		return err
	}
	dbPath := filepath.Join(stateBaseDir, "telemetry.db")
	pidPath := filepath.Join(stateBaseDir, "daemon.pid")

	before := fileSize(dbPath)

	// daemon 是否在跑（决定要不要停、要不要事后拉起）。
	pid := readPidfile(pidPath)
	wasRunning := pid > 0
	if wasRunning {
		fmt.Printf("stopping daemon pid=%d...\n", pid)
		// 死线放宽到 15s：daemon 自己的关闭预算是 10s（server.go 的 5s +
		// serve.go 的 5s），stopDaemon 的 5s 死线会触发 SIGKILL 兜底 → 打断
		// Close 里的 wal_checkpoint(TRUNCATE) → 留下残缺 WAL。
		if err := stopDaemonWithin(pid, 15*time.Second); err != nil {
			return fmt.Errorf("停 daemon 失败: %w", err)
		}
		_ = os.Remove(pidPath)
		fmt.Println("old daemon stopped")
	}

	// 磁盘空间检查：VACUUM 需要约 2× db 的临时空间。29MB 的库峰值约 90MB
	// （db + wal + 备份）→ 空间不够就别让它中途失败留下半成品。
	if avail, err := availableDiskSpace(stateBaseDir); err == nil && before > 0 {
		if avail < uint64(before)*2 {
			return fmt.Errorf("磁盘空间不足：压缩需要约 %d MB 可用空间，只有 %d MB",
				before*2/1000000, avail/1000000)
		}
	}

	if err := vacuumDB(dbPath); err != nil {
		return err
	}

	after := fileSize(dbPath)
	fmt.Printf("db: %s -> %s\n", humanBytes(before), humanBytes(after))

	if wasRunning {
		if err := forkDaemon(); err != nil {
			return err
		}
		if err := waitDaemonReadyFn(); err != nil {
			return fmt.Errorf("新 daemon fork 成功但 3s 内未就绪: %w", err)
		}
		fmt.Println("daemon ready (port :4318 reachable)")
	}
	return nil
}

// vacuumDB 重写 db 文件回收空闲空间，并做一次 WAL checkpoint 让主文件真的变小。
func vacuumDB(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db 失败: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("设置 busy_timeout 失败: %w", err)
	}
	if _, err := db.Exec("VACUUM"); err != nil {
		return fmt.Errorf("VACUUM 失败: %w", err)
	}
	// 没有这一步，VACUUM 的结果还留在 -wal 里，主文件不会变小（§D6）。
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("WAL checkpoint 失败: %w", err)
	}
	return nil
}

// fileSize 返回文件大小；文件不存在返回 0。
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
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

const serveHelp = `用法: ccl serve [--stop | --restore | --status | --restart | --backfill | --compact]

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

--compact
  收缩 telemetry.db：VACUUM 重写文件，把空闲页还给文件系统。
  与 daemon 启动时的自动清理不同，自动清理只删过期数据（空闲页被后续写入复用，
  文件不再涨但不会变小）；--compact 才是让文件**真的变小**的那一步。

  daemon 在跑时会先停它、压缩完再拉起来（原本没在跑就不拉起）。
  停止时用 15s 死线（daemon 自己的关闭预算是 10s），避免 SIGKILL 打断它的
  WAL checkpoint。
  与 --stop / --restore / --status / --restart / --backfill 互斥。

-h, --help
  显示本帮助并退出。`
