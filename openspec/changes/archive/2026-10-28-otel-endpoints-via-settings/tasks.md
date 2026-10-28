## 1. OTel 策略层重写（cmd/cc-launcher/envconfig.go）

- [x] 1.1 把"备份 + 改写"策略表拆成三个纯函数：交付策略（`buildChildOtelEnv`）、转发目标解析（`resolveForwardTarget`）、遗留清理（`cleanupLegacyOtelEnv`）
- [x] 1.2 `buildChildOtelEnv` 按 design D2 的表实现：接管 2 个端点键、尊重 4 个 exporter/开关键、绝不交付 `OTEL_LOGS_EXPORTER`
- [x] 1.3 `resolveForwardTarget` 实现 fallback / per-signal 两个原值的读取，遗留备份键优先于被改写的 live 键
- [x] 1.4 `cleanupLegacyOtelEnv` 实现 design D4 的三条规则（备份还原 / 自我备份删除 / 无备份默认值删除），并实现"检测到 ccl 痕迹才动手"的触发条件
- [x] 1.5 删除 `backupAndInjectOTel`、`isUnderCclControl` 及其调用方
- [x] 1.6 删除 `formatOTelSummary` / `isLocalhostEndpoint`（无调用方的死代码）

## 2. 启动流程接线（cmd/cc-launcher/main.go、launchctx.go）

- [x] 2.1 `injectOTelEnv` 改为 `prepareOtelEnv`：只读 settings.json → 计算交付 env + 转发目标 → 把转发目标写进 ccl 自己的进程环境（fork daemon 之前）
- [x] 2.2 一次性清理接入启动流程：调用 `cleanupLegacyOtelEnv` + `config.Manager.Backup()` + 写 state 标志 + 打印一行提示
- [x] 2.3 `injectLaunchContext` / `buildLaunchSettingsJSON` 把 OTel env 块与 `OTEL_RESOURCE_ATTRIBUTES` 合并进同一个 `--settings` JSON（仍然只有 env 块）
- [x] 2.4 确认调用顺序：清理 → 读配置 → 投递进程环境 → fork daemon → 启动 Claude Code
- [x] 2.5 `migrateSettingsJSON`（provider 迁移）保持不变，与 OTel 清理互不越界

## 3. 状态与命令（internal/state、cmd/cc-launcher/statscmd.go、serve.go）

- [x] 3.1 `internal/state` 增加一次性标志（`otel_legacy_cleaned`），含读写方法
- [x] 3.2 `ccl serve --restore` 改用 `cleanupLegacyOtelEnv`，语义与帮助文本改成"清理旧版 ccl 遗留"
- [x] 3.3 `serve.go` / 顶层帮助文本里对 `--restore` 的描述同步更新
- [x] 3.4 `--restore` 幂等：已清理过就是 no-op，且提示用户当前版本不再写 settings.json

## 4. 测试

- [x] 4.1 `buildChildOtelEnv` 单测：接管 / 尊重 / 不交付 logs / 空 settings 文件全量交付
- [x] 4.2 `resolveForwardTarget` 单测：fallback、per-signal 优先、遗留备份键优先、缺失时不设值
- [x] 4.3 `cleanupLegacyOtelEnv` 单测：还原、自我备份删除、无备份默认值删除、无痕迹不动、幂等、用户自有键不动
- [x] 4.4 `--settings` JSON 构造单测：env 块同时含 `OTEL_RESOURCE_ATTRIBUTES` 与 6 个 OTel 键，且只有 env 块
- [x] 4.5 `go test ./...` 全绿

## 5. 验证

- [x] 5.1 `openspec validate --all --strict` 通过
- [x] 5.2 真机：跑一次 `ccl`，确认 `~/.claude/settings.json` **字节不变**（mtime + hash 对比）
- [x] 5.3 真机：`ccl stats` 能看到 provider / cwd 列（launch context + OTel 接管都生效）
- [x] 5.4 真机：远程 collector 转发仍工作（用本地第二个端口模拟远程 collector）
- [x] 5.5 真机：裸 `claude` 不再进统计

## 6. 文档与发布

- [x] 6.1 README 的"用量监控"章节重写：6 个 key 的交付方式改为 `--settings`，`--restore` 语义更新
- [x] 6.2 README / CHANGELOG 写明行为变化：**裸 `claude` 会话不再被记录**
- [x] 6.3 CHANGELOG 加新版本段
- [ ] 6.4 `openspec archive otel-endpoints-via-settings`
- [ ] 6.5 打 tag + 一次推送（commit 与 tag 同一次 push）
- [ ] 6.6 bump scoop manifest
