## 1. settings 载体（新增模块）

- [x] 1.1 新增 settings 载体模块：内容哈希命名（`settings-<sha256[:16]>.json`）+ `~/.cc-launcher/cache/`（0700）+ 文件 0600
- [x] 1.2 写入幂等：目标已存在且内容一致 → 复用不重写；否则原子写（`.tmp` + rename）
- [x] 1.3 启动时清理：cache 目录下 mtime > **1 年**的 `settings-*.json`（失败只警告）
- [x] 1.4 `settings_unix.go` / `settings_windows.go` 统一用该载体（Windows 删掉 pid 唯一名 + 删除逻辑）

## 2. provider env 改道

- [x] 2.1 `main.go`：provider 的 env 块从 `optsEnv`（进程环境）移出，改进交付 map
- [x] 2.2 `launchctx.go`：交付 map = provider env + OTel env + `OTEL_RESOURCE_ATTRIBUTES`（一个 settings 对象）
- [x] 2.3 语义核对：**ccl 配了的键赢；没配的键不碰**（settings.json / 进程环境继续生效）
- [x] 2.4 `opts.Env` 只保留父进程环境 + `OTEL_RESOURCE_ATTRIBUTES` 冗余通道

## 3. 测试

- [x] 3.1 载体单测：哈希命名、复用（幂等）、权限、原子写、清理阈值（1 年）
- [x] 3.2 交付单测：provider 键进交付 map、不进 optsEnv；ccl 配了的赢、没配的不出现
- [x] 3.3 `go test ./...` + `go test -tags smoke ./...` 全绿

## 4. 真机验证

- [x] 4.1 假 claude（pty 驱动 TUI）→ 抓 `--settings` 的**文件内容**：含 provider 的 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / `HTTP_PROXY`
- [x] 4.2 argv 里只有路径、**无明文 token**（`ps` / `/proc/<pid>/cmdline` 实证）
- [x] 4.3 真 claude + 假 API 服务器 → `--settings` 里的 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / `HTTP_PROXY` 全部生效（请求打到假服务器、auth 头正确、走代理）
- [x] 4.4 settings.json 里手写 `ANTHROPIC_BASE_URL=stale` → 实测**ccl 选的 provider 赢**
- [x] 4.5 并发：两个 ccl 不同 provider → 各读各的（不串）
- [x] 4.6 cache 目录：文件常驻、复用、mtime > 1 年被清

## 5. 文档与发布

- [x] 5.1 **CLAUDE.md 约束 6** 改写（setenv → `--settings` 交付 + 优先级语义）
- [x] 5.2 README：provider 注入通道章节 + "配置合并机制"章节重写
- [x] 5.3 CHANGELOG：新版本段
- [x] 5.4 `openspec archive provider-env-via-settings`
- [x] 5.5 打 tag + 一次推送
- [x] 5.6 bump scoop manifest
