## Context

**现状**：provider 配置用标准 `encoding/json` 解析，共 **6 个解析点**：

| 位置 | 用途 | 失败后果 |
|---|---|---|
| `internal/provider/provider.go:218` | `List()` 解析成 `configFile`（取 note/order/quota） | 该 provider 在列表里标「JSON 解析失败」 |
| `internal/provider/provider.go:235` | `List()` 解析成 `map[string]any`（算透传键数 + schema 校验） | 跳过透传统计与校验 |
| `internal/provider/provider.go:273` | `Get()` 解析成 `map[string]any` | `Get` 报错 |
| `internal/provider/provider.go:304` | `SaveRaw()` 的校验 | 保存被拒（"配置不是合法 JSON"） |
| `internal/quota/refresh.go:438` | 枚举 provider（找有 quota 的） | 该 provider 被跳过 |
| `internal/quota/refresh.go:467` | `LoadProviderRef` | 载入失败 |

**漏一处就会"有的能读有的不能"**——比如 provider 在列表里出现了，但额度查不了。

**好消息：写盘路径天然保留注释**。provider 配置只有两个写点：

| 位置 | 行为 | 对注释的影响 |
|---|---|---|
| `internal/provider/provider.go:314`（`SaveRaw`） | `safefile.Write(path, content, ...)` —— **原样写盘**，不重新序列化 | **注释保留** ✓ |
| `internal/provider/provider.go:343`（`CreateWithEnv`） | `json.MarshalIndent` 生成新文件 | 新文件本来就没注释，无所谓 ✓ |

网页编辑器（`internal/web/providers.go` 的三处 PUT/POST）**全部走 `SaveRaw`** → 原样写盘。而 `migrateSettingsJSON`（`cmd/cc-launcher/main.go:371`）只动 `~/.claude/settings.json`，**不重写 provider 配置**。

所以**只要解析端容忍注释，注释就能完整活下来**——不需要改任何写盘逻辑。

**约束**：

- ccl 只有 **2 个直接依赖**（bubbletea / lipgloss），是刻意保持轻量的 → **不引 JSONC 库**
- 透传给 Claude Code 的 settings 是 ccl **重新序列化**出来的 → 注释只活在 provider 自己的文件里，**不会**进 Claude Code
- provider 配置**不会**被 Claude Code 直接读取（ccl 拆成 `--settings` 交付），所以注释对 Claude Code 无意义也无害

## Goals / Non-Goals

**Goals:**

- provider 配置支持 `//` 行注释与 `/* */` 块注释，写在任意位置
- 注释在加载、校验、网页编辑器保存后都保留
- **解析错误的行号仍指向原文件**（不能因为剥离了注释就错位）
- 不引第三方依赖
- 6 个解析点**行为一致**（不能有的认注释有的不认）

**Non-Goals:**

- **不支持尾随逗号**（JSONC 规范 jsonc.org 本身不允许；本次只解决"注释"这一个明确诉求）
- **不改 `~/.claude/settings.json` 的解析**——见 D6
- 不改 Claude Code 的任何文件格式
- 不给透传给 Claude Code 的 settings 加注释支持（那里没有注释可留）

## Decisions

### D1. 独立小包 `internal/jsonc`，不引第三方库

```go
// Unmarshal 解析 JSONC：先剥注释，再走标准 json.Unmarshal。
func Unmarshal(data []byte, v any) error
```

**为什么不引库**：候选是 `tailscale/hujson`（成熟，但把注释保留在解析结果里，还要额外 `Standardize()`）或 `muhammadmuzzammil1998/jsonc`。ccl 的依赖策略是刻意精简的（2 个直接依赖），为这一个功能引一个 JSON 库不划算；而剥离注释本身只有 ~80 行。

**为什么放独立包而不是 `internal/provider`**：`internal/quota` **不依赖** `internal/provider`（它自己读文件、自己解析）——把工具放进 provider 包会让 quota 反向依赖 provider。独立小包两边都能用，且将来 `settings.json` 若也要支持，同一个包直接复用。

### D2. 剥离用**等长空白**替换，保留换行 → 行号不错位

注释被替换成**等长的空格**（不是删掉），换行符**原样保留**。于是：

- `json.Unmarshal` 报的**行号与列号仍指向原文件的位置**（列号在注释处会偏，但行号准）
- 错误信息里出现 `invalid character '/' looking for beginning of value` 时，用户知道是**哪一行**有注释语法问题

**为什么不用"删掉注释"**：删掉会让后面所有行往上移，错误行号全错——用户看到"第 3 行出错"实际是第 47 行写的注释，全靠猜。

**为什么不用"替换成 \n"**：同样会改变列位置，且连续空行会让文件在编辑器里变得难看。等长空格是唯一不改变任何位置信息的做法。

### D3. 状态机：四个状态，字符串优先

剥离器是一个字节级状态机：

| 状态 | 进入条件 | 处理 |
|---|---|---|
| 普通 | 初始 | 见下 |
| 字符串内 | 遇到 `"` | `\` 下一个字节原样跳过（转义）；`"` 结束字符串 |
| 行注释 | 遇到 `//` | 跳到行尾（换行保留） |
| 块注释 | 遇到 `/*` | 找到 `*/`；其中的换行**逐个保留** |

**最关键的一条**：**字符串里的 `//` 不是注释**。provider 配置里到处都是 URL——

```json
"ANTHROPIC_BASE_URL": "https://ark.cn-beijing.volces.com/api/plan"
```

这里的 `//` 若被当注释，整行就废了。**状态机必须先判字符串再判注释**，这是本实现最容易写错的地方，也是单测的重点。

同理，字符串里的 `/*`、转义引号 `\"`、以及 `\\` 后的字符都要正确处理。

### D4. 6 个解析点统一走 `jsonc.Unmarshal`

**不留"有的地方用标准 Unmarshal"的情况**——那会造出"provider 在列表里能看见、额度却查不了"这种极难查的现象。

改法：在每个解析点把 `json.Unmarshal(data, &x)` 换成 `jsonc.Unmarshal(data, &x)`。**函数签名与错误语义不变**（都是返回 error），所以调用方的代码几乎不用改。

**`SaveRaw` 的校验也换**：它解析出的 `map[string]any` 只用于 schema 校验，**校验通过后写回的是原始 `content`**（含注释）→ 注释保留。

### D5. 注释不进 Claude Code

ccl 拆 provider 配置时，`env` 与其它透传键被**重新序列化**成 `--settings` 指向的文件（内容哈希命名）。注释因此**不会**出现在交付给 Claude Code 的 settings 里。

**这是对的**：注释是 provider 配置的**本地说明**，不是 Claude Code 的设置项。透传出去反而会让 Claude Code 看到一个它不认识的键。

### D6. `~/.claude/settings.json` 的注释**仍然会丢**——明确划界

用户可能会接着问"settings.json 能不能也支持注释"。现状（2026-11-27 实测代码）：

| 路径 | 写盘方式 | 注释 |
|---|---|---|
| 网页 Claude 视图（`internal/web/claude.go:152`） | `safefile.Write(path, content)` —— 原样 | **能保留**（只要解析/校验容忍） |
| CLI 的 `migrateSettingsJSON`（`cmd/cc-launcher/claudesupport.go:144`） | `json.MarshalIndent` —— 重写 | **丢** |
| 网页的校验（`internal/web/claude.go:139`） | `json.Unmarshal` | 带注释 → 保存被拒 |

**本次不动它**，理由：settings.json 的解析点更多（Claude Code 自己也在读它，行为要跟 Claude Code 对齐），且 CLI 的 migrate 路径必须重写（它要改内容）→ 注释在那条路上必然丢。**要在那条路上也保留注释，得改成"就地编辑"而不是"重写整个文件"，那是另一个量级的改动。**

但**这个边界要写进文档**——用户会以为"ccl 支持注释"是全局能力。

### D7. 不支持尾随逗号

`{"a": 1,}` 仍然报解析错误。理由：JSONC 规范（jsonc.org）不允许尾随逗号；而且尾随逗号在"编辑配置文件"时是**误操作的高发区**（删一个键忘了删逗号），报错比容忍它更有帮助。

**如果将来要支持**：剥离器可以顺带把 `}` / `]` 前的逗号去掉——但那是另一个决定，本次不做。

### D8. 错误信息：说清是"注释写错了"还是"JSON 本身错了"

剥离后的解析错误**照原样透出**（`json.Unmarshal` 的 error 里带行号）。但要保证：**错误信息里的行号指向原文件**（靠 D2 的等长空白替换）。

不需要额外包装——标准 JSON 的错误信息已经够清楚（`invalid character 'x' looking for beginning of value` + 行号）。

## Risks / Trade-offs

**[剥离器把字符串里的 `//` 当注释]** → provider 配置直接解析失败，用户以为"ccl 坏了"。缓解：状态机先判字符串；单测专门覆盖"URL 里的 `//`"、"字符串里的 `/*`"、转义引号 `\"`。

**[某个解析点漏改]** → "有的能读有的不能"，极难查。缓解：6 个点全部列出并逐个改；单测覆盖**每个**解析点（不是只测剥离器本身）。

**[注释里写了个 `{` 或 `}`]** → 剥离器只看 `//` 和 `/*`，块注释里的内容整体被替换成空白 → 不会影响 JSON 结构 ✓

**[注释里有 `*/`** → 块注释提前结束，剩下的 `*/` 会被当普通字符 → 解析报错（正确的行为：块注释不能嵌套）。

**[用户以为注释能进 Claude Code]** → 实际不会。缓解：文档写明；注释只活在 provider 文件里。

**[未来某个新代码路径又用标准 `json.Unmarshal` 读 provider 配置]** → 静默不一致。缓解：把 `jsonc.Unmarshal` 作为**读 provider 配置的唯一入口**，并在 provider 包里留一条注释说明"读 provider 配置一律用 jsonc.Unmarshal"。

**[`settings.json` 那边行为不一致]** → 用户以为"ccl 都支持注释了"。缓解：文档明确划界（D6）。

## Migration Plan

**纯增量，零迁移**：

- 不带注释的 provider 配置**解析行为完全不变**（剥离器对无注释的 JSON 是恒等变换）
- 带注释的配置从"加载失败"变成"正常加载"
- **不需要改任何配置文件**
- 回滚 = 删掉 `internal/jsonc` 并把 6 个解析点换回 `json.Unmarshal`（一次提交可回退）
