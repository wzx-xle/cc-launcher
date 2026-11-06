package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/quota"
	"cc-launcher/internal/state"
)

// quotaHelp 是 `ccl quota --help` 的输出。
const quotaHelp = `用法: ccl quota [选项]

显示某个 provider 的额度（窗口限额 / 令牌额度 / 余额）。

**默认只读本地数据文件，一次网络都不发**——数据由 ` + "`ccl serve`" + ` 周期刷新
（默认 60 秒）写入 ~/.cc-launcher/quota.json。加 --refresh 才会联网查一次。

选项:
    --provider <名字>   要查询的 provider（缺省：环境变量 CCL_PROVIDER → state.json 的 active provider）
    --json              输出 JSON（pipeable to jq）
    --refresh           联网查一次并写回数据文件，然后照常输出（不需要 serve 在跑）
    -h, --help          显示本帮助并退出

输出契约:
    { "provider": "...", "source": "...", "stale": false, "fetched_at": 1793943914,
      "dimensions": [ { "id": "interval", "label": "5h", "kind": "window",
                        "duration_seconds": 18000, "used_percentage": 59,
                        "resets_at": 1793961914 } ] }

    kind 有三种：
      window  有周期 + 有分母（5h / 7d 窗口）——可画条形图
      quota   无周期 + 有分母（令牌总额度）
      balance 无分母（只有余额）——只显示数字

退出码:
    0  读到额度
    1  没有额度记录（serve 没跑 / 还没刷新过 / provider 不存在）
    2  该 provider 没有额度接口（正常状态，不是错误）

示例:
    ccl quota --provider minimax-m3 --json | jq '.dimensions[]'
    ccl quota --refresh --json          # 手动刷一次（serve 被停掉时的逃生舱）`

// quotaFlags 是 quota 子命令的 flag 集合。
type quotaFlags struct {
	provider *string
	asJSON   *bool
	refresh  *bool
}

// registerQuotaFlags 把 quota 子命令的 flag 注册到 fs，返回值供 runQuota 与
// cmd/cc-launcher/completion.go::completeAtQuota 共用。
//
// cli-shell-completion §D2：flag 改动自动同步——加新 flag 时（仅改本函数）
// completion 自动跟随。
func registerQuotaFlags(fs *flag.FlagSet) quotaFlags {
	var f quotaFlags
	f.provider = fs.String("provider", "", "要查询的 provider（缺省用 CCL_PROVIDER 环境变量，再缺省用 state.json 的 active provider）")
	f.asJSON = fs.Bool("json", false, "输出 JSON")
	f.refresh = fs.Bool("refresh", false, "联网查一次并写回数据文件（不需要 serve 在跑）")
	return f
}

// quotaExitFn 是 ccl quota 的退出钩子。
//
// 默认调 os.Exit；测试覆盖为 no-op 避免把整个 test 进程杀掉（跟 statsExitFn
// 同模式：退出码是**契约的一部分**，不能靠 return error 表达）。
var quotaExitFn = func(code int) { os.Exit(code) }

// quotaOutput 是 `ccl quota --json` 的输出结构。
//
// 字段是**对外契约**（仓库外的状态栏脚本消费它）：
//   - fetched_at 让消费方不必读数据文件就知道数据多旧
//   - stale 由 ccl 判定（消费方不必自己算阈值）
//   - 每个维度省略自己没有的字段（不写 null）
type quotaOutput struct {
	Provider   string            `json:"provider"`
	Source     string            `json:"source"`
	Stale      bool              `json:"stale"`
	FetchedAt  int64             `json:"fetched_at"`
	Dimensions []quota.Dimension `json:"dimensions"`
}

// runQuota 是 `ccl quota` 的入口。
//
// **默认纯本地读**：状态栏命令实测生成过程中约每 300ms 被调一次，读取侧
// 一次网络都不能发（否则会落进 Claude Code 的 300ms 去抖窗口 + abort 风暴）。
func runQuota(args []string) error {
	fs := flag.NewFlagSet("quota", flag.ContinueOnError)
	// 自己处理 help 与错误文案：flag package 默认会把 "Usage of quota:" 打到
	// stderr 并返回 flag.ErrHelp —— 那样 `ccl quota --help` 会以 exit 1 收场。
	fs.SetOutput(io.Discard)
	fl := registerQuotaFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println(quotaHelp)
			return nil
		}
		return err
	}

	baseDir := cclLauncherBaseDir()
	providersDir := filepath.Join(baseDir, "providers")
	st := quota.NewStore(baseDir)

	// provider 三级回落：显式 --provider > 环境变量 CCL_PROVIDER > state.json
	// 的 active provider。
	//
	// 状态栏子进程继承得到 CCL_PROVIDER，所以消费方只要传
	// `--provider "$CCL_PROVIDER"` 就行；不传也能靠环境变量兜住。
	name := strings.TrimSpace(*fl.provider)
	if name == "" {
		name = strings.TrimSpace(os.Getenv(provider.EnvProviderName))
	}
	if name == "" {
		name = strings.TrimSpace(state.NewManager(baseDir).GetActiveProvider())
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "没有指定 provider：用 --provider，或设置 CCL_PROVIDER 环境变量，或先跑一次 ccl")
		quotaExitFn(1)
		return nil
	}

	// 离线判定"该 provider 有没有额度接口"。
	ref, hasQuota, err := quota.LoadProviderRef(providersDir, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "提供商 %q 不存在\n", name)
			quotaExitFn(1)
			return nil
		}
		fmt.Fprintf(os.Stderr, "读提供商 %q 的配置失败: %v\n", name, err)
		quotaExitFn(1)
		return nil
	}
	if !hasQuota {
		// **纯离线可判**：读 provider 配置就知道它没有额度接口。
		// 退出码 2（正常状态，不是错误）——消费方据此回落到 Claude Code 自己的
		// rate_limits。
		fmt.Fprintf(os.Stderr, "提供商 %q 没有额度接口\n", name)
		quotaExitFn(2)
		return nil
	}

	var (
		dims      []quota.Dimension
		source    string
		fetchedAt int64
	)

	if *fl.refresh {
		// 联网查一次 → 写回数据文件 → 照常输出（输出的是**刚查到的值**）。
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fetched, err := quota.RefreshOne(ctx, ref, st)
		if err != nil {
			if errors.Is(err, quota.ErrNoQuotaInterface) {
				fmt.Fprintf(os.Stderr, "提供商 %q 没有额度接口\n", name)
				quotaExitFn(2)
				return nil
			}
			// **退避中**：被厂商限流后要等一会儿。手动刷不能冲掉退避——它打的
			// 是同一个接口。提示还剩多少秒，给用户可行动的信息。
			var bo *quota.BackoffError
			if errors.As(err, &bo) {
				wait := bo.RetryAfter - time.Now().Unix()
				if wait < 0 {
					wait = 0
				}
				fmt.Fprintf(os.Stderr,
					"提供商 %q 正在退避中（厂商限流），%d 秒后可再试\n", name, wait)
				quotaExitFn(1)
				return nil
			}
			// **失败时不写文件**（旧条目保留）→ 退出码 1。
			fmt.Fprintf(os.Stderr, "刷新 %q 的额度失败: %v\n", name, err)
			quotaExitFn(1)
			return nil
		}
		dims = fetched
		// 注意：ref.Config 可能是 nil（provider 没配 quota 块，靠 base_url
		// 自动识别）——不能直接解引用。
		if ref.Config != nil {
			source = ref.Config.Source
		}
		if source == "" {
			source = detectSource(ref)
		}
		fetchedAt = time.Now().Unix()
	} else {
		// **纯本地读**：不发起任何网络请求。
		file, err := st.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "读额度数据失败: %v\n", err)
			quotaExitFn(1)
			return nil
		}
		entry, ok := file.EntryFor(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "还没有 %q 的额度记录（跑 `ccl serve` 或 `ccl quota --refresh` 抓一次）\n", name)
			quotaExitFn(1)
			return nil
		}
		dims = entry.Dimensions
		source = entry.Source
		fetchedAt = entry.FetchedAt
	}

	// stale 判定：用条目的 fetched_at 算 age，超过该 provider 的
	// refreshInterval（**兜底 60 秒**）→ 标 stale。
	//
	// 阈值额外给 1 秒容忍（quota.StaleTolerance）：fetched_at 只有秒精度，
	// 取整误差最多 +1 秒 → 阈值正好等于周期时，稳态下每次刷新前约 1 秒会
	// 误报 stale（状态栏上表现为 "(stale)" 偶尔闪一下）。
	interval := quota.DefaultRefreshInterval
	if ref.Config != nil {
		interval = ref.Config.Interval()
	}
	stale := time.Since(time.Unix(fetchedAt, 0)) > interval+quota.StaleTolerance

	out := quotaOutput{
		Provider:   name,
		Source:     source,
		Stale:      stale,
		FetchedAt:  fetchedAt,
		Dimensions: dims,
	}
	if out.Dimensions == nil {
		out.Dimensions = []quota.Dimension{}
	}

	if *fl.asJSON {
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	printQuotaTable(out, interval)
	return nil
}

// detectSource 猜一下 provider 用的是哪个 adapter（仅用于 --refresh 的输出
// 字段；猜不到就留空）。
func detectSource(ref quota.ProviderRef) string {
	ad, _, err := ref.Resolve()
	if err != nil {
		return ""
	}
	return ad.Name()
}

// printQuotaTable 打印给人看的额度表格。
//
// **绝对量与百分比都列**：窗口型看百分比，余额型只能看绝对量（无分母）。
func printQuotaTable(out quotaOutput, interval time.Duration) {
	fmt.Printf("provider: %s\n", out.Provider)
	if out.Source != "" {
		fmt.Printf("来源: %s\n", out.Source)
	}
	fmt.Printf("抓取时间: %s", time.Unix(out.FetchedAt, 0).Format("2006-01-02 15:04:05"))
	if out.Stale {
		fmt.Printf("（已过期：超过 %s 未刷新）", interval)
	}
	fmt.Println()

	if len(out.Dimensions) == 0 {
		fmt.Println("（没有额度维度）")
		return
	}
	for _, d := range out.Dimensions {
		fmt.Printf("  %-8s %-8s", d.Label, d.Kind)
		if pct := d.UsedPct(); pct != nil {
			fmt.Printf(" 已用 %.2f%%", *pct)
		}
		if d.Used != nil {
			fmt.Printf(" 已用 %s", formatAmount(d.Used, d.Unit))
		}
		if d.Remaining != nil {
			fmt.Printf(" 剩余 %s", formatAmount(d.Remaining, d.Unit))
		}
		if d.Limit != nil && d.Used == nil {
			fmt.Printf(" 总量 %s", formatAmount(d.Limit, d.Unit))
		}
		if d.Unlimited {
			fmt.Print(" 无限")
		}
		if d.ResetsAt != nil {
			fmt.Printf(" 重置 %s", d.ResetsAt.Format("2006-01-02 15:04"))
		}
		if d.ExpiresAt != nil {
			fmt.Printf(" 到期 %s", d.ExpiresAt.Format("2006-01-02 15:04"))
		}
		fmt.Println()
	}
}

// formatAmount 格式化绝对量（带单位）。
func formatAmount(v *float64, unit string) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%.4f", *v)
	// 去掉多余的尾随零（%.4f 会把 400.0000 变成 "400.0000"）。
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if unit != "" && unit != "%" {
		s += " " + unit
	}
	return s
}
