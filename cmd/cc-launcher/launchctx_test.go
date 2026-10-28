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

// parseSettingsJSON 解析 additional settings JSON，取出 env.OTEL_RESOURCE_ATTRIBUTES。
func parseSettingsJSON(t *testing.T, s string) string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		t.Fatalf("additional settings 不是合法 JSON: %v (%s)", err, s)
	}
	env, _ := obj["env"].(map[string]any)
	if env == nil {
		t.Fatalf("additional settings 缺 env 块: %s", s)
	}
	v, _ := env[launchctx.EnvResourceAttributes].(string)
	return v
}

// TestInjectLaunchContext_WritesEnv 断言进程 env 与 optsEnv 都被注入了上下文。
func TestInjectLaunchContext_WritesEnv(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "")

	cm, _ := newTestConfigMgr(t, nil)
	optsEnv := []string{"PATH=/usr/bin"}
	got, _ := injectLaunchContext(cm, "minimax", "/home/u/proj", "https://api.minimax.cn/anthropic", optsEnv)

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
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, launchctx.EnvResourceAttributes+"=") {
		t.Fatalf("optsEnv 未被注入: %v", got)
	}
}

// TestInjectLaunchContext_BuildsSettingsJSON 断言产出的 additional settings
// 形状正确（只带 env 块），且值里含三个上下文属性。
func TestInjectLaunchContext_BuildsSettingsJSON(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "")

	cm, _ := newTestConfigMgr(t, nil)
	_, settingsJSON := injectLaunchContext(cm, "minimax", "/home/u/proj", "https://api.minimax.cn/anthropic", nil)
	if settingsJSON == "" {
		t.Fatal("未产出 additional settings JSON")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(settingsJSON), &obj); err != nil {
		t.Fatalf("JSON 非法: %v", err)
	}
	if len(obj) != 1 {
		t.Fatalf("additional settings 应只含 env 一个顶层字段, got %v", obj)
	}
	val := parseSettingsJSON(t, settingsJSON)
	for _, want := range []string{
		launchctx.KeyProvider + "=minimax",
		launchctx.KeyCwd + "=",
		launchctx.KeyProviderEndpoint + "=api.minimax.cn",
	} {
		if !strings.Contains(val, want) {
			t.Fatalf("additional settings 的值 %q 缺 %q", val, want)
		}
	}
}

// TestInjectLaunchContext_MergesUserValue 断言 settings.json 里的用户原值被
// **合并**进注入值（不替换）——用户的属性不能因为 ccl 介入而丢失。
//
// 这是 --settings 通道存在的核心原因之一：settings.json 已设
// OTEL_RESOURCE_ATTRIBUTES 时，进程环境那条通道会被顶掉，但 --settings 优先级更高。
func TestInjectLaunchContext_MergesUserValue(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "")

	cm, _ := newTestConfigMgr(t, map[string]any{
		"env": map[string]any{
			launchctx.EnvResourceAttributes: "team=platform",
		},
	})
	_, settingsJSON := injectLaunchContext(cm, "minimax", "/home/u/proj", "", nil)
	val := parseSettingsJSON(t, settingsJSON)

	if !strings.Contains(val, "team=platform") {
		t.Fatalf("用户原值应保留: %q", val)
	}
	if !strings.Contains(val, launchctx.KeyProvider+"=minimax") {
		t.Fatalf("ccl 属性应存在: %q", val)
	}
}

// TestInjectLaunchContext_UserKeyWinsOverStale 断言 ccl 的同名 key 以 ccl 为准。
func TestInjectLaunchContext_UserKeyWinsOverStale(t *testing.T) {
	t.Setenv(launchctx.EnvResourceAttributes, "")

	cm, _ := newTestConfigMgr(t, map[string]any{
		"env": map[string]any{
			launchctx.EnvResourceAttributes: launchctx.KeyProvider + "=stale",
		},
	})
	_, settingsJSON := injectLaunchContext(cm, "minimax", "/home/u/proj", "", nil)
	val := parseSettingsJSON(t, settingsJSON)

	if strings.Contains(val, "stale") {
		t.Fatalf("ccl 同名 key 应覆盖用户旧值: %q", val)
	}
	if !strings.Contains(val, launchctx.KeyProvider+"=minimax") {
		t.Fatalf("ccl 属性应存在: %q", val)
	}
}

// TestInjectLaunchContext_DoesNotTouchSettingsJSON 断言**不**往 settings.json 写
// ccl.* 或 OTEL_RESOURCE_ATTRIBUTES（spec §"the attributes are not written to the
// settings file"），且用户原有的 key 原样保留。
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
