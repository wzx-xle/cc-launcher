## 1. 实现

- [x] 1.1 `cmd/cc-launcher/probe.go`：`forkDaemon` 把 stdout/stderr 接到 `~/.cc-launcher/serve.log`（0600、>2MB 截断）
- [x] 1.2 `cmd/cc-launcher/serve.go`：额度日志加 `[quota]` ASCII 前缀
- [x] 1.3 `internal/quota/refresh.go`：每轮打一行汇总（查了 / 不活跃跳过 / 未到期或退避跳过 / 失败）
- [x] 1.4 单测：汇总行含四个分类计数

## 2. 发布

- [ ] 2.1 `CHANGELOG.md` 加 `0.20.5` 段
- [ ] 2.2 `openspec archive serve-log-to-file`
- [ ] 2.3 打 tag `v0.20.5` + 推送 + bump scoop
