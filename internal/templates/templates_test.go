package templates

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// requiredNonAnthropicKeys 每个非 anthropic 模板的 env 必须包含的 key。
var requiredNonAnthropicKeys = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

// anthropicPathSuffix BASE_URL 必须以这两个后缀之一结尾（Anthropic 兼容路径）。
var anthropicPathSuffix = []string{"/anthropic", "/apps/anthropic"}

// TestNamesAndDescriptions 校验所有模板的 Name / Description 非空且 Name 是合法的 kebab-case。
func TestNamesAndDescriptions(t *testing.T) {
	nameRe := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	for _, tpl := range All {
		if tpl.Name == "" {
			t.Errorf("模板 Name 为空")
			continue
		}
		if !nameRe.MatchString(tpl.Name) {
			t.Errorf("模板 %q Name 不合法（必须是 kebab-case）", tpl.Name)
		}
		if strings.TrimSpace(tpl.Description) == "" {
			t.Errorf("模板 %q Description 为空", tpl.Name)
		}
		if len(tpl.Fields) == 0 {
			t.Errorf("模板 %q Fields 为空", tpl.Name)
		}
	}
}

// TestBodiesAreValidJSON 每个模板的 Body 必须解析为合法 JSON。
func TestBodiesAreValidJSON(t *testing.T) {
	for _, tpl := range All {
		var v map[string]any
		if err := json.Unmarshal([]byte(tpl.Body), &v); err != nil {
			t.Errorf("模板 %q Body 无法解析为 JSON: %v\nBody:\n%s", tpl.Name, err, tpl.Body)
			continue
		}
		// 至少要有 env 字段
		if _, ok := v["env"]; !ok {
			t.Errorf("模板 %q Body 没有 env 字段", tpl.Name)
		}
	}
}

// TestNonAnthropicHaveRequiredEnvKeys 非 anthropic 模板的 env 必须含必需 key。
func TestNonAnthropicHaveRequiredEnvKeys(t *testing.T) {
	for _, tpl := range All {
		if tpl.Name == "anthropic" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(tpl.Body), &v); err != nil {
			// 上一测试已经报告，跳过
			continue
		}
		env, ok := v["env"].(map[string]any)
		if !ok {
			t.Errorf("模板 %q env 字段不是对象", tpl.Name)
			continue
		}
		for _, k := range requiredNonAnthropicKeys {
			if _, ok := env[k]; !ok {
				t.Errorf("模板 %q env 缺少必需 key %q", tpl.Name, k)
			}
		}
	}
}

// TestBaseURLHasAnthropicCompatibleSuffix 非 anthropic 模板的 ANTHROPIC_BASE_URL 必须以 /anthropic 或 /apps/anthropic 结尾。
func TestBaseURLHasAnthropicCompatibleSuffix(t *testing.T) {
	for _, tpl := range All {
		if tpl.Name == "anthropic" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(tpl.Body), &v); err != nil {
			continue
		}
		env, _ := v["env"].(map[string]any)
		url, _ := env["ANTHROPIC_BASE_URL"].(string)
		ok := false
		for _, suffix := range anthropicPathSuffix {
			if strings.HasSuffix(url, suffix) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("模板 %q ANTHROPIC_BASE_URL=%q 必须以 %v 之一结尾", tpl.Name, url, anthropicPathSuffix)
		}
	}
}

// TestFieldKeyUniqueness 每个模板的 Fields Key 必须唯一。
func TestFieldKeyUniqueness(t *testing.T) {
	for _, tpl := range All {
		seen := make(map[string]bool, len(tpl.Fields))
		for _, f := range tpl.Fields {
			if seen[f.Key] {
				t.Errorf("模板 %q 字段 %q 重复", tpl.Name, f.Key)
			}
			seen[f.Key] = true
		}
	}
}

// TestByName 命中与未命中两条路径。
func TestByName(t *testing.T) {
	if _, ok := ByName("deepseek"); !ok {
		t.Error("ByName(\"deepseek\") 应命中")
	}
	if _, ok := ByName("definitely-not-a-type"); ok {
		t.Error("ByName(\"definitely-not-a-type\") 应返回 ok=false")
	}
}

// TestAnthropicOmitsBaseURL 官方直连模板不写 ANTHROPIC_BASE_URL。
func TestAnthropicOmitsBaseURL(t *testing.T) {
	tpl, ok := ByName("anthropic")
	if !ok {
		t.Fatal("anthropic 模板未注册")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(tpl.Body), &v); err != nil {
		t.Fatalf("anthropic Body 无法解析: %v", err)
	}
	env, _ := v["env"].(map[string]any)
	if _, hasBase := env["ANTHROPIC_BASE_URL"]; hasBase {
		t.Errorf("anthropic 模板不应包含 ANTHROPIC_BASE_URL（用官方端点）")
	}
}

// TestAliyunPayPreservesWorkspaceIdPlaceholder aliyun-pay 模板打印时保留 {WorkspaceId} 字面量。
func TestAliyunPayPreservesWorkspaceIdPlaceholder(t *testing.T) {
	tpl, ok := ByName("aliyun-pay")
	if !ok {
		t.Fatal("aliyun-pay 模板未注册")
	}
	if !strings.Contains(tpl.Body, "{WorkspaceId}") {
		t.Errorf("aliyun-pay Body 必须保留 {WorkspaceId} 字面量占位符（用户必须填入）\nBody:\n%s", tpl.Body)
	}
}