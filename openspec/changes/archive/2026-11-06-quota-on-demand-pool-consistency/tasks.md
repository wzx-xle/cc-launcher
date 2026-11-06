## 1. 实现

- [x] 1.1 `internal/quota/store.go`：新增 `UpdatePoolEntries`（写给同池所有 provider）
- [x] 1.2 `internal/quota/refresh.go`：`RefreshOne` 改用它
- [x] 1.3 单测：手动刷新一个 provider → 同池的另一个也同时更新（fetched_at 与维度都一致）
- [x] 1.4 真机：`--refresh minimax-m3` → `minimax-m3.1-flash` 同步更新

## 2. 发布

- [ ] 2.1 `CHANGELOG.md` 加 `0.20.4` 段
- [ ] 2.2 `openspec archive quota-on-demand-pool-consistency`
- [ ] 2.3 打 tag `v0.20.4` + 推送 + bump scoop
