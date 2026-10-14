## Why

TUI 提供商列表（`spec/provider-list-rendering`）的预选项已经统一用行首 **dim `[*]` 标记** 表达，但权限模式列表的默认项却用行尾黄色 `默认` 文本标签——两套完全不同的视觉语言，切换页面时跳跃感强。模式页应当沿用 provider 页已经验证过的"行首 dim `[*]`"三态标记，让两个选择页在视觉上对等。

## What Changes

- `internal/tui/tui.go` 的 `renderModeView`：删除"行尾 `默认` 文本标签"逻辑；改为在行首槽位用与 `renderProviderView` 一致的 dim `[*]` 标记渲染默认项（marker 三态：`[ ]` / dim `[*]` / 亮 `[*]`，与 provider 页保持同构）。
- cursor 行依然用 `cursorStyle` 整行高亮，dim `[*]` 仅在默认项不在 cursor 行时可见——与 provider 页"`preSelected` 跟 `cursor` 重合时由 cursor 覆盖"的既有行为一致。
- `tui.Run` 签名、`defaultMode` 数据来源（仍为 workDir-based `sm.GetPermissionMode(wd)`）、`internal/state` 数据模型、`cmd/cc-launcher/main.go` 全部不动。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `permission-mode-contract`：新增一条 Requirement，规定 TUI 模式列表默认项的行首 dim `[*]` 标记与 provider 列表的 `[*]` 标记同构，并禁止在模式行尾部再追加"默认"文本标签。

## Impact

- `internal/tui/tui.go`：`renderModeView` 单函数调整
- `internal/tui/tui_test.go`：新增/调整渲染层单测覆盖新 marker 三态
- 不涉及 `internal/state`、`cmd/cc-launcher/main.go`、`internal/cli`、`internal/modes`
- 不破坏 `provider-setenv-cwd-binding`、`provider-list-rendering` 已有契约
- 无 schema 变化、无配置迁移、无 CLI 命令变化
