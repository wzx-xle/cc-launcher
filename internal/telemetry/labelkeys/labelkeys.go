// Package labelkeys 定义 metric 行落库时保留的 labels 键白名单。
//
// 为什么是白名单而不是黑名单：Claude Code 的 resource attributes 是**主机级/
// 会话级常量**（service.* / host.* / os.* / terminal.* / user.id …），每行重复
// 存一遍 → 实测每行 labels 占 ~380 字节，其中绝大部分逐字相同。白名单对未来
// 也成立：以后 Claude Code 加新的 resource attribute，不会再撑大每一行。
//
// 为什么放独立叶子包：写入侧（internal/collector）与只读侧（internal/stats）
// 都要用它——只读侧要断言"stats SQL 里用到的每个 labels key 都在白名单里"
// （漏一个即红），否则将来 stats 加 JSON_EXTRACT(m.labels,'$.xxx') 而白名单没
// 同步，那个维度会静默变空、没人拦得住。让 internal/stats 直接 import
// internal/collector 会把 sqlite driver、forwarder 整套拖进 stats 的测试二进制，
// 且方向不干净（collector 是写入侧、stats 是只读侧）。
package labelkeys

import "strings"

// Stored 是 metric 行落库时唯一保留的 labels 键。
//
//   - type         stats 层用它做 token 分类（input / output / cacheRead /
//     cacheCreation）——每行不同，是 labels 里唯一真正"逐行"的键
//   - model        保留 metrics 级的按模型查询能力（sessions.model 只有 session
//     粒度，一个 session 换过 model 就查不出来）
//   - ccl.provider providerName() 的第一级兜底（非 ccl 启动的会话 sessions.provider
//     为空，靠它）
//   - provider     providerName() 的第二级兜底
//
// 刻意**不**保留的：ccl.cwd（完整路径字符串，是剩下最肥的一项，而 sessions.cwd
// 已有它、看板也只读 sessions.cwd）、user.id（64 字节主机指纹）、service.* /
// host.* / os.* / terminal.* / query_source / effort（主机级常量，逐行重复）。
var Stored = []string{"type", "model", "ccl.provider", "provider"}

// storedSet 是 Stored 的查找索引（元素很少，线性扫也够，但 map 更直白）。
var storedSet = func() map[string]bool {
	m := make(map[string]bool, len(Stored))
	for _, k := range Stored {
		m[k] = true
	}
	return m
}()

// Contains 报告 key 是否在白名单里。
func Contains(key string) bool { return storedSet[key] }

// SQLList 返回白名单键的 SQL 字符串字面量列表（`'type', 'model', ...`），
// 供迁移时拼 json_each 的过滤条件用。
//
// 键名全部来自本包的常量（非外部输入），不含引号；仍然逐个转义单引号以防
// 将来有人加进带引号的键。
func SQLList() string {
	var b strings.Builder
	for i, k := range Stored {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("'")
		b.WriteString(escapeSQLString(k))
		b.WriteString("'")
	}
	return b.String()
}

// escapeSQLString 把单引号翻倍（SQLite 的字符串字面量转义）。
func escapeSQLString(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\'' {
			b.WriteString("''")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
