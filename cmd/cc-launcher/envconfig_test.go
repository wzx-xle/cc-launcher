package main

import (
	"reflect"
	"testing"
)

func TestBackupAndInjectOTel_Missing(t *testing.T) {
	got := backupAndInjectOTel(map[string]string{})
	want := map[string]string{
		keyEndpoint:        defaultEndpoint,
		keyMetricsExport:   defaultMetricsExporter,
		keyProtocol:        defaultProtocol,
		keyEnableTelemetry: defaultEnableTelemetry,
		// keyLogsExporter 缺失时不注入（决策 16）
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missing env:\n got=%v\nwant=%v", got, want)
	}
}

func TestBackupAndInjectOTel_ExistingEndpoint(t *testing.T) {
	got := backupAndInjectOTel(map[string]string{
		keyEndpoint: "https://company-otel:4317",
	})
	// endpoint 备份 + 改写为 localhost
	want := map[string]string{
		keyEndpoint:          defaultEndpoint,
		backupKey(keyEndpoint): "https://company-otel:4317",
		keyMetricsExport:     defaultMetricsExporter,
		keyProtocol:          defaultProtocol,
		keyEnableTelemetry:   defaultEnableTelemetry,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("existing endpoint:\n got=%v\nwant=%v", got, want)
	}
}

func TestBackupAndInjectOTel_LogsRespected(t *testing.T) {
	// 用户设了 OTEL_LOGS_EXPORTER=otlp → 备份 + 保留
	got := backupAndInjectOTel(map[string]string{
		keyLogsExporter: "otlp",
	})
	want := map[string]string{
		keyEndpoint:             defaultEndpoint,
		keyMetricsExport:        defaultMetricsExporter,
		keyProtocol:             defaultProtocol,
		keyEnableTelemetry:      defaultEnableTelemetry,
		keyLogsExporter:         "otlp",
		backupKey(keyLogsExporter): "otlp",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("logs respected:\n got=%v\nwant=%v", got, want)
	}
}

func TestBackupAndInjectOTel_BackupNotOverwritten(t *testing.T) {
	// CC_ORIG_ 已存在 → 不覆盖
	got := backupAndInjectOTel(map[string]string{
		keyEndpoint:        "http://localhost:4318", // ccl 已经写过的
		backupKey(keyEndpoint): "https://original.com",
	})
	if got[backupKey(keyEndpoint)] != "https://original.com" {
		t.Errorf("backup should not be overwritten: %v", got)
	}
}

func TestBackupAndInjectOTel_PrometheusUser(t *testing.T) {
	// 用户设 OTEL_METRICS_EXPORTER=prometheus → 备份 + 保留
	got := backupAndInjectOTel(map[string]string{
		keyMetricsExport: "prometheus",
	})
	if got[keyMetricsExport] != "prometheus" {
		t.Errorf("prometheus should be respected, got %v", got)
	}
	if got[backupKey(keyMetricsExport)] != "prometheus" {
		t.Errorf("prometheus should be backed up: %v", got)
	}
}

func TestRestoreOTelEnv_RestoresOrig(t *testing.T) {
	env := map[string]string{
		keyEndpoint:             defaultEndpoint,
		backupKey(keyEndpoint):  "https://company-otel:4317",
		keyMetricsExport:        defaultMetricsExporter,
		keyProtocol:             defaultProtocol,
		keyEnableTelemetry:      defaultEnableTelemetry,
	}
	got := restoreOTelEnv(env)
	want := map[string]string{
		keyEndpoint: "https://company-otel:4317",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("restore:\n got=%v\nwant=%v", got, want)
	}
}

func TestRestoreOTelEnv_RemovesInjectedDefaults(t *testing.T) {
	// ccl 注入的默认值（无对应 backup）→ 删除
	env := map[string]string{
		keyMetricsExport:   defaultMetricsExporter, // "otlp" 是 ccl 默认值
		keyProtocol:        defaultProtocol,       // "http/protobuf" 是 ccl 默认值
		keyEnableTelemetry: defaultEnableTelemetry,
		// 用户的真值，不动
		"ANTHROPIC_API_KEY": "sk-fake",
	}
	got := restoreOTelEnv(env)
	if _, ok := got[keyMetricsExport]; ok {
		t.Errorf("injected metric exporter should be removed: %v", got)
	}
	if _, ok := got[keyProtocol]; ok {
		t.Errorf("injected protocol should be removed: %v", got)
	}
	if got["ANTHROPIC_API_KEY"] != "sk-fake" {
		t.Errorf("user key should not be touched: %v", got)
	}
}

func TestRestoreOTelEnv_Idempotent(t *testing.T) {
	// 跑两次 restore，第二次应是 no-op（map 完全一致）
	env := map[string]string{
		keyEndpoint:             "https://company-otel:4317",
		backupKey(keyEndpoint):  "https://orig.com",
		keyMetricsExport:        defaultMetricsExporter,
		keyProtocol:             defaultProtocol,
		keyEnableTelemetry:      defaultEnableTelemetry,
	}
	first := restoreOTelEnv(env)
	second := restoreOTelEnv(first)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("restore not idempotent: %v vs %v", first, second)
	}
}

func TestIsLocalhostEndpoint(t *testing.T) {
	cases := map[string]bool{
		"http://localhost:4318":      true,
		"http://127.0.0.1:4318":      true,
		"https://company.com":        false,
		"https://otel.example.com":   false,
		"":                           false,
	}
	for in, want := range cases {
		if got := isLocalhostEndpoint(in); got != want {
			t.Errorf("isLocalhostEndpoint(%q): want %v, got %v", in, want, got)
		}
	}
}

func TestBackupKey(t *testing.T) {
	if got := backupKey("FOO"); got != "CC_ORIG_FOO" {
		t.Errorf("got %q", got)
	}
}