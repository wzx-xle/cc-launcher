package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"cc-launcher/internal/provider"
)

// deliveredEnv 跑一次 buildLaunchOptions，返回交付的 settings 文件里的 env 块。
func deliveredEnv(t *testing.T, providerCfg map[string]any) map[string]any {
	t.Helper()
	pm, cm, sm := launchEnv(t, providerCfg)
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
	return env
}

// TestBuildLaunchOptions_QuotaURLDelivered 覆盖：交付给 Claude Code 的 settings
// 对象的 env 块里带有 CCL_QUOTA_URL，值是 daemon 的只读额度端点。
//
// 这是给**仓库外**的消费方（状态栏脚本）用的：拿到它就能**零进程**读额度
// （Windows 上起进程 p50 170ms，状态栏的预算只有 250ms）。
func TestBuildLaunchOptions_QuotaURLDelivered(t *testing.T) {
	env := deliveredEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	got, ok := env[provider.EnvQuotaURL].(string)
	if !ok {
		t.Fatalf("交付的 env 块里应有 %s，实际 %v", provider.EnvQuotaURL, env[provider.EnvQuotaURL])
	}
	if got != "http://127.0.0.1:4318/ccl/quota" {
		t.Errorf("%s = %q, 想要 http://127.0.0.1:4318/ccl/quota", provider.EnvQuotaURL, got)
	}
	if got == "" {
		t.Errorf("%s 不该是空串（缺席 ≠ 空串）", provider.EnvQuotaURL)
	}
}

// TestBuildLaunchOptions_QuotaURLWinsOverProviderEnv 覆盖：provider 自己的 env
// 块里写了同名键 → ccl 的值覆盖它（"ccl 自己的键赢"的纪律）。
func TestBuildLaunchOptions_QuotaURLWinsOverProviderEnv(t *testing.T) {
	env := deliveredEnv(t, map[string]any{
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
			provider.EnvQuotaURL:   "http://elsewhere/quota",
		},
	})

	if got := env[provider.EnvQuotaURL]; got != "http://127.0.0.1:4318/ccl/quota" {
		t.Errorf("provider env 里的同名键应被 ccl 的值覆盖，实际 %v", got)
	}
}

// TestBuildLaunchOptions_QuotaURLInProcessEnv 覆盖：冗余第二通道——进程环境里
// 也有一份 CCL_QUOTA_URL（万一上游某版不把 settings 的 env 块落到子进程环境）。
func TestBuildLaunchOptions_QuotaURLInProcessEnv(t *testing.T) {
	t.Setenv(provider.EnvQuotaURL, "")

	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})
	if _, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", ""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(provider.EnvQuotaURL); got != "http://127.0.0.1:4318/ccl/quota" {
		t.Errorf("进程环境的 %s = %q", provider.EnvQuotaURL, got)
	}
}

// TestBuildLaunchOptions_QuotaURLDeliveredWithoutProvider 覆盖：**没有选中
// provider** 时也交付这个键（它与 provider 无关——端点的地址是常量）。
//
// 缺席 ≠ 空串：消费方据此知道"这个会话不是 ccl 起的"；但 URL 本身与 provider
// 无关，不该被牵连。
func TestBuildLaunchOptions_QuotaURLDeliveredWithoutProvider(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})
	opts, err := buildLaunchOptions(pm, cm, sm, "", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatalf("空 provider 名不该报错: %v", err)
	}
	path := settingsPathFromArgs(t, opts.ExtraArgs)
	data, _ := os.ReadFile(path)
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	env := obj["env"].(map[string]any)
	if got := env[provider.EnvQuotaURL]; got != "http://127.0.0.1:4318/ccl/quota" {
		t.Errorf("没有 provider 时 %s 仍该交付，实际 %v", provider.EnvQuotaURL, got)
	}
}

// TestBuildLaunchOptions_QuotaURLBeatsUserSettings 覆盖：用户自己的
// ~/.claude/settings.json 里写了同名键 → **压不过** ccl 交付的值。
//
// 机制：ccl 走 `--settings`（命令行层）交付 env 块，而 Claude Code 的合并
// 顺序是「命令行层 > 用户 settings.json 的 env 块 > 进程环境」
// （实测结论见 docs/claude-code-settings-merge-semantics.md）。
//
// **ccl 侧该保证的是两件事**（都在这里断言）：
//  1. 交付的 settings 文件里是 ccl 自己的值
//  2. ccl **不碰**用户的 settings.json（它不写那个文件）
func TestBuildLaunchOptions_QuotaURLBeatsUserSettings(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	// 用户自己写的同名键（一个"过期的"地址）。
	if err := cm.SaveSettings(map[string]any{
		"env": map[string]any{provider.EnvQuotaURL: "http://stale/"},
	}); err != nil {
		t.Fatal(err)
	}

	opts, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatal(err)
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
	env := obj["env"].(map[string]any)
	if got := env[provider.EnvQuotaURL]; got != "http://127.0.0.1:4318/ccl/quota" {
		t.Errorf("交付的 %s 应是 ccl 自己的值，实际 %v", provider.EnvQuotaURL, got)
	}

	// 用户自己的 settings.json 不该被 ccl 改写（ccl 不写那个文件）。
	userSettings, err := cm.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	userEnv, _ := userSettings["env"].(map[string]any)
	if got := userEnv[provider.EnvQuotaURL]; got != "http://stale/" {
		t.Errorf("用户的 settings.json 不该被 ccl 改写，实际 %v", got)
	}
}

// TestEnvQuotaURLContract 守住这个对外契约的两条硬约束：
//
//  1. 变量名固定为 CCL_QUOTA_URL（改名 = breaking change）
//  2. 名字**不能带凭证特征**——Claude Code 的
//     CLAUDE_CODE_SUBPROCESS_ENV_SCRUB 会把凭证类变量从子进程环境里删掉
func TestEnvQuotaURLContract(t *testing.T) {
	if provider.EnvQuotaURL != "CCL_QUOTA_URL" {
		t.Errorf("变量名是对外契约，改名等于 breaking change: %q", provider.EnvQuotaURL)
	}
	for _, credentialish := range []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "CREDENTIAL"} {
		if strings.Contains(strings.ToUpper(provider.EnvQuotaURL), credentialish) {
			t.Errorf("变量名带凭证特征 %q → 会被 Claude Code 的 subprocess scrub 误伤", credentialish)
		}
	}
	// URL 形态也是契约的一部分。
	if got := quotaEndpointURL(); got != "http://127.0.0.1:4318/ccl/quota" {
		t.Errorf("额度端点 URL 的形态是对外契约: %q", got)
	}
}
