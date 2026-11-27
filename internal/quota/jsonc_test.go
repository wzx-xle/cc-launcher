package quota

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// commentedQuotaProvider 是带注释的 provider 配置。
//
// **关键**：quota 包**不依赖** internal/provider，它自己读文件、自己解析——
// 所以它有**独立的解析点**（Enumerate 与 LoadProviderRef）。provider 包的
// 解析点认注释，不代表这里也认。
var commentedQuotaProvider = `{
  // 额度走 Access Key 签名（不是推理用的 ark-… key）
  "note": "火山方舟-AgentPlan",
  "env": {
    "ANTHROPIC_BASE_URL": "https://ark.cn-beijing.volces.com/api/plan",
    "ANTHROPIC_AUTH_TOKEN": "ark-test-key"
  },
  "quota": {
    "source": "ark",
    /* 只读子账号 */
    "accessKey": "AKTEST",
    "secretKey": "SKTEST"
  }
}`

// TestEnumerateAcceptsComments 断言 **Enumerate** 认注释。
//
// 漏了它 → 带注释的 provider 在 daemon 的额度刷新里被静默跳过（没有额度可刷）。
func TestEnumerateAcceptsComments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ark.json"), []byte(commentedQuotaProvider), 0o600); err != nil {
		t.Fatal(err)
	}

	refs, err := Enumerate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("应枚举出 1 个 provider，实际 %d", len(refs))
	}
	ref := refs[0]
	if ref.Name != "ark" {
		t.Errorf("Name = %q", ref.Name)
	}
	if ref.Config == nil {
		t.Fatal("quota 块没解析出来")
	}
	if ref.Config.Source != "ark" {
		t.Errorf("source = %q", ref.Config.Source)
	}
	if ref.Config.AccessKey != "AKTEST" || ref.Config.SecretKey != "SKTEST" {
		t.Errorf("凭证没解析出来: %q / %q", ref.Config.AccessKey, ref.Config.SecretKey)
	}
	if ref.BaseURL != "https://ark.cn-beijing.volces.com/api/plan" {
		t.Errorf("base URL = %q", ref.BaseURL)
	}
}

// TestLoadProviderRefAcceptsComments 断言 **LoadProviderRef** 认注释。
//
// 漏了它 → 命令行的 `ccl quota --provider X --refresh` 报"配置解析失败"。
func TestLoadProviderRefAcceptsComments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ark.json"), []byte(commentedQuotaProvider), 0o600); err != nil {
		t.Fatal(err)
	}

	ref, hasQuota, err := LoadProviderRef(dir, "ark")
	if err != nil {
		t.Fatalf("LoadProviderRef 失败: %v", err)
	}
	if !hasQuota {
		t.Fatal("应识别出有额度接口")
	}
	if ref.Config == nil || ref.Config.Source != "ark" {
		t.Fatalf("quota 块没解析出来: %+v", ref.Config)
	}
	if ref.APIKey != "ark-test-key" {
		t.Errorf("APIKey = %q", ref.APIKey)
	}
}

// TestCommentedProviderFetchesQuota 断言**端到端**：带注释的配置能真的查出额度。
//
// 这是最完整的一条：解析 → 选 adapter → 发请求 → 解析响应。
func TestCommentedProviderFetchesQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("请求没有 Authorization 头")
		}
		w.Write([]byte(`{"Result":{"PlanType":"medium",
			"AFPFiveHour":{"Quota":10000,"Used":328.5542,"ResetTime":1795766093000},
			"AFPWeekly":{"Quota":35000,"Used":328.5542,"ResetTime":1795968000000},
			"AFPMonthly":{"Quota":100000,"Used":328.5542,"ResetTime":1798387199000}}}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := `{
  // 带注释的配置也要能查额度
  "env": {
    "ANTHROPIC_BASE_URL": "https://ark.cn-beijing.volces.com/api/plan",
    "ANTHROPIC_AUTH_TOKEN": "ark-test-key"
  },
  "quota": {"source": "ark", "accessKey": "AKTEST", "secretKey": "SKTEST"}
}`
	if err := os.WriteFile(filepath.Join(dir, "ark.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ref, hasQuota, err := LoadProviderRef(dir, "ark")
	if err != nil || !hasQuota {
		t.Fatalf("载入失败: %v %v", err, hasQuota)
	}
	// 端点指到 mock server（region 仍从 base_url 推导，签名需要它）
	dims, err := arkAdapter{}.Fetch(t.Context(), ref.providerInfo(srv.URL))
	if err != nil {
		t.Fatalf("查额度失败: %v", err)
	}
	if len(dims) != 3 {
		t.Errorf("应有 3 个维度，实际 %d", len(dims))
	}
}
