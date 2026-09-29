package main

import (
	"reflect"
	"testing"
)

// telemetry-config-compat §5.4 端到端：场景 A 配置 → 注入 → 还原 → 等价于初始用户配置

func TestRestoreOTelEnv_ScenarioA_EndToEnd(t *testing.T) {
	const fwdKey = "FORWORD_" + keyMetricsEndpoint

	// 初始用户配置（典型公司远程 collector）
	original := map[string]string{
		"ANTHROPIC_API_KEY":                     "sk-fake",
		"OTEL_EXPORTER_OTLP_ENDPOINT":           "http://remote:4318",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":   "http://remote:4318/v1/metrics",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":      "http://remote:4318/v1/logs",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":    "http://remote:4318/v1/traces",
	}

	// 1. 注入（模拟 ccl 启动）
	injected := backupAndInjectOTel(original)

	if injected["CC_ORIG_"+keyEndpoint] != "http://remote:4318" {
		t.Errorf("CC_ORIG endpoint backup missing: %v", injected)
	}
	if injected[fwdKey] != "http://remote:4318/v1/metrics" {
		t.Errorf("FORWORD_ backup missing: %v", injected)
	}
	if injected[keyMetricsEndpoint] != defaultMetricsEndpoint {
		t.Errorf("per-signal metrics endpoint should be overwritten: %v", injected)
	}
	if injected[keyEndpoint] != defaultEndpoint {
		t.Errorf("fallback endpoint should be overwritten: %v", injected)
	}
	if injected["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"] != "http://remote:4318/v1/logs" {
		t.Errorf("logs endpoint should NOT be touched: %v", injected)
	}
	if injected["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] != "http://remote:4318/v1/traces" {
		t.Errorf("traces endpoint should NOT be touched: %v", injected)
	}

	// 2. 还原（模拟 ccl restore）
	restored := restoreOTelEnv(injected)

	if _, ok := restored["CC_ORIG_"+keyEndpoint]; ok {
		t.Errorf("CC_ORIG_ should be deleted after restore: %v", restored)
	}
	if _, ok := restored[fwdKey]; ok {
		t.Errorf("FORWORD_ should be deleted after restore: %v", restored)
	}
	if restored[keyEndpoint] != "http://remote:4318" {
		t.Errorf("fallback endpoint should be restored: %v", restored)
	}
	if restored[keyMetricsEndpoint] != "http://remote:4318/v1/metrics" {
		t.Errorf("per-signal metrics endpoint should be restored: got %q want %q",
			restored[keyMetricsEndpoint], "http://remote:4318/v1/metrics")
	}
	if restored["ANTHROPIC_API_KEY"] != "sk-fake" {
		t.Errorf("user-owned key should NOT be touched: %v", restored)
	}
	if restored["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"] != "http://remote:4318/v1/logs" {
		t.Errorf("logs endpoint should still be user value: %v", restored)
	}
	if restored["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] != "http://remote:4318/v1/traces" {
		t.Errorf("traces endpoint should still be user value: %v", restored)
	}
	if _, ok := restored[keyMetricsExport]; ok {
		t.Errorf("ccl-injected %s should be removed: %v", keyMetricsExport, restored)
	}

	// 3. 关键：restored 必须等价于 original（除了 ccl 注入的默认值被删除）
	if !reflect.DeepEqual(restored[keyEndpoint], original[keyEndpoint]) {
		t.Errorf("endpoint mismatch after restore: got %q want %q", restored[keyEndpoint], original[keyEndpoint])
	}
	if !reflect.DeepEqual(restored[keyMetricsEndpoint], original[keyMetricsEndpoint]) {
		t.Errorf("metrics endpoint mismatch after restore: got %q want %q",
			restored[keyMetricsEndpoint], original[keyMetricsEndpoint])
	}
}