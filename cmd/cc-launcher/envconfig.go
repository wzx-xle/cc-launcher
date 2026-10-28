package main

import (
	"strings"
)

// OTel env key 集合 + ccl 自己的默认值。
//
// **交付通道**（otel-endpoints-via-settings）：6 个 key 全部通过 Claude Code 的
// `--settings` 参数的 env 块交付，ccl **不再写** ~/.claude/settings.json。
//
// 优先级（2026-10-28 真机实测）：**--settings > settings.json > 进程环境**；
// env 块是**逐键深合并**（不是整体替换）。于是有两个动作：
//
//   - "交付某个键" = 覆盖 settings.json 里的同名键 → **接管**
//   - "不交付某个键" = settings.json 里的值继续生效 → **尊重用户**
//
// "用户值"的定义域只有 settings.json 的 env 块，不含进程环境：旧实现把默认值
// 写进 settings 文件，settings 文件优先级高于进程环境，效果等同于"进程环境里
// 的值被默认值盖掉"；新实现直接在更高优先级的 --settings 里交付默认值，效果
// 一致，但**不碰用户的文件**。
const (
	keyEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	keyMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	keyLogsExporter    = "OTEL_LOGS_EXPORTER"
	keyMetricsExport   = "OTEL_METRICS_EXPORTER"
	keyProtocol        = "OTEL_EXPORTER_OTLP_PROTOCOL"
	keyEnableTelemetry = "CLAUDE_CODE_ENABLE_TELEMETRY"

	defaultEndpoint        = "http://localhost:4318"
	defaultMetricsEndpoint = "http://localhost:4318/v1/metrics"
	defaultMetricsExporter = "otlp"
	defaultProtocol        = "http/protobuf"
	defaultEnableTelemetry = "1"
)

// daemon 转发目标变量名。
//
// 这两个变量**只存在于 ccl 的进程环境**（fork daemon 时被子进程继承），不再写
// 进 settings.json。名字沿用旧版，是为了让 daemon 侧
// （internal/collector/forwarder.go）与 otel-receiver spec 一个字都不用改。
const (
	envForwardMetricsEndpoint = "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envOrigEndpoint           = "CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"
)

// 旧版 ccl 在 settings.json 里用的备份键前缀（**仅遗留清理用**）。
const (
	backupPrefixOrig    = "CC_ORIG_"
	backupPrefixForword = "FORWORD_"
)

// otelKeyPolicy 描述单个 OTel key 的交付策略。
type otelKeyPolicy struct {
	key          string
	defaultValue string // 缺失时交付的默认值；空 = 永不交付
	// takeover 为 true 时：不管用户有没有写，ccl 都交付 defaultValue（接管端点）。
	takeover bool
	// backupPrefix 是旧版 ccl 用的备份键前缀，仅遗留清理用。
	backupPrefix string
}

// otelKeyPolicies 是 6 个 key 的策略表。
//
//   - 两个端点键 takeover=true：ccl 必须把导出接到本地 daemon 才能统计用量。
//   - OTEL_LOGS_EXPORTER 的 defaultValue 为空 → **永不交付**：daemon 不解析
//     logs（隐私靠 daemon 架构保证），不主动改用户对 logs exporter 的选择。
//   - 其余三个（metrics exporter / protocol / telemetry 开关）不 takeover：
//     用户自己写了就尊重（不交付这个键），没写才补默认值。
var otelKeyPolicies = []otelKeyPolicy{
	{key: keyEndpoint, defaultValue: defaultEndpoint, takeover: true, backupPrefix: backupPrefixOrig},
	{key: keyMetricsEndpoint, defaultValue: defaultMetricsEndpoint, takeover: true, backupPrefix: backupPrefixForword},
	{key: keyLogsExporter, defaultValue: "", takeover: false, backupPrefix: backupPrefixOrig},
	{key: keyMetricsExport, defaultValue: defaultMetricsExporter, takeover: false, backupPrefix: backupPrefixOrig},
	{key: keyProtocol, defaultValue: defaultProtocol, takeover: false, backupPrefix: backupPrefixOrig},
	{key: keyEnableTelemetry, defaultValue: defaultEnableTelemetry, takeover: false, backupPrefix: backupPrefixOrig},
}

// buildChildOtelEnv 按交付策略算出要通过 `--settings` 交付给子进程的 env 块。
//
// 返回的 map **只含 ccl 要交付的键**：
//   - takeover 键：一定是 ccl 的默认值（接管）
//   - 非 takeover 键：用户自己写了 → 不出现（settings.json 的值生效）；没写 → 补默认值
//   - OTEL_LOGS_EXPORTER：永远不出现
//
// 入参是 settings.json 的 env 块（map[string]any，值不一定是 string）；只关心
// "键在不在"，不关心值的类型。
func buildChildOtelEnv(userEnv map[string]any) map[string]string {
	out := make(map[string]string, len(otelKeyPolicies))
	for _, p := range otelKeyPolicies {
		if p.defaultValue == "" {
			continue // OTEL_LOGS_EXPORTER：永不交付
		}
		if p.takeover {
			out[p.key] = p.defaultValue
			continue
		}
		if _, ok := userEnv[p.key]; ok {
			continue // 用户自己写了 → 尊重（不交付这个键）
		}
		out[p.key] = p.defaultValue
	}
	return out
}

// resolveForwardTarget 从 settings.json 的 env 里取出**用户自己的端点**，作为
// daemon 的转发目标。
//
// 返回 (fallback, perSignal)；空串 = 用户没有配这个端点（不设环境变量）。
//
// 遗留兼容：旧版 ccl 把 live 端点改写成 localhost，真正的用户原值被埋在
// `CC_ORIG_*` 备份键里。清理还没跑（或没跑成功）时，备份键才是真值 → **备份键
// 优先于 live 键**。
func resolveForwardTarget(userEnv map[string]any) (fallback string, perSignal string) {
	fallback = envString(userEnv, keyEndpoint)
	perSignal = envString(userEnv, keyMetricsEndpoint)

	if orig := envString(userEnv, backupPrefixOrig+keyEndpoint); orig != "" {
		fallback = orig
	}
	if orig := envString(userEnv, backupPrefixForword+keyMetricsEndpoint); orig != "" {
		perSignal = orig
	}
	return fallback, perSignal
}

// hasCclArtifact 判断 env 里有没有**旧版 ccl 留下的痕迹**。
//
// 痕迹有两种：
//   - `CC_ORIG_*` / `FORWORD_*` 备份键（旧版的备份机制留下的）
//   - live 兜底端点被改写成 `http://localhost:4318`（旧版的接管写入）
//
// 没有痕迹 → ccl **不许**动用户的文件（清理只在有痕迹时才动手）。
func hasCclArtifact(env map[string]any) bool {
	for k := range env {
		if strings.HasPrefix(k, backupPrefixOrig) || strings.HasPrefix(k, backupPrefixForword) {
			return true
		}
	}
	return envString(env, keyEndpoint) == defaultEndpoint
}

// cleanupLegacyOtelEnv 清掉旧版 ccl 留在 settings.json 里的痕迹，还原用户原值。
//
// 规则（对每个受管 key）：
//
//  1. 有备份键 `CC_ORIG_<KEY>` / `FORWORD_<KEY>`：
//     - 备份值 == ccl 自己的默认值 → 这是**自我备份**（旧版 ccl 给自己注入的
//       值做的备份），用户本来就没有这个键 → live 键也删掉
//     - 否则 → 备份值写回 live 键（还原用户原值）
//     - 备份键一律删除
//  2. 没有备份键、且 live 值 == ccl 自己的默认值 → 是旧版 ccl 注入的默认值
//     （用户从来没有过）→ 删除
//
// 其余键（用户的 ANTHROPIC_* / permissions / 非 string 值……）一律不动。
//
// 返回 (清理后的 env, 是否发生了变化)。入参不被修改。
func cleanupLegacyOtelEnv(env map[string]any) (map[string]any, bool) {
	out := make(map[string]any, len(env))
	for k, v := range env {
		out[k] = v
	}

	changed := false
	for _, p := range otelKeyPolicies {
		bk := p.backupPrefix + p.key
		backup, hasBackup := out[bk]
		live, hasLive := out[p.key]

		if hasBackup {
			delete(out, bk)
			changed = true
			backupStr, backupIsStr := backup.(string)
			if p.defaultValue != "" && backupIsStr && backupStr == p.defaultValue {
				// 自我备份：用户本来没有这个键。live 也正好是默认值 → 一起删；
				// live 被用户改过 → 保留用户的值。
				if hasLive && live == p.defaultValue {
					delete(out, p.key)
				}
				continue
			}
			// 还原用户原值
			out[p.key] = backup
			continue
		}

		// 无备份：live 等于 ccl 的默认值 → 是旧版注入的，用户从来没有过
		if p.defaultValue != "" && hasLive && live == p.defaultValue {
			delete(out, p.key)
			changed = true
		}
	}

	return out, changed
}

// envString 从 env map 里取字符串值；不存在或不是 string 时返回 ""。
func envString(env map[string]any, key string) string {
	if env == nil {
		return ""
	}
	s, _ := env[key].(string)
	return s
}
