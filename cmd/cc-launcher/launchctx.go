package main

import (
	"fmt"
	"os"

	"cc-launcher/internal/config"
	"cc-launcher/internal/envutil"
	"cc-launcher/internal/launchctx"
)

// injectLaunchContext 把本次启动的上下文（provider 名 / 工作目录 / 端点 host）
// 编码进子进程环境的 OTEL_RESOURCE_ATTRIBUTES（web-usage-stats-ui §D15）。
//
// 为什么要随 export 走：daemon 是独立常驻进程，它的环境描述的是"daemon 何时
// 启动"，不是"某个 export 属于哪个会话"。让上下文跟着每次 export 走，
// provider / cwd 才是**逐次 launch 的真值**。
//
// 为什么不写 ~/.claude/settings.json：settings.json 的 env 是全局的，会被所有
// claude 进程继承——直接 `claude` 启动的会话会继承"上一次 ccl 的 cwd"，
// 反而造出假数据。
//
// 写两处的理由（两者都得写，缺一不可）：
//   - optsEnv：Unix 上 provider 选中时 opts.Env 非 nil（是启动时的
//     os.Environ() 快照），launcher 直接把它交给 syscall.Exec——只改进程 env
//     对它无效
//   - 进程 env（os.Setenv）：覆盖 opts.Env == nil 的分支，以及 Windows 上
//     cmd.Env == nil 走父进程 env 的路径
//
// 返回更新后的 optsEnv（原样返回时表示不需要注入）。
func injectLaunchContext(cm *config.Manager, provider, cwd, baseURL string, optsEnv []string) []string {
	// settings.json 的 env 优先级**高于**进程环境。若用户已在那里设了
	// OTEL_RESOURCE_ATTRIBUTES，我们的注入会被整体顶掉 → 检出后警告并降级
	//（provider / cwd 显示未知），**不擅自改**用户的 settings.json。
	if settingsHasResourceAttrs(cm) {
		fmt.Fprintf(os.Stderr,
			"⚠ 检测到 %s 已设置 %s：settings.json 的 env 优先级高于进程环境，会覆盖 ccl 注入的启动上下文\n"+
				"  → provider / 项目维度将为空（显示未知）。请从 settings.json 移除该 key 后重试。\n",
			settingsPathForMessage(cm), launchctx.EnvResourceAttributes)
		return optsEnv
	}

	ctx := launchctx.Context{
		Provider:         provider,
		Cwd:              cwd,
		ProviderEndpoint: launchctx.EndpointHost(baseURL),
	}
	attrs := ctx.Attrs()
	if len(attrs) == 0 {
		return optsEnv
	}

	// 合并用户已有的 OTEL_RESOURCE_ATTRIBUTES（用户原值保留，ccl 同名 key 以 ccl 为准）。
	merged := launchctx.Merge(os.Getenv(launchctx.EnvResourceAttributes), attrs)

	// 进程 env：覆盖 opts.Env == nil 分支与 Windows 的 cmd.Env == nil 路径。
	_ = os.Setenv(launchctx.EnvResourceAttributes, merged)

	// optsEnv 快照：Unix 上 opts.Env 非 nil 时 launcher 直接用它，必须显式写进去。
	if optsEnv != nil {
		optsEnv = envutil.Upsert(optsEnv, launchctx.EnvResourceAttributes, merged)
	}
	return optsEnv
}

// settingsHasResourceAttrs 报告 ~/.claude/settings.json 的 env 块里是否已设
// OTEL_RESOURCE_ATTRIBUTES。
//
// 读不出来（文件不存在 / 解析失败）→ false（不当成冲突，让正常注入路径走）。
func settingsHasResourceAttrs(cm *config.Manager) bool {
	if cm == nil {
		return false
	}
	settings, err := cm.LoadSettings()
	if err != nil {
		return false
	}
	envAny, _ := settings["env"].(map[string]any)
	if envAny == nil {
		return false
	}
	v, ok := envAny[launchctx.EnvResourceAttributes]
	if !ok {
		return false
	}
	s, _ := v.(string)
	return s != ""
}

// settingsPathForMessage 返回 settings.json 路径（仅用于错误提示文本）。
func settingsPathForMessage(cm *config.Manager) string {
	if cm == nil {
		return "~/.claude/settings.json"
	}
	if p, err := config.DefaultSettingsPath(); err == nil {
		return p
	}
	return "~/.claude/settings.json"
}
