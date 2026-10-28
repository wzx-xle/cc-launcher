## Why

`web-usage-stats-ui`（v0.15.x）把 launch 上下文（provider / cwd / provider_endpoint）写进**子进程环境**的 `OTEL_RESOURCE_ATTRIBUTES`，随 Claude Code 的 OTLP payload 送到 daemon。

**Windows 用户实测踩坑**：用户的 `~/.claude/settings.json` 里已设 `OTEL_RESOURCE_ATTRIBUTES`（公司模板 / 旧脚本 / 别的教程），而 **Claude Code 的 settings.json `env` 块优先级高于进程环境** → ccl 注入的上下文被整体顶掉 → provider 维度恒空。

2026-10-28 实测复验（settings.json 写 `claudeside=2` + 进程环境写 `procside=1` → 实际只收到 `claudeside=2`）确认了这个覆盖关系。

**根因**：用进程环境承载上下文，天然会被 settings.json 压一层。

**解法**：Claude Code 有官方参数 `--settings <file-or-json>`（"load **additional** settings from"），实测优先级 **命令行 --settings > settings.json > 进程环境**，且 env 块是**深合并**（不是整块替换）。用它承载 launch 上下文，就绕开了"进程环境被 settings.json 覆盖"这一层。

## What Changes

- launch 上下文的注入通道**从进程环境改为 `--settings`**：
  - ccl 读 `~/.claude/settings.json` 的 `env.OTEL_RESOURCE_ATTRIBUTES`（用户原值，可能没有）
  - 合并出「用户原值 + ccl.provider / ccl.cwd / ccl.provider_endpoint」
  - 以 `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"<合并值>"}}` 经 `--settings` 传给 Claude Code
- **用户原值不丢**：合并而非替换 → Claude Code 拿到的 resource attributes 同时含用户的属性和 ccl 的上下文
- **转发时重置**：daemon 转发给远程 collector 前，删掉 payload 里 ccl 追加的 3 个 attribute key → 远程 collector 收到的资源属性恢复成"用户没装 ccl 时的样子"
- **删除**"settings.json 已设 OTEL_RESOURCE_ATTRIBUTES → 降级 + 警告"的行为（`--settings` 优先级更高，注入不再被封）
- 进程环境的注入**保留**（冗余第二通道 + 现有测试不破）：settings.json 没设该 key 时进程环境仍然生效
- 平台差异：Unix 用内联 JSON 字符串（execve 直传，不经 shell）；**Windows 必须用临时文件**（`cmd /c` 会剥掉 JSON 的双引号），文件在 claude 退出后由 ccl 删除

## Capabilities

### Modified Capabilities

- `session-context-attrs`: 上下文的注入通道从"子进程环境"改为"`--settings` 参数"；新增"用户原值合并"与"转发时重置"两条要求；删除"settings.json 已设该 key 则降级"的降级约定

## Impact

- **改动**：`cmd/cc-launcher/launchctx.go`（构造 additional settings）、新增 `cmd/cc-launcher/settings_unix.go` / `settings_windows.go`（平台分支）、`cmd/cc-launcher/main.go`（接线）、`internal/launcher`（`Options.ExtraArgs`）、`internal/collector`（转发前删 ccl.* attribute）
- **不改**：db schema、HTTP 契约、看板、daemon 的 parser（照旧读 resource attributes 的 `ccl.*`）、Claude Code 的其它 settings（`--settings` 只带 env 一个字段）
- **依赖**：无新增 Go 依赖
- **行为变更**：`ccl` 启动 Claude Code 时多一个 `--settings` 参数；转发给远程 collector 的 payload 里不再含 ccl.* 三个 attribute
- **不在本次范围**：把 OTel 端点接管（`OTEL_EXPORTER_OTLP_*` 那 6 个 key）也搬进 `--settings`、拆掉 settings.json 改写 / 备份 / `--restore` / 控制权交接体系 —— 那是独立的第二步
