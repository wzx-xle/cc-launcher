## Context

TUI 两个选择页当前用两套不同的"默认/预选项"视觉语言：

- **provider 页**（`renderProviderView`，`internal/tui/tui.go`）—— 预选项用行首 dim `[*]` 标记（已被 `spec/provider-list-rendering` 锁为契约）
- **mode 页**（`renderModeView`）—— 默认项用行尾黄色 `默认` 文本标签

两个页的"当前项高亮 + 候选列表"心智模型完全一致，视觉却分裂。本次改动只动 mode 页渲染层，让它向 provider 页对齐。

## Goals / Non-Goals

**Goals:**
- mode 页默认项改用行首 dim `[*]` 标记，跟 provider 页同构
- 三态 marker（`[ ]` / dim `[*]` / 亮 `[*]`）逻辑与 provider 页一致
- 完全不动 `tui.Run` 签名、`defaultMode` 数据来源、state 数据模型、main.go 解析路径

**Non-Goals:**
- 不引入按 (workDir, provider) 维度的默认 mode 记忆
- 不调整 provider 页的渲染
- 不改 modes 包、CLI 层、launcher 层

## Decisions

### Decision 1：marker 三态逻辑照搬 provider 页

直接复用 `renderProviderView` 同一套三态分支：

```go
marker := "[ ]"
if mode.Name == m.defaultMode {
    marker = dimStyle.Render("[*]")
}
if i == m.cursor {
    marker = "[*]"
}
```

不重新设计"dim `[*]` 跟亮 `[*]` 优先级"——`spec/provider-list-rendering` 已经把 cursor-overrides-preSelected 的行为定为契约，mode 页沿用即可，避免在 TUI 内部出现两套高亮优先级。

### Decision 2：删除行尾 `默认` 文本标签

`if mode.Name == m.defaultMode { line += ... tagStyle.Render("默认") }` 整段删除。理由：
- 行首 marker 已经承担"默认"语义，再加行尾标签视觉冗余
- provider 页特意明文写了 "The trailing '默认' tag SHALL NOT be rendered in the provider list"——mode 页既然对齐 provider 页，结论相同
- `tagStyle`（黄色）一旦 mode 页不再使用，调用点消失即可，无需在 `tui.go` 顶部清理 style 变量（可能被未来其它文本标签复用，删 style 属于过度重构）

### Decision 3：不动 `defaultMode` 数据来源

虽然 spec 名字叫"参考提供商的选择"，但本次只做**视觉对齐**——mode 默认仍然是 workDir 维度（`sm.GetPermissionMode(wd)`），不引入 per-provider 记忆。理由：
- per-provider 记忆需要新增 state 字段、扩展 `state.Manager` API、调整 main.go 解析路径——超出"显示模式调整"的范围
- 工作目录粒度的默认已经能覆盖绝大多数用户的"上次用 plan，这次还想要 plan"场景
- 真要做 per-provider 留给后续 change，本次专注于把"参考 provider 选择"理解成"视觉上跟 provider 选择页对齐"

## Risks / Trade-offs

- [行首 `[*]` 跟 description 的 `—` 分隔符在 dim 段叠加导致颜色混读] → 现有 provider 页已经有同样结构（`[*]` + `name` + dim `—` + note），并未引发视觉问题；mode 页复用相同 layout 不会有新风险
- [cursor 在默认项时，整行 cursorStyle 覆盖会让 `[*]` 暂时不可识别为"dim 默认"] → 这正是 `spec/provider-list-rendering` 已有的、可接受的语义——"marker 形状 `[*]` 仍可识别，颜色被 cursor 吞掉"；本 spec 沿用

## Migration Plan

无需迁移：
- 无 state.json schema 变更（不增字段）
- 无配置文件变更
- 无 CLI 命令变更
- 灰度策略：渲染层单点替换，回归 `go test ./...` 通过即上线

## Open Questions

无。
