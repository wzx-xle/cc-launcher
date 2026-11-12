## Why

状态栏读不到 ccl 的额度，Windows 上尤其明显。

实测（2026-11-12，Windows 11 / Claude Code 状态栏脚本）：

| 调用方式 | 耗时 |
|---|---|
| `ccl quota --json`（node `execSync`，状态栏的真实路径） | p50 **166~190ms**，max **252ms** |
| 同一命令在 PowerShell 里直接调 | p50 **266ms**，max **299ms** |

耗时的**全部**在进程启动上：`cmd.exe(scoop shim)` → `cmd.exe(app\ccl.cmd)` → `cc-launcher.exe`。而 `ccl quota` 本身已经是纯本地读（读一个 JSON 文件），毫秒级。

状态栏脚本给这次调用设了 `timeout: 250`。**余量只有 60~80ms**。而系统繁忙时（Claude Code 刚启动、用户配的 `SessionStart`/`Stop`/`UserPromptSubmit` 三个 hook 各自起一个 node 进程、Defender 实时扫描 18MB 的 exe 与上百 MB 的 node.exe、daemon 正在写额度数据文件）→这次调用必然超过 250ms → `execSync` 抛超时 → 脚本 `catch` → 返回 null → **状态栏回落到 Claude Code 自己的 `rate_limits` 槽位**。

用户看到的就是"额度显示成默认的用量"，而且**只在忙的时候错**——闲下来（166ms）就对。这解释了"刚启动不对、等会就正常"。

v0.22.1 修的是另一半（Windows 上额度数据文件被替换时的读竞态），但没有解决"叫不动"。

## What Changes

- **daemon 新增只读端点 `GET /ccl/quota?provider=<名字>`**：返回与 `ccl quota --json` **逐字段同构**的 JSON（`provider` / `source` / `stale` / `fetched_at` / `dimensions`）。只接受**回环来源**（`RemoteAddr` 的 IP 必须是回环地址），非回环请求一律拒绝。
- **既有的 `POST /ccl/activate`（ccl 声明本次用哪个 provider）也加上同一条回环限制**。它与新端点一样属于"ccl 自己 ↔ daemon"的内部交换：daemon 是 ccl fork 出来的子进程（或是用户在本机手动起的），声明的调用方必然与 daemon 同机 → 限回环零风险。此前它无鉴权且 daemon 监听所有网卡，局域网内任意机器都能让 daemon 带着用户的 API key 去查其已配置 provider 的额度端点。
- **`/v1/*`（OTel 协议端点）与 `/ui`、`/api/*`（看板）不在收紧范围内**：前者的合法调用方可能是 WSL2/容器里的 Claude Code（经 NAT 连到 Windows 的 `localhost:4318`，源地址不是回环），后者本就要支持从别的机器打开看板（且已有 token 保护）。daemon 的监听地址保持 `:4318` 不变，收紧只发生在端点级。
- **ccl 通过 `--settings` 的 `env` 块交付 `CCL_QUOTA_URL`**（值是常量 `http://127.0.0.1:4318/ccl/quota`），并在进程环境放一份冗余副本——与 `CCL_PROVIDER` 完全同构的通道与纪律。状态栏脚本拿到它就用 HTTP 读，**不再起进程**（几 ms）。
- **额度输出的组装逻辑抽到 `internal/quota`**：`Output` 类型与 `BuildOutput`（含 stale 判定）从 `cmd/cc-launcher/quotacmd.go` 搬进包内，命令层与 daemon 端点**共用同一份实现**——格式与 stale 判定各只有一处，不会出现"命令说 stale、状态栏说不 stale"。
- **`ccl quota` 命令改为优先走 daemon 端点、不可达时回落直读本地文件**。两者读的是同一个文件、同一份组装逻辑，因此输出完全一致；daemon 没跑时（`--refresh` 逃生舱的场景）依然可用，不退化。
- **`docs/provider-quota.md` 增补端点契约与 `CCL_QUOTA_URL` 的契约**，并给出状态栏脚本的推荐写法（HTTP 优先、命令兜底的双通道）。

不做（non-goals）：

- 不接管、不修改用户的 `~/.claude/settings.json` 的 `statusLine` 键，也不分发状态栏脚本——渲染是用户自己的事，ccl 只保证**读额度的通道够快**。
- 不把额度数据文件的位置与内部格式暴露给消费方。消费方要么走 HTTP 端点，要么走 `ccl quota` 命令，两条路拿到的都是同一份对外契约。
- 不改 `ccl quota --json` 的输出字段、退出码语义（0/1/2）与"不向外网发请求"的纪律。
- 不解决 Windows 上"起进程慢"本身（Defender 扫描、`.cmd` shim 的两层 `cmd.exe`）——那是 `ccl` 启动 Claude Code 的固有开销，与本 change 无关。
- 不改额度刷新的节奏、活跃判定、相位错开与退避——那些是 `provider-quota` 已有的要求，本 change 不碰。

## Capabilities

### New Capabilities

- `quota-read-endpoint`: daemon 上的只读额度端点（路径、参数、回环限制、返回形状、与 `ccl quota --json` 的同构关系），以及 `CCL_QUOTA_URL` 这个对外环境变量的交付契约（通道、缺席语义、ccl 的键赢纪律）。

### Modified Capabilities

- `provider-quota`: 两条要求——①"ccl reports the quota through a command that reads the local data file"：命令现在**优先**通过 daemon 端点读取本地数据（端点不可达时回落直读同一个文件），但"MUST NOT 向额度接口发请求"的纪律不变；②"ccl declares the provider it selected to the daemon"：这条声明现在**只接受回环来源**的请求。

## Impact

- `internal/quota/`：新增对外的 `Output` 类型与 `BuildOutput`（组装 + stale 判定）——从 cmd 层搬进来，包内多一处共享实现。
- `internal/collector/server.go`：新增 `/ccl/quota` 路由；给 `/ccl/activate` 加回环来源校验；`ServerOptions` 新增一个取额度的回调（与既有的 `OnActivate` 同构），HTTP 层不认额度业务。
- `cmd/cc-launcher/serve.go`：实现该回调（读 provider 配置 → 读数据文件 → `BuildOutput`）。
- `cmd/cc-launcher/quotacmd.go`：改用共享的 `BuildOutput`；新增"优先 daemon、回落本地文件"的读取路径；退出码 0/1/2 的判定不变。
- `cmd/cc-launcher/main.go`：`buildLaunchOptions` 里交付 `CCL_QUOTA_URL`。
- `internal/provider/provider.go`：新增常量 `EnvQuotaURL = "CCL_QUOTA_URL"`。
- `docs/provider-quota.md`、`docs/provider-name-env.md`：增补对外契约。
- 风险：daemon 监听 `:4318`（**所有网卡**，不是只回环），所以端点的回环限制是**必需**的——没有它，局域网内任意机器都能读到本机额度余额。回环限制的依据是：本机任何进程本来就能直读额度数据文件，因此回环端点不引入新的本机暴露面。
- 风险：给 `/ccl/activate` 加回环限制**不会打断 ccl 自己**——daemon 是 ccl fork 出来的子进程（或用户在本机手动起的），声明的调用方必然与 daemon 同机。
- 风险：`CCL_QUOTA_URL` 是新增的对外环境变量——消费方读到它就会依赖它。变量名与 URL 形态一旦发布即是对外契约（与 `CCL_PROVIDER` 同等对待：改名 = breaking change）。
