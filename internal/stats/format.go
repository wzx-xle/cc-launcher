package stats

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
)

// FormatTable 把 Result 渲染成文本表格（人类可读）。
//
// 列选择根据 GroupBy 调整：
//   - "session"     → STARTED | PROJECT | PROVIDER | MODEL | IN | OUT | CACHE_CRE | CACHE_READ | COST
//   - "project"     → PROJECT | IN | OUT | CACHE_CRE | CACHE_READ | COST
//   - "provider"    → PROVIDER | IN | OUT | CACHE_CRE | CACHE_READ | COST
//   - "project+provider" → PROJECT | PROVIDER | IN | OUT | CACHE_CRE | CACHE_READ | COST
//
// 末尾追加总计行（TOTAL）。
func FormatTable(w io.Writer, r *Result) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()

	switch r.GroupBy {
	case "project":
		writeTable(tw, []string{"PROJECT", "IN", "OUT", "CACHE_CRE", "CACHE_READ", "COST_USD"},
			func(s SessionStats) []string {
				return []string{
					s.Project,
					fmt.Sprintf("%d", s.InputTokens),
					fmt.Sprintf("%d", s.OutputTokens),
					fmt.Sprintf("%d", s.CacheCreate),
					fmt.Sprintf("%d", s.CacheRead),
					fmt.Sprintf("%.4f", s.CostUSD),
				}
			}, r.Sessions, r.Totals)
	case "provider":
		writeTable(tw, []string{"PROVIDER", "IN", "OUT", "CACHE_CRE", "CACHE_READ", "COST_USD"},
			func(s SessionStats) []string {
				return []string{
					s.Provider,
					fmt.Sprintf("%d", s.InputTokens),
					fmt.Sprintf("%d", s.OutputTokens),
					fmt.Sprintf("%d", s.CacheCreate),
					fmt.Sprintf("%d", s.CacheRead),
					fmt.Sprintf("%.4f", s.CostUSD),
				}
			}, r.Sessions, r.Totals)
	case "project+provider":
		writeTable(tw, []string{"PROJECT", "PROVIDER", "IN", "OUT", "CACHE_CRE", "CACHE_READ", "COST_USD"},
			func(s SessionStats) []string {
				return []string{
					s.Project,
					s.Provider,
					fmt.Sprintf("%d", s.InputTokens),
					fmt.Sprintf("%d", s.OutputTokens),
					fmt.Sprintf("%d", s.CacheCreate),
					fmt.Sprintf("%d", s.CacheRead),
					fmt.Sprintf("%.4f", s.CostUSD),
				}
			}, r.Sessions, r.Totals)
	default: // "session"
		writeTable(tw, []string{"STARTED", "PROJECT", "PROVIDER", "MODEL", "IN", "OUT", "CACHE_CRE", "CACHE_READ", "COST_USD"},
			func(s SessionStats) []string {
				// TOTAL 行的 StartedAt 是 nil（sumSessions 不设），首列留空；
				// 真实 session 行 nil 也按空处理（防御性）。
				started := ""
				if s.StartedAt != nil {
					started = s.StartedAt.Format("2006-01-02 15:04")
				}
				return []string{
					started,
					s.Project,
					s.Provider,
					s.Model,
					fmt.Sprintf("%d", s.InputTokens),
					fmt.Sprintf("%d", s.OutputTokens),
					fmt.Sprintf("%d", s.CacheCreate),
					fmt.Sprintf("%d", s.CacheRead),
					fmt.Sprintf("%.4f", s.CostUSD),
				}
			}, r.Sessions, r.Totals)
	}
}

func writeTable(w *tabwriter.Writer, headers []string,
	row func(SessionStats) []string,
	sessions []SessionStats, totals SessionStats,
) {
	for i, h := range headers {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, h)
	}
	fmt.Fprintln(w)

	for _, s := range sessions {
		cells := row(s)
		for i, c := range cells {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, c)
		}
		fmt.Fprintln(w)
	}

	// 总计行
	if len(sessions) > 0 {
		cells := row(totals)
		for i, c := range cells {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, c)
		}
		fmt.Fprintln(w)
	}
}

// FormatJSON 把 Result 渲染成 JSON 字符串（含缩进）。
//
// jq 友好：sessions 数组每个元素包含完整字段；totals 是聚合行。
func FormatJSON(r *Result) (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// humanizeTokens 决策（stats-time-buckets design §D7）：
//   - 0 → "0"
//   - 1..999 → 原样整数
//   - 1K..999_499 → trimK(n/1000) + "K"（999_499 以下,trimK round 后不会进位到 "1000"）
//   - 999_500..999_999 → 切到 M,用 %.2f
//   - 1M..999_499_999 → trimK(n/1M) + "M"
//   - 999_500_000..999_999_999 → 切到 B,用 %.2f
//   - ≥ 1B → trimK(n/1B) + "B"
//
// trimK 是"3 位总位数"格式化器:
//   0<v<10   → %.2f(整数 1 位 + 小数 2 位,如 "1.23")
//   10≤v<100 → %.1f(整数 2 位 + 小数 1 位,如 "12.3")
//   100≤v<1k → %.0f(整数 3 位,如 "123")
//
// 边界保护：避免 trimK 的 %.0f 把 999.5 round 到 "1000K" 这种不合理单位。
// 提前在 abs 层判断 ≥ 999_500,切档并用 %.2f 显式两位小数。
func humanizeTokens(n int64) string {
	if n == 0 {
		return "0"
	}
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	abs := float64(n)
	switch {
	case abs < 999_500:
		return trimK(abs/1000) + "K"
	case abs < 1_000_000:
		return fmt.Sprintf("%.2fM", abs/1_000_000)
	case abs < 999_500_000:
		return trimK(abs/1_000_000) + "M"
	case abs < 1_000_000_000:
		return fmt.Sprintf("%.2fB", abs/1_000_000_000)
	default:
		return trimK(abs/1_000_000_000) + "B"
	}
}

func trimK(v float64) string {
	switch {
	case v < 10:
		return fmt.Sprintf("%.2f", v)
	case v < 100:
		return fmt.Sprintf("%.1f", v)
	default: // v < 1000
		return fmt.Sprintf("%.0f", v)
	}
}

// bucketBar 决策（stats-time-buckets §D6 + §D8）：
//   - 固定 20 字符宽度,跨终端一致
//   - 完整 8 级半块字符集:▏▎▍▌▋▊▉█(1/8..8/8)
//   - cells = round(tokens/max*20*8),0 ≤ cells ≤ 160
//   - cells/8 个完整█ + 1 个余数半块(余数 0..7)+ 空格补到 20
//   - max == 0 或 tokens == 0 → "·" + 19 空格(MIDDLE DOT 占位)
//   - partial == true(当前小时桶)→ 末位替换为 "⏳"(U+23F3),整体仍 20 字符
//
// rune 计数:半块字符占 3 字节 UTF-8,strings.Builder.Len() 是字节数,不能用来
// 判断"是否到 20 列"。这里改用 rune 数显式计算。
func bucketBar(tokens int64, max int64, partial bool) string {
	if max == 0 || tokens == 0 {
		// rune 数:1(·)+19(空格)=20
		runes := make([]rune, 0, 20)
		runes = append(runes, '·')
		for range 19 {
			runes = append(runes, ' ')
		}
		if partial {
			runes[19] = '⏳' // partial 桶零值仍用 ⏳ 标识,让用户一眼识别
		}
		return string(runes)
	}

	cells := int(math.Round(float64(tokens) / float64(max) * 20 * 8))
	if cells < 0 {
		cells = 0
	}
	if cells > 160 {
		cells = 160
	}

	fullBars := cells / 8
	remainder := cells % 8

	halfBlocks := []rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉'}

	// 显式构造字符串(rune 数 = 20,无 byte/rune 混淆)
	var visual int
	var sb strings.Builder
	sb.Grow(20 + 32) // 20 rune + 半块 UTF-8 多字节

	for i := 0; i < fullBars && visual < 20; i++ {
		sb.WriteRune('█')
		visual++
	}
	if remainder > 0 && visual < 20 {
		sb.WriteRune(halfBlocks[remainder-1])
		visual++
	}
	for visual < 20 {
		sb.WriteRune(' ')
		visual++
	}

	// invariant: 构造出 20 rune 字符串(WriteRune 按 rune 计数,与 byte 完全无关)
	if partial {
		// partial 时柱末位替换为 ⏳,整体仍 20 rune
		runes := []rune(sb.String())
		runes[19] = '⏳'
		return string(runes)
	}
	return sb.String()
}

// FormatBucketTable 渲染桶聚合表(决策 stats-time-buckets §1.4 + §D6 + §D8):
//
//   - 表头:BUCKET | SESSIONS | IN | OUT | CACHE_CRE | CACHE_READ | COST_USD | TOTAL_TOKENS(柱)
//   - hourly 模式(任一行 IsCurrent=true)→ 表头下追加注 "(* = 当前小时,数据仍在累计)"
//   - 数据行:Label / 数值字段(humanize, cost 用 %.4f) / 柱状图
//   - partial 桶柱末加 ⏳;零值桶或 max=0 用 · + 19 空格占位
//   - TOTAL 行:Label="TOTAL",数值字段 humanize,柱列渲染为 20 个 ─ + " (total)"
//
// rows 为 nil/空时只打表头(便于调用方处理空数据情形)。
func FormatBucketTable(w io.Writer, rows []BucketRow, totals *BucketRow) {
	// padding=1(非默认 2):column 总宽更紧,控 ≤80-col(spec scenario 4 hard cap)
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	// defer 先 Flush tabwriter,再输出 hourly note —— note 走 tabwriter 之外
	// 避免让 \t 撑宽 column 8(注 21 chars > bar 20),但放在 header+data+total 之后
	// 是 footnote 风格(更符合 spec 措辞 "header note")。
	defer func() {
		tw.Flush()
		for _, r := range rows {
			if r.IsCurrent {
				fmt.Fprintln(w, "(* = 当前小时,数据仍在累计)")
				return
			}
		}
	}()

	// max for normalization:全表 total tokens 最大值(maxTokens=0 → 所有桶全 zero,统一占位)
	var maxTokens int64
	for _, r := range rows {
		t := r.InputTokens + r.OutputTokens + r.CacheCreate + r.CacheRead
		if t > maxTokens {
			maxTokens = t
		}
	}

	fmt.Fprintln(tw, "BUCKET\tSESSIONS\tIN\tOUT\tCACHE_CRE\tCACHE_READ\tCOST_USD\tTOTAL_TOKENS")

	// hourly 注已移至 defer 内:Flush 后输出到 w,在 header+data+total 之下
	for _, r := range rows {
		t := r.InputTokens + r.OutputTokens + r.CacheCreate + r.CacheRead
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%.4f\t%s\n",
			r.Label,
			r.Sessions,
			humanizeTokens(r.InputTokens),
			humanizeTokens(r.OutputTokens),
			humanizeTokens(r.CacheCreate),
			humanizeTokens(r.CacheRead),
			r.CostUSD,
			bucketBar(t, maxTokens, r.IsCurrent),
		)
	}

	if totals != nil {
		sepLine := strings.Repeat("─", 20) + " (total)"
		fmt.Fprintf(tw, "TOTAL\t%d\t%s\t%s\t%s\t%s\t%.4f\t%s\n",
			totals.Sessions,
			humanizeTokens(totals.InputTokens),
			humanizeTokens(totals.OutputTokens),
			humanizeTokens(totals.CacheCreate),
			humanizeTokens(totals.CacheRead),
			totals.CostUSD,
			sepLine,
		)
	}
}