# usage-monitoring-via-otel

ccl-launcher 启动后台 daemon 监听 OTLP gRPC :4317，接收 Claude Code 的遥测（metrics + logs + traces），落 SQLite，提供 ccl stats CLI 查询本地用量。Claude Code 必须开 CLAUDE_CODE_ENABLE_TELEMETRY=1。
