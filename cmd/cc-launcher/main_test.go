package main

import (
	"os"
	"path/filepath"
	"testing"

	"cc-launcher/internal/config"
)

// telemetry-config-compat §4.3

func TestInjectOTelEnv_FORWORD_Propagation(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 3)

	// 准备典型公司远程 collector 配置：用户设了 per-signal metrics endpoint
	initial := map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT":      "http://remote:4318",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://remote:4318/v1/metrics",
		},
	}
	if err := cm.SaveSettings(initial); err != nil {
		t.Fatal(err)
	}

	const fwdKey = "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	// 确保初始 env 是干净的，避免被其它测试污染
	t.Setenv(fwdKey, "")

	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	got := os.Getenv(fwdKey)
	if got != "http://remote:4318/v1/metrics" {
		t.Errorf("FORWORD_ env must be set to user original value: got %q", got)
	}
}

// telemetry-config-compat §4.4

func TestInjectOTelEnv_FORWORD_EdgeCases(t *testing.T) {
	const fwdKey = "FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"

	t.Run("user missing per-signal metrics → FORWORD_ not set", func(t *testing.T) {
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		// 仅设 fallback，无 per-signal metrics
		initial := map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://remote:4318",
			},
		}
		if err := cm.SaveSettings(initial); err != nil {
			t.Fatal(err)
		}
		t.Setenv(fwdKey, "")

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}
		if got := os.Getenv(fwdKey); got != "" {
			t.Errorf("FORWORD_ should NOT be set when user missing per-signal metrics: got %q", got)
		}
	})

	t.Run("pre-existing FORWORD_ env → overwritten with settings.json backup", func(t *testing.T) {
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		initial := map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://user-original:9999/v1/metrics",
			},
		}
		if err := cm.SaveSettings(initial); err != nil {
			t.Fatal(err)
		}
		// 预存一个不一样的值（模拟 daemon 已启动后残留）
		t.Setenv(fwdKey, "http://stale:1/v1/metrics")

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}
		if got := os.Getenv(fwdKey); got != "http://user-original:9999/v1/metrics" {
			t.Errorf("FORWORD_ must be overwritten with backup value: got %q", got)
		}
	})

	t.Run("idempotent — second call without daemon changes does not harm env", func(t *testing.T) {
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		initial := map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://user-original:9999/v1/metrics",
			},
		}
		if err := cm.SaveSettings(initial); err != nil {
			t.Fatal(err)
		}
		t.Setenv(fwdKey, "")

		// 第一次注入
		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("first injectOTelEnv: %v", err)
		}
		firstVal := os.Getenv(fwdKey)

		// 第二次注入（settings.json 没变 → injectOTelEnv 仍然写 env，幂等）
		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("second injectOTelEnv: %v", err)
		}
		secondVal := os.Getenv(fwdKey)
		if firstVal != secondVal {
			t.Errorf("FORWORD_ value changed between calls: %q → %q", firstVal, secondVal)
		}
		if firstVal != "http://user-original:9999/v1/metrics" {
			t.Errorf("FORWORD_ should hold user backup: %q", firstVal)
		}
	})
}