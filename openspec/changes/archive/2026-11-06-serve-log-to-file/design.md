## Context

daemon 的启动方式：`forkDaemon` 用 `exec.Command(exe, "serve")` + detached
（Unix 用 Setpgid、Windows 用 CREATE_NEW_PROCESS_GROUP），**不继承任何终端**。

原实现 `cmd.Stdout = nil; cmd.Stderr = nil` → Go 的 exec 包会把子进程的 stdout/
stderr 接成 **os.DevNull** → 日志全丢。

## Decisions

### 决策 1：日志落 `~/.cc-launcher/serve.log`

- 位置：与其他状态同目录（用户知道在哪：`ccl serve --status` 也会提到它）
- 权限 0600（可能含本机路径、provider 名）
- **追加模式**，超过 2MB 截断（O_TRUNC）——日志是"出事时看"的，不是归档品

父进程在 `forkDaemon` 返回时 Close 自己那份句柄 → 子进程那份仍然有效（Unix fork
与 Windows CreateProcess 都复制句柄）。

### 决策 2：额度日志加 `[quota]` ASCII 标记

Windows 控制台把 Go 写的 UTF-8 按 GBK 解码 → 中文乱码。但**时间戳、provider
名、HTTP 状态码、数字都是 ASCII** → 只要有一个 ASCII 标记，就能用
`findstr` / `Select-String` 把相关行捞出来，而不必读懂中文。

### 决策 3：每轮打一行汇总

单看"失败了谁"看不出这一轮**查了几个**（是不是全查了）。汇总行给出四个数：
查了 / 不活跃跳过 / 未到期或退避跳过 / 失败——**这四个数直接对应"为什么
429"的几种可能**：

| 现象 | 结论 |
|---|---|
| 查了 8（= 全部） | 活跃判定没生效（老 daemon？）或 8 个都活跃 → 总量超限 |
| 查了 1-2 | 活跃过滤生效 ✓ |
| 失败 8 | 全被限流 |

## Risks / Trade-offs

- 日志文件会含本机路径与 provider 名 → 0600
- 日志文件增长 → 2MB 截断（只在**启动时**检查一次，运行中不再检查）
