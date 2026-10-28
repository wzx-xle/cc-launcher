package main

import (
	"testing"
)

// =============================================================================
// otel-endpoints-via-settings：交付策略 / 转发目标 / 遗留清理
// =============================================================================

// TestBuildChildOtelEnv_Takeover 断言两个端点键**总是**被接管。
func TestBuildChildOtelEnv_Takeover(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]any
	}{
		{"空 env", map[string]any{}},
		{"用户有远程端点", map[string]any{keyEndpoint: "https://company-otel:4317"}},
		{"用户有 per-signal 端点", map[string]any{keyMetricsEndpoint: "https://m:4318/v1/metrics"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildChildOtelEnv(tc.env)
			if got[keyEndpoint] != defaultEndpoint {
				t.Errorf("%s = %q, want %q", keyEndpoint, got[keyEndpoint], defaultEndpoint)
			}
			if got[keyMetricsEndpoint] != defaultMetricsEndpoint {
				t.Errorf("%s = %q, want %q", keyMetricsEndpoint, got[keyMetricsEndpoint], defaultMetricsEndpoint)
			}
		})
	}
}

// TestBuildChildOtelEnv_RespectsUserValue 断言用户自己写过的键**不被交付**
// （不交付 = settings.json 的值继续生效）。
func TestBuildChildOtelEnv_RespectsUserValue(t *testing.T) {
	env := map[string]any{
		keyMetricsExport:   "prometheus",
		keyProtocol:        "grpc",
		keyEnableTelemetry: "0",
		keyLogsExporter:    "otlp",
	}
	got := buildChildOtelEnv(env)
	for _, k := range []string{keyMetricsExport, keyProtocol, keyEnableTelemetry, keyLogsExporter} {
		if _, ok := got[k]; ok {
			t.Errorf("%s 用户自己写过，不该被交付, got %q", k, got[k])
		}
	}
	// 端点仍然接管
	if got[keyEndpoint] != defaultEndpoint {
		t.Errorf("%s 应被接管, got %q", keyEndpoint, got[keyEndpoint])
	}
}

// TestBuildChildOtelEnv_MissingGetsDefault 断言用户没写的键补默认值。
func TestBuildChildOtelEnv_MissingGetsDefault(t *testing.T) {
	got := buildChildOtelEnv(map[string]any{})
	want := map[string]string{
		keyEndpoint:        defaultEndpoint,
		keyMetricsEndpoint: defaultMetricsEndpoint,
		keyMetricsExport:   defaultMetricsExporter,
		keyProtocol:        defaultProtocol,
		keyEnableTelemetry: defaultEnableTelemetry,
	}
	if len(got) != len(want) {
		t.Fatalf("交付的键数 = %d, want %d (%v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// TestBuildChildOtelEnv_NeverDeliversLogs 断言 OTEL_LOGS_EXPORTER 永不交付。
func TestBuildChildOtelEnv_NeverDeliversLogs(t *testing.T) {
	got := buildChildOtelEnv(map[string]any{keyLogsExporter: "otlp"})
	if _, ok := got[keyLogsExporter]; ok {
		t.Errorf("%s 不该被交付, got %q", keyLogsExporter, got[keyLogsExporter])
	}
}

// TestResolveForwardTarget 断言转发目标的解析。
func TestResolveForwardTarget(t *testing.T) {
	t.Run("fallback 与 per-signal 各取各的", func(t *testing.T) {
		env := map[string]any{
			keyEndpoint:        "https://fallback:4317",
			keyMetricsEndpoint: "https://metrics:4318/v1/metrics",
		}
		fb, ps := resolveForwardTarget(env)
		if fb != "https://fallback:4317" {
			t.Errorf("fallback = %q", fb)
		}
		if ps != "https://metrics:4318/v1/metrics" {
			t.Errorf("per-signal = %q", ps)
		}
	})

	t.Run("没有端点 → 两个都空", func(t *testing.T) {
		fb, ps := resolveForwardTarget(map[string]any{})
		if fb != "" || ps != "" {
			t.Errorf("没有端点时应为空, got %q / %q", fb, ps)
		}
	})

	t.Run("遗留备份键优先于被改写的 live 键", func(t *testing.T) {
		env := map[string]any{
			keyEndpoint:                            defaultEndpoint,
			backupPrefixOrig + keyEndpoint:         "https://company-otel:4317",
			keyMetricsEndpoint:                     defaultMetricsEndpoint,
			backupPrefixForword + keyMetricsEndpoint: "https://metrics:4318/v1/metrics",
		}
		fb, ps := resolveForwardTarget(env)
		if fb != "https://company-otel:4317" {
			t.Errorf("fallback 应取备份键, got %q", fb)
		}
		if ps != "https://metrics:4318/v1/metrics" {
			t.Errorf("per-signal 应取备份键, got %q", ps)
		}
	})

	t.Run("非 string 值被忽略", func(t *testing.T) {
		env := map[string]any{keyEndpoint: 4318}
		fb, _ := resolveForwardTarget(env)
		if fb != "" {
			t.Errorf("非 string 值应被忽略, got %q", fb)
		}
	})
}

// TestCleanupLegacyOtelEnv 断言遗留清理的三条规则。
func TestCleanupLegacyOtelEnv(t *testing.T) {
	t.Run("备份值还原回 live 键", func(t *testing.T) {
		env := map[string]any{
			keyEndpoint:                    defaultEndpoint,
			backupPrefixOrig + keyEndpoint: "https://company-otel:4317",
		}
		got, changed := cleanupLegacyOtelEnv(env)
		if !changed {
			t.Fatal("应有变化")
		}
		if got[keyEndpoint] != "https://company-otel:4317" {
			t.Errorf("原值应被还原, got %v", got[keyEndpoint])
		}
		if _, ok := got[backupPrefixOrig+keyEndpoint]; ok {
			t.Errorf("备份键应被删除, got %v", got)
		}
	})

	t.Run("自我备份（备份值 == ccl 默认值）→ live 键也删", func(t *testing.T) {
		env := map[string]any{
			keyEndpoint:                    defaultEndpoint,
			backupPrefixOrig + keyEndpoint: defaultEndpoint,
		}
		got, changed := cleanupLegacyOtelEnv(env)
		if !changed {
			t.Fatal("应有变化")
		}
		if _, ok := got[keyEndpoint]; ok {
			t.Errorf("自我备份时 live 键应被删除, got %v", got)
		}
		if _, ok := got[backupPrefixOrig+keyEndpoint]; ok {
			t.Errorf("备份键应被删除, got %v", got)
		}
	})

	t.Run("自我备份但 live 被用户改过 → 保留用户的值", func(t *testing.T) {
		env := map[string]any{
			keyEndpoint:                    "https://my-own:4318",
			backupPrefixOrig + keyEndpoint: defaultEndpoint,
		}
		got, _ := cleanupLegacyOtelEnv(env)
		if got[keyEndpoint] != "https://my-own:4318" {
			t.Errorf("用户改过的值应保留, got %v", got[keyEndpoint])
		}
	})

	t.Run("无备份且值 == ccl 默认值 → 删除", func(t *testing.T) {
		env := map[string]any{
			keyMetricsExport: defaultMetricsExporter,
			keyProtocol:      defaultProtocol,
		}
		got, changed := cleanupLegacyOtelEnv(env)
		if !changed {
			t.Fatal("应有变化")
		}
		if _, ok := got[keyMetricsExport]; ok {
			t.Errorf("%s 应被删除, got %v", keyMetricsExport, got)
		}
		if _, ok := got[keyProtocol]; ok {
			t.Errorf("%s 应被删除, got %v", keyProtocol, got)
		}
	})

	t.Run("无备份且值 != ccl 默认值 → 保留", func(t *testing.T) {
		env := map[string]any{keyMetricsExport: "prometheus"}
		got, changed := cleanupLegacyOtelEnv(env)
		if changed {
			t.Error("不应有变化")
		}
		if got[keyMetricsExport] != "prometheus" {
			t.Errorf("用户值应保留, got %v", got[keyMetricsExport])
		}
	})

	t.Run("用户自有键与非 string 值不动", func(t *testing.T) {
		env := map[string]any{
			"ANTHROPIC_BASE_URL": "https://api.example.com",
			"OTEL_SOME_NUMBER":   4318,
		}
		got, changed := cleanupLegacyOtelEnv(env)
		if changed {
			t.Error("不应有变化")
		}
		if got["ANTHROPIC_BASE_URL"] != "https://api.example.com" {
			t.Errorf("用户键不该动, got %v", got)
		}
		if got["OTEL_SOME_NUMBER"] != 4318 {
			t.Errorf("非 string 值不该动, got %v", got)
		}
	})

	t.Run("幂等", func(t *testing.T) {
		env := map[string]any{
			keyEndpoint:                    defaultEndpoint,
			backupPrefixOrig + keyEndpoint: "https://company-otel:4317",
		}
		first, _ := cleanupLegacyOtelEnv(env)
		second, changed := cleanupLegacyOtelEnv(first)
		if changed {
			t.Errorf("第二次应无变化, got %v", second)
		}
		if len(second) != len(first) {
			t.Errorf("第二次应无变化: %v vs %v", first, second)
		}
	})

	t.Run("入参不被修改", func(t *testing.T) {
		env := map[string]any{
			keyEndpoint:                    defaultEndpoint,
			backupPrefixOrig + keyEndpoint: "https://company-otel:4317",
		}
		cleanupLegacyOtelEnv(env)
		if env[keyEndpoint] != defaultEndpoint {
			t.Errorf("入参不应被修改, got %v", env)
		}
		if _, ok := env[backupPrefixOrig+keyEndpoint]; !ok {
			t.Errorf("入参的备份键不应被删除, got %v", env)
		}
	})
}
