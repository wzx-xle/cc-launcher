# stats-metric-first-datapoint-drop

修复 ccl stats token/cost 统计高估:SDK close-flush 首个 datapoint 被重复计入。参考 Datadog OTEL-2649 的 shouldConsumeInitialValue heuristic,落 db 时存 OTel StartTimeUnixNano,统计时丢弃 series 中 start_time 早于 daemon 启动时间的首个 datapoint
