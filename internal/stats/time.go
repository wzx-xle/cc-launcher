package stats

import "time"

// TodayStartMillis 返回本地时区今日 00:00 的 UnixMilli。
//
// "今日" 按用户感知的本地日历日切分（不是 UTC）；ccl 用户场景下本地时区合理。
//
// 本函数是"本地日历日起点"的唯一来源（stats-time-buckets §D5），任何用今日 sessions
// 统计、桶聚合起点、当前小时 partial 判定等场景都必须走它，避免跨包语义漂移。
//
// 历史:原 `cmd/cc-launcher/statscmd.go::todayStartMillis` 是本函数的唯一调用点;
// 该函数迁移到本包后,旧函数删除。
func TodayStartMillis() int64 {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return start.UnixMilli()
}