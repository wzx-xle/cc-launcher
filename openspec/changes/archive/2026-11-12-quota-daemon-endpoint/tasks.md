## 1. 额度输出组装搬到 internal/quota（共享实现）

- [x] 1.1 `internal/quota/output.go`（新文件）：新增 `Output` 类型 —— 字段与 json tag 与现有 `cmd/cc-launcher/quotacmd.go` 的 `quotaOutput` **逐字一致**（`provider` / `source` / `stale` / `fetched_at` / `dimensions`）
- [x] 1.2 `internal/quota/output.go`：新增 `BuildOutput(ref ProviderRef, entry Entry, now time.Time) (Output, bool)` —— 组装 + stale 判定；`ok=false` 表示"没有记录"
      —— stale 判定口径必须与现有实现**逐字一致**：fetched_at 与 refreshInterval 比较；refreshInterval 读不到 → 兜底 60 秒；额外给 `quota.StaleTolerance`（1 秒）容忍（fetched_at 只有秒精度）
- [x] 1.3 `internal/quota/output.go`：`Dimensions` 为空时输出空数组 `[]`，**不得**输出 `null`（现有实现的行为）
- [x] 1.4 单测（`internal/quota/output_test.go`）：记录比 refreshInterval 老 → stale；在 interval + 容忍量内 → 不 stale；refreshInterval 读不到 → 兜底 60 秒；dimensions 为空 → `[]` 而非 null
- [x] 1.5 `cmd/cc-launcher/quotacmd.go`：删掉 cmd 层的 `quotaOutput` 类型，改用 `quota.Output`；stale 判定改调 `quota.BuildOutput` → **输出必须逐字节不变**
- [x] 1.6 回归测试：锁住 `quota.Output` 的 json tag 与字段名（改名/改 tag = 破坏对外契约 → 测试必须红）

## 2. daemon 的只读额度端点

- [x] 2.1 `internal/collector/server.go`：`ServerOptions` 新增 `OnQuota func(provider string) (any, int)` —— 第二个值是 HTTP 状态码（200 有数据 / 404 无记录或 provider 未配置 / 422 无额度接口）
      —— 返回 `any` 而非具体类型：采集层不 import `internal/quota`（保持与 `Mount` 一样的解耦纪律）
- [x] 2.2 `internal/collector/server.go`：`OnQuota` 为 nil 时**不注册** `/ccl/quota` 路径（与 `OnActivate` 一致：不暴露一个"什么都不做"的端点）
- [x] 2.3 `internal/collector/server.go`：新增 handler —— ①取 `provider` 查询参数（缺失或空 → 400）；②**回环校验**（见第 3 组的共用 helper）；③调 `OnQuota` → 按返回的状态码与值应答
- [x] 2.4 `cmd/cc-launcher/serve.go`：实现 `OnQuota` —— `quota.LoadProviderRef(providersDir, name)`：provider 未配置 → 404；无额度接口 → 422；`quota.NewStore(baseDir).Load()` 取条目 → 无条目 → 404；有条目 → `quota.BuildOutput` → 200
      —— **必须用 `quota.Store.Load()`**（含 v0.22.1 加的 5ms 重试），**不能裸 `os.ReadFile`**：Windows 上文件正被替换时裸读会拿到 ErrNotExist
- [x] 2.5 `cmd/cc-launcher/serve.go`：`OnQuota` 的 provider 名要过白名单校验（复用 `providerExists` 的路径穿越防护）——名字来自 HTTP 查询串
- [x] 2.6 单测（`cmd/cc-launcher/quota_test.go` 侧）：daemon 可达（httptest 注入假 daemon）→ 命令走端点；daemon 不可达 → 回落本地文件且**输出与走端点时相同**

## 3. 收紧 daemon 的内部交换端点（只限 /ccl/*）

- [x] 3.1 `internal/collector/server.go`：抽一个共用的回环判定 helper —— `net.SplitHostPort(r.RemoteAddr)` → `net.ParseIP` → `ip.IsLoopback()`；非回环 → 403
      —— **只看 `RemoteAddr`，不看任何请求头**（daemon 是裸 HTTP server，没有可信转发链；看 `X-Forwarded-For` 等于把判定交给请求方）
- [x] 3.2 `internal/collector/server.go`：`POST /ccl/activate` handler 加回环校验 —— 非回环来源 → 403，且**不得**有任何副作用（不校验 provider 名、不标记活跃、不触发刷新）
      —— 零风险论证：daemon 是 ccl fork 出来的子进程（或用户在本机手动 `ccl serve` 起的）→ 声明的调用方必然与 daemon 同机 → 永远是回环来源
- [x] 3.3 `internal/collector/server.go`：`/v1/metrics`、`/v1/logs`、`/v1/traces` 与 `/ui`、`/api/*` **不加**回环校验
      —— 理由：OTel 协议端点的合法调用方可能是 WSL2/容器里的 Claude Code（WSL2 连 Windows 的 `localhost:4318` 走 NAT，Windows 侧看到的是虚拟网段地址，**不是回环**）→ 一收紧遥测就断；看板本就要支持从别的机器打开（已有 token 保护）
- [x] 3.4 `internal/collector/server.go`：**daemon 的监听地址保持 `:4318` 不变** —— 绑到 `127.0.0.1` 会同时打断上面两类，收紧只落在端点级
- [x] 3.5 单测（`internal/collector/server_test.go`）：`/ccl/activate` 非回环来源 → 403 且 provider 未被标记活跃（断言 `activity` 里没有它）；`/ccl/quota` 非回环来源 → 403 且不返回任何额度；回环来源 → 200/204 正常；请求带 `X-Forwarded-For: <非回环>` 但来源是回环 → **仍然放行**（证明判定不读请求头）；`/v1/metrics` 非回环来源 → **仍然服务**（证明收紧没蔓延）
- [x] 3.6 端到端验证：`ccl` 启动 Claude Code 时声明 provider **仍然生效**（serve.log 里能看到 `provider "xxx" 激活，立即刷新一轮`）—— 这是"收紧没打断自己"的唯一硬证据

## 4. ccl quota 命令优先走 daemon、回落本地文件

- [x] 4.1 `cmd/cc-launcher/quotacmd.go`：新增 daemon 读取路径 —— HTTP GET `http://127.0.0.1:4318/ccl/quota?provider=<名字>`，**超时 500ms**（回环请求是毫秒级，500ms 足够宽松又不至于在 daemon 假死时卡住）
- [x] 4.2 `cmd/cc-launcher/quotacmd.go`：状态码映射 —— 200 → exit 0 并打印文档；404 → exit 1；422 → exit 2；**端点不可达 / 超时 / 非 2xx → 回落直读本地文件**（走原有判定路径）
- [x] 4.3 `cmd/cc-launcher/quotacmd.go`：`--refresh` 路径**不变** —— 它自己联网查 → 写文件 → 直接 `quota.BuildOutput` 组装（刚查到的数据，不绕 daemon）
- [x] 4.4 单测（`cmd/cc-launcher/quota_test.go`）：端点返回 200 → 打印端点的文档；端点返回 404 → exit 1；端点返回 422 → exit 2；端点不可达 → 回落本地文件且**输出与走端点时相同**；`--refresh` 仍走自己的路径
- [x] 4.5 `docs/provider-quota.md`：把"默认只读本地数据文件，一次网络都不发"改成"只读**本机**数据（daemon 可达时经 daemon 的回环端点读，否则直读数据文件）；**不向任何额度接口发请求**"——回环 HTTP ≠ 向外网发请求，原纪律的意图（不打扰厂商接口）不变

## 5. CCL_QUOTA_URL 的交付

- [x] 5.1 `internal/provider/provider.go`：新增常量 `EnvQuotaURL = "CCL_QUOTA_URL"`（与 `EnvProviderName` 同族——都是 ccl 注入给消费方的只读变量）
- [x] 5.2 `cmd/cc-launcher/main.go`：`buildLaunchOptions` 里把 `CCL_QUOTA_URL` 加进交付的 env 块（主通道）+ `os.Setenv` 一份冗余副本 —— **值 = `http://127.0.0.1:4318/ccl/quota`**（daemon 端口是常量 `daemonAddr`）
- [x] 5.3 `cmd/cc-launcher/main.go`：**无条件交付**（不因 daemon 没起来就不给）——避免把 `buildLaunchOptions` 与 `ensureDaemonRunning` 的先后顺序耦合进来；消费方拿到 URL 连不上就自己回落
- [x] 5.4 `cmd/cc-launcher/main.go`：provider 配置的 `env` 块里若写了同名键 → **ccl 的值覆盖**（沿用"ccl 自己的键赢"的既有纪律）
- [x] 5.5 单测（`cmd/cc-launcher/settingsdelivery_test.go` 或同类）：交付的 settings JSON 的 env 块含 `CCL_QUOTA_URL`；provider 配置写了同名键时被 ccl 的值覆盖；用户 settings.json 的同名键也被压过

## 6. 文档

- [x] 6.1 `docs/provider-quota.md`：增补**端点契约** —— 路径与参数、回环限制、状态码表（200/400/403/404/422）、返回字段（与 `ccl quota --json` 同构）
- [x] 6.2 `docs/provider-quota.md`：增补 **`CCL_QUOTA_URL` 的契约** —— 名字、值、两条交付通道、"ccl 的键赢"、缺席 ≠ 空串、改名 = breaking change
- [x] 6.3 `docs/provider-quota.md`：给出**状态栏脚本的推荐写法**（可直接粘贴）—— HTTP 优先（Node 内置 http，不起进程）+ 命令兜底（`ccl quota --json`）；并说明**输出同构 → 消费方的解析逻辑一行都不用改**
- [x] 6.4 `docs/provider-quota.md`：说明**端点的来源限制**与**为什么 `/v1/*` 与看板不限**（给将来的维护者留判据，避免有人"顺手把整个 daemon 都锁了"）
- [x] 6.5 `docs/provider-name-env.md`：加一行交叉引用（`CCL_QUOTA_URL` 是同族的对外变量，契约在 `docs/provider-quota.md`）
- [x] 6.6 `README.md`：额度一节提一句"状态栏可以零进程读额度"（指向 `docs/provider-quota.md`）

## 7. 验证

- [x] 7.1 `go build ./... && GOOS=windows go build ./... && go vet ./... && go test ./... && go test -tags smoke ./...` 全绿；`gofmt -l .` 为空
- [x] 7.2 `openspec validate --all` 通过
- [x] 7.3 **变异验证**：把回环判定改成恒真 → 3.5 的非回环测试必须红（activate 与 quota 两处都要红）；把 `BuildOutput` 的 stale 阈值改掉 → 1.4 的 stale 测试必须红
- [x] 7.4 Linux 真机：起 daemon → `curl "http://127.0.0.1:4318/ccl/quota?provider=<name>"` 的返回与 `ccl quota --json` **逐字段一致**；`/ccl/activate` 从非回环来源被拒；从非回环地址访问 `/v1/metrics` **仍被服务**（证明收紧没蔓延）
- [x] 7.5 Linux 真机：真 ccl 启动 → serve.log 里有 `provider "xxx" 激活，立即刷新一轮`（证明收紧没打断 ccl 自己的声明）；检查交付的 settings JSON 的 env 块含 `CCL_QUOTA_URL`；真 claude 消费该文件 → statusLine 子进程读得到该变量
- [x] 7.6 Windows 复测：状态栏改为 HTTP 读取后，额度**稳定显示**（不再随系统繁忙回落）；`ccl quota --json` 的耗时分布仍记录在案（说明为什么需要零进程通道）
      —— **实测（2026-11-13，Windows 11 24H2 / node v22.23.2 / scoop 装在 D:\Scoop，工具 = bin/statusline-probe.js，n=30）**：
      - 通道耗时：HTTP 回环端点 p50 **6.1ms** / p95 7.7ms / max 12.9ms（0/30 超时）；
        命令 `ccl quota --json` p50 **263ms** / p95 272ms / max 272ms（**30/30 全部超时**）
        → 命令通道**常态越过 250ms 预算**（2026-11-12 早先抽样是 p50 166~190ms / max 252ms，
        只看出"忙的时候会超时"；30 次采样才看清是常态失败）
      - 状态栏实测（喂带哨兵 rate_limits 的 stdin，判据 = 额度段是否等于不可能撞上的
        哨兵 4321%）：**空闲 30/30、繁忙（8 个 CPU hog）30/30 都读到 ccl 的额度，0 次回落**
        → 额度**稳定显示** ✓
      - **遗留**：状态栏**整体** p50 = 286ms（空闲）/ 314ms（繁忙），**超过 250ms 预算**——
        但额度通道只占 6ms，剩下的花在 node 启动与 `git branch --show-current` 子进程上。
        额度数据是对的（30/30 正确），但 Claude Code 仍可能因超时 abort 这次调用 →
        **下一个瓶颈是状态栏脚本自身，不是 ccl**（见 docs/provider-quota.md 的诊断脚本一节）
      - **复测后的收尾（2026-11-13 第二轮）**：把状态栏脚本的 `git branch --show-current`
        换成**零进程读 `.git/HEAD`**（`bin/patch-statusline-git.js`，幂等 + 备份 + 语法自检；
        普通仓 / worktree / detached HEAD / 非 git 目录四个场景与 git 命令输出**逐一致**），
        并把 probe 的状态栏测量改成**异步 spawn**（spawnSync 的同步包装在 Windows 上要
        ~117ms，是测量工具自身的开销）→ 状态栏整体 **307.6ms → 140.7ms（空闲，进 250ms
        预算，余量 109ms）**；繁忙（8 个 CPU hog）258.4ms，只超 8.4ms
        - 最终构成：**node 启动 128.5ms + 额度通道 7.5ms + git 零进程 0.24ms**（≈140.7ms）
        - 剩下的 128.5ms 是 **node 启动本身**（Windows 上起任何未签名 exe 都要付 Defender
          深扫）→ **ccl 这边已无优化空间**（额度通道只剩 7.5ms）；要再进一步只能换掉
          node 写状态栏（超出本 change 范围）
