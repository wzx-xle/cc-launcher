package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cc-launcher/internal/config"
	"cc-launcher/internal/launchctx"
)

// newTestConfigMgr 在临时目录里造一个 config.Manager。
func newTestConfigMgr(t *testing.T, settings map[string]any) (*config.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backupDir := filepath.Join(dir, "backups")
	if settings != nil {
		data, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return config.NewManager(settingsPath, backupDir, config.DefaultMaxBackups), settingsPath
}

// TestInjectLaunchContext_WritesEnv 断言注入写进进程 env 与 optsEnv，
// 且值里含三个 ccl.* key。
func TestInjectLaunchContext_WritesEnv(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "")

	cm, _ := newTestConfigMgr(t, nil)
	optsEnv := []string{"PATH=/usr/bin"}
	got := injectLaunchContext(cm, "minimax", "/home/u/my project", "https://api.minimax.cn/anthropic", optsEnv)

	val := os.Getenv(launchctx.EnvResourceAttributes)
	if val == "" {
		t.Fatal("进程 env 未被注入")
	}
	for _, want := range []string{
		launchctx.KeyProvider + "=minimax",
		launchctx.KeyCwd + "=",
		launchctx.KeyProviderEndpoint + "=api.minimax.cn",
	} {
		if !strings.Contains(val, want) {
			t.Fatalf("注入值 %q 缺 %q", val, want)
		}
	}

	// optsEnv 快照里也必须有（Unix launcher 直接用它）
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, launchctx.EnvResourceAttributes+"=") {
		t.Fatalf("optsEnv 未被注入: %v", got)
	}
	if !strings.Contains(joined, launchctx.KeyProvider+"=minimax") {
		t.Fatalf("optsEnv 缺 provider: %v", got)
	}
}

// TestInjectLaunchContext_MergesExisting 断言用户已有的属性值被保留。
func TestInjectLaunchContext_MergesExisting(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "team=platform")

	cm, _ := newTestConfigMgr(t, nil)
	injectLaunchContext(cm, "minimax", "/home/u/proj", "", nil)

	val := os.Getenv(launchctx.EnvResourceAttributes)
	if !strings.Contains(val, "team=platform") {
		t.Fatalf("用户原值应保留: %q", val)
	}
	if !strings.Contains(val, launchctx.KeyProvider+"=minimax") {
		t.Fatalf("ccl 属性应存在: %q", val)
	}
}

// TestInjectLaunchContext_DoesNotTouchSettingsJSON 断言**不**往 settings.json 写
// ccl.* 或 OTEL_RESOURCE_ATTRIBUTES（spec §"the attributes are not written to
// the settings file"）。
func TestInjectLaunchContext_DoesNotTouchSettingsJSON(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "")

	cm, settingsPath := newTestConfigMgr(t, map[string]any{
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN": "sk-user-token",
		},
	})
	injectLaunchContext(cm, "minimax", "/home/u/proj", "https://api.minimax.cn", []string{"PATH=/usr/bin"})

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	envAny, _ := settings["env"].(map[string]any)
	for _, k := range []string{
		launchctx.KeyProvider,
		launchctx.KeyCwd,
		launchctx.KeyProviderEndpoint,
		launchctx.EnvResourceAttributes,
	} {
		if _, ok := envAny[k]; ok {
			t.Fatalf("settings.json 不应含 %q: %v", k, envAny)
		}
	}
	// 用户原有的 key 必须保留
	if envAny["ANTHROPIC_AUTH_TOKEN"] != "sk-user-token" {
		t.Fatalf("用户原有 env 被改动: %v", envAny)
	}
}

// TestInjectLaunchContext_SettingsOverrideDegrades 断言 settings.json 已设
// OTEL_RESOURCE_ATTRIBUTES 时不注入（降级为未知），并给出警告。
func TestInjectLaunchContext_SettingsOverrideDegrades(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "")

	cm, _ := newTestConfigMgr(t, map[string]any{
		"env": map[string]any{
			launchctx.EnvResourceAttributes: "team=platform",
		},
	})
	optsEnv := []string{"PATH=/usr/bin"}
	got := injectLaunchContext(cm, "minimax", "/home/u/proj", "", optsEnv)

	if v := os.Getenv(launchctx.EnvResourceAttributes); v != "" {
		t.Fatalf("settings.json 冲突时不应注入, got %q", v)
	}
	if strings.Join(got, "\n") != strings.Join(optsEnv, "\n") {
		t.Fatalf("optsEnv 不应被改动: %v", got)
	}
}
