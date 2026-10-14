# Tasks: tui-mode-default-marker

## 1. TUI 渲染：`renderModeView` 默认项改用行首 dim `[*]`

- [x] 1.1 `internal/tui/tui.go` 的 `renderModeView`：当 `mode.Name == m.defaultMode` 时，把该行 marker 改为 `dimStyle.Render("[*]")`（替换现行 `[ ]`）；cursor 行的 `[*]` / `cursorStyle` 行为不变
- [x] 1.2 同一函数：删除行尾 `tagStyle.Render("默认")` 追加（已被行首 dim `[*]` 取代）
- [x] 1.3 跑 `go build ./...` 通过（编译验证）

## 2. TUI 单测：覆盖 mode 页 marker 形态

- [x] 2.1 `internal/tui/tui_test.go` 新增 `TestRenderModeView_DefaultShowsDimAsterisk`：构造 `defaultMode = "plan"`，cursor 移到 `acceptEdits`，断言 `model.View()` 在 stageMode 下：
  - `strings.Count(view, "[*]") == 2`（cursor 行 + 默认行）
  - 其它行带 `[ ]`
  - 关键断言：`!strings.Contains(view, "默认")`（行尾 tag 移除）
- [x] 2.2 新增 `TestRenderModeView_DefaultIsCursor`：构造 `defaultMode = "auto"` 且 cursor 落在 `auto`，断言 `View()` 在 stageMode 下：
  - `strings.Count(view, "[*]") == 1`（cursor 行覆盖了默认行的 dim）
  - `!strings.Contains(view, "默认")`
- [x] 2.3 新增 `TestRenderModeView_NoDefaultAllBlank`：构造 `defaultMode = "manual"`、cursor 在 `manual`，然后 `defaultMode = "definitely-not-a-mode"` 走 `m.defaultMode` 不匹配任何 mode 的路径（覆盖 spec Scenario "No default mode available"）；断言：
  - `strings.Count(view, "[*]") == 1`（仅 cursor 行）
  - 其它 mode 行带 `[ ]`
  - `!strings.Contains(view, "默认")`
- [x] 2.4 新增 `TestRenderModeView_DescriptionStillRendered`：构造一个 mode 列表 + `defaultMode`，断言 description 文案（"—" 前缀 + 中文描述）依然出现在 `View()` 中（确认 dim `[*]` 替换没有顺带砍掉 description 渲染）
- [x] 2.5 跑 `go test ./internal/tui/...` 全绿（含已有 `TestRun_DefaultMode` 等不破坏）

## 3. 全量回归

- [x] 3.1 跑 `go test ./...` 全绿（确认未破坏 `provider-setenv-cwd-binding` 相关的 TUI 测试 + `permission-mode-contract` modes 包测试）
- [x] 3.2 跑 `go vet ./...` 无 warning
- [x] 3.3 跑 `go build ./...` 通过

## 4. 文档与归档

- [x] 4.1 `CHANGELOG.md` `[Unreleased]` 段加 1 条目："TUI 权限模式页默认项视觉变更：行首 dim `[*]` 标识（原行尾"默认" tag 移除），与提供商选择页对齐"
- [x] 4.2 跑 `openspec validate tui-mode-default-marker --strict` 通过（`openspec verify` 在该 CLI 版本不存在，用 `openspec validate --strict` 替代）
- [x] 4.3 跑 `openspec archive tui-mode-default-marker --yes` 归档
