package stats

import (
	"encoding/json"
	"fmt"
	"io"
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