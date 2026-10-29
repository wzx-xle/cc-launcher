package main

import (
	"testing"
)

// otel-endpoints-via-settings §5 端到端：场景 A 配置 → 交付 / 转发 / 遗留清理
//
// 场景 A = 典型公司远程 collector：
//   - 兜底端点 + per-signal metrics 端点都指向远程
//   - logs / traces 的 per-signal 端点也在（ccl 不该动）
//   - 有一个 ccl 不碰的用户自有键
func TestOtelEnv_ScenarioA_EndToEnd(t *testing.T) {
	original := map[string]any{
		"ANTHROPIC_API_KEY":                  "sk-fake",
		keyEndpoint:                          "http://remote:4318",
		keyMetricsEndpoint:                   "http://remote:4318/v1/metrics",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":   "http://remote:4318/v1/logs",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://remote:4318/v1/traces",
	}

	// 1. 交付给子进程的 env 块（--settings）
	deliver := buildChildOtelEnv(original)
	if deliver[keyMetricsEndpoint] != defaultMetricsEndpoint {
		t.Errorf("per-signal metrics 端点应被接管为 %q, got %q", defaultMetricsEndpoint, deliver[keyMetricsEndpoint])
	}
	// 兜底端点（总配置）不交付——logs / traces 不该被 ccl 劫持
	if _, ok := deliver[keyEndpoint]; ok {
		t.Errorf("兜底端点不该被交付, got %q", deliver[keyEndpoint])
	}
	// logs / traces 的 per-signal 端点不在 ccl 的管理范围 → 不交付、不动
	if _, ok := deliver["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"]; ok {
		t.Errorf("logs 端点不该被交付: %v", deliver)
	}
	if _, ok := deliver["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"]; ok {
		t.Errorf("traces 端点不该被交付: %v", deliver)
	}

	// 2. 转发目标 = 用户自己的端点（daemon 的进程环境变量）
	fallback, perSignal := resolveForwardTarget(original)
	if fallback != "http://remote:4318" {
		t.Errorf("转发 fallback 应为用户原值, got %q", fallback)
	}
	if perSignal != "http://remote:4318/v1/metrics" {
		t.Errorf("转发 per-signal 应为用户原值, got %q", perSignal)
	}

	// 3. 遗留清理：没有旧版痕迹 → 什么都不动
	if hasCclArtifact(original) {
		t.Errorf("场景 A 没有旧版痕迹, 不该被判定为有痕迹")
	}
	cleaned, changed := cleanupLegacyOtelEnv(original)
	if changed {
		t.Errorf("没有痕迹时不该有变化: %v", cleaned)
	}
	if cleaned["ANTHROPIC_API_KEY"] != "sk-fake" {
		t.Errorf("用户自有键不该动: %v", cleaned)
	}
}

// TestOtelEnv_ScenarioB_LegacyUpgrade 端到端：旧版 ccl 留下的痕迹 → 升级后清理。
//
// 旧版 ccl 跑过之后的 settings.json 长这样：
//   - 兜底端点被改写成 localhost:4318
//   - 用户的真原值被埋在 CC_ORIG_ / FORWORD_ 备份键里
//   - ccl 注入的默认值（exporter / protocol / telemetry 开关）在文件里
func TestOtelEnv_ScenarioB_LegacyUpgrade(t *testing.T) {
	legacy := map[string]any{
		"ANTHROPIC_API_KEY":                      "sk-fake",
		keyEndpoint:                              defaultEndpoint,
		backupPrefixOrig + keyEndpoint:           "http://remote:4318",
		keyMetricsEndpoint:                       defaultMetricsEndpoint,
		backupPrefixForword + keyMetricsEndpoint: "http://remote:4318/v1/metrics",
		keyMetricsExport:                         defaultMetricsExporter,
		keyProtocol:                              defaultProtocol,
		keyEnableTelemetry:                       defaultEnableTelemetry,
	}

	// 有旧版痕迹
	if !hasCclArtifact(legacy) {
		t.Fatal("应被判定为有旧版痕迹")
	}

	// 清理 → 还原用户原值、删掉 ccl 的注入
	cleaned, changed := cleanupLegacyOtelEnv(legacy)
	if !changed {
		t.Fatal("应有变化")
	}
	if cleaned[keyEndpoint] != "http://remote:4318" {
		t.Errorf("用户原值应被还原, got %v", cleaned[keyEndpoint])
	}
	if cleaned[keyMetricsEndpoint] != "http://remote:4318/v1/metrics" {
		t.Errorf("per-signal 原值应被还原, got %v", cleaned[keyMetricsEndpoint])
	}
	for _, k := range []string{keyMetricsExport, keyProtocol, keyEnableTelemetry} {
		if _, ok := cleaned[k]; ok {
			t.Errorf("ccl 注入的 %s 应被删除: %v", k, cleaned)
		}
	}
	if cleaned["ANTHROPIC_API_KEY"] != "sk-fake" {
		t.Errorf("用户自有键不该动: %v", cleaned)
	}

	// 清理之后再解析转发目标 → 仍是用户的原值
	fallback, perSignal := resolveForwardTarget(cleaned)
	if fallback != "http://remote:4318" {
		t.Errorf("转发 fallback = %q", fallback)
	}
	if perSignal != "http://remote:4318/v1/metrics" {
		t.Errorf("转发 per-signal = %q", perSignal)
	}

	// 清理之后再算交付块 → metrics 端点照样接管
	deliver := buildChildOtelEnv(cleaned)
	if deliver[keyMetricsEndpoint] != defaultMetricsEndpoint {
		t.Errorf("metrics 端点应被接管, got %q", deliver[keyMetricsEndpoint])
	}
	if _, ok := deliver[keyEndpoint]; ok {
		t.Errorf("兜底端点不该被交付, got %q", deliver[keyEndpoint])
	}
}
