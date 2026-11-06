## Why

**额度按 key 分，同一把 key 的多个 provider 引用的是同一个额度池** → 它们的额度
是**同一个事实**，必须同源。

serve 的刷新路径本来就是对的（结果写给所有引用该池的 provider），但**手动刷新**
（`ccl quota --provider X --refresh`）只写了被点名的那个。

真机实测（`minimax-m3` 与 `minimax-m3.1-flash` 共用同一把 key）：

```
改前:  --refresh minimax-m3
  minimax-m3            fetched_at=1793962430  ← 新
  minimax-m3.1-flash    fetched_at=1793959598  ← 旧（同一把 key，差 2300 秒）
```

**同一把 key 的额度，两个 provider 显示不同时刻的数据** → 用户看到会懵（"我明明
刚刷新过，为什么另一个 provider 的数字不一样"）。

## What Changes

- 手动刷新成功后，结果写给**所有引用该额度池的 provider**（与 serve 的刷新路径
  一致）

## Capabilities

### Modified Capabilities

- `provider-quota`: 手动刷新（on-demand refresh）也必须让同池的 provider 记录到
  同一份结果

## Impact

- **改动** `internal/quota/store.go`：新增 `UpdatePoolEntries`（写给同池所有
  provider）
- **改动** `internal/quota/refresh.go`：`RefreshOne` 改用它
- **改动** `openspec/specs/provider-quota/spec.md`：on-demand refresh 那条要求
- 风险：无（只是让同源数据一致）
