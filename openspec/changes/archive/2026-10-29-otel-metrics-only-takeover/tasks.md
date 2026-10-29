## 1. 交付策略层（cmd/cc-launcher/envconfig.go）

- [x] 1.1 策略表加 `deliver` 字段（正交于 takeover）：false = 永不进交付的 env 块
- [x] 1.2 兜底端点键 `OTEL_EXPORTER_OTLP_ENDPOINT` 设为 `deliver=false`（仍留在表里——遗留清理要用它的默认值判自我备份）
- [x] 1.3 `buildChildOtelEnv` 跳过 `!deliver` 的键
- [x] 1.4 注释更新：说明"为什么只接管 metrics"（总配置 = logs/traces 的必经之路）

## 2. 测试

- [x] 2.1 `buildChildOtelEnv` 单测：兜底端点**永不交付**（新增专项用例）
- [x] 2.2 既有单测同步（Takeover / MissingGetsDefault / RespectsUserValue / 集成场景 A / B）
- [x] 2.3 `go test ./...` + `go test -tags smoke ./...` 全绿

## 3. 真机验证

- [x] 3.1 假远程 collector（192.168.1.20:43181）+ 真 daemon + 真 claude：logs **直达** collector（`POST /v1/logs`），metrics 经 daemon 转发（`POST /v1/metrics`）
- [x] 3.2 daemon 落库带 provider / cwd（统计链路未受影响）
- [x] 3.3 ccl 交付的 `--settings` env 块里**没有**兜底端点键

## 4. 文档与发布

- [x] 4.1 README：交付表改成"只接管 metrics"；补一段"logs / traces 现在去哪了"
- [x] 4.2 CHANGELOG：加 v0.16.1 段（Fix：logs/traces 不再被 ccl 意外拦截）
- [x] 4.3 `openspec archive otel-metrics-only-takeover`
- [x] 4.4 打 tag + 一次推送
- [x] 4.5 bump scoop manifest
