## 1. 常量与注入

- [x] 1.1 `internal/provider/provider.go`：新增常量 `EnvProviderName = "CCL_PROVIDER"`，注释写明它是给仓库外消费方用的对外契约（值 = provider 的 `Name`），改名字按 breaking change 处理
- [x] 1.2 `cmd/cc-launcher/main.go` 的 `buildLaunchOptions`：把 `CCL_PROVIDER` 加进交付的 env 块。注入点放在 `deliver` 汇流**之后**——这样 provider 自己的 env 块里若写了同名键，ccl 的值自然覆盖它（沿用"ccl 自己的键赢"的纪律）
- [x] 1.3 同处：再 `os.Setenv` 一份冗余副本（与 `ANTHROPIC_MODEL` 的处理并列，注释写明这是第二通道、第一通道是 `--settings`）
- [x] 1.4 防御：`providerName` 为空时**不注入**该键（不交付空值——spec 要求"缺席"与"空串"可区分）

## 2. 单元测试

- [x] 2.1 交付的 env 块含 `CCL_PROVIDER`，值 == provider 名（provider 名取自配置文件名去 `.json`）
- [x] 2.2 provider 自己的 env 块含 `CCL_PROVIDER=other` 时，交付的值是 ccl 的 provider 名
- [x] 2.3 进程环境被设置（用 `t.Setenv` 隔离，避免污染测试进程）
- [x] 2.4 `providerName` 为空 → 交付的 env 块里**没有** `CCL_PROVIDER` 这个键（不是空串）
- [x] 2.5 确定性：同一 provider 连续构建两次，产出的 settings JSON 逐字节相同

## 3. 文档（对外契约）

- [x] 3.1 新增 `docs/provider-name-env.md`：记录 `CCL_PROVIDER` 的完整契约——变量名、值的语义（provider 的 `Name`，不是 note、不是 endpoint）、两条交付通道、缺席语义（= 不是 ccl 起的会话）、消费方渲染前的 sanitize 责任、以及"改这个变量名是 breaking change"
- [x] 3.2 `docs/claude-code-settings-merge-semantics.md` 增补一节：statusLine 子进程环境的实测结论——settings `env` 块可见 / 父进程环境可见 / **`OTEL_` 前缀的变量被剥离**（前缀规则，`OTELX_` 不受影响；与 `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` 无关）；以及 scrub 的影响（凭证类变量被剥离，`CCL_PROVIDER` 不受影响）。这是本 change 的地基，也说明了"为什么不能复用 `OTEL_RESOURCE_ATTRIBUTES` 里的 `ccl.provider`"
- [x] 3.3 `README.md` 增补：provider 名出现在环境里（消费方怎么读、拿到什么、缺席意味着什么）
- [x] 3.4 `CLAUDE.md` 约束 6 增补一句：`CCL_PROVIDER` 是 ccl 注入给消费方的只读变量，与 provider 生效链路无关

## 4. 真机验证

- [x] 4.1 真 claude + pty 驱动 + 探针 statusLine 脚本（脚本把 `process.env` 写盘）：确认 statusLine 子进程**读得到** `CCL_PROVIDER`，值 == 选中的 provider 名
      - 做法：两段拼一条链路——A 段真 ccl（pty 驱动 TUI + 假 claude 抓 argv）产出交付文件，B 段真 claude 消费**同一个文件** + 探针 statusLine 读 env
      - 结果：交付文件 env 块含 `CCL_PROVIDER=test-prov`；探针读到 `CCL_PROVIDER=[test-prov]` ✓
- [x] 4.2 对照组：交付里没有 env 块时 → 探针读不到 `CCL_PROVIDER`
      - 做法：给 claude 一个**只含 statusLine、不含 env 块**的 settings（"不经 ccl 交付"的场景，直连 claude 是它的超集），并断言启动 claude 的父进程环境里本来就没有 `CCL_PROVIDER`
      - 结果：探针读到 `CCL_PROVIDER=[<unset>]` ✓（父进程环境也无该变量）
- [x] 4.3 沙箱对照组：`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` 与 `=0` 各跑一次，确认**两种配置下探针都读得到** `CCL_PROVIDER`（变量名不能带凭证特征的直接证据；同时确认 `ANTHROPIC_AUTH_TOKEN` 在 scrub 开启时确实被剥离）
      - 结果：`SCRUB=0` → `CCL_PROVIDER=[test-prov]`、token 在；`SCRUB=1` → `CCL_PROVIDER=[test-prov]`、token 被剥离 ✓
- [x] 4.4 回归：`go test ./...` + `go test -tags smoke ./...`——**16 个包全绿**；`internal/collector` 的 `TestServer_ScenarioA_PerSignalMetrics_EndToEnd` 失败是**既有问题**（在 pristine HEAD 上同样失败：测试把 `127.0.0.1` 与通配监听地址 `[::]:port` 手工拼成 `http://127.0.0.1[::]:port`），与本改动无关，未在本 change 内修（修法：`"http://"+addr+"/v1/metrics"`）
      - 状态栏上出现 provider 名的一环由用户侧确认（脚本在 `~/.claude/statusline.js`，不在本仓库）
- [x] 4.5 复测：改动 `docs/claude-code-settings-merge-semantics.md` 里"Claude Code 一升级要重验"的提醒——本 change 依赖同一批实测行为，升级后需重跑 4.1

## 5. 发布

- [x] 5.1 `CHANGELOG.md` 的 `[Unreleased]` 下加 `0.19.0` 段（按既有分类格式：Features / Tests / OpenSpec）
- [x] 5.2 `openspec archive provider-name-in-statusline`
- [x] 5.3 按 CLAUDE.md 的 Release Tag 流程：`git commit` CHANGELOG → `git tag -a v0.19.0` → **一次推送** `git push origin main` + 单独推 tag（`bin/bump-version.sh` 建的是 lightweight tag，`--follow-tags` 不会带上，必须单独 `git push origin v0.19.0`）
