# `CCL_PROVIDER`：ccl 注入给消费方的 provider 名

> 状态：ccl v0.19.0 起生效。这份文档描述的是一个**对外契约**——
> 仓库外的程序可以依赖它。

## 一句话

ccl 每次启动 Claude Code 时，把**本次选中的 provider 的名字**放进环境变量
`CCL_PROVIDER`。仓库外的程序（最典型的是 Claude Code 的状态栏脚本）读它，
就知道"这个 Claude Code 会话是用哪个 provider 起的"。

```js
// ~/.claude/statusline.js
const provider = process.env.CCL_PROVIDER; // undefined = 这个会话不是 ccl 起的
```

## 为什么需要它

Claude Code 的 `statusLine` 脚本拿到的 stdin JSON 里**没有 provider 信息**
（只有 `model.display_name`、`workspace`、`cost`、`context_window`、
`rate_limits`、`vim.mode`、`worktree.name` 等）。Claude Code 内部也没有
"当前用了哪个 provider"的概念——provider 是 ccl 这一层的概念。

于是只剩环境变量一条路。

## 值的语义

**值 = provider 的 `Name` = provider 配置文件名去掉 `.json`。**

配置在 `~/.cc-launcher/providers/<name>.json`：

| 配置文件 | `CCL_PROVIDER` 的值 |
|---|---|
| `claude-official.json` | `claude-official` |
| `minimax-m3.json` | `minimax-m3` |
| `minimax-m3-stliu14.json` | `minimax-m3-stliu14` |

**不包含** provider 的其它信息：

- **不是 `note`**。`note` 是用户的个人备忘（实际样本里是 `"潘振兴"`、`"刘世通"`
  这样的人名），大多为空——它是备忘，不是标识。
- **不是 endpoint host**。多个 provider 可以连同一个 endpoint 但用不同账号
  （真实样本：三个 minimax provider 都连 `api.minimax.cn`，但是三个账号）。
  endpoint 区分不了账号，名字能。

值是**原值**：不 percent-encode、不转义、不截断、不拼接。

## 两条交付通道

| 通道 | 说明 |
|---|---|
| `--settings` 交付对象的 `env` 块 | **主通道**。命令行层在 Claude Code 的优先级栈第 2 层，压过用户 `~/.claude/settings.json` 的 `env` 块，也压过进程环境 |
| 进程环境 | **冗余第二通道**。万一上游某版不把 settings 的 `env` 块落到子进程环境，这一份还在 |

两条通道的值相同。真机实测（Claude Code 2.1.311，2026-11-05）：两条都能在
statusLine 子进程里读到。

## 缺席 ≠ 空串

- **变量不存在** → 这个会话**不是 ccl 启动的**（用户直接跑了 `claude`）。
- ccl **永远不会**交付空值。变量要么不存在，要么是一个非空的名字。

消费方据此决定"没有 provider 时不显示这一段"，而不是显示一个空白。

## 消费方的责任

provider 的名字来自文件名，理论上可以包含控制字符或终端转义序列。

- **ccl 侧不转义、不清洗**——这个变量表达的就是"provider 的名字"，ccl 不改写它。
- **消费方在渲染到终端前要剥掉 ANSI 转义序列与控制字符，并给显示长度设上限**。

状态栏是直接渲染到终端的，这一步不能省。

## 改名 = breaking change

`CCL_PROVIDER` 这个名字、以及它的值语义，都是对外契约。改名会**静默**弄坏
所有仓库外的消费方（它们只是读不到变量，不会报错）。

两条额外约束：

1. **名字不能带凭证特征**（`TOKEN` / `KEY` / `SECRET` / `PASSWORD`）。
   Claude Code 的 `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` 会把**凭证类**变量从
   子进程环境里删掉（真机实测：`ANTHROPIC_AUTH_TOKEN` 在 scrub 开启时被剥离，
   而 `CCL_PROVIDER` 不受影响）。带凭证特征的变量名会被误伤，特性静默失效。
2. **不能改值的语义**（比如改成拼上 endpoint、或改成 note）。

要改名的话，走一次 breaking change：写进 `openspec/changes/` 的 proposal，
在 CHANGELOG 里标 BREAKING。

## 它不承载 provider 的生效语义

provider 的**生效**靠的是 `--settings` 交付 provider 自己的 `env` 块
（见 `provider-settings-injection`）。`CCL_PROVIDER` 只是把名字告诉消费方，
与 provider 能不能生效无关。

想看 provider 实际连到哪，看 provider 配置里的 `ANTHROPIC_BASE_URL`。

## 同族的对外变量

`CCL_QUOTA_URL` 是**同族**的对外变量（ccl 注入给消费方的只读变量，同样是
"--settings 的 env 块 + 进程环境"两条通道、同样"ccl 的键赢"、同样改名 =
breaking change）：它把 daemon 上**只读额度端点**的地址交给消费方，让状态栏
能**零进程**读额度。

它的完整契约在 [`provider-quota.md`](provider-quota.md) 的
「[零进程读额度](provider-quota.md#零进程读额度daemon-的只读端点)」一节。

## 相关的外部行为

statusLine 子进程能看到什么环境变量，取决于 Claude Code 的子进程环境策略。
实测结论见 [`claude-code-settings-merge-semantics.md`](claude-code-settings-merge-semantics.md)
的"statusLine 子进程的环境"一节——其中最关键的一条是：**`OTEL_` 前缀的变量
会被剥离**，所以不能靠 `OTEL_RESOURCE_ATTRIBUTES` 里的 `ccl.provider` 属性来
显示 provider 名（ccl 那个通道是给 daemon 的数据链路用的，不是给显示用的）。

## 变更历史

| 版本 | 变化 |
|---|---|
| v0.19.0 | 首次交付 `CCL_PROVIDER` |
