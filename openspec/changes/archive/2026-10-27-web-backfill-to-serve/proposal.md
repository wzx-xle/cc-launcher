## Why

`web-usage-stats-ui` 把看板挂在 daemon 已监听的 `:4318` 上（`/ui` + `/api/stats`），CLI 入口是顶层子命令 `ccl web`。实施后复核发现两处 CLI 契约上的错位：

1. **`--backfill` 塞在 `ccl web` 里语义错位**：回填修的是 `sessions.cwd`（daemon 的数据修复动作），与"看板页面"毫无关系，只是恰好共用同一个 `telemetry.db`。用户想补历史数据，却要先想到"web"这个看板入口——名字与动作对不上。
2. **顶层命令从 4 个涨到 5 个**：`ccl web` 与 `ccl stats` 语义相邻（都是看用量），用户容易混；而 `ccl serve --status` 已经会打印看板 URL，`serve` 侧本来就带看板信息。

`ccl serve` 现有 4 个 flag（`--stop` / `--restore` / `--status` / `--restart`）**全部是"一次性动作后返回"**的 daemon 生命周期 / 维护动作——`--backfill` 正好是同一类，加进去语义一致，不引入新的类别。

## What Changes

- **`ccl web --backfill` 移除**，等价能力挪到 **`ccl serve --backfill`**（不限量历史 cwd 回填）
- `ccl serve` 的 flag 组从 4 个互斥 flag 变成 5 个互斥 flag（`--backfill` 与其它四个同为"一次性动作"，进同一个互斥组）
- `ccl web` 保留为**看板入口**：`ccl web`（确保 daemon 在跑 + 打印 URL）、`ccl web --rotate`（轮换 token）、`ccl web --open`（开浏览器）
- `ccl web --help` 的 flag 列表相应少一条；`ccl serve --help` 多一条
- shell completion 自动跟随（两个子命令的 flag 都由 `register*Flags` 单点注册）

## Capabilities

### Modified Capabilities

- `cli-help`: `ccl web --help` 的 flag 列表去掉 `--backfill`；`ccl serve --help` 增加 `--backfill`
- `cli-shell-completion`: `ccl __complete web ""` 的候选去掉 `--backfill`；`ccl __complete serve ""` 的候选增加 `--backfill`
- `daemon-lifecycle`: 新增 `ccl serve --backfill` 的行为约定（不限量回填、幂等、不改 schema）

## Impact

- **改动**：`cmd/cc-launcher/web.go`（去掉 `--backfill` 分支与 flag）、`cmd/cc-launcher/serve.go`（`registerServeFlags` 加 `--backfill`、互斥组加一项、分发到回填实现）、回填实现挪到 serve 侧
- **不改**：看板本身（`internal/web`）、daemon 的挂载方式、db schema、HTTP 契约
- **行为变更（破坏性）**：`ccl web --backfill` 不再被识别（会报 unknown flag）；`ccl serve --backfill` 是新增
- **依赖**：无新增 Go 依赖
