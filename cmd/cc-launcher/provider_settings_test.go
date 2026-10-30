package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// TestLoadProviderConfig 断言：provider 的 env 块被完整读出。
func TestLoadProviderConfig(t *testing.T) {
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

	pcfg, err := loadProviderConfig(pm, "deepseek")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if pcfg.BaseURL != "https://api.deepseek.com/anthropic" {
		t.Errorf("baseURL = %q", pcfg.BaseURL)
	}
	for k, want := range map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.deepseek.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "sk-ds",
		"HTTP_PROXY":           "http://127.0.0.1:7890",
	} {
		if pcfg.Env[k] != want {
			t.Errorf("%s = %q, want %q", k, pcfg.Env[k], want)
		}
	}
	// note 是 ccl 元数据 → 不透传
	if _, ok := pcfg.Passthrough["note"]; ok {
		t.Errorf("note 不该被透传: %+v", pcfg.Passthrough)
	}
}

// TestLoadProviderConfig_EdgeCases 断言异常输入不炸。
func TestLoadProviderConfig_EdgeCases(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"noenv": map[string]any{"note": "没有 env 块"},
		"weird": map[string]any{"env": map[string]any{"A": 42, "B": "ok"}},
	})

	pcfg, err := loadProviderConfig(pm, "noenv")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(pcfg.Env) != 0 || pcfg.BaseURL != "" {
		t.Errorf("没有 env 块时应为空: %v / %q", pcfg.Env, pcfg.BaseURL)
	}

	// 非 string 的 env 值 → 转成字符串 + 警告（实测 Claude Code 自己也这么转）
	pcfg, err = loadProviderConfig(pm, "weird")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if pcfg.Env["A"] != "42" {
		t.Errorf("非 string 值应被转成字符串: %v", pcfg.Env)
	}
	if pcfg.Env["B"] != "ok" {
		t.Errorf("string 值应保留: %v", pcfg.Env)
	}
	if len(pcfg.Warnings) == 0 {
		t.Error("非 string 的 env 值应产生警告")
	}

	// provider 不存在 → 报错（不再静默返回空：静默会让"选中的 provider 没生效"
	// 变成一句没人看的日志）
	if _, err := loadProviderConfig(pm, "nope"); err == nil {
		t.Error("不存在的 provider 应报错")
	}
	// 名字为空 → 空配置，不报错
	pcfg, err = loadProviderConfig(pm, "")
	if err != nil {
		t.Fatalf("空名字不该报错: %v", err)
	}
	if len(pcfg.Env) != 0 || len(pcfg.Passthrough) != 0 {
		t.Errorf("空名字应为空: %+v", pcfg)
	}
}

// TestLoadProviderConfig_TopLevelKeysPassthrough 断言：provider 的顶层键被拆成
// 透传键，ccl 自己的元数据键被排除。
func TestLoadProviderConfig_TopLevelKeysPassthrough(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"full": map[string]any{
			"note":  "备注",
			"order": 3,
			"model": "some-model",
			"permissions": map[string]any{
				"allow": []any{"Bash(ls:*)"},
			},
			"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.example.com"},
		},
	})

	pcfg, err := loadProviderConfig(pm, "full")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if pcfg.Passthrough["model"] != "some-model" {
		t.Errorf("model 应被透传: %+v", pcfg.Passthrough)
	}
	perms, ok := pcfg.Passthrough["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions 应被透传: %+v", pcfg.Passthrough)
	}
	if perms["allow"] == nil {
		t.Errorf("permissions.allow 应原样透传: %+v", perms)
	}
	if _, ok := pcfg.Passthrough["note"]; ok {
		t.Error("note 不该被透传")
	}
	if _, ok := pcfg.Passthrough["order"]; ok {
		t.Error("order 不该被透传")
	}
	if _, ok := pcfg.Passthrough["env"]; ok {
		t.Error("env 不该出现在透传键里（它走 env 通道）")
	}
}

// TestLoadProviderConfig_WrongTypeBlocksLaunch 断言：已知键类型写错 → 报错，
// 不启动（Claude Code 会丢弃整个 settings 文件，provider 凭证会一起消失）。
func TestLoadProviderConfig_WrongTypeBlocksLaunch(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"broken": map[string]any{
			"model": map[string]any{"name": "x"},
			"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
		},
	})

	_, err := loadProviderConfig(pm, "broken")
	if err == nil {
		t.Fatal("类型写错应报错并阻止启动")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("错误应点名 model: %v", err)
	}
}

// TestLoadProviderConfig_UnknownKeyPasses 断言：未知顶层键放行（Claude Code 自己
// 也是静默忽略，不能因此拦住用户）。
func TestLoadProviderConfig_UnknownKeyPasses(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"future": map[string]any{
			"someBrandNewKey": map[string]any{"a": 1},
			"env":             map[string]any{"A": "B"},
		},
	})

	pcfg, err := loadProviderConfig(pm, "future")
	if err != nil {
		t.Fatalf("未知键不该报错: %v", err)
	}
	got, ok := pcfg.Passthrough["someBrandNewKey"].(map[string]any)
	if !ok {
		t.Fatalf("未知键应原样透传: %+v", pcfg.Passthrough)
	}
	// provider 文件是 JSON 解析来的 → 数字是 float64。
	if got["a"] != float64(1) {
		t.Errorf("未知键的值应原样保留: %+v", got)
	}
}

// TestModelFromPassthrough 断言：model 键的提取。
func TestModelFromPassthrough(t *testing.T) {
	if m, ok := modelFromPassthrough(map[string]any{"model": "some-model"}); !ok || m != "some-model" {
		t.Errorf("应取出 model: %q / %v", m, ok)
	}
	if _, ok := modelFromPassthrough(map[string]any{}); ok {
		t.Error("没有 model 时应返回 false")
	}
	if _, ok := modelFromPassthrough(map[string]any{"model": 42}); ok {
		t.Error("model 不是字符串时应返回 false")
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

	pcfg, err := loadProviderConfig(pm, "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	providerEnv := pcfg.Env
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
	if pcfg.BaseURL == "" {
		t.Error("baseURL 不该为空")
	}
}
