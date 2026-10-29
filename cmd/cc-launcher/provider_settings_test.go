package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cc-launcher/internal/provider"
)

// newTestProviderMgr 造一个临时 providers 目录的 provider.Manager。
func newTestProviderMgr(t *testing.T, files map[string]any) *provider.Manager {
	t.Helper()
	dir := t.TempDir()
	for name, cfg := range files {
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return provider.NewManager(dir)
}

// TestProviderEnvMap 断言 provider 的 env 块被完整读出。
func TestProviderEnvMap(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"deepseek": map[string]any{
			"note": "深度求索",
			"env": map[string]any{
				"ANTHROPIC_BASE_URL":   "https://api.deepseek.com/anthropic",
				"ANTHROPIC_AUTH_TOKEN": "sk-ds",
				"HTTP_PROXY":           "http://127.0.0.1:7890",
			},
		},
	})

	env, baseURL := providerEnvMap(pm, "deepseek")
	if baseURL != "https://api.deepseek.com/anthropic" {
		t.Errorf("baseURL = %q", baseURL)
	}
	for k, want := range map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.deepseek.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "sk-ds",
		"HTTP_PROXY":           "http://127.0.0.1:7890",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
}

// TestProviderEnvMap_EdgeCases 断言异常输入不炸。
func TestProviderEnvMap_EdgeCases(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"noenv": map[string]any{"note": "没有 env 块"},
		"weird": map[string]any{"env": map[string]any{"A": 42, "B": "ok"}},
	})

	env, baseURL := providerEnvMap(pm, "noenv")
	if len(env) != 0 || baseURL != "" {
		t.Errorf("没有 env 块时应为空: %v / %q", env, baseURL)
	}

	env, _ = providerEnvMap(pm, "weird")
	if _, ok := env["A"]; ok {
		t.Errorf("非 string 值应被跳过: %v", env)
	}
	if env["B"] != "ok" {
		t.Errorf("string 值应保留: %v", env)
	}

	// provider 不存在 / 名字为空
	env, baseURL = providerEnvMap(pm, "nope")
	if len(env) != 0 || baseURL != "" {
		t.Errorf("不存在的 provider 应为空: %v / %q", env, baseURL)
	}
	env, baseURL = providerEnvMap(pm, "")
	if len(env) != 0 || baseURL != "" {
		t.Errorf("空名字应为空: %v / %q", env, baseURL)
	}
}

// TestDeliverMap_ProviderWinsOverSettingsFile 断言交付语义：
// **ccl 配了的键赢；ccl 没配的键不出现**（settings.json 的值继续生效）。
//
// 这是 provider-settings-injection 的核心：ccl 配置的优先级高于用户在
// settings.json 里的同名值——否则"配一个独立提供商"就毫无意义。
func TestDeliverMap_ProviderWinsOverSettingsFile(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"deepseek": map[string]any{
			"env": map[string]any{
				"ANTHROPIC_BASE_URL":   "https://api.deepseek.com/anthropic",
				"ANTHROPIC_AUTH_TOKEN": "sk-ds",
			},
		},
	})

	providerEnv, baseURL := providerEnvMap(pm, "deepseek")
	otelEnv := buildChildOtelEnv(map[string]any{
		"ANTHROPIC_MODEL": "user-model", // 用户在 settings.json 里写的，ccl 没配
	})

	deliver := make(map[string]string, len(providerEnv)+len(otelEnv))
	for k, v := range providerEnv {
		deliver[k] = v
	}
	for k, v := range otelEnv {
		deliver[k] = v
	}

	// ccl 配了的 → 交付
	if deliver["ANTHROPIC_BASE_URL"] != "https://api.deepseek.com/anthropic" {
		t.Errorf("provider 的端点应被交付: %q", deliver["ANTHROPIC_BASE_URL"])
	}
	if deliver["ANTHROPIC_AUTH_TOKEN"] != "sk-ds" {
		t.Errorf("provider 的 token 应被交付: %q", deliver["ANTHROPIC_AUTH_TOKEN"])
	}
	// ccl 没配的 → **不交付**（settings.json 的值继续生效）
	if _, ok := deliver["ANTHROPIC_MODEL"]; ok {
		t.Errorf("ccl 没配的键不该被交付: %v", deliver)
	}
	// provider 的 base url 用来算 launch 上下文的端点 host
	if baseURL == "" {
		t.Error("baseURL 不该为空")
	}
}
