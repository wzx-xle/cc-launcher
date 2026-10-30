package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"cc-launcher/internal/config"
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
// deliver 是调用方要交付给子进程的 env 块（provider 的 env + OTel 的 env，见
// envconfig.go 的 buildChildOtelEnv 与 main.go 的 loadProviderConfig）。它和 launch
// 上下文**合并进同一个 `--settings` 对象**——所有变量传递统一成一条通道。
//
// passthrough 是 provider 配置里要透传的顶层键（model / permissions / hooks …，
// 已排除 env 与 ccl 自己的元数据键）。它和 env 块一起构成交付对象。
//
// 返回：additional settings 的 JSON。
func injectLaunchContext(cm *config.Manager, provider, cwd, baseURL string, deliver map[string]string, passthrough map[string]any) (string, error) {
	ctx := launchctx.Context{
		Provider:         provider,
		Cwd:              cwd,
		ProviderEndpoint: launchctx.EndpointHost(baseURL),
	}
	attrs := ctx.Attrs()

	// 交付的 env 块 = 调用方给的（provider env + OTel env）+ launch 上下文。
	out := make(map[string]string, len(deliver)+1)
	for k, v := range deliver {
		out[k] = v
	}

	if len(attrs) > 0 {
		// 用户原值（settings.json 的 env 块）——合并进去，不能丢。
		// 注意：原值是**用户自己写的形态**（未编码），ccl 追加的部分才编码。
		userValue, _ := settingsResourceAttrsValue(cm)
		merged := launchctx.Merge(userValue, attrs)

		// 冗余第二通道：进程环境（万一某个 Claude Code 版本不认 --settings）。
		// launcher 在 opts.Env 为 nil 时用 os.Environ()（exec 时的实时值）→
		// 这里 os.Setenv 的值会进子进程 ✓
		_ = os.Setenv(launchctx.EnvResourceAttributes, merged)
		out[launchctx.EnvResourceAttributes] = merged
	}

	// 主通道：--settings。
	settingsJSON, err := buildLaunchSettingsJSON(out, passthrough)
	if err != nil {
		return "", fmt.Errorf("构造 additional settings 失败: %w", err)
	}
	return settingsJSON, nil
}

// buildLaunchSettingsJSON 把要交付的 env 块和 provider 的透传键包成 Claude Code 的
// additional settings。
//
// 形状：`{"env":{...}, ...provider 的透传键}`。
//
// settings.json 自己的其它设置（用户没让 provider 配的那些键）不受影响——
// Claude Code 的 settings 是深合并，`--settings` 里的键赢，用户没被覆盖的键
// 继续生效（provider-settings-passthrough）。
//
// **确定性**：`encoding/json` 对 map 的 key 按字典序输出，所以同一份输入永远
// 产出同一串 JSON —— 交付文件是按内容哈希命名的（settingsfile.go），内容不稳
// 就等于每次启动都新建一个缓存文件。
func buildLaunchSettingsJSON(env map[string]string, passthrough map[string]any) (string, error) {
	envObj := make(map[string]any, len(env))
	for k, v := range env {
		envObj[k] = v
	}

	obj := make(map[string]any, len(passthrough)+1)
	for k, v := range passthrough {
		obj[k] = v
	}
	// ccl 自己的键赢（provider 的透传键不覆盖 ccl 的交付）。
	obj["env"] = envObj

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
