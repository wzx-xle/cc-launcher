package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"cc-launcher/internal/provider"
)

// TestBuildLaunchOptions_ProviderNameDelivered 覆盖 provider-name-in-statusline：
// 交付给 Claude Code 的 settings 对象的 env 块里带有 CCL_PROVIDER，值是本次
// 选中的 provider 的名（provider 配置文件名去掉 .json）。
//
// 这是给**仓库外**的消费方（最典型的是 Claude Code 的状态栏脚本）用的：
// statusLine 拿到的 stdin JSON 里没有 provider 信息，环境变量是唯一的通道。
func TestBuildLaunchOptions_ProviderNameDelivered(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	// launchEnv 建的 provider 文件叫 e2e.json → provider 名是 "e2e"。
	opts, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}

	path := settingsPathFromArgs(t, opts.ExtraArgs)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("交付内容不是合法 JSON: %v\n%s", err, data)
	}
	env, ok := obj["env"].(map[string]any)
	if !ok {
		t.Fatalf("交付内容缺 env 块: %s", data)
	}
	if env[provider.EnvProviderName] != "e2e" {
		t.Errorf("交付的 env 块里应有 %s=e2e，实际 %v", provider.EnvProviderName, env[provider.EnvProviderName])
	}
}

// TestBuildLaunchOptions_ProviderNameWinsOverProviderEnv 覆盖：provider 自己的
// env 块里写了同名键 → ccl 的值覆盖它（"ccl 自己的键赢"的纪律）。
func TestBuildLaunchOptions_ProviderNameWinsOverProviderEnv(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN":   "sk-x",
			provider.EnvProviderName: "something-else",
		},
	})

	opts, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatal(err)
	}
	path := settingsPathFromArgs(t, opts.ExtraArgs)
	data, _ := os.ReadFile(path)
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	env := obj["env"].(map[string]any)
	if env[provider.EnvProviderName] != "e2e" {
		t.Errorf("provider env 里的同名键应被 ccl 的值覆盖，实际 %v", env[provider.EnvProviderName])
	}
}

// TestBuildLaunchOptions_ProviderNameInProcessEnv 覆盖：冗余第二通道——进程环境
// 里也有一份 CCL_PROVIDER（万一上游某版不把 settings 的 env 块落到子进程环境）。
//
// t.Setenv 先登记原值，测试结束时自动还原 → 不污染测试进程。
func TestBuildLaunchOptions_ProviderNameInProcessEnv(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	// 登记原值以便测试后还原
	t.Setenv(provider.EnvProviderName, "")

	if _, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", ""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(provider.EnvProviderName); got != "e2e" {
		t.Errorf("进程环境的 %s = %q, want e2e", provider.EnvProviderName, got)
	}
}

// TestBuildLaunchOptions_NoProviderNameNotDelivered 覆盖：providerName 为空 →
// **不交付这个键**（不是交付空串）。
//
// spec 要求"缺席"与"空串"可区分：变量不存在 = 这个会话不是 ccl 起的；
// 空串没法表达这件事。
func TestBuildLaunchOptions_NoProviderNameNotDelivered(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	t.Setenv(provider.EnvProviderName, "")

	opts, err := buildLaunchOptions(pm, cm, sm, "", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatalf("空 provider 名不该报错: %v", err)
	}
	path := settingsPathFromArgs(t, opts.ExtraArgs)
	data, _ := os.ReadFile(path)
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	env := obj["env"].(map[string]any)
	if v, ok := env[provider.EnvProviderName]; ok {
		t.Errorf("没有 provider 时不该有这个键，实际值 %q", v)
	}
	if got := os.Getenv(provider.EnvProviderName); got != "" {
		t.Errorf("没有 provider 时进程环境也不该有值，实际 %q", got)
	}
}

// TestBuildLaunchOptions_ProviderNameDeterministic 覆盖：同一 provider 连续构建
// 两次 → 交付文件逐字节相同（内容哈希命名 → 同一个文件复用，不堆积）。
func TestBuildLaunchOptions_ProviderNameDeterministic(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	opts1, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatal(err)
	}
	path1 := settingsPathFromArgs(t, opts1.ExtraArgs)
	data1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatal(err)
	}

	opts2, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatal(err)
	}
	path2 := settingsPathFromArgs(t, opts2.ExtraArgs)
	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}

	if path1 != path2 {
		t.Errorf("同内容应复用同一个文件: %s vs %s", path1, path2)
	}
	if string(data1) != string(data2) {
		t.Errorf("同内容的两次交付应逐字节相同:\n%s\n---\n%s", data1, data2)
	}
}

// TestEnvProviderNameContract 守住这个对外契约的两条硬约束：
//
//  1. 变量名固定为 CCL_PROVIDER（改名 = breaking change，会静默弄坏所有
//     仓库外的消费方）
//  2. 名字**不能带凭证特征**——Claude Code 的
//     CLAUDE_CODE_SUBPROCESS_ENV_SCRUB 会把凭证类变量从子进程环境里删掉，
//     带 TOKEN / KEY / SECRET / PASSWORD 特征的变量名会被误伤，特性静默失效
//     （真机实测：scrub 开启时 ANTHROPIC_AUTH_TOKEN 被剥离，CCL_PROVIDER 不受影响）
func TestEnvProviderNameContract(t *testing.T) {
	if provider.EnvProviderName != "CCL_PROVIDER" {
		t.Errorf("变量名是对外契约，改名等于 breaking change: %q", provider.EnvProviderName)
	}
	for _, credentialish := range []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "CREDENTIAL"} {
		if strings.Contains(strings.ToUpper(provider.EnvProviderName), credentialish) {
			t.Errorf("变量名带凭证特征 %q → 会被 Claude Code 的 subprocess scrub 误伤", credentialish)
		}
	}
}
