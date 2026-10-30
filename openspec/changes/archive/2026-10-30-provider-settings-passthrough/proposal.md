## Why

provider 配置文件里的非 `env` 键（`model`、`permissions`、`hooks`、`outputStyle` …）目前**被静默丢弃**：ccl 只读取 `env` 块，交付给 Claude Code 的 settings 对象形状被硬编码成 `{"env":{…}}`。用户按 `requirement.md` 里"提供商配置要和 claude code 的本身的配置结构一样"去写这些键，既没有报错也没有警告，Claude Code 照常按默认跑——这是一个"配了但永远不生效"的陷阱。

同时，`--settings` 这条交付通道一旦失效（Claude Code 因 schema 拒绝而丢弃整个 settings 文件），ccl 会**静默退化**：Claude Code 继续用用户 `~/.claude/settings.json` 里的 `ANTHROPIC_*`，或退回它自己的登录态，ccl 选中的 provider 根本没生效，而 ccl 一个字都不说。这与项目"配了独立提供商就一定用它"的承诺直接冲突。

## What Changes

- **provider 配置的顶层非 `env` 键按 Claude Code settings 结构原样透传**到 `--settings` 交付对象。ccl 自己的元数据键（`note`、`order`）不透传。
- **交付前做 settings schema 校验**：provider 配置里出现已知键但类型不对时，ccl **在启动前报错退出**，不生成一个会被 Claude Code 丢弃整个文件的 settings 对象。校验在 ccl 侧完成（不依赖 Claude Code 的 stderr），因为 Unix 上 ccl 用 `syscall.Exec` 替换进程、没有观察 Claude Code 输出的时机。
- **`model` 键的优先级补丁**：Claude Code 中 `ANTHROPIC_MODEL` 环境变量压过任何文件里的 `model` 键。provider 携带 `model` 时，ccl 同步在进程环境中设置 `ANTHROPIC_MODEL`，否则 provider 指定的模型会被用户 shell 里的同名变量顶掉。
- **"provider 直接覆盖用户配置"从隐含行为变成显式契约**：provider 交付的键覆盖 `~/.claude/settings.json` 的同名键（`--settings` 位于优先级栈的命令行层，实测成立）；provider 未配置的键保持原状。ccl 不修改、不删除用户 settings 文件里的任何键。
- **通道可用性前置检查**：ccl 在启动前确认当前 Claude Code 支持 `--settings`；不支持时明确报错，而不是让 provider 静默失效。

不做（non-goals）：不改 Claude Code 的权限模式传递机制（继续用 `--permission-mode` flag）；不把 provider 配置的键写回 `~/.claude/settings.json`；不引入 `permissions.defaultMode` 走 `--settings` 的新路径；不改 `ccl provider add` 的交互式录入形态（新增键暂用手工编辑 provider 文件）。

## Capabilities

### New Capabilities
- `provider-settings-passthrough`: provider 配置的非 `env` 顶层键如何透传到 Claude Code 的 `--settings`，哪些键被排除，`model` 键与环境变量的优先级处理。
- `provider-settings-load-guard`: 交付前的 settings schema 校验，以及 Claude Code 不支持 `--settings` 时的明确失败语义（防止 provider 静默失效）。

### Modified Capabilities
- `claude-settings-delivery`: "交付的 settings 只含一个 env 块" 这条要求改为"含 env 块 + provider 透传键"，并明确其余非 env 设置仍然保留生效。
- `provider-settings-injection`: 覆盖语义从"仅 `env` 块内的键"扩展到"provider 配置的所有键"，并补上 `model` 键与 `ANTHROPIC_MODEL` 环境变量的优先级规则、加载失败时不得退化为用户自有 provider 的要求。

## Impact

- `internal/provider/provider.go`：provider 配置的读取需要保留除 `env` 之外的顶层键（当前 `Get` 返回 `map[string]any`，但 `providerEnvMap` 只取 `env`）。
- `cmd/cc-launcher/main.go`：`providerEnvMap` 拆成"env 键"与"透传键"两部分；`model` 键同步 setenv。
- `cmd/cc-launcher/launchctx.go`：`buildLaunchSettingsJSON` 的输出形状从固定 `{"env":{…}}` 改为"env 块 + provider 透传键"，并保留 key 排序的确定性。
- 新增 settings schema 校验（Claude Code 顶层键与类型白名单），以及 `--settings` 支持性的前置探测。
- `cmd/cc-launcher/settingsfile.go`：缓存文件名按内容哈希的机制不变，但内容形状变化会影响同名复用（内容不同 → 不同哈希 → 不同文件），无需改写机制。
- 风险：Claude Code 的 settings 顶层键有 170+ 个且随版本增删，白名单需要跟随版本演进；不认识的键按"静默忽略"处理（与 Claude Code 自身行为一致）。
