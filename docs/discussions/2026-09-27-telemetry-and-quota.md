# 讨论记录：用量监控 + 提供商余额

**日期**：2026-09-27
**会话**：OpenSpec explore 模式
**结论**：拆成两个独立 change

## 背景

ccl-launcher 之前只能"启动 Claude Code"，对**用量**和**余额**完全无感知。用户希望：
1. **本地用量监控**：能知道"我每个 provider 花了多少 token / 多少钱"
2. **提供商余额查询**：能知道"我账号还剩多少钱 / 配额"

## 核心发现

调研中确认了几件**改变整个方案**的事：

| 发现 | 来源 | 影响 |
|---|---|---|
| Claude Code v2.1.x 原生支持 OTel | `~/.claude/settings.json` 已有 `CLAUDE_CODE_ENABLE_TELEMETRY=1` | ccl 不必自己从 stdout 抓数据 |
| OTel 推送已包含 token / cost / session / API 请求 | [Claude Code Monitoring Docs](https://code.claude.com/docs/en/monitoring-usage) | 不需要价格表，Claude Code 自己算 cost |
| 标准 OTLP 端口是 gRPC `:4317` | 同上 | 用户零配置就能用 |
| `~/.claude/projects/<hash>/<session>.jsonl` 含 `usage.input_tokens` 等 | 本机实测：单 session 268万 input tokens 可读 | OTel 不可用时的 fallback |
| 5 家 provider 余额 API 各不相同 | 各自文档 | 实现工作量不均 |

## 用户决策（关键路径分歧点）

| 问题 | 选项 | 用户选 |
|---|---|---|
| 远程 + 本地都要吗？ | 两件 / 只远程 / 只本地 | **两件** |
| 本地记什么？ | 启动次数 / +时长 / token 计数 | **token 计数** |
| 余额 API 用 token 查询，隐私？ | 默认查 / provider 关 / 显式 flag | **默认查**（业内标准） |
| OTel 用法？ | 不加 / opt-in / always-on | **OTel 接收端**（用户提出"后台服务监听推送"） |
| 旧 jsonl 解析怎么办？ | 砍 / 兼容 / 双路 | **砍**（OTel 是更权威数据源） |
| daemon 怎么启？ | 手动 / 按需 / systemd | **按需**（每次 ccl 启动时拉起） |
| 存储？ | SQLite / JSONL / BoltDB | **SQLite** |
| quota check 还做吗？ | 砍 / OTel 代替 / 保留 | **保留**（独立 feature） |

## 关键设计澄清

**用量 ≠ 余额**：

```
用量 (usage)               余额 (balance/quota)
─────────────              ─────────────────────
"我已用多少"               "我还剩多少"
"deepseek 花了多少？"       "deepseek 还剩 ¥XX ?"
OTel push (本地)            provider API (远程)
SQLite 持久                  不持久
按需查询                     按需查询
```

两者不是替代，是互补。拆为两个 spec 避免相互污染。

## 拆为两个 change 的理由

| | change 1: usage-monitoring-via-otel | change 2: provider-quota-check |
|---|---|---|
| 立即可交付 | ✅ OTel 数据源已验证、本机已开 | ❌ 5 家 API 文档参差、aliyun 需额外确认 |
| 用户感知 | 启动就有数据可看 | 要等各家余额 API 都通 |
| 失败可降级 | daemon 启不起时退到 jsonl（可选）| 单家 API 挂了不影响其他 |
| 依赖 Claude Code | 必须开 OTEL_METRICS_EXPORTER | 不依赖 |
| 限制 | — | **目前只能验证 DeepSeek + MiniMax**（其它家 API 行为需要后续专门探索） |

**拆开的好处**：
- change 1 能马上 ship，OTel 路径完全闭环
- change 2 范围小（2 家而非 5 家），可以等更多 API 验证后再扩
- 两个 spec 互不耦合，可独立 review

## 用户明确点（必须记住的约束）

1. **拆成两个 change**
2. **change 1 先做，要"立马能感知到"**
3. **change 2 现在只做 DeepSeek + MiniMax**；glm / aliyun / anthropic 的余额 API 留待后面单独验证
4. **用量与余额必须分开维护**——不要在实现里把它们混进同一个 spec / capability

## 配套交付

| 文档 | 路径 |
|---|---|
| 本讨论文档 | `docs/discussions/2026-09-27-telemetry-and-quota.md` |
| Change 1 proposal | `openspec/changes/usage-monitoring-via-otel/` |
| Change 2 proposal | `openspec/changes/provider-quota-check/` |
| 参考链接 | https://code.claude.com/docs/en/monitoring-usage |

## 下一步

1. 退出 explore 模式
2. `openspec new change usage-monitoring-via-otel`
3. `openspec new change provider-quota-check`
4. 各自填 proposal / design / specs / tasks
5. 各自 `/opsx:apply`

---

## 补充：项目（cwd）维度

**讨论时间**：2026-09-27（追加）
**触发点**：用户问"`ccl stats` 是不是要分项目"

### 结论

`ccl stats` **必须支持项目维度**。原因：

- 用户通常在多个项目下用 ccl-launcher（个人仓 / 工作仓 / 学习仓 等）
- Claude Code 每个 session 都在自己的 `cwd` 下启动，jsonl 里 `cwd` 是事实数据
- 没有项目维度，stats 输出会变成"一坨混在一起的 session"，跨项目用户的可读性极差

### 设计

```
cwd (SQLite 存的绝对路径)        filepath.Base(cwd)        PROJECT 显示
/home/zxwang/workspace/foo       foo                      foo
/home/zxwang/study/bar           bar                      bar
```

- **存储**：`sessions.cwd TEXT`（绝对路径），已存在；加索引 `sessions(cwd, started_at)`
- **显示**：`PROJECT` 列 = `filepath.Base(cwd)`
- **过滤**：`--project <substring>` 在 cwd 上 substring 匹配（支持全路径或 basename 子串）
- **聚合**：`--by-project` 按 PROJECT 列聚合；与 `--by-provider` 可叠加

### 用户场景

| 问题 | 命令 |
|---|---|
| 这个月每个项目各花了多少？ | `ccl stats --since 30d --by-project` |
| `cc-launcher` 项目用过哪些 provider？ | `ccl stats --project cc-launcher --by-provider` |
| 当前项目近 7 天 token 用量 | `ccl stats --since 7d --project $(basename $(pwd))` |

### 不做什么

- **不自动按当前 cwd 过滤**：用户可能想看历史所有项目；隐式过滤会让人困惑（"我昨天明明用过，今天 stats 没数据？"）
- **不做项目重命名 / 别名**：basename 已可读；如果用户两个项目 basename 撞名，用全路径 substring 区分
- **不做 project 树状结构**（git worktree 等）：复杂场景，单仓多 worktree 用户极少

### 对 change 1 的影响

已在 spec 里加：
- `telemetry-storage`: 索引 `sessions(cwd, started_at)` + 新场景"cwd is the project's working directory at session start"
- `stats-query`: 表加 `PROJECT` 列；新场景 `--by-project` / `--project <path>` / 叠加；HTTP `/stats?project=cc-launcher`