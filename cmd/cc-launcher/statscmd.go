package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cc-launcher/internal/config"
	"cc-launcher/internal/stats"
)

// statsHelp 是 `ccl stats --help` 的输出。
const statsHelp = `用法: ccl stats [选项]

直接读 ~/.cc-launcher/telemetry.db 显示 token / cost 聚合（默认最近 7 天）。
不需要 daemon 运行中——db 文件已落盘就行。

选项:
    --since <YYYY-MM-DD>   起始日期（含）；默认 7 天前
    --by-provider          按 provider 聚合（一个 provider 一行）
    --by-project           按 project 聚合（cwd basename 一行）
    --project <子串>       按 cwd 子串过滤（如 cc-launcher）
    --json                 输出 JSON（pipeable to jq）
    -h, --help             显示本帮助并退出`

// runStats 是 `ccl stats` 入口。
func runStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	sinceStr := fs.String("since", "", "起始日期（YYYY-MM-DD）")
	byProvider := fs.Bool("by-provider", false, "按 provider 聚合")
	byProject := fs.Bool("by-project", false, "按 project 聚合")
	project := fs.String("project", "", "按 cwd 子串过滤")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, a := range fs.Args() {
		if a == "-h" || a == "--help" {
			fmt.Println(statsHelp)
			return nil
		}
	}

	q := stats.Query{
		ByProvider: *byProvider,
		ByProject:  *byProject,
		Project:    *project,
	}
	if *sinceStr != "" {
		t, err := time.Parse("2006-01-02", *sinceStr)
		if err != nil {
			return fmt.Errorf("--since 解析失败（需要 YYYY-MM-DD）: %w", err)
		}
		q.Since = t
	}

	baseDir := cclLauncherBaseDir()
	dbPath := filepath.Join(baseDir, "telemetry.db")

	result, err := stats.QueryStats(dbPath, q)
	if err != nil {
		if errors.Is(err, stats.ErrNoDatabase) {
			fmt.Fprintln(os.Stderr, "no telemetry data yet; run `ccl` to start a session")
			os.Exit(1)
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

	if len(result.Sessions) == 0 {
		fmt.Printf("(no data in window since %s)\n", result.Since.Format("2006-01-02"))
		return nil
	}

	fmt.Printf("Window since: %s   Group by: %s\n\n",
		result.Since.Format("2006-01-02"), groupByLabel(result.GroupBy))
	stats.FormatTable(os.Stdout, result)
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

// stopHelp 是 `ccl stop --help` 的输出。
const stopHelp = `用法: ccl stop

发 SIGTERM 给 ~/.cc-launcher/daemon.pid 里的 daemon。
daemon 收到 SIGTERM 后 5s 内 graceful shutdown（503 metrics + drain queue + 关 db）。
超时则 ccl stop 发 SIGKILL。`

// runStop 是 `ccl stop` 入口。
func runStop(args []string) error {
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

// restoreHelp 是 `ccl restore --help` 的输出。
const restoreHelp = `用法: ccl restore

读 ~/.claude/settings.json，还原 ccl 注入的 OTel env：
  - CC_ORIG_<KEY> 还原到 <KEY>，删 CC_ORIG_*
  - 删除 ccl 注入的默认值（如 OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318）
  - 其它用户自有的 env 键不动

不可逆：清掉后下次 ccl 启动会重新注入。

操作前会先备份当前 settings.json 到 ~/.cc-launcher/backups/settings-<时间戳>.json。`

// runRestore 是 `ccl restore` 入口。
func runRestore(args []string) error {
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
	cfg := config.NewManager(settingsPath, backupDir, 10)

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