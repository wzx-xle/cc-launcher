package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cc-launcher/internal/config"
	"cc-launcher/internal/provider"
	"cc-launcher/internal/state"
)

// launchEnv 造一套隔离的运行环境：临时 HOME + 临时 providers / state /
// ~/.claude/settings.json，外加一个"支持 --settings"的假 claude。
func launchEnv(t *testing.T, providerCfg map[string]any) (*provider.Manager, *config.Manager, *state.Manager) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	// 假 claude：`--help` 输出里带 --settings（探测会跑它）。
	bin := t.TempDir()
	claude := filepath.Join(bin, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\necho '  --settings <file>'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	providersDir := filepath.Join(home, ".cc-launcher", "providers")
	if err := os.MkdirAll(providersDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(providerCfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providersDir, "e2e.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	pm := provider.NewManager(providersDir)
	sm := state.NewManager(filepath.Join(home, ".cc-launcher"))
	cm := config.NewManager(
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".cc-launcher", "backups"),
		config.DefaultMaxBackups,
	)
	return pm, cm, sm
}

// settingsPathFromArgs 从 opts.ExtraArgs 里取出 --settings 的文件路径。
func settingsPathFromArgs(t *testing.T, args []string) string {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--settings" {
			return args[i+1]
		}
	}
	t.Fatalf("ExtraArgs 里没有 --settings: %v", args)
	return ""
}

// TestBuildLaunchOptions_DeliveredContent 覆盖 provider-settings-passthrough 的
// 端到端契约：交付给 Claude Code 的 settings 文件里——
//   - 有 provider 的 env（凭证、端点）
//   - 有 provider 的顶层键（model / permissions）
//   - **没有** ccl 自己的元数据键（note / order）
//   - argv 里只有路径，没有明文凭证
func TestBuildLaunchOptions_DeliveredContent(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"note":  "端到端测试",
		"order": 7,
		"model": "e2e-model",
		"permissions": map[string]any{
			"allow": []any{"Bash(echo e2e)"},
		},
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://api.e2e.example.com/anthropic",
			"ANTHROPIC_AUTH_TOKEN": "sk-e2e-secret",
		},
	})

	opts, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}

	path := settingsPathFromArgs(t, opts.ExtraArgs)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读交付文件失败: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("交付内容不是合法 JSON: %v\n%s", err, data)
	}

	// env 块
	env, ok := obj["env"].(map[string]any)
	if !ok {
		t.Fatalf("交付内容缺 env 块: %s", data)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.e2e.example.com/anthropic" {
		t.Errorf("端点没交付: %s", data)
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-e2e-secret" {
		t.Errorf("凭证没交付: %s", data)
	}

	// 顶层透传键
	if obj["model"] != "e2e-model" {
		t.Errorf("model 没透传: %s", data)
	}
	if obj["permissions"] == nil {
		t.Errorf("permissions 没透传: %s", data)
	}

	// ccl 自己的元数据键不该出现
	if _, ok := obj["note"]; ok {
		t.Errorf("note 不该交付: %s", data)
	}
	if _, ok := obj["order"]; ok {
		t.Errorf("order 不该交付: %s", data)
	}

	// argv 里不能有明文凭证
	joined := strings.Join(opts.ExtraArgs, " ")
	if strings.Contains(joined, "sk-e2e-secret") {
		t.Errorf("argv 里出现明文凭证: %s", joined)
	}

	// 交付文件在 cache 目录里，权限 0600（目录 0700）
	if !strings.Contains(path, filepath.Join(".cc-launcher", "cache")) {
		t.Errorf("交付文件应在 cache 目录: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("交付文件权限 = %v, want 0600", info.Mode().Perm())
	}
}

// TestBuildLaunchOptions_BrokenProviderBlocksLaunch 覆盖
// provider-settings-load-guard：provider 配置有已知键类型写错 → **不启动**，
// 也不产出交付文件。
func TestBuildLaunchOptions_BrokenProviderBlocksLaunch(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"model": map[string]any{"name": "x"},
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	opts, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err == nil {
		t.Fatal("类型写错应阻止启动")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("错误应点名 model: %v", err)
	}
	if len(opts.ExtraArgs) != 0 {
		t.Errorf("失败时不应产出 --settings 参数: %v", opts.ExtraArgs)
	}
}

// TestBuildLaunchOptions_ModelDeliveredAsEnv 覆盖：provider 配了 model → 交付的
// env 块里出现 ANTHROPIC_MODEL。
//
// 真机实测（Claude Code 2.1.308）：`ANTHROPIC_MODEL` 环境变量压过任何文件里的
// `model` 键，但用户 settings.json 的 env 块又压过进程环境——所以决定性路径是
// **把 ANTHROPIC_MODEL 放进 --settings 的 env 块**，只 setenv 是不够的。
func TestBuildLaunchOptions_ModelDeliveredAsEnv(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"model": "e2e-model",
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	t.Setenv("ANTHROPIC_MODEL", "shell-model")
	opts, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", "")
	if err != nil {
		t.Fatal(err)
	}

	// 交付的 env 块里必须有 ANTHROPIC_MODEL
	path := settingsPathFromArgs(t, opts.ExtraArgs)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	env := obj["env"].(map[string]any)
	if env["ANTHROPIC_MODEL"] != "e2e-model" {
		t.Errorf("交付的 env 块里应有 ANTHROPIC_MODEL=e2e-model: %s", data)
	}
	// 顶层 model 键也照常透传
	if obj["model"] != "e2e-model" {
		t.Errorf("顶层 model 应照常透传: %s", data)
	}
	// 进程环境也同步一份（低优先级冗余）
	if got := os.Getenv("ANTHROPIC_MODEL"); got != "e2e-model" {
		t.Errorf("进程环境的 ANTHROPIC_MODEL = %q, want e2e-model", got)
	}
}

// TestBuildLaunchOptions_ProviderEnvModelWins 覆盖：provider 的 env 块里已经有
// ANTHROPIC_MODEL → 尊重它，不被顶层 model 键覆盖（provider 内部一致性）。
func TestBuildLaunchOptions_ProviderEnvModelWins(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"model": "e2e-model",
		"env": map[string]any{
			"ANTHROPIC_AUTH_TOKEN": "sk-x",
			"ANTHROPIC_MODEL":      "env-model",
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
	if env["ANTHROPIC_MODEL"] != "env-model" {
		t.Errorf("provider env 里的 ANTHROPIC_MODEL 应保留: %s", data)
	}
}

// TestBuildLaunchOptions_NoModelKeepsShellValue 覆盖：provider 没配 model →
// 用户 shell 的 ANTHROPIC_MODEL 保持不变（provider 没配的键不碰）。
func TestBuildLaunchOptions_NoModelKeepsShellValue(t *testing.T) {
	pm, cm, sm := launchEnv(t, map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	t.Setenv("ANTHROPIC_MODEL", "shell-model")
	if _, err := buildLaunchOptions(pm, cm, sm, "e2e", "default", "/tmp/e2e-cwd", ""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ANTHROPIC_MODEL"); got != "shell-model" {
		t.Errorf("ANTHROPIC_MODEL = %q, want shell-model", got)
	}
}
