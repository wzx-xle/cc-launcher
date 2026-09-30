# Tasks: telemetry-config-compat

> 实际代码锚点：envconfig.go `cmd/cc-launcher/envconfig.go`、forwarder.go `internal/collector/forwarder.go`、server.go `internal/collector/server.go`、main.go `cmd/cc-launcher/main.go:164` `injectOTelEnv`、测试 `cmd/cc-launcher/envconfig_test.go`、`internal/collector/{forwarder,server}_test.go`。

## 1. envconfig 策略表 + restore 扩展

- [x] 1.1 在 `envconfig.go` 常量块（第 15-28 行）添加 `keyMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"` 与 `defaultMetricsEndpoint = "http://localhost:4318/v1/metrics"`
- [x] 1.2 给 `otelEnvAction` struct（第 31-36 行）加字段 `backupPrefix string`（默认 `"CC_ORIG_"`；per-signal metrics 用 `"FORWORD_"`），让 backup 命名空间可参数化（不必新加 `backupKeyForword()` 函数）
- [x] 1.3 在 `otelActions` 表（第 45-51 行）追加条目：
  ```go
  {key: keyMetricsEndpoint, defaultValue: defaultMetricsEndpoint, injectIfMissing: false, override: true, backupPrefix: "FORWORD_"},
  ```
  （仅 `override=true`；缺失时 `injectIfMissing=false` 不注入；backupPrefix 用新前缀）
- [x] 1.4 修改 `backupAndInjectOTel` 第 79 行 `bk := backupKey(act.key)` → `bk := act.backupPrefix + act.key`（同时移除对 `backupKey()` 的调用，因为 prefix 已参数化）
- [x] 1.5 修改 `restoreOTelEnv` 第 115 行同上：`bk := backupKey(act.key)` → `bk := act.backupPrefix + act.key`
- [x] 1.6 在 `envconfig_test.go` 添加 `TestBackupAndInject_PerSignalMetrics`：
  - 用户原值非空 → 备份到 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` + 改写为 `http://localhost:4318/v1/metrics`
  - 用户原值缺失 → 不注入、不创建 backup key
  - `FORWORD_*` backup key 已存在（再次跑 ccl）→ 不覆盖原 backup
- [x] 1.7 在 `envconfig_test.go` 添加 `TestRestoreOTelEnv_FORWORD`：含 `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 的 env map 还原后该 key 消失、原值恢复
- [x] 1.8 在 `envconfig_test.go` 添加 `TestPerSignalMetrics_EdgeCases`
  - 用户原值为空字符串（`""`）→ 视为缺失，不注入
  - 用户原值带尾部斜杠（`http://remote:4318/`）→ 原样备份到 `FORWORD_*`
  - 用户原值缺 scheme（`remote:4318`）→ 原样备份
  - 用户原值为 `"   "`（仅空白）→ 视为非空，触发备份

## 2. forwarder 触发条件 + URL 改为 /v1/metrics

- [x] 2.1 在 `forwarder.go` 第 32-33 行把常量改名 `envOrigEndpoint` → `envForwardMetricsEndpoint`，值改为 `"FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"`（保留 envAuthHeader 不变）
- [x] 2.2 在 `Forwarder` struct（第 25-30 行）加字段 `target string` 改为已经带 `/v1/metrics` 后缀的完整路径（`strings.TrimRight` 后直接拼接 `/v1/metrics`，不再拼 signal）
- [x] 2.3 重写 `NewForwarderFromEnv()` 第 42-53 行判定逻辑：
  1. 先 `os.Getenv("FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")` — 非空非 loopback → 用它
  2. 否则 `os.Getenv(envOrigEndpoint)`（旧的 `CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT`，常量化保留）— 非空非 loopback → 用它
  3. 都未设或都是 loopback → 禁用
- [x] 2.4 修改 `NewForwarder()` 第 58-72 行：构造时把 `endpoint` 规范化成完整 URL（trim `/` + 拼接 `/v1/metrics`），存到 `f.target`
- [x] 2.5 修改 `post()` 第 89 行 `url := f.target + "/v1/" + signal` → `url := f.target`（signal 参数化场景不再使用，可保留 `signal` 入参但不使用，或移除入参）
- [x] 2.6 更新 `forwarder.go` 顶部注释（第 17-23 行）和 `NewForwarderFromEnv` 注释（第 40 行）：说明 forwarder 现为 metrics-only，trigger 优先级 FORWORD_ > CC_ORIG_
- [x] 2.7 在 `forwarder_test.go` 添加 `TestNewForwarderFromEnv_PerSignalPriority`：env 同时存在 `FORWORD_` 与 `CC_ORIG_` 时，`f.target` 用 FORWORD_ 的 URL
- [x] 2.8 在 `forwarder_test.go` 添加 `TestNewForwarderFromEnv_FallbackPath`：仅 `CC_ORIG_` 存在时（无 `FORWORD_`），forwarder 仍能启用，target 用 CC_ORIG_ URL
- [x] 2.9 在 `forwarder_test.go` 添加 `TestNewForwarderFromEnv_LocalhostSkip`：`FORWORD_` 与 `CC_ORIG_` 都指向 localhost 时 `Enabled()` 返回 false
- [x] 2.10 在 `forwarder_test.go` 添加 `TestForwarder_EdgeCases`：
  - `FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=""` → `Enabled()` 返回 false
  - baseURL 已含 `/v1/metrics`（`http://remote:4318/v1/metrics`）→ `f.target` 不重复成 `/v1/metrics/v1/metrics`
  - baseURL 带尾部斜杠（`http://remote:4318/`）→ `f.target` 不含重复 `/`
  - baseURL 缺 scheme（`remote:4318`）→ 透传不补全

## 3. server handler 改 200 only（logs/traces）

- [x] 3.1 `server.go` 第 56-57 行 mux 注册保留不变，但 method `handleForwardOnly` 第 176 行改名 `handleAckOnly`（语义清晰：仅回 200）
- [x] 3.2 `handleAckOnly` 内（第 187-189 行）删除 `if s.fwd != nil { s.fwd.Forward(signal, body) }` 整个 if 块——forwarder 不再被调用
- [x] 3.3 `handleMetrics` 第 158-160 行的 `s.fwd.Forward("metrics", forwardBody)` 保留不变（metrics 仍转发）
- [x] 3.4 更新 `Server` struct 注释（第 18-25 行）和 `handleForwardOnly` → `handleAckOnly` 的 doc comment：logs/traces 仅回 200，不持久不转发
- [x] 3.5 在 `server_test.go` 添加 `TestServer_LogsHandlerAckOnly`：POST `/v1/logs` 返回 200，**且 forwarder 未被调用**（用 mock forwarder 验证 `Forward()` 次数为 0）
- [x] 3.6 在 `server_test.go` 添加 `TestServer_TracesHandlerAckOnly`：POST `/v1/traces` 同上
- [x] 3.7 在 `server_test.go` 添加 `TestServer_AckOnly_EdgeCases`：
  - forwarder 启用时（Forwarder 非 nil），logs/traces POST 仍只回 200，**不触发 Forward()**
  - logs/traces POST 无 body（Content-Length: 0）→ 仍返回 200
  - logs/traces POST 带非法 protobuf bytes → 仍返回 200，不 panic
  - 并发 10 个 logs + 10 个 traces POST → 全部 200，无 race
- [x] 3.8 升级现有 forward 测试（如有）：确保现有 `/v1/metrics` 测试仍通过；如有针对 logs/traces 的旧测试用例，更新其 `Forward()` 次数期望从 1 改为 0

## 4. injectOTelEnv 同步 FORWORD_ 到进程 env

- [x] 4.1 `main.go` 第 197-201 行同步循环已存在：
  ```go
  for k, v := range updated {
      if strings.HasPrefix(k, "CC_ORIG_") {
          _ = os.Setenv(k, v)
      }
  }
  ```
  修改为同时匹配两个前缀：
  ```go
  for k, v := range updated {
      if strings.HasPrefix(k, "CC_ORIG_") || strings.HasPrefix(k, "FORWORD_") {
          _ = os.Setenv(k, v)
      }
  }
  ```
- [x] 4.2 在 `injectOTelEnv` 函数 doc comment（第 160-163 行）加一行：明确说明该循环同步 `CC_ORIG_*` 与 `FORWORD_*` 两类备份键到子进程 env
- [x] 4.3 在 `main_test.go`（如不存在则新建）添加 `TestInjectOTelEnv_FORWORD_Propagation`：构造带 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` 的 settings.json → 调 `injectOTelEnv` → 验证 `os.Getenv("FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")` 返回用户原值
- [x] 4.4 在 `main_test.go` 添加 `TestInjectOTelEnv_FORWORD_EdgeCases`：
  - 用户原值缺失（settings.json 无 `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`）→ `os.Getenv("FORWORD_...")` 返回空字符串
  - `os.Setenv` 已存在的 env var（测试前置用 `t.Setenv`）→ 被覆盖为 settings.json 中的备份值
  - daemon 已运行后再调 `injectOTelEnv`（不 fork）→ `os.Setenv` 仍执行（无害，daemon 已忽略新 env）

## 5. 集成测试 / 端到端验证

- [x] 5.1 场景 A 测试（典型公司远程 collector）：mock 一个 daemon 接收 metrics，验证 SQLite 有数据 + forwarder POST 到 `FORWORD_` URL
- [x] 5.2 场景 B 测试（仅 fallback 用户）：`CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT` 触发路径仍工作
- [x] 5.3 场景 C 测试（全新用户）：与现状行为一致（5 个 fallback key 注入 + 无 FORWORD_）
- [x] 5.4 `ccl restore` 端到端测试：含 FORWORD_ 的 settings.json 还原后等价于初始用户配置
- [x] 5.5 `go test ./...` 全量跑：envconfig / collector / main 全部包测试通过
- [x] 5.6 跑 `go vet ./...` 与 `golangci-lint run`（如有配置）：无 warning/error

## 6. 文档

- [x] 6.1 更新 README.md：新增"per-signal endpoint 兼容"段落，描述 FORWORD_ 命名空间由来与 per-signal 接管范围（仅 metrics）
- [x] 6.2 在 README 中加 scenario A 的 settings.json example（典型公司远程 collector 配置）
- [x] 6.3 更新任何引用旧 forwarder 行为的 doc 片段（logs/traces 不再转发）
- [x] 6.4 在 envconfig.go 顶部 doc comment（第 9-14 行）加一行：解释为什么 metrics per-signal 用新 FORWORD_ 命名空间而其他 fallback key 仍用 CC_ORIG_

## 7. 提交

- [x] 7.1 logical commit 1：`feat(envconfig): backup+rewrite OTEL_EXPORTER_OTLP_METRICS_ENDPOINT under FORWORD_ namespace`（§1）
- [x] 7.2 logical commit 2：`feat(collector): forwarder triggers on FORWORD_; /v1/logs /v1/traces return 200 only`（§2 + §3）
- [x] 7.3 logical commit 3：`feat(launcher): injectOTelEnv propagates FORWORD_ to subprocess env`（§4）
- [x] 7.4 logical commit 4：`test(envconfig,forwarder,server): cover per-signal metrics + ack-only handlers + edge cases`（§1.6-1.8、§2.7-2.10、§3.5-3.8、§4.3-4.4）
- [x] 7.5 logical commit 5：`docs(readme): document FORWORD_ namespace + per-signal metrics endpoint takeover`（§6）

## 8. 验证（手动 smoke test + 归档）

- [x] 8.1 跑 `openspec validate telemetry-config-compat` 校验变更结构
- [x] 8.2 跑 `openspec show telemetry-config-compat --json` 检查 artifacts 完整性
- [x] 8.3 smoke test — 启动 daemon + curl 发 metrics 请求：`go build` → `./cc-launcher serve` 后台启动 → POST `http://localhost:4318/v1/metrics` → 验证 SQLite 有数据 + mock OTLP HTTP 接收器收到 POST
- [x] 8.4 smoke test — daemon 收到 logs/traces 只回 200：POST `http://localhost:4318/v1/logs` 和 `/v1/traces`（valid + invalid body 各一次）→ 验证返回 200，且 mock 远程 collector **未收到** logs/traces POST
- [x] 8.5 smoke test — settings.json 改写 + 还原：准备场景 A 配置（含 FORWORD_/CC_ORIG_ 备份 + ccl 注入的默认）→ 跑 `ccl restore` → 验证 settings.json 等价于初始用户配置
- [x] 8.6 smoke test — `ccl stats` 有数据：基于 §8.3 启动的 daemon + 发了 metrics 之后，跑 `ccl stats` → 验证输出含 `claude_code.session.count` 等指标
- [x] 8.7 所有 task 完成 + 上述验证通过后跑 `opsx:archive` 归档