## Context

`web-usage-stats-ui` 的 launch 上下文靠**进程环境**的 `OTEL_RESOURCE_ATTRIBUTES` 送进 OTLP payload 的 resource attributes。Windows 用户实测发现：只要 `~/.claude/settings.json` 的 `env` 块里已设 `OTEL_RESOURCE_ATTRIBUTES`，Claude Code 就用 settings.json 的值覆盖进程环境的值 → ccl 的上下文彻底送不到 → provider 维度恒空。

2026-10-28 的实测把三条事实钉死了：

| 事实 | 验证方式 | 结果 |
|---|---|---|
| settings.json 的 env 优先级高于进程环境 | settings.json 写 `claudeside=2` + 进程环境写 `procside=1` | 只收到 `claudeside=2` |
| `--settings` 优先级高于 settings.json | settings.json 写 `userside=1` + `--settings` 写 `claudeside=2` | 只收到 `claudeside=2` |
| `--settings` 的 env 块是深合并 | settings.json 有 `OTEL_EXPORTER_OTLP_ENDPOINT`（本地 daemon）+ `--settings` 只给 env | metrics 正常到达本地 daemon（endpoint 未被挤掉） |

结论：**用 `--settings` 承载上下文，就绕开了"进程环境被压一层"的问题**。

## Decisions

### D1. 上下文经 `--settings` 注入，进程环境注入保留为冗余

`--settings` 是主通道；进程环境的注入**保留不动**：

- 保留的理由：settings.json **没**设该 key 时（大多数用户），进程环境的值仍然生效 → 冗余第二通道，且万一某个 Claude Code 版本不认 `--settings`，仍有一道防线
- 不保留会更"干净"，但会让现有测试失效、且丢掉冗余
- 代价：两份注入逻辑。**用同一个合并函数**产出最终值，避免漂移

### D2. 用户原值必须合并进注入值，不能替换

ccl 注入的值 = 「settings.json 里的用户原值」+ 「ccl 的三个上下文属性」。

- 直接替换会**丢用户的属性**（用户配的 `team=platform` 之类就没了）
- 合并用 `launchctx.Merge`（已有实现：ccl 的同名 key 以 ccl 为准，用户其它条目原样保留）
- 注意：合并结果里的**用户原值部分**保持未编码（用户自己写的形态），**ccl 追加的部分**用 percent-encode（Claude Code 按 `,`/`=` 拆属性，值里有逗号/等号会拆错）

### D3. 转发时删掉 ccl.* 三个 attribute（而不是备份原值再改字符串）

用户提出的"备份原值到 FORWORD_*、转发时重置"思路在此**等价但更省事**：

- OTLP payload 里的 resource attributes 是**展开的独立 key**（实测：`ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` 各自独立，`userside` 也在），不是原始的 `OTEL_RESOURCE_ATTRIBUTES` 字符串
- 所以"重置" = **删掉 ccl 追加的 3 个 key** → 剩下的就是"用户原值 + Claude Code 自带的 attributes"
- 不需要 `FORWORD_*` 备份命名空间、不需要改字符串、不需要知道原值长什么样
- ⚠️ **本地存的 labels 必须保留 ccl.***：`stats.providerName()` 的兜底读的就是 labels 里的 `ccl.provider` → 只改**转发的那份 body**，不动本地库

### D4. 平台差异：Unix 内联 JSON / Windows 临时文件

- **Unix**：`syscall.Exec` 直传 argv，不经 shell → JSON 里的双引号是普通字符 → 内联 JSON 字符串安全 ✓
- **Windows**：`launcher_windows.go` 走 `exec.Command("cmd", "/c", claudePath, args...)` → **cmd.exe 会剥掉 JSON 的双引号**，内联 JSON 会被拆坏 → 必须写文件（`--settings <path>`，官方支持文件或 JSON 字符串两种形式）
  - 临时文件放 `os.TempDir()`，权限 `0600`（内容含用户原值，可能敏感）
  - 清理：Windows 上 `launcher.Launch` 是 `cmd.Run()`（等子进程结束）→ claude 退出后 ccl 的代码还在 → 可删 ✓
  - Unix 上 exec 后 ccl 进程被替换 → 删不了 → 所以 Unix 不用文件
- 文件路径本身不含空格（`os.TempDir()` + 固定名），避免 cmd.exe 的引号问题

### D5. 删除"settings.json 已设该 key → 降级 + 警告"

`--settings` 优先级更高 → 注入不再被封 → **降级路径与警告都不再需要**。删掉 `settingsHasResourceAttrs` 的检测与警告分支。

保留 settings.json 的**读取**（要拿用户原值来合并）。

### D6. daemon 的 parser 一行不改

上下文仍然以 resource attributes 的 `ccl.provider` / `ccl.cwd` / `ccl.provider_endpoint` 三个独立 key 出现在 payload 里 → parser 现有的读取逻辑照旧工作。

本次只加"转发前删 key"这一步。

## Risks / Trade-offs

- **[R1] `--settings` 是 Claude Code 的公开参数，但语义可能变** —— 它现在是 "additional settings"（深合并）。若将来改成整块替换，ccl 注入会失效 → 症状是 provider 维度变空（可被 `ccl stats` 发现）。缓解：进程环境注入作为冗余通道还在（虽然优先级最低）
- **[R2] 转发前的 protobuf 改写** —— unmarshal → 删 3 个 key → marshal。Go 的 `google.golang.org/protobuf` 默认保留 unknown fields，往返不丢字段。代价：只在配了远程 collector 时存在（每次转发一次 unmarshal+marshal）
- **[R3] 转发出去的 body 字节 ≠ Claude Code 原始字节** —— 字段顺序 / protobuf 编码细节可能变，语义等价。只有远程 collector 做 body 签名校验（极罕见）才会受影响
- **[R4] Windows 临时文件残留** —— claude 被强杀时 ccl 来不及删 → 残留一个 0600 的小文件在 temp 目录。下次启动时清理旧的（按 mtime 阈值）
- **[R5] `--settings` 的 JSON 里有用户原值** —— 会出现在进程命令行（`ps` 可见）→ 用户原值里的敏感信息（若用户自己在 OTEL_RESOURCE_ATTRIBUTES 里放了 secret——这本来就是个不该放 secret 的地方）会暴露给本机其他用户。Windows 上走文件则无此问题

## Migration Plan

1. 改 `internal/launcher`（`Options.ExtraArgs`）+ 新增平台分支文件 + 改 `cmd/cc-launcher/launchctx.go`（构造 `--settings`）+ 改 `main.go`（接线）
2. 改 `internal/collector`（转发前删 ccl.*）
3. 删掉降级警告分支
4. 测试 + 真机端到端（真实 claude 会话 → daemon 收到 ccl.* → db 里 provider/cwd 有真值）
5. 更新 README / CHANGELOG，归档 change，发 patch 版本

## Open Questions

- 第二步（把 OTel 端点接管也搬进 `--settings`、拆掉 settings.json 改写体系）是否要做、何时做 —— 本次明确不做
- `--settings` 里的用户原值是否需要在 daemon 侧也做"转发重置"之外的其它处理（如原值里含 ccl.* 同名 key）—— 由 `launchctx.Merge` 的同名覆盖规则处理
