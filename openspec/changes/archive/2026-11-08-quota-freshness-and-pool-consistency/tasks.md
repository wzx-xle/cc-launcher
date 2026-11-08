## 1. quota 包：刷新粒度提升到额度池级

- [x] 1.1 `internal/quota/refresh.go`：`RefreshAll` 的 provider 循环拆成两段 —— 先对**所有** ref 做 `Resolve` + 算 `PoolID` 并建 `providerPool` 映射（不再按活跃过滤就 `continue`）；`live` 集合仍收**全部** provider（Prune 的依据不变）
- [x] 1.2 由「池内任一 provider 在 `active` 名单里」推出**活跃池集合**；`active` 参数语义不变（仍是 provider 名集合，池换算在包内完成 → `serve.go` 的 `activeProviders()` 不用改）
- [x] 1.3 新增包级常量 `DefaultFreshnessThreshold = 10 * time.Minute`；`RefreshAll` 新增 `freshness time.Duration` 参数（传 0 = 关闭保鲜）
- [x] 1.4 到期判定分三档（design.md D2）：**退避中** → 跳过；**活跃池** → `dueAt = min(next_at, fetched_at + interval)`；**不活跃池** → `dueAt = max(next_at, fetched_at + freshness)`；不活跃池**没有条目**时跳过（保持"没人看的不查"）
- [x] 1.5 刷新成功后，结果写给池内**所有** provider（不再只写 active 的）—— `poolOf` 匹配必须覆盖不活跃成员
- [x] 1.6 `RefreshResult` 的计数语义从 provider 改成**额度池**；刷新轮次日志文案加"（池）"标注
- [x] 1.7 单测：①同池一活跃一不活跃 → 两者记录**同一时刻**；②不活跃池记录超过 10 分钟 → 被刷；③不活跃池记录未超 10 分钟 → **不被刷**（防止退化成每轮全刷）；④活跃池记录陈旧 → 立即被刷（不等 next_at）；⑤退避中的池即使超保鲜期也不被刷

## 2. collector：provider 激活端点

- [x] 2.1 `internal/collector/server.go`：`ServerOptions` 新增 `OnActivate func(provider string) error`（返回非 nil → 拒绝该请求）
- [x] 2.2 新增 `POST /ccl/activate?provider=X` handler：先调 `OnActivate`（业务侧做白名单校验），返回 nil 才 `activity.Mark(provider, now)` 并回 204；返回 error 回 400
- [x] 2.3 未配置 `OnActivate` 时该路径返回 404（不暴露一个什么都不做的端点）
- [x] 2.4 单测：合法名 → 204 + Activity 被标记 + 回调被调用；非法名 → 400 + Activity **未**被标记；未配置回调 → 404

## 3. serve：接线与兜底

- [x] 3.1 `cmd/cc-launcher/serve.go`：`quotaRefreshLoop` 持有 `activateCh chan string`，在 `select` 里监听 → 收到 provider 名就**立即**跑一轮刷新（不等 ticker）；通道满时丢弃并记日志（不阻塞 HTTP handler）
- [x] 3.2 `collector.NewServer` 的 `ServerOptions.OnActivate` 接到 `activateCh`：先做**白名单校验**（`providers/` 目录里真实存在的 `.json` 名，防注入 / 防对任意 URL 发请求），通过才往通道写
- [x] 3.3 daemon 启动时读 `state.json` 的 `active_provider` → `srv.Activity().Mark(provider, now)` 兜底（`SetActiveProvider` 写在 `ensureDaemonRunning()` 之前 → ccl 拉起 daemon 时已能读到本次选中的 provider）
- [x] 3.4 刷新轮次日志改用池计数（design.md D6）
- [x] 3.5 单测：激活信号触发立即刷新；白名单拒绝不触发；启动兜底让首轮有池可刷

## 4. ccl：向 daemon 声明本次使用的 provider

- [x] 4.1 `cmd/cc-launcher/main.go`：`ensureDaemonRunning()` 之后、`launcher.Launch()` 之前调 `declareProviderToDaemon(result.Provider)`
- [x] 4.2 实现：POST `http://127.0.0.1:4318/ccl/activate?provider=<url-encoded name>`，超时 500ms，**fire-and-forget**（不等响应、不阻塞启动）；连接失败/超时/非 2xx 一律**静默忽略**（Claude Code 的 OTel 是兜底）
- [x] 4.3 单测：provider 名做 URL 转义；daemon 不可达时不阻塞启动、不报错

## 5. 文档

- [x] 5.1 `docs/provider-quota.md`：说明刷新粒度是**额度池**、兜底保鲜 10 分钟规则、ccl→daemon 激活通道、日志计数单位为池

## 6. 验证与发布

- [x] 6.1 `go test ./...` 全绿；`go test -tags smoke ./...`（如有）
- [x] 6.2 真机：重启 daemon → `serve.log` 的**首轮**必须是"查了 ≥1"（不再空转）；确认启动后 5 秒内 `quota.json` 的 `fetched_at` 已更新
- [x] 6.3 真机：跑一次 `ccl` → `quota.json` 在 Claude Code 起来**之前**就已更新（验证激活通道）
- [x] 6.4 真机：确认 `minimax-m3` 与 `minimax-m3.1-flash`（同池）的 `fetched_at` **一致**（同池同源）
- [x] 6.5 真机：确认不活跃的池**不会**每 15 秒被刷（看 serve.log 的"不活跃跳过"计数稳定，请求量不涨）
- [x] 6.6 `CHANGELOG.md` 加版本段（patch：修缺陷，用户不需新动作；但"刷新粒度从 provider 改为池"是可见的行为变化，版本号若有异议按用户定）
- [x] 6.7 `openspec archive quota-freshness-and-pool-consistency`
- [x] 6.8 打 tag + 推送（scoop 是否跟发由用户定）
