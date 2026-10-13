## Context

ccl 的 TUI（`internal/tui`）在 `stageProvider` 阶段渲染 provider 列表，行为：
- 每个 provider 用 `[ ]` 标识，cursor 所在行用 `[*]` 标识（同时整行 `cursorStyle` 高亮）。
- TUI 启动时通过 `preSelectedProvider` 传入预选项（按 cwd binding → active_provider 顺序解析，详见 `specs/provider-cwd-binding`）；当前实现把"预选"语义用行尾 `tagStyle.Render("默认")` 表达，行末多一段彩色标签。

provider 配置文件（`~/.cc-launcher/providers/<name>.json`）当前只有 `env` 块（map[string]any），`internal/provider/provider.go` 的 `Info` struct 只有 `Name` 和 `Error`，`List()` 也不读其他字段。

本次 change 四件事都集中在 TUI 列表与 provider 配置的元数据层：
1. 把"预选"信号从行尾 tag 改为左侧 dim 灰 `[*]`，与 cursor 在视觉上区分（颜色不同）。
2. provider 配置加 `note` 字段；TUI 渲染时显示。
3. provider 配置加 `order` 字段；`List()` 返回前按 `(Order asc, Name asc)` 稳定排序。
4. `ccl provider --new` 写盘后追加可选的 `note` 询问 + `order` 建议值打印（不让用户在 prompt 里填 order，只显示）。

## Goals / Non-Goals

**Goals:**
- TUI 用户能一眼分辨"哪个是 TUI 已预选的 provider"，无需扫到行尾。
- 用户能在 provider JSON 里写一行 `note` 备注，让 TUI 列表自带说明。
- 用户能通过 `order` 数值控制 provider 列表顺序，order 越小越靠前。
- 现有无 `note` / `order` 字段的 provider JSON 继续工作，零迁移。
- `ccl provider --new` 流程结束后，用户能就地补填 `note` 并看到 `order` 建议值，无需另开编辑器。

**Non-Goals:**
- **不**给 `ccl provider --new` 加 `--note` / `--order` CLI flag（见 Resolved Decisions R2）。
- 不改变 provider 配置文件其它字段（`env`）的语义。
- 不在 `provider-list-rendering` spec 中锁 `ccl provider --list` 排序行为（见 Resolved Decisions R1）。
- 不改 cursor 行视觉（仍由 `cursorStyle` 整行高亮）。

## Decisions

### 1. 视觉：dim 灰 `[*]` 与 cursor `[*]` 共存

**决策**：保留 `[*]` / `[ ]` 两态 marker 形态。当某行是 pre-selected 时，把它的 `[ ]` 替换为 `dimStyle.Render("[*]")`；当某行是 cursor 时，cursorStyle 整行覆盖（与现行实现一致）。当某行**同时是** pre-selected + cursor 时，dimStyle 标记会被 cursorStyle 覆盖，但 marker 形态 `[*]` 仍可见——这与"非 cursor 时看 marker 颜色、cursor 时看整行高亮"的双信号规则一致。

**备选**：
- (A) cursor 改用 `▶` 符号，pre-selected 仍用 `[*]`——拒绝：会改 cursor 形态，跨 stage（mode / session）行为需要同步改，scope 蔓延。
- (B) 移除 cursor `[*]`，cursor 整行高亮不再叠加 marker——拒绝：现行 mode / session stage 也用 `[*]` 形态，破坏一致性。

**理由**：保持 cursor 形态不变、marker 形态不变，仅把 pre-selected 从行尾 tag 搬到左侧 dim 槽位，影响面最小。

### 2. 解析：用未导出 struct unmarshal + 容错

**决策**：在 `internal/provider/provider.go` 定义一个未导出 struct `configFile{ Env map[string]any; Note string; Order int }`，`List()` 内 `json.Unmarshal(data, &cfg)`。解析失败的 provider 仍走原 `Error` 路径；解析成功但 `Order` 字段类型非法（如 `"ten"`）时，把该 provider 标 `Error`（与 `json.Unmarshal` 错误同等处理），其它 provider 不受影响。

**备选**：
- (A) `var v any; json.Unmarshal(data, &v)` 后用 `v.(map[string]any)` 再做类型断言——拒绝：断言噪音多，`json.Number` 还要再处理。
- (B) 把 `note` / `order` 解析为指针类型（`*string` / `*int`）区分"字段缺失 vs 字段为零值"——拒绝：业务上"零值"和"缺失"语义等价（note 空串等价于不显示，order 0 等价于回退排序），不需要区分。

**理由**：用 typed struct 把字段合法性交给 `json.Unmarshal`；不需要零值/缺失区分。

### 3. 排序：稳定排序 + 字典序 tiebreak

**决策**：`Manager.List()` 在收齐所有 `Info` 后调用 `slices.SortStableFunc(infos, func(a, b Info) int { if a.Order != b.Order { return a.Order - b.Order }; return strings.Compare(a.Name, b.Name) })`。稳定性保证 `order` 相等时保留原顺序；显式 `Name` 比较保证结果与"底层 fs readdir 顺序"解耦。

**备选**：
- (A) `sort.Slice` + 自定义 Less——拒绝：不保证稳定性，且 Go 1.21+ 推荐用 `slices` 包。
- (B) 在 fs readdir 后就地排序（按文件名）——拒绝：用户传 `order` 时本意就是覆盖文件名字典序。

**理由**：slices.SortStableFunc 是当前项目已使用的 idiom（看 `internal/stats/bucket.go` 等已用 `sort.Slice`），本 change 跟随项目惯例；不引入新依赖。

### 4. note 与 error 冲突：error 优先

**决策**：`renderProviderView` 当 `p.Error != ""` 时只渲染 error，不渲染 note（与现行"行尾加 dim 错误说明"逻辑等价位置）。spec §"TUI provider list shows note after name" 的 Scenario "Errored provider keeps error rendering precedence" 已锁定此行为。

**备选**：
- (A) note + error 并存渲染——拒绝：同一行两个 dim 段视觉混乱，且 note 通常是基于正确配置写的，errored 状态下意义不大。
- (B) errored 状态直接 hide——拒绝：现状就是显示 error 信息（帮助用户诊断），不应回退。

**理由**：保留现行 error 行为，把 note 视作"成功路径上的额外信息"。

### 5. TUI 列表中"无 pre-selection" 时不显示 `[*]`

**决策**：`m.preSelectedProvider == ""` 时（如 `dir_bindings` 空且 `active_provider` 也空），所有行 marker 均为 `[ ]`，与"所有行未预选"的语义一致。spec §"TUI pre-selected provider is marked with a dim asterisk" Scenario "No pre-selection available" 已锁定。

**备选**：
- (A) 无预选时把第一行当预选——拒绝：会改变 `runTUI` 的解析逻辑（`main.go` 已经做 cwd → active → "" 的回退），scope 蔓延。
- (B) 无预选时所有行都用 dim `[*]`——拒绝：会让用户误以为所有都是"默认"。

**理由**：尊重上游解析逻辑，TUI 仅作"展示层"。

### 6. `ccl provider --new` 写盘后追加可选 note / order 步骤（不改 spec step 数，模板字段 prompts 边界保留）

**决策**：`ccl provider --new` 流程中：
- **保留**现有 template field prompts 步骤（spec 锁定的 `len(template.Fields)+1`）。
- **不**新增 `--note` / `--order` CLI flag（避免改 `provider-new-guided-flow` spec step 数 Scenario 的契约面）。
- **追加**两个**写盘后**的可选 metadata 步骤（不在 template field prompts 边界内）：
  1. **note 询问**：`note (可选备注，回车跳过): ` + `readLine`。空字符串 / EOF → 跳过；非空 → `os.ReadFile` + 顶层 JSON patch（`json.Unmarshal` 到 `map[string]any`、set/remove `note` key、`json.MarshalIndent` 回写；**不重写 env 块**）。
  2. **order 提示**：`排序值: <N> (现有 max=<M>, 建议 max+10；手改 JSON order 字段覆盖)` + `print`。**不读输入**。`<M>` = 写盘**前**的 `max(Order)`（从 `List()` 读，新文件未纳入）；`<N>` = `<M>+10`。

**理由**：
- 用户已确认：note 让用户填（可填可跳），order 只显示建议值（不读输入）。
- spec 改动最小：只把 step 数 Scenario 改"template field prompts"（边界清晰），不破现有契约。
- 写盘后追加是**可逆**的：env 块写入失败时不进 metadata 步骤；note 步骤 EOF 不回滚（与 `Cancellation is preserved` 在 template prompts 阶段行为不冲突——env 块已写，回滚是另一条代码路径，scope 蔓延）。
- order 提示用 `max(Order)+10` 是给"留 1-9 间隔"留余地（用户手改时不用动其它 provider）。

**EOF 语义区分**（这是核心，避免与 `Cancellation is preserved` Requirement 冲突）：
- template field prompts 阶段 EOF → 已取消，env 块**未写入**（沿用现有 spec）。
- note 阶段 EOF → 跳过 note（`note = ""`），env 块**已写入保留**（写盘后无法回滚，回滚要单独 delete-on-EOF 代码路径，scope 不值）。

**实现要点**：
- `internal/cli/cli.go:newProvider` 第 421 行 `CreateWithEnv` 之后插入 metadata 块；`max` 在调用 `CreateWithEnv` 之前算（写盘**前**快照）。
- note 写回用 `os.ReadFile` + `json.Unmarshal` + set + `json.MarshalIndent`；不动 env 块。
- 现有 `TestGuidedFlow_AllTypes_RoundTrip` 的 `guidedLines` 末尾追加 `""`（note 跳过），mock EOF 走"跳过"路径。
- 新增测试（`internal/cli/cli_test.go`）：
  - `TestGuidedFlow_NewProvider_PostWrite_NoteEmptySkipped`
  - `TestGuidedFlow_NewProvider_PostWrite_NoteWritesToFile`
  - `TestGuidedFlow_NewProvider_PostWrite_OrderHintUsesMaxPlusTen`
  - `TestGuidedFlow_NewProvider_PostWrite_OrderHintZeroBaseline`
  - `TestGuidedFlow_NewProvider_PostWrite_OrderHintExcludesNewFile`
  - `TestProviderList_HonorsOrder`（R1 的软测试，CLI --list 排序副作用记录）

## Risks / Trade-offs

- [TUI 视觉冲击] 用户升级后看到行尾"默认"tag 消失、左侧多一个 dim `[*]`——可能短暂不适应。→ 在 CHANGELOG 段明确写"视觉变更"，并在 release notes 提一句。
- [Go 1.25.4 `slices` 包] `go.mod` 已确认 1.25.4（高于 1.21 阈值），`slices.SortStableFunc` 可用。→ 无 fallback 需要。
- [Order 字段类型不匹配] 现有 JSON 里若 `order` 是字符串（虽然当前没人这么写），会让该 provider 标 Error 整个无法点。→ 文档说"order 必须是 number"；spec Scenario "Config with non-numeric order marks only that provider as Error" 已锁定行为。
- [ccl provider --list 的输出顺序变化] 这是 List() 排序的副作用，但用户可能依赖原文件名字典序。→ 视为可接受：order 是显式 opt-in，缺省值 0 + 字典序 tiebreak 与原行为基本一致。CHANGELOG 提一句。
- [Bubble Tea lipgloss 颜色优先级] `cursorStyle.Render(line)` 包裹整行后，行内 `dimStyle.Render("[*]")` 会被外层覆盖——这是预期行为，spec Scenario "Pre-selected provider is the cursor" 显式要求"marker 形态 `[*]` 仍可见"是因为 cursorStyle 的 ANSI 颜色代码只覆盖背景/前景，marker 字符仍可被解析出。→ 实施时用 `m.providers[i]` 的子串测试（不依赖 ANSI 颜色代码）来验证 marker 形态。
- [写盘后 note 步骤 EOF 不回滚] 与"整流程可取消"心智模型略有出入。→ spec 显式说明 env 块已写入的不可回滚性；CHANGELOG 提一句"创建后建议 Ctrl-D 直接退出即'跳过 note'"。
- [note 步骤用 ReadFile + MarshalIndent 写回] 引入额外的 JSON parse/serialize 往返，可能改变字段顺序 / 缩进风格。→ 仍用 `json.MarshalIndent(..., "", "  ")`（与 `CreateWithEnv` 一致），env 块字段顺序由 `Marshal` 决定（map 顺序，但 JSON 对象语义上无序）；如有用户对 diff 敏感，CHANGELOG 提一句"写回会重格式化整个文件"。

## Resolved Decisions

### R1. `ccl provider --list` 排序行为不锁 spec，加软测试记录

**决定**：本次 change 不在 `provider-list-rendering` spec 中加 `ccl provider --list` 的 Requirement（避免 spec 名字被撑破 TUI 关注点）。改 `List()` 加排序后，CLI `--list` 输出**自动**按 `order` 排序，作为已知副作用。CHANGELOG 写明" --list 输出顺序在多 provider 场景下由 `order` 字段控制"。新增软测试 `TestProviderList_HonorsOrder`（在 `internal/cli/cli_test.go`）用 `strings.Contains` + 顺序断言记录该行为；任何回归需要同步改测试 + spec。

**理由**：spec 是契约，scope 应该最小；`--list` 排序是 `List()` 改动的自然副作用。软测试 + CHANGELOG 足够记录"是什么"，无需升格为硬 spec。

### R2. `ccl provider --new` 写盘后追加可选 note 询问 + order 提示（不加 `--note` / `--order` flag）

**决定**：见 Decisions §6。

## Migration Plan

- 无需迁移脚本：
  - 现有 provider JSON 文件（无 `note` / `order`）继续工作，`Info.Note = ""` / `Order = 0`，按 Name 升序展示。
  - 升级后用户首次启动 TUI 看到视觉变化（行尾"默认"tag 消失、左侧 dim `[*]`）。
  - 升级后用户首次用 `ccl provider --new` 会在写盘后看到 note 询问 + order 提示；回车跳过即可维持原行为。
- 回滚：
  - TUI 视觉回滚：删除 build 中的 `dimStyle.Render("[*]")` 分支与 `slices.SortStableFunc` 即可恢复原行为。
  - `--new` 元数据步骤回滚：删除 `newProvider` 末尾的 note / order 块即可恢复原行为。
- 文档同步更新：
  - `CHANGELOG.md` `[Unreleased]` 段加 3 条目：
    1. "Provider list 视觉变更：默认项左侧 dim `[*]` 标识"
    2. "Provider 配置新增 `note` / `order` 字段（`order` 越小越靠前）"
    3. "`ccl provider --new` 写盘后追加可选 `note` 询问与 `order` 建议值提示"
  - README "TUI 列表"小节（如有）补充"默认项视觉约定"。
  - README "Provider 配置"小节（如有）补充 `note` / `order` 字段说明。
