## Context

### 关键洞察：serve 已经在收 OTel，"谁在活动"是现成的信息

```
ccl（TUI 选 provider）→ 启动 Claude Code
  └─ launchctx 把 provider 名写进子进程环境的 OTEL_RESOURCE_ATTRIBUTES
        └─ launchctx.KeyProvider = "ccl.provider"
              └─ Claude Code 的 OTel SDK 把它当 resource attribute 随 metrics 推给 :4318
                    └─ collector/parser.go:165  meta.provider = resAttrs[launchctx.KeyProvider]
                          └─ **collector 每收到一条 metrics 就知道是哪个 provider**
```

所以"这个 provider 最近有没有人在用"**不需要新状态**——collector 手里就有。

### 实测：Claude Code 每 60 秒推一次 metrics

本机 `telemetry.db`（2026-11-06）：

```
provider                  相邻两条 metrics 的间隔（秒）
  minimax-m3-stliu14      [240001, 60001, 60001, 60000, 60001, 180001, 60000, 60000]
```

- **60 秒一次**（稳定）
- 240001 / 180001 是**换 provider 那段时间的真空期**（那个 provider 确实没人用）
  → OTel 活跃度**天然**就是"谁在干活"的信号

### 数据的消费者只有状态栏

```
statusline.js → ccl quota --provider "$CCL_PROVIDER" --json → 读 ~/.cc-launcher/quota.json
```

状态栏显示的永远是**当前会话那个 provider** 的额度 → 其他 provider 的额度数据
**当前没有任何消费者**。

## Decisions

### 决策 1：活动信号 = OTel（不是"启动过会话"）

初版想过用 `state.json` 记"最近启动过会话的 provider"，被否掉了：

| | state.json 记启动 | **OTel 活跃（采纳）** |
|---|---|---|
| 状态来源 | 要新增字段 + 写入点 | **collector 已经在解析时拿到 provider** ✓ |
| "没人用"的判定 | 启动过 ≠ 还在用（用户可能几小时没发消息） | **没消息 = 没活动 = 没人看** ✓（正是需求） |
| 长会话 | 标记不衰减 → 一直查 | 用户还在发消息 → 一直活跃 ✓；离开 → 自动停 ✓ |
| 多会话 | 要自己维护集合 | **自动覆盖**（每个会话各自推自己的 provider）✓ |

用户原话："长时间没有消息，用量显示也没人看。有消息的就按周期其他刷新" → OTel 活动
**就是这个语义本身**。

### 决策 2：窗口 = 5 分钟

- OTel 周期实测 60 秒 → 窗口必须 **> 60 秒**，否则两次推送之间会被误判成不活跃
- 5 分钟 = 4 个周期 → 容忍 3 次连续丢失（网络抖动、Claude Code 暂停导出）
- 更长（如 30 分钟）→ 用户离开半小时后还在查 → 回到"全查"的毛病
- 更短（如 2 分钟）→ 一次推送丢失就误判 → 额度无故停更

### 决策 3：活跃度放内存（collector 进程内）

- 不写 SQLite（那是"用量历史"，不是"活跃信号"；写进去要处理高频写 + 清理）
- 不写 state.json（那是 ccl 的启动状态，与"运行时活动"不同层）
- **serve 重启后清空** → 要等下一次 OTel（≤60 秒）→ 期间不查任何 provider
  （保守，无害：额度数据文件里的旧条目还在，状态栏显示旧值 + stale）

### 决策 4：`--refresh` 不受过滤

`ccl quota --provider X --refresh` 是用户**显式点名** → 无论 X 最近有没有活动都照查。
理由：`--refresh` 是 serve 没跑时的**逃生舱**，也是"我刚充值了看看额度"的手段 → 被
"最近有活动"过滤掉就失去意义。

### 决策 5：名单为空 → 不查任何 provider

刚装好 / 刚重启 serve / 用户全在别处 → 名单为空 → 一个请求都不发。

代价：状态栏暂时没额度数据（但也没有活跃会话 → 没人看）。**绝不能因此阻塞
Claude Code 启动**。

## Risks / Trade-offs

- **会话刚启动的前 60 秒没额度数据**：要等第一条 OTel（≤60s）+ 一个 ticker 周期
  → 状态栏回落 `rate_limits`（显示 `--`）→ 不影响启动
- **用户离开 5 分钟 → 数据停更 → stale**：可见（状态栏标 stale），且
  `ccl quota --refresh` 可立即修复
- **collector 与额度刷新不同层**：collector 负责"谁在活动"（它最清楚），serve 负责
  "查额度" → 通过 `Server.Activity()` 暴露，不产生包依赖倒置
