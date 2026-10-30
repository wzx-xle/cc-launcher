package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/settingsschema"
)

// providerConfig 是选中的 provider 配置的解析结果。
type providerConfig struct {
	// Env 是 provider 的 env 块（值已统一转成字符串）。
	Env map[string]string
	// Passthrough 是要透传给 Claude Code 的顶层键（已排除 env 与 ccl 自己的
	// 元数据键 note / order）。
	Passthrough map[string]any
	// BaseURL 是 env 里的 ANTHROPIC_BASE_URL（用于算 launch 上下文的端点 host）。
	BaseURL string
	// Warnings 是校验过程中的非阻断提示。
	Warnings []string
}

// loadProviderConfig 读 provider 配置 → 校验 → 拆成 env 块与透传键。
//
// **校验在拆分之前**，因为 Claude Code 对 settings 的校验粒度是整个文件：
// 一个已知键的类型不对，Claude Code 会丢弃**整个** settings 文件，连 env
// 块里的 provider 凭证一起消失（provider-settings-load-guard）。
//
// 校验失败返回 error——调用方必须阻止启动，不要"警告一下继续跑"：
// 继续跑的结果是 Claude Code 静默退回用户自己的 provider，而用户以为
// 自己选中的 provider 生效了。
func loadProviderConfig(pm *provider.Manager, name string) (providerConfig, error) {
	out := providerConfig{
		Env:         map[string]string{},
		Passthrough: map[string]any{},
	}
	if name == "" {
		return out, nil
	}

	cfg, err := pm.Get(name)
	if err != nil {
		return out, err
	}

	// 顶层键的类型校验（env 块内部不校验：实测 Claude Code 会自己把非字符串
	// 的 env 值转成字符串，不会丢整个文件）。
	res := settingsschema.Validate(cfg)
	if !res.OK() {
		return out, fmt.Errorf("提供商 %q 的配置有误：\n  - %s\n请修正配置文件后重试（配置文件在 %s）",
			name, strings.Join(res.Errors, "\n  - "), pm.ProvidersDir())
	}
	out.Warnings = res.Warnings

	cleaned := res.Cleaned

	// env 块：值统一转成字符串（非字符串 → 警告，与 Claude Code 自己的行为一致）。
	if envAny, ok := cleaned["env"].(map[string]any); ok {
		for k, v := range envAny {
			s, exact := envValueToString(v)
			if !exact {
				out.Warnings = append(out.Warnings,
					fmt.Sprintf("env.%s 的值不是字符串，已转成 %q", k, s))
			}
			out.Env[k] = s
			if k == "ANTHROPIC_BASE_URL" {
				out.BaseURL = s
			}
		}
	}

	// 顶层键：除 env 与 ccl 元数据键外，全部透传。
	for k, v := range cleaned {
		if k == "env" || provider.IsReservedKey(k) {
			continue
		}
		out.Passthrough[k] = v
	}

	return out, nil
}

// envValueToString 把 env 块里的值转成字符串。
//
// 返回 (字符串, 是否本来就是字符串)。float64 用 'f' 格式（不是科学计数法）：
// 3000000 要写成 "3000000" 而不是 "3e+06"——环境变量里出现科学计数法是很怪的事。
func envValueToString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), false
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), false
	case json.Number:
		return x.String(), false
	case nil:
		return "", false
	default:
		return fmt.Sprintf("%v", x), false
	}
}

// modelFromPassthrough 取透传键里的 model（Claude Code 的 `model` 设置键）。
//
// 返回 ("", false) 表示 provider 没配 model，或配的值不是字符串。
func modelFromPassthrough(passthrough map[string]any) (string, bool) {
	v, ok := passthrough["model"]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}
