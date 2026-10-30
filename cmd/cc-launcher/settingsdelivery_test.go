package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildLaunchSettingsJSON_Shape 断言：交付对象的形状 = env 块 + provider 的
// 透传键（provider-settings-passthrough）。
func TestBuildLaunchSettingsJSON_Shape(t *testing.T) {
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.example.com",
		"ANTHROPIC_AUTH_TOKEN": "sk-x",
	}
	passthrough := map[string]any{
		"model": "some-model",
		"permissions": map[string]any{
			"allow": []any{"Bash(ls:*)"},
		},
	}

	out, err := buildLaunchSettingsJSON(env, passthrough)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("交付内容不是合法 JSON: %v\n%s", err, out)
	}

	// env 块
	gotEnv, ok := obj["env"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 env 块: %s", out)
	}
	if gotEnv["ANTHROPIC_BASE_URL"] != "https://api.example.com" {
		t.Errorf("env 块内容不对: %+v", gotEnv)
	}
	// 透传键
	if obj["model"] != "some-model" {
		t.Errorf("model 未被透传: %s", out)
	}
	if obj["permissions"] == nil {
		t.Errorf("permissions 未被透传: %s", out)
	}
}

// TestBuildLaunchSettingsJSON_CclOwnEnvWins 断言：ccl 自己的 env 块赢过透传键里
// 同名的 env（provider 的 env 走 env 通道，不该被顶层键覆盖）。
func TestBuildLaunchSettingsJSON_CclOwnEnvWins(t *testing.T) {
	out, err := buildLaunchSettingsJSON(
		map[string]string{"A": "from-ccl"},
		map[string]any{"env": map[string]any{"A": "from-provider"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatal(err)
	}
	env := obj["env"].(map[string]any)
	if env["A"] != "from-ccl" {
		t.Errorf("ccl 自己的 env 应赢: %s", out)
	}
}

// TestBuildLaunchSettingsJSON_Deterministic 断言：内容稳定（交付文件按内容
// 哈希命名，内容不稳 = 每次启动都新建缓存文件）。
func TestBuildLaunchSettingsJSON_Deterministic(t *testing.T) {
	env := map[string]string{"Z": "1", "A": "2", "M": "3"}
	passthrough := map[string]any{"model": "m", "permissions": map[string]any{"allow": []any{"x"}}}

	first, err := buildLaunchSettingsJSON(env, passthrough)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := buildLaunchSettingsJSON(env, passthrough)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("内容不稳定:\n%s\n%s", first, again)
		}
	}
	// env 键按字典序
	envStart := strings.Index(first, `"env"`)
	if envStart < 0 {
		t.Fatal("交付内容里没有 env")
	}
	if !strings.Contains(first, `"A":"2"`) {
		t.Errorf("env 键应存在: %s", first)
	}
	if strings.Index(first, `"A":"2"`) > strings.Index(first, `"M":"3"`) {
		t.Errorf("env 键应按字典序: %s", first)
	}
	if strings.Index(first, `"M":"3"`) > strings.Index(first, `"Z":"1"`) {
		t.Errorf("env 键应按字典序: %s", first)
	}
}
