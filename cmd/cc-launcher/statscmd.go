package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cc-launcher/internal/config"
	"cc-launcher/internal/stats"
	"cc-launcher/internal/web"
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
    --by-model             按 model 聚合（一个 model 一行）
    --project <子串>       按 cwd 子串过滤（如 cc-launcher）
    --model <子串>         按 model 子串过滤（如 MiniMax）
    --provider <子串>      按 provider 子串过滤（如 minimax）
    --json                 输出 JSON（pipeable to jq）
    -h, --help             显示本帮助并退出`

// statsFlags 是 stats 子命令的 flag 集合。
//
// 抽成 struct 而非一串指针返回值：flag 数量在增长（web-usage-stats-ui 加了
// --by-model / --model / --provider），一串裸返回值容易接错位置。
type statsFlags struct {
	day        *int
	byProvider *bool
	byProject  *bool
	byModel    *bool
	project    *string
	model      *string
	provider   *string
	asJSON     *bool
}

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
func registerStatsFlags(fs *flag.FlagSet) statsFlags {
	var f statsFlags
	f.day = fs.Int("day", -1, "时间桶聚合:1 表示今天 24 小时 hourly,N(1-90) 表示最近 N 天 daily（缺省 -1 = 旧 detail 行为）")
	f.byProvider = fs.Bool("by-provider", false, "按 provider 聚合")
	f.byProject = fs.Bool("by-project", false, "按 project 聚合")
	f.byModel = fs.Bool("by-model", false, "按 model 聚合")
	f.project = fs.String("project", "", "按 cwd 子串过滤")
	f.model = fs.String("model", "", "按 model 子串过滤")
	f.provider = fs.String("provider", "", "按 provider 子串过滤")
	f.asJSON = fs.Bool("json", false, "输出 JSON")
	return f
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
//     (1) JSON 输出 result（BucketRows/BucketTotals 由 QueryStats 自动填,omitempty）
//     (2) --day 未启用 + 明细非空：旧 detail 表（向后兼容）
//     (3) --day 未启用 + 明细空：(no data in window since ...)
//     (4) --day 启用 + 明细非空：detail 表 + 空行 + 桶聚合表
//     (5) --day 启用 + 明细空：(no data in window ...) + 桶聚合表（无空行,跟 hint 紧凑）
//
// runStats 是 `ccl stats` 入口。
//
// 决策（stats-time-buckets §2.2）：
//   - --day 校验：<1 或 >90 → stderr + statsExitFn(2)（Go flag parse error 约定）
//   - q.Buckets：day != 0 ? &day : nil（QueryStats 内部据此启用桶聚合）
//   - 输出分支 4 种：
//     (1) JSON 输出 result（BucketRows/BucketTotals 由 QueryStats 自动填,omitempty）
//     (2) --day 未启用 + 明细非空：旧 detail 表（向后兼容）
//     (3) --day 未启用 + 明细空：(no data in window since ...)
//     (4) --day 启用 + 明细非空：detail 表 + 空行 + 桶聚合表
//     (5) --day 启用 + 明细空：(no data in window ...) + 桶聚合表（无空行,跟 hint 紧凑）
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
	// 自己处理 help 与错误文案：flag package 默认会把 "Usage of stats:" 打到
	// stderr 并返回 flag.ErrHelp —— 那样 `ccl stats --help` 会以 exit 1 收场，
	// 且用户看到的是 flag 列表而不是本命令的用法说明。
	fs.SetOutput(io.Discard)
	fl := registerStatsFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println(statsHelp)
			return nil
		}
		return err
	}

	// --day 越界校验（决策 §2.2）。用 stderr + statsExitFn(2) 走 Go flag parse error 约定;
	// 不通过 fmt.Errorf 返回(那会走 main 的"错误: ..."包装,跟 Go flag 标准不一致)。
	if *fl.day != -1 && (*fl.day < 1 || *fl.day > 90) {
		fmt.Fprintf(os.Stderr, "--day 必须在 1 到 90 之间, got: %d\n", *fl.day)
		statsExitFn(2)
	}

	q := stats.Query{
		ByProvider: *fl.byProvider,
		ByProject:  *fl.byProject,
		ByModel:    *fl.byModel,
		Project:    *fl.project,
		Model:      *fl.model,
		Provider:   *fl.provider,
		Clock:      base.Clock, // 仅 Clock 来自 base 注入,其它字段以 CLI flag 为准
	}
	if *fl.day > 0 {
		d := *fl.day
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

	if *fl.asJSON {
		out, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	if *fl.day == -1 {
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

	// --day 启用:仅桶聚合表(不再前置 detail 表;--day 语义是"换桶视图",
	// 想看 session 明细请用 ccl stats 不传 --day)。--json 路径 shape 不动,
	// 仍含 sessions + totals + bucket_rows + bucket_totals 完整结构。
	if len(result.Sessions) == 0 {
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
	case "model":
		return "model"
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
// 语义（otel-endpoints-via-settings）：新版 ccl **不再改写** settings.json，
// 所以本命令不再是"撤销当前接管"，而是"清理旧版 ccl 留下的痕迹"。
const restoreHelp = `用法: ccl serve --restore

清理**旧版 ccl** 留在 ~/.claude/settings.json 里的 OTel 注入痕迹：
  - CC_ORIG_<KEY> / FORWORD_<KEY> 还原到 <KEY>，删掉备份键
  - 删除旧版 ccl 注入的默认值（如 OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318）
  - 其它用户自有的 env 键不动

说明：新版 ccl 通过 Claude Code 的 --settings 参数交付 OTel 配置，
**不再改写 settings.json**——所以这里没有"当前接管"可撤销，
只有旧版留下的痕迹需要清理。升级后第一次跑 ccl 会自动清理一次。

操作前会先备份当前 settings.json 到 ~/.cc-launcher/backups/settings-<时间戳>.json。`

// runRestoreInner 是 `ccl serve --restore` 的实现（原 `ccl restore` 入口改名 unexported）。
//
// 语义（otel-endpoints-via-settings）：清理旧版 ccl 留在 settings.json 里的痕迹，
// 用与自动迁移同一份实现（cleanupLegacyOtelEnv）——幂等。
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
	if !hasCclArtifact(env) {
		fmt.Println("settings.json 没有旧版 ccl 的注入痕迹；无需清理")
		return nil
	}

	cleaned, changed := cleanupLegacyOtelEnv(env)
	if !changed {
		fmt.Println("settings.json 没有旧版 ccl 的注入痕迹；无需清理")
		return nil
	}

	if err := cfg.Backup(); err != nil {
		return fmt.Errorf("备份失败: %w", err)
	}
	settings["env"] = cleaned
	if err := cfg.SaveSettings(settings); err != nil {
		return fmt.Errorf("写 settings 失败: %w", err)
	}

	fmt.Println("已清理旧版 ccl 留在 settings.json 的 OTel 注入痕迹")
	fmt.Println("提示：新版 ccl 通过 --settings 交付 OTel 配置，不再改写 settings.json。")
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

	// 看板 URL（token 缺失就生成一份——`ccl web` 读同一个文件）
	if tok, err := web.LoadOrCreateToken(filepath.Join(stateBaseDir, web.TokenFileName)); err == nil {
		fmt.Printf("       web: http://127.0.0.1:4318/ui?token=%s\n", tok)
	} else {
		fmt.Printf("       web: token 不可用（%v）\n", err)
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
