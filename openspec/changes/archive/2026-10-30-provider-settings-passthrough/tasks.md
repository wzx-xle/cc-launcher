## 1. provider 配置的键拆分

- [x] 1.1 `internal/provider`：provider 配置的顶层键读取（保留 `env` 之外的键），`Get` 返回值结构不变但新增"顶层键集合"入口
- [x] 1.2 排除清单：`note` / `order` 不参与透传（代码里留注释说明"新增 ccl 元数据键必须同步这里"）
- [x] 1.3 `cmd/cc-launcher/main.go`：`providerEnvMap` 拆成"env 键"与"透传键"两份产出，透传键值原样保留（不转类型）
- [x] 1.4 冲突仲裁：provider 透传键与 ccl 自己的键（OTel / 启动上下文）同名时，ccl 自己的赢

## 2. settings schema 校验

- [x] 2.1 新增 Claude Code settings 顶层键 → 类型白名单表（string/number/boolean/array/object），表头注明来源（官方 settings-reference）与抓取时的 CLI 版本
- [x] 2.2 校验器：已知键类型大类不符 → 报错（阻断）；对象字段类型不符 → 丢弃该字段 + 警告；已知数组里的坏元素 → 剔除该元素 + 警告；`env` 内非字符串值 → 转字符串 + 警告（实测 Claude Code 自己也这么转，不阻断）；未知键 → 放行
- [x] 2.3 校验器不改动通过校验的值与交付形状（除剔除坏数组元素）
- [x] 2.4 校验器单测：model 给对象 → 报错；env 值给数字 → 不阻断（转字符串 + 警告）；未知键 → 放行；`permissions.allow` 混入数字 → 剔除+警告；`permissions.allow` 整体给字符串 → 丢字段+警告不阻断；合法配置 → 原样通过
- [x] 2.5 白名单的可维护性：加"键数量/来源"的自检测试（防止手改坏表结构）

## 3. 交付对象构造

- [x] 3.1 `cmd/cc-launcher/launchctx.go`：`buildLaunchSettingsJSON` 从"只带 env"扩展为"env 块 + provider 透传键"，顶层键与 env 键都按字典序输出（内容确定性 → 哈希文件名稳定）
- [x] 3.2 `model` 键 → 作为 `ANTHROPIC_MODEL` 放进**交付的 env 块**（真机实测：只 setenv 进程环境会被用户 settings.json 的 env 块压掉）+ 进程环境冗余一份
- [x] 3.3 单测：透传键出现在交付对象里、`note`/`order` 不在、顺序稳定、model 键触发 setenv、provider 不配 model 时不 setenv

## 4. `--settings` 支持性探测

- [x] 4.1 探测函数：执行 `claude --help`，检查输出是否含 `--settings`
- [x] 4.2 缓存：按 claude 可执行文件的路径 + 大小 + mtime 缓存到 `~/.cc-launcher/cache/`，命中则跳过探测
- [x] 4.3 探测失败（不支持）→ 明确报错（说明 provider 配置无法交付、建议升级 Claude Code）并**不启动** claude
- [x] 4.4 探测单测：假 claude 脚本分别返回"支持/不支持"，验证缓存命中与未命中两条路径

## 5. 接线与失败语义

- [x] 5.1 `main.go` 接线：provider 配置 → 校验 → 构造交付对象 → 写 settings 文件 → 启动
- [x] 5.2 校验失败 / 探测失败 → 打印错误并以非零码退出（不启动 claude、不静默继续）
- [x] 5.3 进程环境里的 provider 副本**保留**（daemon 转发要用代理变量），但代码注释与文档措辞改为"仅供 daemon 转发，不是 provider 生效的保障"
- [x] 5.4 回归：`go test ./...` + `go test -tags smoke ./...` 全绿

## 6. CLI 辅助

- [x] 6.1 `ccl provider validate <name>` 子命令：对指定 provider 跑一遍校验并打印结果（不启动 claude）
- [x] 6.2 `ccl provider list` 里标注哪些 provider 含非 `env` 键（便于用户知道哪些 provider 会被透传）

## 7. 真机验证

- [x] 7.1 交付内容端到端验证：把交付逻辑抽成 `buildLaunchOptions` 后用 Go 测试跑完整链路（pty 驱动 TUI 在本机收不到按键，`script`/原生 pty 都不行 → 改用可测的函数边界）；断言交付文件含 provider 的 `env` 与顶层透传键、不含 `note`/`order`、argv 无明文凭证、文件权限 0600
- [x] 7.2 真 claude + 假 API 服务器：provider 的 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` 生效（请求打到假服务器、auth 头正确）
- [x] 7.3 真 claude：provider 顶层 `model` 键生效，且用户 shell 的 `ANTHROPIC_MODEL` **压不掉**它
- [x] 7.4 真 claude：`~/.claude/settings.json` 里的同名 `ANTHROPIC_*` **压不掉** provider（覆盖语义实测）
- [x] 7.5 真 claude：provider 配了类型错的键 → ccl 启动前就报错，**Claude Code 没有被启动**
- [x] 7.6 记录外部行为结论到 `docs/`（Claude Code settings 合并语义：列表并集、未知键静默、已知键类型错丢整个文件）

## 8. 文档与发布

- [x] 8.1 README：provider 文件格式章节（顶层键可透传 + 校验规则 + 排除清单）
- [x] 8.2 `CLAUDE.md` 约束 6 同步（provider 键的覆盖语义、model 键的环境变量规则）
- [x] 8.3 CHANGELOG：新版本段
- [ ] 8.4 `openspec archive provider-settings-passthrough`
- [ ] 8.5 打 tag + 一次推送
