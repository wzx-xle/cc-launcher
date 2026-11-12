package quota

import "time"

// Output 是**对外**的额度输出结构。
//
// 字段与 json tag 都是**对外契约**（仓库外的状态栏脚本消费它）：
//
//   - provider / source 让消费方知道"这是谁的额度、从哪个 adapter 来的"
//   - fetched_at 让消费方不必读数据文件就知道数据多旧
//   - stale 由 ccl 判定（消费方不必自己算阈值）
//   - dimensions 每个维度省略自己没有的字段（不写 null）
//
// **为什么住在 internal/quota 而不是 cmd 层**：`ccl quota` 命令与 daemon 的
// 只读额度端点必须给出**同一份文档**——两份 stale 判定迟早分叉（阈值、容忍
// 量、refreshInterval 的兜底各写一遍）。共用一份实现是唯一可靠的保证。
type Output struct {
	Provider   string      `json:"provider"`
	Source     string      `json:"source"`
	Stale      bool        `json:"stale"`
	FetchedAt  int64       `json:"fetched_at"`
	Dimensions []Dimension `json:"dimensions"`
}

// BuildOutput 组装一份对外的额度输出。
//
// ref 提供该 provider 的刷新周期（stale 判定用），entry 是数据文件里的条目。
//
// 返回的第二个值 false = **没有记录**（这条条目从没被抓取过，FetchedAt <= 0）——
// 调用方据此区分"没额度"（命令退出码 1 / HTTP 404）与"有额度"。
//
// now 显式传进来而不是内部取 time.Now()：stale 判定就卡在阈值上，测试要能
// 精确构造边界。
func BuildOutput(ref ProviderRef, entry Entry, now time.Time) (Output, bool) {
	if entry.FetchedAt <= 0 {
		return Output{}, false
	}
	dims := entry.Dimensions
	if dims == nil {
		// **空数组而不是 null**：消费方不该区分"没有维度"与"字段缺失"——
		// jq 的 `.dimensions[]` 对 null 会报错，对 [] 才安全。
		dims = []Dimension{}
	}
	interval := IntervalOf(ref)
	age := now.Sub(time.Unix(entry.FetchedAt, 0))
	return Output{
		Provider:   ref.Name,
		Source:     entry.Source,
		Stale:      age > interval+StaleTolerance,
		FetchedAt:  entry.FetchedAt,
		Dimensions: dims,
	}, true
}

// IntervalOf 返回该 provider 实际生效的刷新周期。
//
// **读不到配置时兜底 60 秒**（provider 没配 quota 块 → Config 为 nil）——与
// DefaultRefreshInterval 一致。命令与端点都用它算 stale 阈值，别处再写一遍
// 兜底逻辑就会分叉。
func IntervalOf(ref ProviderRef) time.Duration {
	return ref.Config.Interval()
}
