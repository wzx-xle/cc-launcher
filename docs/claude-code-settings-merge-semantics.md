# Claude Code 的 `--settings` 合并语义（实测结论）

> 测于 **2026-10-30**，Claude Code CLI **2.1.308**（npm 包 `@anthropic-ai/claude-code`）。
> 方法：官方文档（code.claude.com/docs/en/settings、settings-reference、env-vars）
> + 本机真机实验（真 claude + 假 API 服务器 + `SessionStart` hook 观测进程环境）。
>
> 「子进程的环境」一节另测于 **2026-11-05**，Claude Code CLI **2.1.311**
> （真 claude + pty 驱动 TUI + 探针 statusLine 脚本观测子进程环境）。
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

## 子进程的环境（statusLine / hooks / MCP 看到什么）

> 实测于 **2026-11-05**，Claude Code CLI **2.1.311**。方法：真 claude + pty
> 驱动 TUI + 探针 statusLine 脚本（脚本把自己的 `process.env` 写盘）。

Claude Code 启动的子进程（statusLine 命令、hooks、MCP server）能读到哪些
环境变量，直接决定"ccl 能把什么交给仓库外的消费方"。

| 来源 | 子进程能读到？ |
|---|---|
| `--settings` 交付对象的 `env` 块 | ✅ 能 |
| 父进程环境（ccl 的 `os.Setenv`） | ✅ 能 |
| **`OTEL_` 前缀的变量** | ❌ **被剥离** |

### `OTEL_` 前缀的剥离

- 是**前缀规则**，不是"含 OTEL 字样"：`OTELX_THING` 不受影响，`MY_OTEL_VAR`
  不受影响。
- 与 `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` **无关**——`SCRUB=0` 时照样被剥离。
- **推论**：ccl 现有的启动上下文通道（`OTEL_RESOURCE_ATTRIBUTES` 里的
  `ccl.provider` 属性）**不能**拿来给子进程消费方用。那个通道服务的是
  daemon 的数据链路（OTel 导出），不是显示。想让消费方知道 provider 名，
  必须用**专用变量名**——见 [`provider-name-env.md`](provider-name-env.md)。

### `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`

这个开关（默认启用，依赖 bubblewrap）会把**凭证类**变量从子进程环境里删掉：

| 变量 | `SCRUB=0` | `SCRUB=1` |
|---|---|---|
| `CCL_PROVIDER`（普通值） | 在 | **在** |
| `ANTHROPIC_AUTH_TOKEN`（凭证） | 在 | **被剥离** |

**推论**：ccl 注入给消费方的变量**不能带凭证特征**（`TOKEN` / `KEY` /
`SECRET` / `PASSWORD`）——否则会被误伤，特性静默失效。

### 凭证会到达子进程

`SCRUB=0`（或没开这个开关）时，**父进程环境里的凭证会到达 statusLine 子进程**
（实测：子进程读到了父进程的 `ANTHROPIC_AUTH_TOKEN`）。

也就是说 `statusLine` 命令（和 hooks、MCP server）能看到用户 shell 里的
凭证。**别运行来路不明的状态栏脚本**——它能读到你的 token。开
`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` 可以挡住这一层（代价是需要 bubblewrap）。

### statusLine 命令本身

- 用 **`/bin/sh -c "<command>"`** 执行（本机 `/bin/sh` → dash），所以 inline
  命令只能写 POSIX sh 语法（`[[ ]]`、数组、`source` 都会炸）。写成
  `node xxx.js` 这样的外部脚本则无此问题。
- 每次刷新都**新起一个进程**，`process.env` 现读现用 → 消费方不能把这个值
  缓存到会话级。
- Claude Code 会给它设 `COLUMNS` / `LINES`，不能用 `tput cols`。
- settings 的 `env` 块**不做 shell 展开**（`~`、`$HOME` 保持字面）。

### 复现步骤

```bash
# 探针：把子进程环境写盘
cat > /tmp/probe.sh <<'EOF'
#!/bin/sh
env | grep -Ei 'ccl|otel|anthropic' | sort > /tmp/envdump.txt
EOF
chmod +x /tmp/probe.sh

cat > /tmp/s.json <<'EOF'
{"env":{"CCL_PROVIDER":"claude-official","OTEL_SERVICE_NAME":"svc"},
 "statusLine":{"type":"command","command":"/tmp/probe.sh"}}
EOF

claude --settings /tmp/s.json    # 在已信任目录里跑（陌生目录会先弹信任提示）
cat /tmp/envdump.txt
# → CCL_PROVIDER 在，OTEL_SERVICE_NAME 不在
```

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
- 「子进程的环境」一节（`OTEL_` 剥离、scrub 剥凭证）同样是外部行为，
  **Claude Code 一升级要重验**——`CCL_PROVIDER` 这个对外契约的地基就在那儿
  （见 [`provider-name-env.md`](provider-name-env.md)）。
