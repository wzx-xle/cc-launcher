## Why

**provider 的 JSON 配置现在写不了注释，而配置里最该被解释的就是那些需要外部知识才能填对的字段。**

本机实测的例子：`ark-deepseek-flash` 的 `quota` 块里有 `accessKey` / `secretKey` 两个键——**去哪里申请、怎么建只读子账号、为什么要用子账号而不是主账号**，这些信息**只存在于人的脑子里**。配置里没有任何地方能记，换台机器、隔两周回来，全靠回忆。

而 JSON 的语法不允许注释——写进去就是解析失败，provider 直接加载不了。

**这不是"想要花哨功能"，是配置格式缺了一个记事的地方。** 而这类"配置旁边要贴一张说明"的需求会越来越多（厂商的特殊口径、额度接口的坑、企业网关的私有路径……）。

## What Changes

- **provider 配置支持 JSONC 注释**：`//` 行注释与 `/* */` 块注释，写在配置的任意位置
- **注释在加载、校验、网页编辑器保存后都保留**——关键在于保存走的是**原样写盘**（`SaveRaw` 不做 `MarshalIndent`），所以只要解析端容忍注释，注释就不会丢
- **注释不进 Claude Code**：透传给 Claude Code 的 settings 是 ccl **重新序列化**出来的，注释只活在 provider 自己的文件里
- **解析错误仍然指向原文件的行号**——剥离注释时用等长空白替换、保留换行，行号不会错位
- **不引第三方库**：ccl 只有 2 个直接依赖，是刻意保持轻量的；注释剥离器自己写（~80 行状态机）
- **不支持尾随逗号**：JSONC 规范（jsonc.org）本身不允许；本次只解决"注释"这一个明确诉求

## Capabilities

### New Capabilities

（无——这是既有配置能力的格式扩展）

### Modified Capabilities

- `provider-list-rendering`: 新增一条 requirement——provider 配置文件接受 JSONC 注释（`//` 与 `/* */`），注释在加载、校验与保存后保留，且解析错误仍指向原文件的位置

## Impact

**新增**

- `internal/jsonc/` —— 注释剥离器（状态机）+ 单测
- `internal/jsonc/jsonc.go` / `jsonc_test.go`

**修改**

- `internal/provider/provider.go` —— 4 处解析点（`List` ×2 / `Get` / `SaveRaw` 的校验）改用 JSONC 解析
- `internal/quota/refresh.go` —— 2 处解析点（枚举 / `LoadProviderRef`）改用 JSONC 解析
- `docs/provider-quota.md` —— 补「provider 配置支持注释」的说明 + 「怎么申请方舟 AK/SK」的操作步骤

**行为变更**

- 带注释的 provider 配置**从"加载失败"变成"正常加载"**（之前是 JSON 解析错误）
- 注释**不会**出现在 `ccl provider list` 的输出、**不会**传给 Claude Code（透传是重新序列化的）

**不涉及**

- 不改 `~/.claude/settings.json` 的解析（**它那边注释仍会丢**——`claudesupport.go` 用 `json.MarshalIndent` 重写整个文件；那是另一件事，见 design.md 的 D6）
- 不支持尾随逗号
- 不改 Claude Code 的任何文件格式
