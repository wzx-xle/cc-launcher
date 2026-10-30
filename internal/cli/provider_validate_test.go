package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProviderFile 直接写一个 provider 配置文件（绕开 CreateWithEnv，
// 因为要写 env 以外的顶层键）。
func writeProviderFile(t *testing.T, pm interface {
	ProvidersDir() string
}, name string, cfg map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pm.ProvidersDir(), name+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestProviderValidate_ValidConfig 覆盖 provider-settings-load-guard：合法配置
// → 校验通过，并列出顶层透传键。
func TestProviderValidate_ValidConfig(t *testing.T) {
	c, pm, _ := setupCLI(t)
	writeProviderFile(t, pm, "full", map[string]any{
		"note":  "备注",
		"order": 2,
		"model": "some-model",
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	cap := captureStdout(t)
	err := c.Run([]string{"provider", "validate", "full"})
	output := cap()

	if err != nil {
		t.Fatalf("合法配置不该报错: %v", err)
	}
	if !strings.Contains(output, "顶层透传键: model") {
		t.Errorf("应列出顶层透传键:\n%s", output)
	}
	if strings.Contains(output, "note") && strings.Contains(output, "顶层透传键: note") {
		t.Errorf("note 是 ccl 元数据，不该出现在透传键里:\n%s", output)
	}
	if !strings.Contains(output, "✓") {
		t.Errorf("应报告校验通过:\n%s", output)
	}
}

// TestProviderValidate_BrokenConfig 覆盖 provider-settings-load-guard：已知键
// 类型写错 → 报错并指出是哪个键。
func TestProviderValidate_BrokenConfig(t *testing.T) {
	c, pm, _ := setupCLI(t)
	writeProviderFile(t, pm, "broken", map[string]any{
		"model": map[string]any{"name": "x"},
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})

	cap := captureStdout(t)
	err := c.Run([]string{"provider", "validate", "broken"})
	output := cap()

	if err == nil {
		t.Fatal("类型写错应报错")
	}
	if !strings.Contains(output, "model") {
		t.Errorf("应点名 model:\n%s", output)
	}
	if !strings.Contains(err.Error(), "错误") {
		t.Errorf("错误信息应说明有错误: %v", err)
	}
}

// TestProviderValidate_UnknownProvider 覆盖：不存在的 provider → 报错。
func TestProviderValidate_UnknownProvider(t *testing.T) {
	c, _, _ := setupCLI(t)
	if err := c.Run([]string{"provider", "validate", "nope"}); err == nil {
		t.Fatal("不存在的 provider 应报错")
	}
}

// TestProviderList_ShowsPassthroughCount 覆盖：provider 列表标注顶层透传键数量。
func TestProviderList_ShowsPassthroughCount(t *testing.T) {
	c, pm, _ := setupCLI(t)
	writeProviderFile(t, pm, "full", map[string]any{
		"model": "m",
		"env":   map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-x"},
	})
	writeProviderFile(t, pm, "plain", map[string]any{
		"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "sk-y"},
	})

	cap := captureStdout(t)
	_ = c.Run([]string{"provider", "--list"})
	output := cap()

	if !strings.Contains(output, "1 个透传键") {
		t.Errorf("应标注透传键数量:\n%s", output)
	}
	if strings.Contains(output, "plain  （") {
		t.Errorf("只有 env 的 provider 不该被标注:\n%s", output)
	}
}
