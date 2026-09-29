package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cc-launcher/internal/config"
)

// backup-on-control-handover §1.2 + §1.3：isUnderCclControl 主路径 + 边界 case。
func TestIsUnderCclControl(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]any
		want bool
	}{
		// §1.2 主路径：4 个 case
		{
			name: "fallback endpoint is ccl default → in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
			},
			want: true,
		},
		{
			name: "per-signal metrics endpoint is ccl default → in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318/v1/metrics",
			},
			want: true,
		},
		{
			name: "both endpoints are ccl defaults → in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318/v1/metrics",
			},
			want: true,
		},
		{
			name: "neither endpoint matches ccl defaults → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "https://company-otel:4317",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://other-tool:4318/v1/metrics",
			},
			want: false,
		},
		// §1.3 边界 case
		{
			name: "env is nil → NOT in control (treated as handover boundary)",
			env:  nil,
			want: false,
		},
		{
			name: "endpoint key absent → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
			},
			want: false,
		},
		{
			name: "endpoint value is nil → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": nil,
			},
			want: false,
		},
		{
			name: "endpoint value is non-string (int) → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": 4318,
			},
			want: false,
		},
		{
			name: "endpoint value is localhost:4318 but wrong scheme → NOT in control",
			env: map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://localhost:4318",
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isUnderCclControl(tc.env)
			if got != tc.want {
				t.Errorf("isUnderCclControl(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

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

// backup-on-control-handover §2.2 + §2.3：injectOTelEnv 条件 Backup 触发测试。
//
// `config.Manager` 是 concrete type 没有 interface 可 mock，所以用 t.TempDir() +
// os.ReadDir(backupDir) 在 injectOTelEnv 前后文件数对比作为间接证据。
// （控制下：文件数不变；不在控制：+1）

// clearOTelBackupEnvs 清理 injectOTelEnv 会 Setenv 的所有备份键，避免测试间污染。
func clearOTelBackupEnvs(t *testing.T) {
	t.Helper()
	keys := []string{
		"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT",
		"CC_ORIG_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"CC_ORIG_OTEL_LOGS_EXPORTER",
		"CC_ORIG_OTEL_METRICS_EXPORTER",
		"CC_ORIG_OTEL_EXPORTER_OTLP_PROTOCOL",
		"CC_ORIG_CLAUDE_CODE_ENABLE_TELEMETRY",
		"FORWORD_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"CC_FORWARD_AUTH_HEADER",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestInjectOTelEnv_BackupTriggeredWhenNotInControl(t *testing.T) {
	clearOTelBackupEnvs(t)

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 3)

	// settings.json 含用户原值（非 ccl 默认）→ 不在 ccl 控制下
	initial := map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	}
	if err := cm.SaveSettings(initial); err != nil {
		t.Fatal(err)
	}

	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	// 验证 backupDir 下出现新备份
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("readDir backupDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup file, got %d (entries=%v)", len(entries), entries)
	}

	// 备份内容应反映"接管前"的用户原值
	data, err := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var backup map[string]any
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatalf("backup json: %v", err)
	}
	env := backup["env"].(map[string]any)
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
		t.Errorf("backup should contain user original endpoint: got %v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

func TestInjectOTelEnv_BackupSkippedWhenInControl(t *testing.T) {
	clearOTelBackupEnvs(t)

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	cm := config.NewManager(settingsPath, backupDir, 3)

	// settings.json 已含 ccl 默认 endpoint → 在 ccl 控制下
	initial := map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		},
	}
	if err := cm.SaveSettings(initial); err != nil {
		t.Fatal(err)
	}

	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	// backupDir 应**不存在**——Backup 未被触发就没创建该目录。
	// 比"目录存在但文件数为 0"更准确：避免误判"Backup 创建了空目录"。
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(backupDir)
		t.Errorf("expected backupDir absent (ccl in control → skip), but exists with %d entries (err=%v)", len(entries), err)
	}
}