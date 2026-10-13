# Tasks: provider-list-display-improvements

## 1. provider 包：Info 扩展 + List 解析与排序

- [x] 1.1 在 `internal/provider/provider.go` 给 `Info` struct 增加 `Note string` 与 `Order int` 字段（含简短注释）
- [x] 1.2 在同文件定义未导出 `configFile` struct（`Env map[string]any; Note string; Order int`），修改 `List()` 循环用 `json.Unmarshal(data, &cfg)` 替代 `var v any` 路径，把 `cfg.Note` / `cfg.Order` 写入 `Info`
- [x] 1.3 `List()` 末尾用 `slices.SortStableFunc(infos, ...)` 按 `(Order asc, Name asc)` 稳定排序（项目 `go.mod` 已是 1.25.4，`slices` 包可用）
- [x] 1.4 容错：`json.Unmarshal` 失败仍写 `Info.Error`；其它 provider 不受影响（沿用现行行为）

## 2. provider 单测：覆盖 note / order 解析与排序

- [x] 2.1 `internal/provider/provider_test.go` 新增 `TestList_ParsesNoteAndOrder`：写三个 provider JSON（带/不带 `note`、`order` 不同值），断言 `Info.Note` / `Info.Order` 正确
- [x] 2.2 新增 `TestList_SortedByOrderThenName`：三个 provider 写 `order=20 / 5 / 10`，断言 `List()` 返回顺序为 5 / 10 / 20
- [x] 2.3 新增 `TestList_NonNumericOrderMarksOnlyThatProviderAsError`：写 `order: "ten"` 与一个正常 provider，断言前者 `Error != ""`、后者 `Error == ""` 且正常 provider 的 `Note` / `Order` 解析正确
- [x] 2.4 新增 `TestList_ErroredProviderIncludedAndSorted`：写一个无效 JSON + 一个 `order=5` 的有效 JSON，断言两者都返回、且有效 provider 排在前面
- [x] 2.5 跑 `go test ./internal/provider/...` 全绿

## 3. TUI 渲染：默认项 dim `[*]` + note 显示

- [x] 3.1 `internal/tui/tui.go` 的 `renderProviderView`：当 `p.Name == m.preSelectedProvider && p.Error == ""` 时，把该行 marker 改为 `dimStyle.Render("[*]")`（替换现行 `[ ]`）；cursor 行的 `[*]` / `cursorStyle` 行为不变
- [x] 3.2 同一函数：移除行尾 `tagStyle.Render("默认")` 追加（已被左侧 dim `[*]` 取代）
- [x] 3.3 同一函数：当 `p.Note != ""` 且 `p.Error == ""` 时，在 `name` 后追加 `dimStyle.Render("  "+p.Note)`；errored 状态保持现行 error 渲染（note 不渲染，避免双 dim 段）
- [x] 3.4 跑 `go build ./...` 通过（编译验证）

## 4. TUI 单测：覆盖 marker 形态与 note 渲染

- [x] 4.1 `internal/tui/tui_test.go` 新增 `TestRenderProviderView_PreSelectedShowsDimAsterisk`：构造 `preSelectedProvider = "deepseek"`，cursor 移到 `anthropic`，断言 `model.View()` 包含 deepseek 行的 `[*]` 字符（用 `strings.Contains` 验证 marker 形态，不依赖 ANSI 颜色码）
- [x] 4.2 新增 `TestRenderProviderView_NoTagAtEnd` 断言 View 不再含 `"默认"` 字串（行尾 tag 移除）
- [x] 4.3 新增 `TestRenderProviderView_NoteShownAfterName`：构造带 `Note = "国内 / 按量计费"` 的 provider，断言 View 包含该 note 文本
- [x] 4.4 新增 `TestRenderProviderView_NoteSuppressedWhenErrored`：构造 `Error != ""` 且 `Note != ""` 的 provider，断言 View 含 error 文本且不含 note 文本
- [x] 4.5 新增 `TestRenderProviderView_NoPreSelectedAllBlankMarkers`：`preSelectedProvider = ""` 时所有行 marker 为 `[ ]`
- [x] 4.6 跑 `go test ./internal/tui/...` 全绿

## 5. CLI --new 写盘后追加可选 note / order 步骤

- [x] 5.1 `internal/cli/cli.go:newProvider`：在 `CreateWithEnv` 写盘**之前**，先 `c.providers.List()` 算 `max(Order)`（写盘前快照）
- [x] 5.2 同一函数：写盘成功**之后**插入两段：(a) `fmt.Print("note (可选备注，回车跳过): ")` + `readLineFn`（空字符串/EOF 视为 `note=""`）；(b) `fmt.Printf("排序值: %d (现有 max=%d, 建议 max+10；手改 JSON order 字段覆盖)\n", max+10, max)`
- [x] 5.3 若 `readLine` 返回的非空字符串：把 `<name>.json` 重新读出（`os.ReadFile`），`json.Unmarshal` 到 `map[string]any`，`cfg["note"] = strings.TrimSpace(s)`，`json.MarshalIndent` 回写（保持 `env` 块不动，缩进用 `("", "  ")` 与 `CreateWithEnv` 一致）
- [x] 5.4 写盘失败时跳过 metadata 块（直接返回 `CreateWithEnv` 的 error，不进 note / order 步骤）

## 6. CLI 单测：--new 写盘后 metadata 步骤

- [x] 6.1 `internal/cli/cli_test.go:guidedLines` 在每种 type 的 lines 末尾追加 `""`（note 跳过；让 `TestGuidedFlow_AllTypes_RoundTrip` 仍能 EOF 收尾）
- [x] 6.2 新增 `TestGuidedFlow_NewProvider_PostWrite_NoteEmptySkipped`：mockStdin 末尾空字符串，断言写盘文件无 `note` 字段，`Manager.Get(name)` 不返回 `note` key
- [x] 6.3 新增 `TestGuidedFlow_NewProvider_PostWrite_NoteWritesToFile`：mockStdin 末尾 `"国内 / 按量计费"`，断言文件有 `note = "国内 / 按量计费"`，`env` 块原 keys 都在
- [x] 6.4 新增 `TestGuidedFlow_NewProvider_PostWrite_OrderHintUsesMaxPlusTen`：预先写一个 `bravo.json` 含 `order=20`，`--new` 跑完后捕获 stdout，断言含 `排序值: 30` 与 `现有 max=20`
- [x] 6.5 新增 `TestGuidedFlow_NewProvider_PostWrite_OrderHintZeroBaseline`：空 providers dir 跑 `--new`，断言含 `现有 max=0` 与 `排序值: 10`
- [x] 6.6 新增 `TestGuidedFlow_NewProvider_PostWrite_OrderHintExcludesNewFile`：连续跑两次 `--new`（第二次不指定 order），断言第二次的 `max` 不含第一次新写的（用 mock 截 stdout 比较）
- [x] 6.7 新增 `TestProviderList_HonorsOrder`（R1 软测试）：写 3 个 provider JSON 不同 `order`，`ccl provider --list` 后断言输出顺序与 `order` 升序一致
- [x] 6.8 跑 `go test ./internal/cli/...` 全绿（确认 `TestGuidedFlow_AllTypes_RoundTrip` 7 个 type 仍过）

## 7. 全量回归

- [x] 7.1 跑 `go test ./...` 全绿（确认未破坏现有测试，特别是 `provider-cwd-binding` 相关的 TUI 测试 + `provider-new-guided-flow` 7-type round-trip）
- [x] 7.2 跑 `go vet ./...` 无 warning
- [x] 7.3 跑 `go build ./...` 通过

## 8. 文档与归档

- [x] 8.1 `CHANGELOG.md` `[Unreleased]` 段加 3 条目：
  - "Provider list 视觉变更：默认项左侧 dim `[*]` 标识（原行尾"默认" tag 移除）"
  - "Provider 配置新增 `note` / `order` 字段（`order` 越小越靠前）"
  - "`ccl provider --new` 写盘后追加可选 `note` 询问与 `order` 建议值提示（回车跳过 note；order 建议值为 `max(Order)+10`）"
- [x] 8.2 如有 `docs/provider-config.md`，补充 `note` / `order` 字段说明（项目目前可能没有此文档，按实际决定）
- [x] 8.3 跑 `openspec verify provider-list-display-improvements` 通过
- [x] 8.4 跑 `openspec archive provider-list-display-improvements --yes` 归档
