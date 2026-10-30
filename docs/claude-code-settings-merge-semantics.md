# Claude Code 的 `--settings` 合并语义（实测结论）

> 测于 **2026-10-30**，Claude Code CLI **2.1.308**（npm 包 `@anthropic-ai/claude-code`）。
> 方法：官方文档（code.claude.com/docs/en/settings、settings-reference、env-vars）
> + 本机真机实验（真 claude + 假 API 服务器 + `SessionStart` hook 观测进程环境）。
>
> **这份文档是 ccl 的 provider 交付机制的事实基础。** Claude Code 一升级，
> 这里的行为可能变——改 ccl 的交付逻辑前，先重跑本文档末尾的"复现步骤"。

## 优先级栈

从高到低（**环境变量不在这个栈里**）：

1. **Managed settings**（`managed-settings.json` / MDM / claude.ai 控制台）
2. **命令行 `--settings <file|json>`** ← ccl 用的就是这一层
3. Project local（`.claude/settings.local.json`）
4. Shared project（`.claude/settings.json`）
5. User（`~/.claude/settings.json`）

`--settings` 能设置 user settings 能设的任何键（约 173 个顶层键），**不能**设
managed 键与 `~/.claude.json` 的 global config 键。

## 合并规则

| 规则 | 行为 | 证据 |
|---|---|---|
| 列表键（`permissions.allow` 等） | **取并集**，不替换 | 文档明说 + 实测（项目级与 `--settings` 的 hook 数组两个都执行） |
| 标量键（`permissions.defaultMode` 等） | 整值取最高层 | 文档 |
| `env` 块 | **逐变量并集**，同名高优先级赢 | 实测：用户 settings.json 的 `API_TIMEOUT_MS=3000000` 与 `--settings` 的 `MY_PROBE_VAR` 同时在 |
| 未知顶层键 | **静默忽略**（不 warning、不报错） | 实测 + 源码（zod 顶层 `passthrough()`） |
| 已知键类型写错 | **整个 settings 文件被丢弃** | 实测（`These settings files did not load…`） |
| 条目级错误（数组里混入非字符串、未知 hook 事件名） | 只丢该条目，文件其余部分仍生效 | 实测 |
| `env` 块里的值不是字符串 | **不丢文件**，Claude Code 自己转成字符串写进进程环境 | 实测（`123` → `"123"`） |

列表键的例外：`fallbackModel`（有序链）、`modelPicker`、`availableModels`、
`modelSettings` 各自有特殊规则，不走并集。

## 环境变量的位置（最容易踩的地方）

**环境变量不是优先级栈的一层**，而是"按键配对"单独决定：

- `ANTHROPIC_MODEL` **压过任何文件里的 `model` 键**（含 `--settings`）。
- `ANTHROPIC_DEFAULT_MODEL` 只在**没有任何文件**设置 `model` 时才生效。
- 用户 `~/.claude/settings.json` 的 **`env` 块压过进程环境**里的同名变量。

推论（ccl 的 `model` 交付方式就是从这条推出来的）：

| 做法 | provider 的 model 能否生效 |
|---|---|
| 只透传顶层 `model` 键 | ❌ 被 `ANTHROPIC_MODEL` 压过 |
| 只 `os.Setenv("ANTHROPIC_MODEL", …)` | ❌ 被用户 settings.json 的 `env` 块压过 |
| 把 `ANTHROPIC_MODEL` 放进 `--settings` 的 **env 块** | ✅ 唯一同时压过用户 settings 文件与进程环境的通道 |

## ccl 的对应机制

| 外部事实 | ccl 的做法 |
|---|---|
| `--settings` 在优先级栈第 2 层 | provider 的 env 块与顶层键都走 `--settings` 交付（`cmd/cc-launcher/providerconfig.go`、`launchctx.go`） |
| 已知键类型错 → 整个文件被丢 | 交付前用 `internal/settingsschema` 校验，**类型错直接拒绝启动**（ccl 侧校验，Unix 上 `syscall.Exec` 之后没有观察 Claude Code 输出的时机） |
| 未知顶层键静默忽略 | 白名单外的键照常透传，不拦（避免 Claude Code 升级后 ccl 突然拒绝启动） |
| `ANTHROPIC_MODEL` 压过 `model` 键 | provider 的顶层 `model` → 作为 `ANTHROPIC_MODEL` 放进**交付的 env 块** |
| 用户 settings.json 的 env 块压过进程环境 | 进程环境里的 provider 副本**不是** provider 生效的保障；生效靠 `--settings` |
| 列表键取并集 | provider 的 `permissions.allow` 与用户的规则并存，不互相覆盖 |

## 复现步骤

```bash
# 1. 未知顶层键：静默忽略
echo '{"myCustomKey":"x"}' > /tmp/s.json
claude --settings /tmp/s.json -p "say PONG"        # 正常跑，无任何提示

# 2. 已知键类型错：整个文件被丢
echo '{"model": 123}' > /tmp/s.json
claude --settings /tmp/s.json -p "say PONG"        # These settings files did not load…

# 3. 条目级错误：只丢该条目
echo '{"hooks":{"settings.read":[{"hooks":[]}]}}' > /tmp/s.json
claude --settings /tmp/s.json -p "say PONG"        # Unknown hook event "settings.read" was ignored

# 4. env 块的值不是字符串：不丢文件，自己转成字符串
echo '{"env":{"PROBE":123}}' > /tmp/s.json
claude --settings /tmp/s.json -p "say PONG"        # 正常跑；PROBE=123

# 5. 优先级：--settings 的 env 块赢过用户 settings.json 的 env 块
#    （用 CLAUDE_CONFIG_DIR 隔离用户配置，别动真配置）
```

## 维护提醒

- `internal/settingsschema/table.go` 里的键表来自官方 settings-reference，
  **Claude Code 加了新键要补进去**（不补不会出事——未知键本来就放行——但补了能更早发现类型写错）。
- 改这张表前，先重跑上面的复现步骤确认外部行为没变。
