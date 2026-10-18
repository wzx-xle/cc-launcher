package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cc-launcher/internal/config"
	"cc-launcher/internal/stats"
)

// statsHelp 是 `ccl stats --help` 的输出。
const statsHelp = `用法: ccl stats [选项]

直接读 ~/.cc-launcher/telemetry.db 显示 token / cost 聚合。
不传 --day 时默认 7 天 detail 明细；传 --day N 则额外打印按时间桶聚合表。
不需要 daemon 运行中——db 文件已落盘就行。

选项:
    --day <N>              时间桶聚合:1 表示今天 24 小时 hourly,N(1-90) 表示最近 N 天 daily
    --by-provider          按 provider 聚合（一个 provider 一行）
    --by-project           按 project 聚合（cwd basename 一行）
    --project <子串>       按 cwd 子串过滤（如 cc-launcher）
    --json                 输出 JSON（pipeable to jq）
    -h, --help             显示本帮助并退出`

// registerStatsFlags 把 stats 子命令的 flag 注册到 fs，返回值供 runStats 与
// cmd/cc-launcher/completion.go::completeAtStats 共用。
//
// cli-shell-completion §D2：flag 改动自动同步——加新 flag 时（仅改本函数）completion
// 自动跟随；若不抽，`runStats` 加 flag 而 `completeAtStats` 漏改 → 脱节。
//
// 决策（stats-time-buckets §D4 + §D10）：
//   - 砍掉 --since；detail 起点由 `--day` 派生（detail 与桶同窗口,避免割裂）
//   - 新增 --day N（int,1≤N≤90）启用桶聚合；-1 = 旧 ccl stats 行为（向后兼容）
//   - day 越界校验在 runStats（"--day 0 / 91 / -1"）触发 stderr + statsExitFn(2);
//
// default 是 0 还是 -1 的取舍:Go flag Int 无法区分"未传"与"显式 0"。
// 但 --day 0 是非法值（必须 ≥1），0 即非法,默认 -1 同时承担"未传"+"非法"标识。
func registerStatsFlags(fs *flag.FlagSet) (day *int, byProvider, byProject, asJSON *bool, project *string) {
	day = fs.Int("day", -1, "时间桶聚合:1 表示今天 24 小时 hourly,N(1-90) 表示最近 N 天 daily（缺省 -1 = 旧 detail 行为）")
	byProvider = fs.Bool("by-provider", false, "按 provider 聚合")
	byProject = fs.Bool("by-project", false, "按 project 聚合")
	project = fs.String("project", "", "按 cwd 子串过滤")
	asJSON = fs.Bool("json", false, "输出 JSON")
	return
}

// statsExitFn 是 ccl stats 的退出钩子。
//
// 默认调 os.Exit；smoke_test 覆盖为 no-op 避免把整个 test 进程杀掉（跟 completion.go::exitFn
// 同模式：CLI 错误路径走直接退出,不走 return error,语义跟 Go flag package 一致）。
var statsExitFn = func(code int) { os.Exit(code) }

// runStats 是 `ccl stats` 入口。
//
// 决策（stats-time-buckets §2.2）：
//   - --day 校验：<1 或 >90 → stderr + statsExitFn(2)（Go flag parse error 约定）
//   - q.Buckets：day != 0 ? &day : nil（QueryStats 内部据此启用桶聚合）
//   - 输出分支 4 种：
//       (1) JSON 输出 result（BucketRows/BucketTotals 由 QueryStats 自动填,omitempty）
//       (2) --day 未启用 + 明细非空：旧 detail 表（向后兼容）
//       (3) --day 未启用 + 明细空：(no data in window since ...)
//       (4) --day 启用 + 明细非空：detail 表 + 空行 + 桶聚合表
//       (5) --day 启用 + 明细空：(no data in window ...) + 桶聚合表（无空行,跟 hint 紧凑）
// runStats 是 `ccl stats` 入口。
//
// 决策（stats-time-buckets §2.2）：
//   - --day 校验：<1 或 >90 → stderr + statsExitFn(2)（Go flag parse error 约定）
//   - q.Buckets：day != 0 ? &day : nil（QueryStats 内部据此启用桶聚合）
//   - 输出分支 4 种：
//       (1) JSON 输出 result（BucketRows/BucketTotals 由 QueryStats 自动填,omitempty）
//       (2) --day 未启用 + 明细非空：旧 detail 表（向后兼容）
//       (3) --day 未启用 + 明细空：(no data in window since ...)
//       (4) --day 启用 + 明细非空：detail 表 + 空行 + 桶聚合表
//       (5) --day 启用 + 明细空：(no data in window ...) + 桶聚合表（无空行,跟 hint 紧凑）
//
// 真实生产调用走 runStats（无 clock 注入,fallback time.Now）；
// smoke test 走 runStatsWithQuery(stats.Query{Clock: ...}, args) 注入 mock clock,
// 与 collector.Store.WithClock 共享同一 mock today(见 spec/stats-query
// "stats query accepts an optional clock")。
func runStats(args []string) error {
	return runStatsWithQuery(stats.Query{}, args)
}

// runStatsWithQuery 是 runStats 的可注入 clock 版本。
//
// base.Clock 会合并到 args 解析后的 Query;base 的其它字段(ByProvider / ByProject
// / Project / Buckets)被 args 解析结果覆盖(以 CLI flag 为准)。这样设计让 caller
// 只关心 Clock 注入,不污染 CLI flag 行为。
func runStatsWithQuery(base stats.Query, args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	day, byProvider, byProject, asJSON, project := registerStatsFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, a := range fs.Args() {
		if a == "-h" || a == "--help" {
			fmt.Println(statsHelp)
			return nil
		}
	}

	// --day 越界校验（决策 §2.2）。用 stderr + statsExitFn(2) 走 Go flag parse error 约定;
	// 不通过 fmt.Errorf 返回(那会走 main 的"错误: ..."包装,跟 Go flag 标准不一致)。
	if *day != -1 && (*day < 1 || *day > 90) {
		fmt.Fprintf(os.Stderr, "--day 必须在 1 到 90 之间, got: %d\n", *day)
		statsExitFn(2)
	}

	q := stats.Query{
		ByProvider: *byProvider,
		ByProject:  *byProject,
		Project:    *project,
		Clock:      base.Clock, // 仅 Clock 来自 base 注入,其它字段以 CLI flag 为准
	}
	if *day > 0 {
		d := *day
		q.Buckets = &d
	}

	baseDir := cclLauncherBaseDir()
	dbPath := filepath.Join(baseDir, "telemetry.db")

	result, err := stats.QueryStats(dbPath, q)
	if err != nil {
		if errors.Is(err, stats.ErrNoDatabase) {
			fmt.Fprintln(os.Stderr, "no telemetry data yet; run `ccl` to start a session")
			statsExitFn(1)
		}
		return err
	}

	if *asJSON {
		out, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	if *day == -1 {
		// 旧 ccl stats 行为:detail 明细,不回溯桶聚合
		if len(result.Sessions) == 0 {
			fmt.Printf("(no data in window since %s)\n", result.Since.Format("2006-01-02"))
			return nil
		}
		fmt.Printf("Window since: %s   Group by: %s\n\n",
			result.Since.Format("2006-01-02"), groupByLabel(result.GroupBy))
		stats.FormatTable(os.Stdout, result)
		return nil
	}

	// --day 启用:detail + 桶聚合
	if len(result.Sessions) > 0 {
		fmt.Printf("Window since: %s   Group by: %s\n\n",
			result.Since.Format("2006-01-02"), groupByLabel(result.GroupBy))
		stats.FormatTable(os.Stdout, result)
		fmt.Println() // 空行分隔 detail 与 桶聚合
	} else {
		fmt.Printf("(no data in window since %s)\n", result.Since.Format("2006-01-02"))
	}
	stats.FormatBucketTable(os.Stdout, result.BucketRows, result.BucketTotals)
	return nil
}

func groupByLabel(g string) string {
	switch g {
	case "session":
		return "session"
	case "provider":
		return "provider"
	case "project":
		return "project (cwd basename)"
	case "project+provider":
		return "project × provider"
	}
	return g
}

// stopHelp 是 `ccl serve --stop -h` 的输出（runStop 内部化后被 runServe --stop 复用）。
//
// 决策（design.md §D2）：原 `ccl stop` 顶层子命令整合为 `ccl serve --stop` 后，
// 仍允许 `ccl serve --stop -h` 显示本帮助——内容保留 stop 的语义。
const stopHelp = `用法: ccl serve --stop

发 SIGTERM 给 ~/.cc-launcher/daemon.pid 里的 daemon。
daemon 收到 SIGTERM 后 5s 内 graceful shutdown（503 metrics + drain queue + 关 db）。
超时则 ccl serve --stop 发 SIGKILL。`

// runStopInner 是 `ccl serve --stop` 的实现（原 `ccl stop` 入口改名 unexported）。
//
// 决策（design.md §D1-D2）：runStop 改为包内 unexported，函数体一字不动；由 runServe --stop 分支调用。
func runStopInner(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(stopHelp)
		return nil
	}

	baseDir := cclLauncherBaseDir()
	pidPath := filepath.Join(baseDir, "daemon.pid")

	pid := readPidfile(pidPath)
	if pid == 0 {
		fmt.Println("daemon not running")
		return nil
	}

	fmt.Printf("stopping daemon pid=%d...\n", pid)
	if err := stopDaemon(pid); err != nil {
		return fmt.Errorf("stop 失败: %w", err)
	}
	_ = os.Remove(pidPath) // best-effort；serve 也会删
	fmt.Println("daemon stopped")
	return nil
}

// restoreHelp 是 `ccl serve --restore -h` 的输出（runRestore 内部化后被 runServe --restore 复用）。
//
// 决策（design.md §D2）：原 `ccl restore` 顶层子命令整合为 `ccl serve --restore` 后，
// 仍允许 `ccl serve --restore -h` 显示本帮助——内容保留 restore 的语义。
const restoreHelp = `用法: ccl serve --restore

读 ~/.claude/settings.json，还原 ccl 注入的 OTel env：
  - CC_ORIG_<KEY> 还原到 <KEY>，删 CC_ORIG_*
  - 删除 ccl 注入的默认值（如 OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318）
  - 其它用户自有的 env 键不动

不可逆：清掉后下次 ccl 启动会重新注入。

操作前会先备份当前 settings.json 到 ~/.cc-launcher/backups/settings-<时间戳>.json。

补充：backups/ 还包含 ccl 自动产出的\"控制权交接快照\"——
每次 ccl 启动检测到 settings.json 不再处于 ccl 控制下时（如首次启动、
第三方工具覆盖 OTel 端点后），会额外备份一份作为健康基线，便于
回滚到\"接管前\"状态。常量启动（仍在 ccl 控制下）不产生新备份，
避免轮转（保留最近 30 个）被瞬态版本耗光。`

// runRestoreInner 是 `ccl serve --restore` 的实现（原 `ccl restore` 入口改名 unexported）。
//
// 决策（design.md §D1-D2）：runRestore 改为包内 unexported，函数体一字不动；由 runServe --restore 分支调用。
func runRestoreInner(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(restoreHelp)
		return nil
	}

	settingsPath, err := config.DefaultSettingsPath()
	if err != nil {
		return fmt.Errorf("无法获取 settings.json 路径: %w", err)
	}
	if _, err := os.Stat(settingsPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no settings to restore")
			return nil
		}
		return err
	}
	backupDir, err := config.DefaultBackupDir()
	if err != nil {
		return err
	}
	cfg := config.NewManager(settingsPath, backupDir, config.DefaultMaxBackups)

	settings, err := cfg.LoadSettings()
	if err != nil {
		return err
	}

	env, _ := settings["env"].(map[string]any)
	if env == nil {
		fmt.Println("no settings to restore")
		return nil
	}

	envStr := make(map[string]string, len(env))
	for k, v := range env {
		if s, ok := v.(string); ok {
			envStr[k] = s
		}
	}

	origJSON, _ := json.Marshal(settings)
	restored := restoreOTelEnv(envStr)
	settings["env"] = restored
	nowJSON, _ := json.Marshal(settings)

	if string(origJSON) == string(nowJSON) {
		fmt.Println("settings.json 无 ccl 注入；restore 是 no-op")
		return nil
	}

	if err := cfg.Backup(); err != nil {
		return fmt.Errorf("备份失败: %w", err)
	}
	if err := cfg.SaveSettings(settings); err != nil {
		return fmt.Errorf("写 settings 失败: %w", err)
	}

	fmt.Println("settings.json 已还原到 ccl 之前的状态")
	fmt.Println("提示：若想继续用 ccl 用量监控，再次跑 `ccl` 即可（会重新注入）。")
	return nil
}

// runStatusInner 是 `ccl serve --status` 的实现（flatten-stop-restore-into-serve §9）。
//
// 报告 daemon 运行时状态（port reachability + pidfile + db 文件大小 + session 数）。
// 全部 informational，缺失连接不算 error，exit 0。
//
// 状态判断矩阵（design §D8）—— 缩进式分组（label 10 字符宽右对齐 + 冒号 + 子字段缩进 11 空格）：
//
//	端口 :4318  pidfile   输出
//	reachable   存在         daemon: running
//	                           pid=N
//	                           port :4318 reachable
//	reachable   None       daemon: not running
//	                           port :4318 in use by foreign service (no pidfile)
//	unreachable 存在         daemon: not running
//	                           stale pidfile pid=N
//	                           port :4318 unreachable
//	unreachable None       daemon: not running
//	                           port :4318 unreachable
//
//	db 行同理：db label + path / size / count 三行缩进；missing 时只一行。
func runStatusInner() error {
	stateBaseDir, err := stateDirOrError()
	if err != nil {
		return err
	}
	pidPath := filepath.Join(stateBaseDir, "daemon.pid")
	dbPath := filepath.Join(stateBaseDir, "telemetry.db")

	pid := readPidfile(pidPath)
	portReachable := probeDaemonAddr() == nil

	switch {
	case portReachable && pid > 0:
		fmt.Println("   daemon: running")
		fmt.Printf("           pid=%d\n", pid)
		fmt.Println("           port :4318 reachable")
	case portReachable:
		fmt.Println("   daemon: not running")
		fmt.Println("           port :4318 in use by foreign service (no pidfile)")
	case pid > 0:
		fmt.Println("   daemon: not running")
		fmt.Printf("           stale pidfile pid=%d\n", pid)
		fmt.Println("           port :4318 unreachable")
	default:
		fmt.Println("   daemon: not running")
		fmt.Println("           port :4318 unreachable")
	}

	// db 行（缺失不算 error）
	fi, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("       db: missing (no telemetry yet)")
			return nil
		}
		return fmt.Errorf("stat db 失败: %w", err)
	}
	fmt.Printf("       db: %s\n", shortenHomePath(dbPath))
	total, today, err := stats.CountSessionsByWindow(dbPath, stats.TodayStartMillis())
	if err != nil {
		// COUNT 失败不回 error——db 文件本身存在,只是 sqlite 读失败,仍报 size
		fmt.Printf("           %s\n", humanBytes(fi.Size()))
		fmt.Printf("           session count unavailable: %v\n", err)
		return nil
	}
	fmt.Printf("           %s\n", humanBytes(fi.Size()))
	fmt.Printf("           %d sessions (%d today)\n", total, today)
	return nil
}

// shortenHomePath 把绝对路径缩成 ~/... 形式（仅当 prefix 命中 UserHomeDir 时）。
//
// ccl 把所有数据放 $HOME/.cc-launcher/ 下；显示完整 /home/zxwang/... 太长。
// 仅用于 `ccl serve --status` 输出，不导出。
func shortenHomePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if rel, ok := strings.CutPrefix(p, home); ok {
		return "~" + rel
	}
	return p
}

// humanBytes 把字节数转成 SI 单位字符串（KB/MB/GB, 1 位小数）。
//
// 仅用于 `ccl serve --status` 输出，不导出；不处理 locale。
func humanBytes(size int64) string {
	const (
		kb = 1024
		mb = 1024 * 1024
		gb = 1024 * 1024 * 1024
	)
	switch {
	case size < kb:
		return fmt.Sprintf("%d B", size)
	case size < mb:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(kb))
	case size < gb:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(mb))
	default:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(gb))
	}
}