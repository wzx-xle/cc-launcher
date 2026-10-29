## Why

**provider 配置的优先级反了。** 现在 provider 的 env 走 **setenv（进程环境）**——而进程环境是 Claude Code 优先级**最低**的通道，被 `~/.claude/settings.json` 的 `env` 块压住：

> 你在 settings.json 里手写过任何 `ANTHROPIC_*`（公司模板 / 脚本 / 教程留下的），**你在 ccl 里选的 provider 就完全不生效**。

这跟"配一个独立提供商"的目的一开始就冲突 —— 配了等于没配。而 OTel 那套 key 走 `--settings`（优先级最高），ccl 说了算。**同一个 ccl 里两套语义相反的通道**，是这个 bug 的根源。

同时，Unix 上 `--settings <JSON>` 是**命令行参数**，`/proc/<pid>/cmdline` 权限 0444（同机任何用户可读）→ provider 的 `ANTHROPIC_AUTH_TOKEN` 会**明文进 argv**。所以 provider env 不能就这么塞进 `--settings` —— 得先解决载体问题。

## What Changes

### 1. provider env 改走 `--settings`，ccl 配置的赢

- provider 配置里的 `env` 块**合并进 `--settings` 的 `env` 块**，与 OTel key、launch 上下文**同一个 settings 对象**
- 语义：**ccl 配置里有的键 → ccl 赢**（覆盖 settings.json 的同名值）；**ccl 没配的键 → 不碰**（settings.json 的手写值继续生效）
- **删掉** provider env 的 setenv 注入（`opts.Env` 不再承载 provider 键）

### 2. `--settings` 的载体改成「可复用的常驻文件」（两个平台统一）

不再是"每次启动写一个临时文件、退出时删"（Unix 上 `syscall.Exec` 替换进程 → 根本没有"退出"那一刻 → 现在的 `Cleanup` 在 Unix 上**从来不执行**）。改成：

- 目录 `~/.cc-launcher/cache/`（0700）
- 文件名 `settings-<sha256(内容)[:16]>.json` —— **内容哈希命名**：
  - 同样的内容 → 同一个文件（**复用**）
  - 不同内容 → 不同文件（**并发时各读各的，不会串**）
  - 同名即同内容 → **写入幂等**：目标已存在且内容一致时直接复用，不重写（Windows 上也就不会撞上"文件被 claude 占用"）
- 权限：文件 0600；**原子写**（临时文件 + rename）
- **不删除**（常驻）；启动时清理该目录下 **mtime 超过 1 年** 的文件
- **两个平台同一套策略**（Windows 不再删文件、不再用 pid 唯一名）
- Unix 的 launcher **不用改**（不需要 Cleanup、不需要 fork+wait、不需要信号转发）

## Impact

- Affected specs: 新建 `provider-settings-injection`、`claude-settings-delivery`；改 `launcher-provider-env`、`provider-cwd-binding`
- Affected code: `cmd/cc-launcher/main.go`（provider env 改道）、`cmd/cc-launcher/launchctx.go`（合并交付）、`cmd/cc-launcher/settings_unix.go` + `settings_windows.go`（统一为常驻缓存文件）、新增 settings 载体模块
- Affected docs: **CLAUDE.md 约束 6**（"provider 配置通过 setenv 注入到子进程环境" → 改写）
- Unaffected: provider 配置文件格式、provider 选择/TUI 流程、`migrateSettingsJSON`（历史注入清理）、daemon 与转发链路、db schema / 看板
- 用户可感知：**选的 provider 一定生效**（不再被 settings.json 的手写值压住）；`ps aux` 不再能看到 provider 的 token；`~/.claude/settings.json` 里手写的 `ANTHROPIC_*` 被当前 provider 覆盖（**这是本程序的目的，不提示**）
