package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cc-launcher/internal/provider"
)

// TestPassthroughHasNoComments 断言**注释不进 Claude Code**。
//
// 透传给 Claude Code 的 settings 是 ccl **重新序列化**出来的——注释在解析阶段
// 就没了，所以它既不会变成顶层键，也不会变成 env 里的键。
//
// 这条很重要：注释是 provider 配置的**本地说明**，不是 Claude Code 的设置项；
// 透传出去等于给 Claude Code 塞一个它不认识的键。
func TestPassthroughHasNoComments(t *testing.T) {
	dir := t.TempDir()
	content := `{
  // 额度走 Access Key 签名查（不是推理用的 ark-… key）
  "note": "火山方舟-AgentPlan",
  "env": {
    // Agent Plan 专用路径前缀
    "ANTHROPIC_BASE_URL": "https://ark.cn-beijing.volces.com/api/plan",
    "ANTHROPIC_AUTH_TOKEN": "ark-test-key"
  },
  /* quota 是 ccl 自己的元数据，不透传 */
  "quota": {"source": "ark", "accessKey": "AKTEST", "secretKey": "SKTEST"}
}`
	if err := os.WriteFile(filepath.Join(dir, "ark.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadProviderConfig(provider.NewManager(dir), "ark")
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}

	// env 块：只有配置里真的有的键
	if len(cfg.Env) != 2 {
		t.Errorf("env 块应有 2 个键（注释不算），实际 %d: %v", len(cfg.Env), cfg.Env)
	}
	for k, v := range cfg.Env {
		if strings.Contains(v, "注释") || strings.Contains(v, "额度走") {
			t.Errorf("env.%s 的值里混进了注释: %q", k, v)
		}
	}
	// 顶层透传键：note / order / quota 都是 ccl 的元数据，不透传
	for k := range cfg.Passthrough {
		if strings.Contains(k, "注释") {
			t.Errorf("注释变成了透传键: %q", k)
		}
	}
	// quota 块仍被识别（它是 ccl 自己的元数据）
	if cfg.Quota == nil || cfg.Quota.Source != "ark" {
		t.Errorf("quota 块没识别出来: %+v", cfg.Quota)
	}
}
