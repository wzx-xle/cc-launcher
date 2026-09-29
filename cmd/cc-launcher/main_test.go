package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// backup-on-control-handover §3：完整 spec scenario 覆盖。

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
//
// 每个 subtest 对应 spec "claude-code-env-config" 的一个 Scenario。
// 用 t.Run 集中管理便于 spec 修订时定位测试。

func TestInjectOTelEnv_SpecScenarios(t *testing.T) {
	// §3.5 回归测试：Manager.Backup 对不存在的 settings.json 返回 nil 且不创建目录。
	// 提到最前面因为 §3.1 等 scenario 隐含依赖此契约。
	t.Run("backup no-op when settings.json missing", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "settings.json"), filepath.Join(dir, "backups"), 3)
		if err := cm.Backup(); err != nil {
			t.Fatalf("Backup on missing settings.json must be no-op, got err: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
			t.Errorf("backupDir should NOT be created when settings.json is missing (stat err=%v)", err)
		}
	})

	// §3.1：首次 ccl 启动，settings.json 健康基线被备份。
	t.Run("first-run backup captures user original", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		entries, err := os.ReadDir(filepath.Join(dir, "b"))
		if err != nil {
			t.Fatalf("readDir backupDir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 backup, got %d", len(entries))
		}
		data, _ := os.ReadFile(filepath.Join(dir, "b", entries[0].Name()))
		var backup map[string]any
		if err := json.Unmarshal(data, &backup); err != nil {
			t.Fatal(err)
		}
		env := backup["env"].(map[string]any)
		if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
			t.Errorf("backup must capture user original endpoint, got %v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
		}
	})

	// §3.2：ccl 已在控制中（每次常规启动），跳过 Backup。
	t.Run("in-control skips backup", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
			entries, _ := os.ReadDir(filepath.Join(dir, "b"))
			t.Errorf("backupDir must be absent (in-control skip), got %d entries (stat err=%v)", len(entries), err)
		}
	})

	// §3.3：第三方覆盖 OTel 端点但 CC_ORIG_* 残留，触发 Backup。
	// ccl 重新接管但不擦除 CC_ORIG_*（那是 restore 的工作）。
	t.Run("third-party overwrite triggers backup and keeps CC_ORIG_", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":                "http://other-tool:4318",
				"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT":         "https://company-otel:4317",
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		// 1. 触发了 Backup
		entries, err := os.ReadDir(filepath.Join(dir, "b"))
		if err != nil {
			t.Fatalf("readDir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 backup, got %d", len(entries))
		}
		// 备份内容反映"接管前"——三方覆盖态
		data, _ := os.ReadFile(filepath.Join(dir, "b", entries[0].Name()))
		var backup map[string]any
		json.Unmarshal(data, &backup)
		bEnv := backup["env"].(map[string]any)
		if bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://other-tool:4318" {
			t.Errorf("backup must capture pre-takeover state, got endpoint=%v", bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
		}

		// 2. ccl 接管后 CC_ORIG_* 仍在（restore 才清）
		settings, _ := cm.LoadSettings()
		curEnv := settings["env"].(map[string]any)
		if curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
			t.Errorf("ccl must take over endpoint, got %v", curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
		}
		if curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
			t.Errorf("CC_ORIG_ must NOT be erased by injectOTelEnv (that's restore's job), got %v", curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"])
		}
	})

	// §3.4：per-signal metrics 接管路径识别为 ccl 控制中。
	t.Run("per-signal metrics takeover is recognized as in-control", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"env": map[string]any{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://user-company:4317", // 兜底非 localhost
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318/v1/metrics", // per-signal 是 ccl 默认
			},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
			entries, _ := os.ReadDir(filepath.Join(dir, "b"))
			t.Errorf("backupDir must be absent (per-signal takeover → in-control), got %d entries", len(entries))
		}
	})

	// §3.7：settings.json 存在但无 env 字段（用户从未用 OTel）。
	// 也是控制权交接边界 → Backup 触发。
	t.Run("settings.json without env field triggers backup", func(t *testing.T) {
		clearOTelBackupEnvs(t)
		dir := t.TempDir()
		cm := config.NewManager(filepath.Join(dir, "s.json"), filepath.Join(dir, "b"), 3)
		cm.SaveSettings(map[string]any{
			"enabledPlugins": map[string]any{"some-plugin": true},
		})

		if err := injectOTelEnv(cm); err != nil {
			t.Fatalf("injectOTelEnv: %v", err)
		}

		entries, err := os.ReadDir(filepath.Join(dir, "b"))
		if err != nil {
			t.Fatalf("readDir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 backup (no env field = handover boundary), got %d", len(entries))
		}
	})
}

// backup-on-control-handover §3.6：Backup 失败仅警告，不阻断 ccl 主流程。
//
// 测试方式：os.Chmod(backupDir, 0) 制造不可写 + defer 恢复；断言
//   - injectOTelEnv 返回 nil（不向上传播 Backup 错误）
//   - SaveSettings 仍发生（OTel 注入仍完成）
//   - log 输出含警告行（用 log.SetOutput 重定向到 buffer）
//
// 平台限制：仅 Linux/macOS 生效；Windows 上 t.Skipf 跳过。
func TestInjectOTelEnv_BackupFailureOnlyWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("Backup 权限测试在 Windows 不可靠（chmod 语义差异）")
	}
	clearOTelBackupEnvs(t)

	dir := t.TempDir()
	backupDir := filepath.Join(dir, "b")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backupDir, 0); err != nil {
		t.Fatalf("chmod 0: %v", err)
	}
	if os.Geteuid() == 0 {
		_ = os.Chmod(backupDir, 0755)
		t.Skipf("root 用户不受 chmod 0 限制，跳过该测试")
	}
	t.Cleanup(func() { _ = os.Chmod(backupDir, 0755) })

	cm := config.NewManager(filepath.Join(dir, "s.json"), backupDir, 3)
	cm.SaveSettings(map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	})

	// 重定向 log 输出（log 包内部缓存了默认 stderr 的引用，
	// 替换 os.Stderr 变量无效，必须用 log.SetOutput）。
	var logBuf bytes.Buffer
	origLogOutput := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origLogOutput) })

	if err := injectOTelEnv(cm); err != nil {
		t.Errorf("injectOTelEnv must NOT return Backup error, got: %v", err)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "injectOTelEnv: 备份 settings.json 失败") {
		t.Errorf("log output should contain Backup warning, got: %q", logged)
	}

	// 验证 SaveSettings 仍发生（应用级备份 + endpoint 改写完成）
	settings, err := cm.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	env := settings["env"].(map[string]any)
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
		t.Errorf("OTel injection must complete despite Backup failure, got endpoint=%v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}