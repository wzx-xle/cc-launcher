# stats-clock-injection

给 internal/stats 加 clock 注入（Query.Clock + QueryBucketSummary 加 nowFn 参数），让 smoke test 不再依赖 time.Now() 真实 today，避免 sub-test 跨日期边界失败
