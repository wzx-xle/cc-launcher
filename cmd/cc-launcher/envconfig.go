package main

import (
	"fmt"
	"maps"
	"strings"
)

// OTel env key 集合。
//
// 决策（见 design.md §15-16）：
//   - 5 个 key 全备份（CC_ORIG_<KEY>）+ 注入默认值
//   - 用户原值一律尊重（备份即可）
//   - OTEL_LOGS_EXPORTER：缺失时不主动注入（隐私靠 daemon 不解析 logs 保证）
const (
	keyEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	keyLogsExporter   = "OTEL_LOGS_EXPORTER"
	keyMetricsExport  = "OTEL_METRICS_EXPORTER"
	keyProtocol       = "OTEL_EXPORTER_OTLP_PROTOCOL"
	keyEnableTelemetry = "CLAUDE_CODE_ENABLE_TELEMETRY"

	defaultEndpoint       = "http://localhost:4318"
	defaultMetricsExporter = "otlp"
	defaultProtocol        = "http/protobuf"
	defaultEnableTelemetry = "1"
	defaultLogsExporter   = "" // 不主动注入：缺失时不动；只备份用户已有值。
	// 决策 16：本地隐私靠 daemon 不解析 logs 保证，不强制覆盖 OTEL_LOGS_EXPORTER。
)

// otelEnvAction 描述单个 key 的处理策略。
type otelEnvAction struct {
	key             string
	defaultValue    string // 缺失时注入的值；空 = 不注入
	injectIfMissing bool   // 默认值非空时，是否缺失就注入
	override        bool   // 用户已有值时是否改写为默认值（默认 false = 备份并尊重）
}

// otelActions 5 个 key 的策略表。
//
// 注意：
//   - OTEL_EXPORTER_OTLP_ENDPOINT 必须 override=true：ccl 把 endpoint 指向本地 daemon
//     才能拦截 OTel 推送做 stats + forwarding。
//   - OTEL_LOGS_EXPORTER 的 defaultValue 为空 + injectIfMissing=false → 缺失时不注入。
//     决策 16 的精神：daemon 不解析 logs 已经保护隐私；不主动改用户行为。
var otelActions = []otelEnvAction{
	{key: keyEndpoint, defaultValue: defaultEndpoint, injectIfMissing: true, override: true},
	{key: keyLogsExporter, defaultValue: defaultLogsExporter, injectIfMissing: false, override: false},
	{key: keyMetricsExport, defaultValue: defaultMetricsExporter, injectIfMissing: true, override: false},
	{key: keyProtocol, defaultValue: defaultProtocol, injectIfMissing: true, override: false},
	{key: keyEnableTelemetry, defaultValue: defaultEnableTelemetry, injectIfMissing: true, override: false},
}

// backupKey 返回 "CC_ORIG_<KEY>"。
func backupKey(k string) string { return "CC_ORIG_" + k }

// isLocalhostEndpoint 判断 endpoint 是否指向 localhost（不拉 daemon 的判定）。
func isLocalhostEndpoint(v string) bool {
	return strings.Contains(v, "localhost") || strings.Contains(v, "127.0.0.1")
}

// backupAndInjectOTel 应用策略表到 env map，返回处理后的 map。
//
// 语义：
//   - 每个 key：
//     - 若 key 已存在于 env：
//       - 若 CC_ORIG_<KEY> 不存在 → 备份原值到 CC_ORIG_<KEY>
//       - 若 CC_ORIG_<KEY> 已存在 → 不动（避免覆盖用户备份）
//     - 若 key 不存在：
//       - 若 defaultValue 非空且 injectIfMissing → 注入默认值
//       - 否则不动
//   - 注入的默认值不会自动备份（因为用户从未设过该 key）
//
// 入参 env 可为 nil；返回的新 map 不影响入参（拷贝）。
func backupAndInjectOTel(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	maps.Copy(out, env)

	for _, act := range otelActions {
		bk := backupKey(act.key)
		_, hasOrig := out[bk]

		if cur, exists := out[act.key]; exists {
			// 已设：备份（如未备份）+ 按 override 决定是否改写。
			if !hasOrig {
				out[bk] = cur
			}
			if act.override {
				out[act.key] = act.defaultValue
			}
		} else {
			// 未设：注入默认值（如策略允许）。
			if act.injectIfMissing && act.defaultValue != "" {
				out[act.key] = act.defaultValue
			}
		}
	}

	return out
}

// restoreOTelEnv 反向操作：从 env map 移除 ccl 的痕迹，恢复原值。
//
// 语义（见 design.md §16 + spec "ccl restore"）：
//   - 对每个 CC_ORIG_<KEY>：写 <KEY> = CC_ORIG_<KEY> 值；删 CC_ORIG_<KEY>
//   - 对 5 个 key 中的"ccl 注入的默认值"（无对应 CC_ORIG_）：删除该 key
//     判断标准：CC_ORIG_<KEY> 不存在 → ccl 是注入的
//   - 其它 key：不动
//
// 返回处理后的新 map；不影响入参。
func restoreOTelEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	maps.Copy(out, env)

	for _, act := range otelActions {
		bk := backupKey(act.key)
		orig, hasOrig := out[bk]
		if hasOrig {
			// 有备份 → 还原原值 + 删备份。
			out[act.key] = orig
			delete(out, bk)
		} else {
			// 无备份 → 这 key 要么用户从未设过（ccl 注入的默认）、要么用户自有但 ccl 没动它。
			// 我们区分：如果是 ccl 注入的默认值就删；否则保留。
			cur, exists := out[act.key]
			if exists && cur == act.defaultValue {
				delete(out, act.key)
			}
			// 注意：如果用户自有值恰好等于 defaultValue（如 "otlp"），restore 会误删。
			// 决策：可接受；这是 restore 的"全清"语义（spec 明确）。
		}
	}

	return out
}

// formatOTelSummary 打印一段人类可读的描述（用于日志/帮助）。
func formatOTelSummary(env map[string]string) string {
	var lines []string
	for _, k := range []string{keyEndpoint, keyLogsExporter, keyMetricsExport, keyProtocol, keyEnableTelemetry} {
		if v, ok := env[k]; ok {
			lines = append(lines, fmt.Sprintf("  %s=%s", k, v))
		}
	}
	if len(lines) == 0 {
		return "  (none)"
	}
	return strings.Join(lines, "\n")
}