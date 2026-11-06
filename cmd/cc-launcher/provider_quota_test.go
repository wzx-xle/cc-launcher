package main

import (
	"strings"
	"testing"
	"time"

	"cc-launcher/internal/quota"
)

// TestLoadProviderConfig_QuotaBlock 断言：provider 配的 `quota` 块能被读到，
// 且**不出现在透传键里**（quota 是 ccl 自己的元数据键）。
func TestLoadProviderConfig_QuotaBlock(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"iflytek": map[string]any{
			"env": map[string]any{
				"ANTHROPIC_BASE_URL":   "https://one.iflytek.com/api/llm/console/chat/",
				"ANTHROPIC_AUTH_TOKEN": "sk-i",
			},
			"quota": map[string]any{
				"source":          "newapi",
				"endpoint":        "https://one.iflytek.com/api/llm/console/chat/api/usage/token",
				"quotaPerUnit":    float64(500000),
				"displayUnit":     "USD",
				"refreshInterval": float64(300),
			},
		},
	})

	pcfg, err := loadProviderConfig(pm, "iflytek")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if pcfg.Quota == nil {
		t.Fatal("quota 块应被读到")
	}
	if pcfg.Quota.Source != "newapi" {
		t.Errorf("source = %q", pcfg.Quota.Source)
	}
	if pcfg.Quota.Endpoint != "https://one.iflytek.com/api/llm/console/chat/api/usage/token" {
		t.Errorf("endpoint = %q", pcfg.Quota.Endpoint)
	}
	if pcfg.Quota.QuotaPerUnit != 500000 {
		t.Errorf("quotaPerUnit = %v", pcfg.Quota.QuotaPerUnit)
	}
	if pcfg.Quota.DisplayUnit != "USD" {
		t.Errorf("displayUnit = %q", pcfg.Quota.DisplayUnit)
	}
	if pcfg.Quota.RefreshInterval != 300*time.Second {
		t.Errorf("refreshInterval = %v, want 300s", pcfg.Quota.RefreshInterval)
	}

	// **quota 不进透传键**
	if _, ok := pcfg.Passthrough["quota"]; ok {
		t.Errorf("quota 是 ccl 元数据，不该被透传: %+v", pcfg.Passthrough)
	}
}

// TestLoadProviderConfig_NoQuotaBlock 断言：没配 quota 块时为空。
func TestLoadProviderConfig_NoQuotaBlock(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"plain": map[string]any{
			"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
		},
	})

	pcfg, err := loadProviderConfig(pm, "plain")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if pcfg.Quota != nil {
		t.Errorf("没配 quota 时应为空: %+v", pcfg.Quota)
	}
}

// TestLoadProviderConfig_QuotaDefaultInterval 断言：refreshInterval 缺省 60 秒。
func TestLoadProviderConfig_QuotaDefaultInterval(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"m": map[string]any{
			"env":   map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
			"quota": map[string]any{"source": "minimax"},
		},
	})

	pcfg, err := loadProviderConfig(pm, "m")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if pcfg.Quota == nil {
		t.Fatal("quota 块应被读到")
	}
	if pcfg.Quota.RefreshInterval != 0 {
		t.Errorf("未配时 RefreshInterval 应为零值（由 Interval() 兜底）: %v", pcfg.Quota.RefreshInterval)
	}
	if got := pcfg.Quota.Interval(); got != quota.DefaultRefreshInterval {
		t.Errorf("实际刷新周期 = %v, want %v", got, quota.DefaultRefreshInterval)
	}
	if quota.DefaultRefreshInterval != 60*time.Second {
		t.Errorf("默认刷新周期应为 60s, got %v", quota.DefaultRefreshInterval)
	}
}

// TestLoadProviderConfig_QuotaParseErrorWarns 断言：quota 块写错 → 警告，
// **不阻断启动**（额度是锦上添花，provider 能不能生效才是生死线）。
func TestLoadProviderConfig_QuotaParseErrorWarns(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"broken": map[string]any{
			"env":   map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic"},
			"quota": map[string]any{"source": float64(42)},
		},
	})

	pcfg, err := loadProviderConfig(pm, "broken")
	if err != nil {
		t.Fatalf("不该报错（额度配置问题不阻断启动）: %v", err)
	}
	if len(pcfg.Warnings) == 0 {
		t.Error("quota 块解析失败应产生警告")
	}
	if pcfg.Quota != nil {
		t.Errorf("解析失败时 Quota 应为空: %+v", pcfg.Quota)
	}
}

// TestLoadProviderConfig_PlaintextBaseURLWarns 断言：明文 http:// 的 base_url
// 产生警告，https:// 的不产生。
func TestLoadProviderConfig_PlaintextBaseURLWarns(t *testing.T) {
	pm := newTestProviderMgr(t, map[string]any{
		"plain": map[string]any{
			"env": map[string]any{"ANTHROPIC_BASE_URL": "http://api.example.com/anthropic"},
		},
		"tls": map[string]any{
			"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.example.com/anthropic"},
		},
	})

	pcfg, err := loadProviderConfig(pm, "plain")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range pcfg.Warnings {
		if strings.Contains(w, "http://") {
			found = true
			if !strings.Contains(w, "api.example.com") {
				t.Errorf("警告应点名明文 base_url: %s", w)
			}
		}
	}
	if !found {
		t.Errorf("http:// 的 base_url 应产生警告: %+v", pcfg.Warnings)
	}

	pcfg2, err := loadProviderConfig(pm, "tls")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range pcfg2.Warnings {
		if strings.Contains(w, "http://") {
			t.Errorf("https:// 的 base_url 不该产生明文警告: %s", w)
		}
	}
}
