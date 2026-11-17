## Context

**服务**：codex2api v3.1.6（Go + Gin + React/Vite），自建，把公司的 ChatGPT Codex 账号池转成 OpenAI/Anthropic 兼容 API。本机 provider `gpt-6-bqshao3` 指向 `http://49.51.34.139:8317`。

**实测到的公开接口**（`Authorization: Bearer <sk-...>` 鉴权）：

| 端点 | 状态 | 说明 |
|---|---|---|
| `GET /api/key-usage/summary`（别名 `/me`） | **200** | key 级用量 + 自助统计报告 |
| `GET /api/key-usage/accounts` | 404「账号用量查询未启用」 | **上游 ChatGPT 账号**的 5h/7d 限额百分比，需要管理员在后台开权限 |
| `GET /api/image-studio/quota` | 404 | 生图门户额度，与本 provider 无关 |

`/api/key-usage/summary` 的响应（节选，实测 2026-11-17）：

```json
{
  "key": {
    "name": "王志翔", "key": "sk-8****...****54f9",
    "quota_limit": 0, "quota_used": 0.8024772, "total_used": 0.8024772,
    "reset_count": 0, "limits": {"allow_live": true, "upstream_channel": "codex"},
    "status": "active", "created_at": "2026-11-17T15:38:11+08:00"
  },
  "range": {"name": "30d", "start": "...", "end": "..."},
  "usage": { "summary": {...}, "windows": {"today": {...}, "last_5h": {...}, "last_7d": {...}, "last_30d": {...}}, "models": [...] },
  "model_request_usage": []
}
```

**上游源码口径**（`database/postgres.go`，v3.1.6）：

```sql
-- 每次请求按 user_billed 累加，两个字段同步涨
UPDATE api_keys SET quota_used = COALESCE(quota_used,0) + $1,
                    total_used  = COALESCE(total_used,0) + $1 WHERE id = $2

-- ResetAPIKeyQuota：只清 quota_used，total_used 不动
SET quota_used = 0, reset_count = reset_count + 1, last_reset_at = $1
```

即：`quota_used` = **自上次手动重置以来的累计花费**（"配额周期"是人为的，只有管理员点重置才换周期）；`total_used` = **历史总花费**（永不清零）。两者是**不同口径**。

**ccl 现有约束**：
- 额度模型只有三种 kind：`window`（有周期 + 有分母）、`quota`（无周期 + 有分母）、`balance`（无分母，只表示剩余量）。`Used` 与 `Remaining` 是**两个独立字段**，方向相反不许合并。
- 厂商差异全部关在 adapter 里；adapter 不许改 `Dimension` 的语义。
- 额度查询失败**必须降级**：保留旧条目 → 自动 stale → 绝不影响 Claude Code 启动。

## Goals / Non-Goals

**Goals:**

- 让 codex2api 类型的 provider 能查到额度，状态栏能看到"这个 key 花了多少 / 额度用了多少百分比"。
- 厂商改字段 / 关开关 / 限流时**降级而不是崩**。
- 不动 ccl 的对外契约（`dimensions` 结构、`source` 取值语义、`Dimension` 的 kind 集合）。

**Non-Goals:**

- **不映射 summary 里的 5h/7d 流量窗口**（见 D1）。
- **不接 `/api/key-usage/accounts`**（上游账号级 5h/7d 限额，见 D4）。
- **不接 admin API**（`/api/admin/*` 需要 Admin Secret；把管理级凭证放进 provider 的 env 块 → 会随 `--settings` 明文交付给 Claude Code，代价与收益不成比例）。
- 不改 provider 配置的既有键，不做迁移。

## Decisions

### D1: 只出一个维度（key 级金额预算），不映射流量窗口

summary 的 `usage.windows.last_5h` / `last_7d` 是该 key 最近 5h/7d **花掉的钱**（从 usage_logs 按请求时间戳切出来的窗口），**没有分母**——key 本身没有金额上限，硬上限在上游 ChatGPT 账号那边。

ccl 的三个 kind 都装不下它：

| 映射方式 | 为什么否决 |
|---|---|
| `window` + `Limit=nil` | `window` 的定义是"有周期 + **有分母**"；无分母时 `used_percentage` 恒为 null，状态栏渲染成 `--`，等于白给一个维度 |
| `balance` + `remaining=窗口内花费` | `balance` 的语义是**剩余量**，方向相反。CLAUDE.md 明写"余额是剩余、额度是已用，方向相反，合并就要靠约定猜方向——约定就是 bug 的来源" |
| `quota` + 人造分母 | 编一个上限就是**说谎**（CLAUDE.md 对"只有月额度的厂商塞进 five_hour/seven_day"也是这个理由） |

代价：状态栏看不到"最近 5h 花了多少"，只有 key 的总额/周期额。要看烧钱速度得走 D4 的 accounts 端点（需管理员开权限）。

### D2: 维度口径 = `quota_used` / `quota_limit`；`total_used` 只进 `Raw`

`quota_used` 与 `quota_limit` 是**同一口径**（同属"当前配额周期"）：上游的额度耗尽判定就是 `QuotaLimit > 0 && QuotaUsed >= QuotaLimit`。配成一对才能派生出正确的已用百分比。

`total_used` 是**历史累计**，与 `quota_limit` 不同口径（后者是当前周期的上限）——拿 `total_used / quota_limit` 算百分比会在跨过重置点后算出假数字。**所以它不作为维度**，只放进 `Raw` 供排查。

`quota_limit == 0` = 无限额度 → 维度标记 `Unlimited: true`、**不设 Limit**（不派生百分比，状态栏也就画不出条形图——这是诚实的结果：没有上限就没有百分比）。

`quota_used` 的"周期"由管理员手动重置决定，**下次重置时间不可知** → 维度**不填 `resets_at`**（填 `last_reset_at` 是过去时，会误导）。`last_reset_at` / `reset_count` 放进 `Raw`。

### D3: 靠显式 `source` 启用，`Match()` 恒 false

codex2api 可以部署在任意 host（这里是裸 IP + 端口），没有任何可据以识别的主机特征。识别错比识别不到更糟——会拿着用户的 API key 去打一个不是 codex2api 的地址。`Match()` 恒 false，provider 配置必须写 `"quota": {"source": "codex2api"}`（与 New API 同一处置）。

### D4: 端点 = `<base_url 去尾斜杠> + /api/key-usage/summary`

- 去掉 base_url 的尾斜杠（与 newapi adapter 同款，避免 `//api/...`）。
- base_url 里的**路径前缀保留** → 反代到子路径的部署也能用。
- 不选 `/api/key-usage/accounts`：那是**上游 ChatGPT 账号**的 5h/7d 限额（`usage_percent_5h` / `usage_percent_7d`），不是本 key 的额度；且服务端默认关闭"账号用量查询"权限（实测 404「账号用量查询未启用」），要用得管理员先在后台开权限。用户明确要求"只看 key"。

### D5: 失败语义

| 情况 | 处置 |
|---|---|
| HTTP 404（服务端关闭公开用量页） | 刷新失败 → 保留旧条目 → 自动 stale；**不影响 Claude Code 启动** |
| body 形状变了 / 解析失败 | 同上 |
| HTTP 429 | `fetchJSON` 已统一识别成 `RateLimitError` → 走既有退避（指数增长、写进数据文件、`--refresh` 也遵守） |
| key 无效 / 停用 | 服务端返回 401 → 刷新失败 → 同上 |

## Risks / Trade-offs

- **[无限额度时状态栏显示 `--`]** → 维度数据本身是对的（`used` 有值、`unlimited: true`），是**消费方**的渲染选择：状态栏脚本的 `quotaKindStr` 在没有百分比时只认 `remaining`（nil）→ 输出 `--`。两条出路：(a) 服务端给这个 key 配一个金额上限 `quota_limit`（管理员点一下）→ 立刻变成带条形图的百分比；(b) 消费方加一条 fallback 显示"已用"。**ccl 侧不改** —— 保持对外契约不变是这个设计的直接收益。→ 写进 `docs/provider-quota.md`，让用户知道这是渲染侧的事。
- **[服务端升级改字段]** → 解析失败走降级（保留旧值 + stale），不会崩；实测结论记在 `docs/provider-quota.md`，改 adapter 前先看它。
- **[服务端关闭公开用量页]** → 查不到额度。用户可能误以为"额度是 0" → 错误信息里带上服务端的原文（`{"error":"API Key 自助用量页未启用"}`），文档说明开关位置。
- **[流量窗口信息丢失]** → 已知取舍（D1）。要看上游 ChatGPT 账号的 5h/7d 限额得开 accounts 权限，那是另一个口径的事。
- **[key 级金额预算 ≠ 上游 ChatGPT 限额]** → 前者是"这个 key 花了多少钱"，后者是"这个 ChatGPT 账号还能用多少"。两者都要看得开 accounts 权限。

## Migration Plan

1. 合入 adapter + 注册 + 测试 + 文档（无对外契约变化 → 可随时回滚）。
2. 本机 provider 配置加 `"quota": {"source": "codex2api"}`（**用户本地文件，不进仓库**）→ 生效。
3. 回滚：把 provider 配置里的 `quota` 块删掉即可（provider 不带额度接口 → ccl 走"无额度"路径）。

## Open Questions

- 状态栏脚本（`~/.claude/statusline.js`，**仓库外的用户个人配置**）要不要加"无限额度时显示已用金额"的 fallback？还是给 key 在服务端配一个金额上限更省事？—— 待用户决定，不影响本 change 的实现。
