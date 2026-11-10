// Package quota 查询 provider 的额度（限额 / 配额 / 余额），并把结果记到本地
// 数据文件里，供 `ccl quota` 与 Claude Code 状态栏读取。
//
// 分层（provider-quota change design.md §决策 3-4）：
//
//	adapter 层  厂商差异（识别、端点推导、响应解析）——每个厂商一个 adapter
//	model 层    统一的 Dimension 类型与 Kind 分类——只有一处
//	store 层    数据文件读写（原子写、读-改-写）
//	refresh 层  枚举 / 去重 / 周期判定 / HTTP 客户端
//
// **为什么是维度列表而不是两个固定槽位**：Claude Code 的 rate_limits 把窗口
// 时长焊进了键名（five_hour / seven_day），只有月额度的厂商塞进去就是**说谎**，
// 而且第三个窗口（百炼的 5h + 周 + 月）没有位置。这里用"有周期 + 有分母"两个
// 正交属性分类，厂商有几个窗口就输出几个维度。
package quota

import (
	"fmt"
	"strings"
	"time"
)

// DefaultRefreshInterval 是额度刷新的默认周期。
//
// 60 秒的取舍：额度数据的变化速度是分钟级（5 小时窗口按分钟走），而状态栏
// 命令的调用频率是每 300ms 一次——两者必须解耦。60 秒让"最紧的那个窗口"
// 的误差不超过 1 分钟，同时把厂商接口的调用量压到可忽略（去重后本机约
// 5 次/分钟）。
const DefaultRefreshInterval = 60 * time.Second

// StaleTolerance 是 stale 判定额外给的容忍。
//
// **为什么需要**：`fetched_at` 只存到秒（向下取整），而数据年龄的峰值 ≈
// 刷新周期 − 刷新耗时（刷新是在"该刷新的时刻"**之后**才完成的：HTTP 请求 +
// 文件写入）。取整误差最多 +1 秒 → 阈值正好等于周期时，**稳态下每次刷新前
// 约 1 秒会误报 stale**（实测 age 峰值 60.75~61.48 秒，42 秒采样里每个
// provider 越线 4~8 次）——状态栏上表现为 "(stale)" 偶尔闪一下。
//
// **为什么是 1 秒**：取整误差的上界恰是 1 秒（frac < 1），而刷新耗时恒 > 0
// → 真实年龄峰值恒 < 周期 + 1 秒 → 这个容忍能**完全**覆盖，不会引入新的
// 误报窗口。
const StaleTolerance = time.Second

// Kind 是额度维度的类别。
type Kind string

const (
	// KindWindow 有周期 + 有分母：会重置的额度窗口（MiniMax 的 5h / 7d）。
	KindWindow Kind = "window"
	// KindQuota 无周期 + 有分母：总额度制，不按周期重置（New API 的令牌额度）。
	KindQuota Kind = "quota"
	// KindBalance 无分母：只有剩余量，算不出百分比（DeepSeek 的预付费余额）。
	KindBalance Kind = "balance"
)

// Dimension 是一个额度维度。
//
// 三个正交属性：
//   - 有没有分母（Limit != nil）→ 决定能不能算百分比、能不能画条形图
//   - 会不会重置（Duration > 0 / ResetsAt != nil）→ 决定是不是"窗口"
//   - 是不是无限（Unlimited）→ 无上限时百分比无意义
//
// **Used 与 Remaining 分成两个字段**，不合并成一个 Amount：余额是"剩余"、
// 额度是"已用"，方向相反，合并就要靠约定猜方向——约定就是 bug 的来源。
type Dimension struct {
	// ID 是稳定标识（"interval" / "weekly" / "balance" / "quota"）。
	ID string
	// Label 是展示名，**必须全是 ASCII**（见 DeriveLabel）。
	Label string
	// Kind 是维度类别。
	Kind Kind
	// Duration 是窗口时长；0 表示不重置。
	Duration time.Duration
	// Used 是已用绝对量；nil 表示接口没报。
	Used *float64
	// Limit 是总量（分母）；nil 表示不知道分母。
	Limit *float64
	// Remaining 是剩余绝对量；nil 表示接口没报。
	Remaining *float64
	// Unit 是单位（"%" / "USD" / "CNY" / "tokens"），**必须全是 ASCII**。
	Unit string
	// ResetsAt 是下次重置时间（窗口型）。
	ResetsAt *time.Time
	// ExpiresAt 是到期时间（令牌型，可选）。
	ExpiresAt *time.Time
	// Unlimited 表示无限额度（此时不派生百分比）。
	Unlimited bool
	// Raw 是厂商原始字段，便于排查（vendor 改了字段时看它）。
	Raw map[string]any
}

// UsedPct 派生"已用百分比"。
//
// **它是派生的**，不是接口给的，也不是存出来的：*Used / *Limit * 100。
//
// 返回 nil（而不是 0）的三种情况：分母未知（Limit == nil）、分母为 0
// （*Limit == 0）、无限额度（Unlimited）。"未知"与"零"必须可区分——状态栏
// 在前者显示 "--"、在后者显示满格。
func (d Dimension) UsedPct() *float64 {
	if d.Unlimited || d.Limit == nil || *d.Limit == 0 || d.Used == nil {
		return nil
	}
	v := *d.Used / *d.Limit * 100
	return &v
}

// Finalize 归一化一个维度：清洗 unit、按 kind 派生 label。
//
// adapter 解析完响应后必须调它——**label 永远不取自厂商响应的原始字符串**
// （厂商的字段可能是中文、可能含控制字符，而状态栏要把它渲染到终端一行里）。
func (d *Dimension) Finalize() {
	d.Unit = SanitizeASCII(d.Unit)
	if d.Unit == "" {
		d.Unit = "amount"
	}
	d.Label = DeriveLabel(d.Kind, d.Duration, d.Unit)
}

// DeriveLabel 按 kind 派生维度的展示标签。
//
//   - 窗口型（kind == window）→ 从时长派生：18000s→"5h"、604800s→"7d"、
//     2592000s→"30d"
//   - 其它 → **unit 的值**（"CNY" / "USD" / "tokens"）
//
// 非窗口型为什么用 unit 当 label：余额 / 总额度没有天然的短标签（不是 "5h"），
// 而它的 unit 恰好就是货币代码——既是准确语义又是纯 ASCII。
func DeriveLabel(kind Kind, duration time.Duration, unit string) string {
	if kind == KindWindow && duration > 0 {
		return DurationLabel(duration)
	}
	return unit
}

// DurationLabel 把窗口时长转成 ASCII 短标签。
//
// 单位选择按"能整除的最大粒度"：天 → 小时 → 分钟 → 秒。
// 先判天是必须的——604800 能同时被 3600 和 86400 整除，先判小时会得到
// "168h" 而不是 "7d"。
func DurationLabel(d time.Duration) string {
	sec := int64(d / time.Second)
	switch {
	case sec >= 86400 && sec%86400 == 0:
		return fmt.Sprintf("%dd", sec/86400)
	case sec >= 3600 && sec%3600 == 0:
		return fmt.Sprintf("%dh", sec/3600)
	case sec >= 60 && sec%60 == 0:
		return fmt.Sprintf("%dm", sec/60)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

// SanitizeASCII 去掉字符串里所有非 ASCII 可见字符。
//
// 状态栏是直接渲染到终端的一行文本——中文字形缺失时无法渲染，控制字符会
// 把整行搞乱。厂商响应里的任何字符串（货币代码、错误消息）都要过这一道。
func SanitizeASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// f64 返回 v 的指针（构造 Dimension 字段时省事）。
func f64(v float64) *float64 { return &v }

// clampPct 把百分比夹到 [0, 100]。
//
// MiniMax 给的是**剩余**百分比，adapter 转换时用 100 - remaining；厂商偶尔
// 会返回 100.5 之类的越界值 → 夹住，别把状态栏的条形图画崩。
func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
