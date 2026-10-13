## Why

ccl 的 TUI provider 列表当前用两套并存的"默认"信号，行尾的"默认"标签（`tagStyle`）和 cursor 位置（`[*]`），用户难以一眼看清"哪个是 TUI 已预选的"。同时 provider 配置文件只有 `env` 块，没有元数据承载——用户想给某个 provider 写一句备注（如"国内/按量付费"），或想固定某几个 provider 在列表里的顺序，都只能靠文件名排序，没有声明式手段。

把"默认项"信号从行尾 tag 改为列表最左侧的灰色 `[*]`（和 cursor 共用形态，但用 `dimStyle` 区分），并在配置文件中增加可选 `note` / `order` 字段，让 provider 列表既能一眼读懂默认项，又能携带可读的元数据。

## What Changes

- **新增** TUI provider 列表渲染规则：每个 provider 行用 `[*]` / `[ ]` 标识；cursor 当前项用 `cursorStyle` 高亮整行；TUI 预选项（即"默认"provider，与 `dir_bindings[wd]` 或 `active_provider` 解析一致）用 `dimStyle` 灰色 `[*]` 标识（与 cursor 形态相同、颜色不同）。行尾"默认"标签移除。
- **新增** provider 配置文件（`~/.cc-launcher/providers/<name>.json`）schema 新增两个可选字段：
  - `note` (string)：一行说明，渲染在 provider 名后、空格分隔、`dimStyle` 灰色显示。
  - `order` (number)：排序权重，越小越靠前；缺省或相等时回退到原文件名字典序。
- **修改** `internal/provider.Manager.List()`：解析每个 provider JSON 的 `note` / `order` 字段（解析失败仅记 `Error`，不影响其他 provider），按 `order` 升序排序后返回。
- **新增** TUI 在 `renderProviderView` 中按 `Info.Note` 渲染备注列。
- **新增** `provider.Info` struct 扩展 `Note` / `Order` 字段。
- **修改** `ccl provider --new` 引导流程：写盘后追加两个**可选**步骤——询问 `note`（回车跳过）、打印 `order: <max(Order)+10>` 建议值（只显示，不读输入）。`provider-new-guided-flow` spec 中锁定 step 数 / 模板字段提示数量的 Requirement / Scenario 同步更新，明确"template field prompts"与"写盘后可选元数据步骤"的边界。

## Capabilities

### New Capabilities

- `provider-list-rendering`：TUI provider 列表的视觉与配置元数据规范。包括配置文件可选 `note` / `order` 字段、List() 排序行为、TUI 默认项用灰色 `[*]` 标识、TUI 备注显示。

### Modified Capabilities

- `provider-new-guided-flow`：`ccl provider --new` 流程在写盘后追加"询问 note（可选） + 打印建议 order"两个步骤，**与现有"template field prompts"分离**。锁定 step 数的 Requirement / Scenario 同步改为锁定"template field prompts count"（`len(template.Fields)+1`），不再约束写盘后的可选步骤。`Cancellation is preserved` 语义保留（EOF 仍视为"已取消"整个流程，包括写盘后步骤）。

## Impact

- 代码：
  - `internal/provider/provider.go`：`Info` struct 新增 `Note string` / `Order int` 字段；`List()` 在循环里 `json.Unmarshal` 顶层结构到带 `Note` / `Order` 的中间结构，解析失败容错（仅 `Error` 字段标记）；`List()` 末尾按 `Order` 升序、`Name` 升序稳定排序后返回。
  - `internal/tui/tui.go`：
    - `renderProviderView` 移除行尾"默认"tag 块；新增"该行是预选项"分支：marker 改为 `dimStyle.Render("[*]")`，其它行 marker 保持 `[ ]`（cursor 行的 `[*]` 仍由 `cursorStyle` 整行覆盖，颜色优先级与现行实现一致）。
    - `renderProviderView` 在 `name` 后追加 `dimStyle.Render("  "+p.Note)`（仅在 `Note != ""` 时）。
  - `internal/provider/provider_test.go`：补 `List()` 解析 `note` / `order`、容错、排序的单测。
  - `internal/tui/tui_test.go`：补"预选项 marker 形态" / "note 显示" 的渲染快照单测。
  - `internal/cli/cli.go:newProvider`：在 `CreateWithEnv` 写盘成功后，新增 (a) 提示 `note (可选备注，回车跳过):` 并 `readLine` 一行，空行视为 `note=""`；非空时用 `os.ReadFile` + 顶层 JSON patch 把 `note` 写回文件（不重写 `env` 块）；(b) 打印 `排序值: <max+10> (建议手改 JSON order 字段)`，**不读输入**。`max(Order)` 来自 `providers.List()`，新文件还未被下次 `List()` 看到，所以"现有 max"等于"**写盘前**的 max"。
  - `internal/cli/cli_test.go:guidedLines`：在每种 type 的 lines 末尾追加 `""`（note 跳过）；`TestGuidedFlow_AllTypes_RoundTrip` 行为保持（仍跑通 7 个 type 写盘 + 解析）。
- 测试：
  - provider：`note` / `order` 字段缺失时不报错；非法值（如 `order` 是字符串）仅该 provider `Error`，不影响其他；多个 provider 有/无 `order` 时按规则排序。
  - TUI：预选项在非 cursor 行时显示 `[*]`（dim 风格）；cursor 移动到其它行时，预选项仍保留 dim `[*]`；预选项同时是 cursor 时，整行 `cursorStyle` 高亮（不冲突）。
  - CLI：写盘后 `note` 提示空字符串 = 跳过；`note = "国内"` 写入后被 `Manager.Get(name)` 读出；`order` 建议值 = 写盘前的 `max(Order)+10`（`max` 来自现存 provider 列表）。
- 文档：
  - `docs/provider-config.md`（如有）补充 `note` / `order` 字段说明。
  - README "TUI 列表"小节（如有）补充"默认项视觉约定"。
- 兼容性：
  - 老 provider JSON 文件（无 `note` / `order`）继续工作，行为等价于 `note=""` / `order=0`。
  - `ccl provider --new` 现有 template field prompts 流程不变；新增的写盘后 `note` 询问 / `order` 提示为**可选**步骤（回车跳过 / 不读输入），与 spec `provider-new-guided-flow` 锁定的"template field prompts"边界清晰分离。
  - TUI 用户在升级后看到的视觉变化：默认项的 `[*]` 现在移到最左、且为灰色；行尾"默认"标签消失——这是有意设计选择。
