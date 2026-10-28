## 1. launcher 支持追加参数

- [x] 1.1 `internal/launcher/launcher.go`：`Options` 加 `ExtraArgs []string`（追加给 Claude Code 的原始参数）
- [x] 1.2 `internal/launcher/launcher.go`：`resolveClaude` 把 `ExtraArgs` 拼进 args（在既有 flag 之后）
- [x] 1.3 单测：ExtraArgs 被拼进最终 argv；既有参数不受影响

## 2. cmd 层构造 additional settings

- [x] 2.1 `cmd/cc-launcher/settings_unix.go`（`!windows`）：`settingsArgs(json string) (args []string, cleanup func(), err error)` → `["--settings", json]`，cleanup 是 no-op（Unix 上 exec 后 ccl 进程被替换，无法删文件 → 不用文件）
- [x] 2.2 `cmd/cc-launcher/settings_windows.go`（windows）：同名函数 → 写 `os.TempDir()` 下的临时文件（0600）→ `["--settings", path]`，cleanup 删文件（Windows 上 launcher 等子进程退出，ccl 的代码还在）
- [x] 2.3 `cmd/cc-launcher/launchctx.go`：`buildLaunchSettingsJSON(cm, provider, cwd, baseURL) (string, error)` —— 读 settings.json 的 `env.OTEL_RESOURCE_ATTRIBUTES` 原值 → `launchctx.Merge(原值, attrs)` → 产出 `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"<合并值>"}}`
- [x] 2.4 `cmd/cc-launcher/launchctx.go`：**删掉**"settings.json 已设该 key → 降级 + 警告"分支（`--settings` 优先级更高，不再有被封的情况）；保留 settings.json 的**读取**（要拿用户原值）
- [x] 2.5 `cmd/cc-launcher/main.go`：把 additional settings 的参数接到 `launcher.Options.ExtraArgs`；Windows 下在 `launcher.Launch` 返回后调 cleanup
- [x] 2.6 单测：合并逻辑（原值保留 + ccl 同名 key 覆盖）；JSON 形状正确；Unix 分支产出内联参数

## 3. daemon 转发前剥掉 ccl.* attribute

- [x] 3.1 `internal/collector` 新增 `stripCclAttributes(body []byte) ([]byte, error)`：unmarshal ExportMetricsServiceRequest → 从每个 Resource 的 attributes 里删 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` → marshal
- [x] 3.2 `internal/collector/server.go`：`handleMetrics` 转发前用剥过的 body（**本地存的 labels 不动**）
- [x] 3.3 单测：剥掉后 3 个 key 消失、用户的 key 保留、unknown fields 不丢；无 ccl.* 时 body 不变

## 4. 端到端验证

- [x] 4.1 真机：settings.json 里**故意**设 `OTEL_RESOURCE_ATTRIBUTES=team=platform` → 跑 ccl → daemon 收到 `ccl.*` + `team` → db 里 provider/cwd 有真值
- [x] 4.2 真机：settings.json 不设该 key → 同样正常
- [x] 4.3 `ccl stats` 的 PROJECT / PROVIDER 列有真值
- [x] 4.4 `go build ./...` + `go test ./...` + `go test -tags smoke ./...` 全绿
- [x] 4.5 转发重置的端到端：本地 db 的 labels 仍有 `ccl.*`，转发出去的那份没有（用假 collector 验）

## 5. 文档与发布

- [x] 5.1 README：更新"provider / 项目维度从哪来"段落（注入通道改为 `--settings`；settings.json 已设该 key 也不再降级）
- [x] 5.2 CHANGELOG：加本次版本段（Features / Compatibility）
- [x] 5.3 `openspec archive`；发 patch 版本 + 连带 bump scoop
