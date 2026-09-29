//go:build smoke

// backup-on-control-handover §7.3-§7.6 sandbox smoke test。
//
// 在 sandbox tmpdir 上模拟真实 HOME，验证 backup 触发语义：
//   - §7.3 首跑 backup：pristine settings → ccl 启动 → 新增快照含原值
//   - §7.4 控制中跳过：第二次启动 → backups/ 文件数不变
//   - §7.5 第三方覆盖 backup：改 endpoint → ccl 启动 → 新增三方覆盖态快照
//   - §7.6 restore 不受影响：跑 ccl restore 逻辑 → settings.json 还原到接管前
//
// 不通过 ccl 二进制的 TUI（避免交互阻塞），直接调 main 包函数；
// 端到端路径与 ccl runTUI 调 injectOTelEnv 完全等价。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cc-launcher/internal/config"
)

// smokeSandbox 准备 sandbox HOME + ccl 配置目录，返回 settings / backups 路径。
func smokeSandbox(t *testing.T) (settingsPath, backupDir string) {
	t.Helper()
	dir := t.TempDir()
	settingsPath = filepath.Join(dir, ".claude", "settings.json")
	backupDir = filepath.Join(dir, ".cc-launcher", "backups")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatal(err)
	}
	return settingsPath, backupDir
}

func writeSettings(t *testing.T, path string, content map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSmoke_FirstRunBackup(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)

	// pristine settings.json 含用户原值（ccl 未接管）
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	})

	cm := config.NewManager(settingsPath, backupDir, 10)
	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	// §7.3 断言：backups/ 新增快照含原值
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("readDir backupDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup after first run, got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	var backup map[string]any
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatal(err)
	}
	env := backup["env"].(map[string]any)
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
		t.Errorf("first-run backup must capture user original endpoint, got %v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

func TestSmoke_InControlSkipsBackup(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)
	cm := config.NewManager(settingsPath, backupDir, 10)

	// 模拟"已注入"状态：endpoint 是 ccl 默认值
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		},
	})

	// 第一次 injectOTelEnv：应在控制下，不备份
	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("first injectOTelEnv: %v", err)
	}
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 0 {
		t.Errorf("in-control first run should NOT backup, got %d entries", len(entries))
	}

	// §7.4 第二次启动（仍控制中）→ 文件数应仍为 0
	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("second injectOTelEnv: %v", err)
	}
	entries, _ = os.ReadDir(backupDir)
	if len(entries) != 0 {
		t.Errorf("§7.4 in-control second run must NOT add backup, got %d entries", len(entries))
	}
}

func TestSmoke_ThirdPartyOverwriteTriggersBackup(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)
	cm := config.NewManager(settingsPath, backupDir, 10)

	// 模拟第三方覆盖：endpoint 是非 ccl 默认值 + CC_ORIG_* 残留
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://other-tool:4318",
			"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT": "https://company-otel:4317",
		},
	})

	if err := injectOTelEnv(cm); err != nil {
		t.Fatalf("injectOTelEnv: %v", err)
	}

	// §7.5 断言：backups/ 新增三方覆盖态快照
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("readDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup after third-party overwrite, got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	var backup map[string]any
	json.Unmarshal(data, &backup)
	bEnv := backup["env"].(map[string]any)
	if bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://other-tool:4318" {
		t.Errorf("backup must reflect pre-takeover state (third-party endpoint), got %v", bEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}

	// ccl 接管后 CC_ORIG_* 仍在
	settings, _ := cm.LoadSettings()
	curEnv := settings["env"].(map[string]any)
	if curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://localhost:4318" {
		t.Errorf("ccl must take over endpoint, got %v", curEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	if curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
		t.Errorf("CC_ORIG_ must NOT be erased by injectOTelEnv, got %v", curEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}

func TestSmoke_RestoreUnaffected(t *testing.T) {
	settingsPath, backupDir := smokeSandbox(t)
	cm := config.NewManager(settingsPath, backupDir, 10)

	// 模拟"已注入"状态：endpoint 是 ccl 默认值 + CC_ORIG_* 有原值
	writeSettings(t, settingsPath, map[string]any{
		"env": map[string]any{
			"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4318",
			"CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT":  "https://company-otel:4317",
		},
	})

	// §7.6 调 restoreOTelEnv（等价于 `ccl restore` 内部的 settings 重写）
	settings, err := cm.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	envAny := settings["env"].(map[string]any)
	envStr := make(map[string]string, len(envAny))
	for k, v := range envAny {
		if s, ok := v.(string); ok {
			envStr[k] = s
		}
	}
	restored := restoreOTelEnv(envStr)
	settings["env"] = restored
	if err := cm.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	// 验证 settings.json 等价于"首次 ccl 注入前 + restore 已清 CC_ORIG_ backup"
	final, _ := cm.LoadSettings()
	finalEnv := final["env"].(map[string]any)
	// restore 语义：CC_ORIG_<KEY> 还原到 <KEY>，删 CC_ORIG_*
	if finalEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://company-otel:4317" {
		t.Errorf("restore must restore user original to endpoint, got %v", finalEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	if _, ok := finalEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"]; ok {
		t.Errorf("CC_ORIG_ backup must be removed after restore, got %v", finalEnv["CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
}
