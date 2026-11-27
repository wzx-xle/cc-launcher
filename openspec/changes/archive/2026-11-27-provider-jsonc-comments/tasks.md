## 1. 注释剥离器（internal/jsonc）

- [x] 1.1 新建 `internal/jsonc/jsonc.go`：导出 `Unmarshal(data []byte, v any) error`——**先剥注释，再走标准 `json.Unmarshal`**。**只用标准库**（不引 hujson 之类的 JSONC 库：ccl 只有 2 个直接依赖，是刻意保持轻量的）
- [x] 1.2 实现**字节级状态机**，四个状态：普通 / 字符串内 / 行注释 / 块注释
  - 普通态：`"` → 字符串态；`//` → 行注释态；`/*` → 块注释态
  - 字符串态：`\` 下一个字节**原样跳过**（转义）；`"` → 普通态；**其它字节一律不是注释**
  - 行注释态：跳到行尾（**换行保留**）
  - 块注释态：找到 `*/`；其中的**换行逐个保留**
- [x] 1.3 **注释用等长空白替换**（不是删掉），**换行符原样保留** → `json.Unmarshal` 报的**行号仍指向原文件**（列号在注释处会偏，行号必须准）。删掉注释会让后面所有行往上移，用户看到"第 3 行出错"实际是第 47 行写的注释
- [x] 1.4 **最关键的一条**：**字符串里的 `//` 不是注释**。provider 配置里全是 URL（`https://ark.cn-beijing.volces.com/api/plan`）——状态机必须**先判字符串再判注释**。同理字符串里的 `/*`、转义引号 `\"`、`\\` 后的字符
- [x] 1.5 **不支持尾随逗号**（`{"a":1,}` 仍报解析错误）：JSONC 规范（jsonc.org）本身不允许，且尾随逗号是"删键忘删逗号"的高发区，报错比容忍更有帮助
- [x] 1.6 块注释里出现 `*/` → 提前结束，剩下的 `*/` 当普通字符 → 解析报错（正确的行为：块注释不能嵌套）
- [x] 1.7 **单测（护栏，重点）**：
  - URL 里的 `//` 不是注释
  - 字符串里的 `/* */` 不是注释
  - 转义引号 `\"` 后的 `//` 不是注释
  - 块注释跨多行 → 换行保留、后续行的**行号不变**
  - 行注释在行尾 → 该行其余部分被忽略
  - 注释在对象成员之间 / 数组元素之间 / 键的前后
  - 无注释的 JSON → 剥离是**恒等变换**（字节级相同）
  - 尾随逗号 → 仍然报错
  - 非法 JSON → 报的行号是原文件的行号

## 2. 接到 6 个解析点

- [x] 2.1 `internal/provider/provider.go:218`（`List()` 解析成 `configFile`）→ 改用 `jsonc.Unmarshal`
- [x] 2.2 `internal/provider/provider.go:235`（`List()` 解析成 `map[string]any`，算透传键数 + schema 校验）→ 改用 `jsonc.Unmarshal`
- [x] 2.3 `internal/provider/provider.go:273`（`Get()`）→ 改用 `jsonc.Unmarshal`
- [x] 2.4 `internal/provider/provider.go:304`（`SaveRaw()` 的校验）→ 改用 `jsonc.Unmarshal`。**写盘仍用原始 `content`**（`safefile.Write` 原样写）→ 注释保留
- [x] 2.5 `internal/quota/refresh.go:438`（枚举 provider，找有 quota 的）→ 改用 `jsonc.Unmarshal`
- [x] 2.6 `internal/quota/refresh.go:467`（`LoadProviderRef`）→ 改用 `jsonc.Unmarshal`
- [x] 2.7 **用 LSP `findReferences` 确认没有漏掉的解析点**——漏一处就会造出"provider 在列表里能看见、额度却查不了"这种极难查的现象。`grep "json.Unmarshal"` 会把 quota 的**厂商响应解析**（`adapter_*.go`、`store.go`）一起扫出来，那些**不是** provider 配置，**不要改**
- [x] 2.8 在 `internal/provider/provider.go` 顶部留一条注释：**读 provider 配置一律用 `jsonc.Unmarshal`**（防止将来有人新写一个标准 `json.Unmarshal` 的读路径）

## 3. 行为验证

- [x] 3.1 **单测：注释在保存后保留**——构造带注释的 provider 配置 → `SaveRaw` → 读回文件**逐字节相同**（含注释）
- [x] 3.2 **单测：网页编辑路径保留注释**——`internal/web/providers.go` 的三处（PUT / POST 新建 / 复制重命名）**全部走 `SaveRaw`** → 注释保留。确认没有一条路径走 `MarshalIndent` 重写
- [x] 3.3 **单测：注释不进 Claude Code**——带注释的 provider 配置 → ccl 拆出的透传 settings 里**没有注释、也没有承载注释的键**
- [x] 3.4 **单测：6 个解析点行为一致**——同一份带注释的配置，经 `List()` / `Get()` / `SaveRaw` 校验 / `quota` 枚举 / `LoadProviderRef` 五条路径读，结果**必须一致**（不能有的认注释有的不认）
- [x] 3.5 **单测：带注释的配置在列表里不是 Error**（`Info.Error == ""`）
- [x] 3.6 **单测：解析失败的行号指向原文件**——第 10 行有语法错误、上面几行有注释 → 错误信息说第 10 行
- [x] 3.7 **手工验证**：给本机 `ark-deepseek-flash` 的配置加注释（含"怎么申请 AK/SK"的说明），确认 `ccl provider list` 正常、`ccl quota --provider ark-deepseek-flash --refresh` 正常、网页编辑保存后注释还在

## 3.5 前端编辑器（**实施中发现的追加项**）

> 原设计漏了前端：后端容忍注释了，但 `editor.js` 的 `validate()` 用**标准
> `JSON.parse`** → 带注释的配置被判非法 → **保存按钮被禁用**（"能读不能改"）。
> 实测截图确认：页面显示"JSON 解析失败"，保存按钮 disabled。

- [x] 3.5.1 新建 `internal/web/assets/jsonc.js`：JS 版注释剥离器（与 Go 版同语义：等长空白替换、换行保留、字符串里的 `//` 不是注释、不支持尾随逗号），暴露 `window.CCL.jsonc.parse`
- [x] 3.5.2 `editor.js` 的 `createEditor` 加 `opts.jsonc` 选项；`validate()` 在该选项下先剥注释再 parse
- [x] 3.5.3 `providers.js` 的 createEditor 传 `jsonc: true`（只有 provider 视图传——Claude 视图后端仍用标准 JSON，两边不一致会更误导）
- [x] 3.5.4 `index.html` 加 `jsonc.js` 的 script 标签；`assets.go` 的 embed 列表加 `assets/jsonc.js`；`assets_test.go` 的资源断言同步
- [x] 3.5.5 **高亮器也要认注释**：`highlight.js` 的 tokenizer 遇到 `//` / `/* */` 时输出 `tok-comment` token——**不处理会导致高亮错乱**（注释里的引号会被当字符串开始，后面整片内容跟着错）；`style.css` 补 `.tok-comment` 样式
- [x] 3.5.6 前端单测 `internal/web/jstest/jsonc.test.mjs`（11 条，与 Go 版一一对应）。**注意 Node 24 的 V8 错误格式有两种**：`Expected ... at position N`（带位置）与 `Unexpected token 'X', "..." is not valid JSON`（**只有片段、无位置**）→ 后者要用"出错字符在字符串字面量之外的第一次出现"来定位
- [x] 3.5.7 实测：网页打开带注释的 provider → **不报解析错误、保存按钮可用**；点保存 → 文件**逐字节不变**（`diff` 0 行，注释全在）

## 4. 文档

- [x] 4.1 `docs/provider-quota.md`：在「`quota` 块」一节补 **provider 配置支持 JSONC 注释**（`//` 与 `/* */`；注释在保存后保留；**不支持尾随逗号**；注释不进 Claude Code）
- [x] 4.2 `docs/provider-quota.md`：补**「怎么申请方舟 AK/SK」的操作步骤**（建只读 IAM 子账号 → 绑只读策略 → 建 AccessKey → 写进 `quota` 块），并写明**为什么用子账号而不是主账号**（查额度是纯只读需求，主账号 AK 是账号全权）+ **策略生效有 1~5 分钟延迟** + **子账号必须绑定到同一套餐主账号**
- [x] 4.3 补**划界说明**：`~/.claude/settings.json` 的注释**仍会丢**——网页 Claude 视图是原样写盘（能保留），但 CLI 的 `migrateSettingsJSON` 用 `json.MarshalIndent` 重写整个文件（必丢）。别让用户以为"ccl 全局支持注释"
