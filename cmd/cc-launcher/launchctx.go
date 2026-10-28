package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"cc-launcher/internal/config"
	"cc-launcher/internal/envutil"
	"cc-launcher/internal/launchctx"
)

// injectLaunchContext 把本次启动的上下文（provider 名 / 工作目录 / 端点 host）
// 编码进 OTEL_RESOURCE_ATTRIBUTES，并把 OTel 端点接管一起打包成 Claude Code 的
// `--settings` 参数。
//
// 为什么要随 export 走：daemon 是独立常驻进程，它的环境描述的是"daemon 何时
// 启动"，不是"某个 export 属于哪个会话"。让上下文跟着每次 export 走，
// provider / cwd 才是**逐次 launch 的真值**。
//
// 为什么不写 ~/.claude/settings.json：settings.json 的 env 是全局的，会被所有
// claude 进程继承——直接 `claude` 启动的会话会继承"上一次 ccl 的 cwd"，
// 反而造出假数据。
//
// **两条通道**（launch-context-settings-injection）：
//
//  1. `--settings`（主通道）：Claude Code 的官方参数，优先级
//     **命令行 --settings > settings.json > 进程环境**（2026-10-28 实测）。
//     settings.json 的 env 块会覆盖进程环境——若只走进程环境，用户 settings.json
//     里已设的 OTEL_RESOURCE_ATTRIBUTES 会把注入整体顶掉（Windows 用户实测踩坑）。
//     走 --settings 就绕开了这一层。
//  2. 进程环境（冗余第二通道）：settings.json 没设该 key 时仍然生效；万一某个
//     Claude Code 版本不认 --settings，还有一道防线。
//
// 用户原值（settings.json 的 env.OTEL_RESOURCE_ATTRIBUTES）**合并**进注入值，
// 不替换——用户的属性不能因为 ccl 介入而丢失。
//
// otelEnv 是本次要交付给子进程的 OTel env 块（端点接管 + 补默认值，见
// envconfig.go 的 buildChildOtelEnv）。它和 launch 上下文**合并进同一个
// `--settings` 对象**——两个通道统一成一条。
//
// 返回：更新后的 optsEnv + additional settings 的 JSON（空串 = 不注入）。
func injectLaunchContext(cm *config.Manager, provider, cwd, baseURL string, optsEnv []string, otelEnv map[string]string) ([]string, string) {
	ctx := launchctx.Context{
		Provider:         provider,
		Cwd:              cwd,
		ProviderEndpoint: launchctx.EndpointHost(baseURL),
	}
	attrs := ctx.Attrs()

	// 交付的 env 块 = OTel 端点接管 + launch 上下文的 resource attributes。
	deliver := make(map[string]string, len(otelEnv)+1)
	for k, v := range otelEnv {
		deliver[k] = v
	}

	if len(attrs) > 0 {
		// 用户原值（settings.json 的 env 块）——合并进去，不能丢。
		// 注意：原值是**用户自己写的形态**（未编码），ccl 追加的部分才编码。
		userValue, _ := settingsResourceAttrsValue(cm)
		merged := launchctx.Merge(userValue, attrs)

		// 通道 2：进程环境。
		_ = os.Setenv(launchctx.EnvResourceAttributes, merged)
		if optsEnv != nil {
			// Unix 上 provider 选中时 opts.Env 非 nil（是启动时的 os.Environ() 快照），
			// launcher 直接把它交给 syscall.Exec——只改进程 env 对它无效。
			optsEnv = envutil.Upsert(optsEnv, launchctx.EnvResourceAttributes, merged)
		}
		deliver[launchctx.EnvResourceAttributes] = merged
	}

	// 通道 1：--settings（主通道）。
	settingsJSON, err := buildLaunchSettingsJSON(deliver)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ 构造 additional settings 失败（provider / 项目维度将为空）: %v\n", err)
		return optsEnv, ""
	}
	return optsEnv, settingsJSON
}

// buildLaunchSettingsJSON 把要交付的 env 块包成 Claude Code 的 additional settings。
//
// 形状固定为 `{"env":{...}}`——**只带 env 一个字段**，settings.json 的其它设置
// （permissions 等）不受影响（Claude Code 的 settings 是深合并，实测确认）。
//
// key 按字典序输出，保证同一份输入产出稳定的 JSON（便于测试与排查）。
func buildLaunchSettingsJSON(env map[string]string) (string, error) {
	envObj := make(map[string]any, len(env))
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		envObj[k] = env[k]
	}

	obj := map[string]any{"env": envObj}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // JSON 字符串里的 < > & 不必转义，可读性更好
	if err := enc.Encode(obj); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// settingsResourceAttrsValue 读 ~/.claude/settings.json 的 env 块里已设的
// OTEL_RESOURCE_ATTRIBUTES 值。
//
// 返回 ("", false) 表示没设 / 文件不存在 / 解析失败 / 值是空串——都不当成
// 冲突（只是没有可合并的用户原值）。
//
// 与 v0.15.0 时的同名检测用途不同：那时是"检出冲突 → 警告 + 降级"；现在
// --settings 通道优先级更高，不存在被封的情况，这个值只用来**合并**。
func settingsResourceAttrsValue(cm *config.Manager) (string, bool) {
	if cm == nil {
		return "", false
	}
	settings, err := cm.LoadSettings()
	if err != nil {
		return "", false
	}
	envAny, _ := settings["env"].(map[string]any)
	if envAny == nil {
		return "", false
	}
	v, ok := envAny[launchctx.EnvResourceAttributes]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	if s == "" {
		return "", false
	}
	return s, true
}
