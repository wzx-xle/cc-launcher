# Design: provider env 走 --settings + 可复用的常驻载体

## Context

provider 的 env 现在走 **setenv（进程环境）**，而进程环境是 Claude Code 优先级**最低**的通道 → 用户在 `~/.claude/settings.json` 里手写过任何 `ANTHROPIC_*`，**ccl 里选的 provider 就完全不生效**。这与"配独立提供商"的目的直接冲突。

同时 Unix 上 `--settings <JSON>` 是命令行参数，`/proc/<pid>/cmdline` 权限 0444 → provider 的 token 会明文进 argv。

## 调研结论（全部真机实测，2026-10-29）

### 一、Claude Code 的 `--settings` env 块 = 写进 process.env → 所有键都生效

| 键 | 放在 `--settings` 里 | 证据 |
|---|---|---|
| `ANTHROPIC_BASE_URL` | ✅ | 请求打到假服务器 `POST /v1/messages` |
| `ANTHROPIC_MODEL` | ✅ | claude 报 `probe-model-from-settings` |
| `ANTHROPIC_AUTH_TOKEN` | ✅ | 请求头 `Bearer sk-token-from-settings`（非进程环境那份） |
| `HTTP_PROXY` / `HTTPS_PROXY` | ✅ | 代理形式请求 `POST http://api.fake.invalid/v1/messages` |

> 测法坑：拿 `api.anthropic.com` 当 base url 测代理会**假阴性**（Claude Code 对该域名特殊处理、直连出去了）。必须用**只有走代理才可达**的域名（如 `.invalid`）才能测出代理是否生效。

→ provider env 走 `--settings` 无技术障碍，**代理变量一起走**。

### 二、载体的三条约束（实测）

| 约束 | 实测结果 | 结论 |
|---|---|---|
| `/tmp` + 固定名 | 攻击者预置软链 → ccl 写进去的 token 被攻击者读到 | ❌ 必须私有目录（`O_NOFOLLOW` 也能挡，但私有目录更干净） |
| 固定名 + 并发 | A 写 → B 写 → **A 读到 B 的 provider** | ❌ 用错账号 = 钱花错地方 |
| 内容哈希名 | 同内容→同路径（复用）；不同内容→不同文件（各读各的） | ✅ 两个问题一起解决 |

### 三、内容哈希命名的额外红利：写入幂等

哈希由内容决定 → **同名必同内容** → 目标文件已存在且内容一致时**直接复用、不重写**。这同时解决了 Windows 上"文件被 claude 占用导致覆盖失败"的问题（Windows 也就不需要删除、不需要 pid 唯一名 → **两平台同一套策略**）。

## Decisions

### D1: provider env 合并进 `--settings` 的 env 块

```
交付的 env = provider 配置的 env 块
           + OTel key（接管/补默认值）
           + OTEL_RESOURCE_ATTRIBUTES（launch 上下文）
```

- **ccl 配置里有的键 → ccl 赢**（覆盖 settings.json 的同名值）
- **ccl 没配的键 → 不碰**（settings.json 的手写值继续生效）

**进程环境里也放一份**（低优先级）：daemon 的 forwarder 用 `http.Client` 默认 Transport → `ProxyFromEnvironment` → 读进程环境的 `HTTP_PROXY`/`HTTPS_PROXY`；daemon 是 fork 出来的（继承 ccl 的环境）→ provider 配了代理时，**转发到远程 collector 才走得通**。Claude Code 侧仍是 `--settings` 赢。

（这条是实现期发现的回归：只走 `--settings` 的话，daemon 转发会丢掉用户的代理设置。）

### D2: 载体 = `~/.cc-launcher/cache/` 下的内容哈希文件

| 项 | 值 |
|---|---|
| 目录 | `~/.cc-launcher/cache/`，0700 |
| 文件名 | `settings-<sha256(内容)[:16]>.json` |
| 文件权限 | 0600 |
| 写入 | 已存在且内容一致 → 复用；否则原子写（`.tmp` + rename） |
| 删除 | **不删**；启动时清 mtime > **1 年**的 `settings-*.json` |

哈希取**内容**（完整 JSON）而非 provider 名：内容相同 → 同一文件（复用），内容不同 → 不同文件（并发不串）。

Unix 的 launcher **一行不改**（`syscall.Exec` 替换进程 → 没有"退出"那一刻 → 不需要 Cleanup → 现有 `opts.Cleanup` 字段在 Unix 上继续是 no-op）。

Windows：同一套策略 → `settings_windows.go` 的 pid 唯一名 + 删除逻辑全部删掉。

### D3: 清理策略

启动时（runTUI）扫描 `~/.cc-launcher/cache/`，删掉 mtime 超过 **1 年**的 `settings-*.json`。

- 为什么 1 年：内容哈希命名 → 文件数 = 用过的不同（provider × cwd × 端点）组合数，增长很慢；1 年足够宽松，几乎不会删到还在用的。
- 只删 `settings-*.json` 前缀的（该目录专用，但保持保守）。
- 清理失败只警告，不阻断启动。

### D4: 覆盖语义与 CLAUDE.md

- CLAUDE.md 约束 6 改写：「provider 配置通过 setenv 注入到子进程环境」→「provider 配置通过 `--settings` 参数交付，ccl 配置的优先级高于用户在 settings.json 里的同名值」。
- **不提示**用户 settings.json 里有会被覆盖的 `ANTHROPIC_*` —— 这正是本程序的目的。

## 各通道的最终形态

| 通道 | 承载什么 | 优先级 |
|---|---|---|
| `--settings` 的 env 块 | provider env + OTel key + launch 上下文 | **最高**（ccl 说了算） |
| `~/.claude/settings.json` 的 env 块 | 用户自己的一切（ccl 没配的键） | 中 |
| 进程环境 | 父进程环境（PATH/HOME…）+ `OTEL_RESOURCE_ATTRIBUTES` 冗余 | 最低 |

## Risks

| 风险 | 处置 |
|---|---|
| 内容哈希碰撞（16 hex = 64 bit） | 概率 ~1e-19；且写入前校验内容一致，不一致则重写 |
| `--settings` 的 env 块不再写进 process.env（未来版本变化） | 当前版本实测生效；provider env 的核心是 Claude Code 读 ANTHROPIC_*，这依赖 Claude Code 的实现，spec 里写明该依赖 |
| cache 目录无限增长 | mtime > 1 年清理；文件数上界 = 用过的配置组合数 |
| 旧 provider env 仍留在用户 shell 的进程环境里 | 优先级最低 → `--settings` 赢 ✓ |
