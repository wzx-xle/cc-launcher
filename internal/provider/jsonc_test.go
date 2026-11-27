package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 带注释的 provider 配置——用来验证注释在各个读路径上行为一致。
const commentedProvider = `{
  // 方舟 Agent Plan：额度靠 Access Key 签名查（不是推理用的 ark-… key）
  "note": "火山方舟-AgentPlan",
  "order": 10,
  "env": {
    // 数据面地址：Agent Plan 专用路径前缀
    "ANTHROPIC_BASE_URL": "https://ark.cn-beijing.volces.com/api/plan",
    /* 推理凭证，与额度查询用的 AK/SK 是两回事 */
    "ANTHROPIC_AUTH_TOKEN": "ark-test-key"
  },
  "quota": {
    "source": "ark",
    // 只读子账号的 AK，别用主账号
    "accessKey": "AKTEST",
    "secretKey": "SKTEST"
  }
}`

// TestCommentedProviderLoads 断言带注释的配置**正常加载**（之前是 JSON 解析失败）。
func TestCommentedProviderLoads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ark.json"), []byte(commentedProvider), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)

	infos, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("应列出 1 个 provider，实际 %d", len(infos))
	}
	info := infos[0]
	if info.Error != "" {
		t.Fatalf("带注释的配置不该报错: %s", info.Error)
	}
	if info.Note != "火山方舟-AgentPlan" {
		t.Errorf("Note = %q", info.Note)
	}
	if info.Order != 10 {
		t.Errorf("Order = %d", info.Order)
	}
	// 透传键数：注释**不算键**
	if info.PassthroughKeys != 0 {
		t.Errorf("透传键数 = %d，期望 0（注释不是键）", info.PassthroughKeys)
	}
}

// TestCommentedProviderGet 断言 `Get()` 也能读带注释的配置。
func TestCommentedProviderGet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ark.json"), []byte(commentedProvider), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)

	cfg, err := m.Get("ark")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	env, ok := cfg["env"].(map[string]any)
	if !ok {
		t.Fatalf("env 块不对: %v", cfg["env"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://ark.cn-beijing.volces.com/api/plan" {
		t.Errorf("base URL 不对: %v", env["ANTHROPIC_BASE_URL"])
	}
	// quota 块是 ccl 自己的元数据，也在同一个 map 里
	if _, ok := cfg["quota"]; !ok {
		t.Errorf("quota 块缺失: %v", cfg)
	}
}

// TestSaveRawPreservesComments 是**核心断言**：保存后**注释逐字节保留**。
//
// SaveRaw 是原样写盘（不重新序列化），所以只要校验端容忍注释，注释就不会丢。
func TestSaveRawPreservesComments(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if err := m.SaveRaw("ark", []byte(commentedProvider)); err != nil {
		t.Fatalf("SaveRaw 失败: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ark.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != commentedProvider {
		t.Errorf("保存后内容变了（注释丢了？）:\n got: %s\nwant: %s", got, commentedProvider)
	}
}

// TestSaveRawRejectsBrokenJSON 断言**坏 JSON 仍然被拒**。
//
// 容忍注释不等于容忍任何东西——JSON 本身写坏了照样要拒。
func TestSaveRawRejectsBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	if err := m.SaveRaw("bad", []byte(`{"env": {"A": "1",}}`)); err == nil {
		t.Error("尾随逗号应被拒")
	}
	if err := m.SaveRaw("bad2", []byte(`{"env": {"A": "1"} /* 没闭合`)); err == nil {
		t.Error("没闭合的块注释应被拒")
	}
}

// TestParseErrorPointsAtOriginalLine 断言**报的行号是原文件的行号**。
//
// 上面前 5 行有注释，第 6 行有语法错误——剥注释若动了位置，报出来的就会是别的行。
func TestParseErrorPointsAtOriginalLine(t *testing.T) {
	dir := t.TempDir()
	bad := "{\n" +
		"  // 注释 1\n" +
		"  // 注释 2\n" +
		"  /* 块 */\n" +
		"  // 注释 4\n" +
		"  \"env\": @\n" +
		"}"
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)

	infos, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("应列出 1 个，实际 %d", len(infos))
	}
	if infos[0].Error == "" {
		t.Fatal("坏配置应标 Error")
	}
	if !strings.Contains(infos[0].Error, "第 6 行") {
		t.Errorf("错误信息应指出第 6 行（实际报错: %s）", infos[0].Error)
	}
}

// TestCommentedConfigHasNoCommentKeys 断言**注释不会变成键**。
//
// 注释在解析阶段就没了——它既不在顶层键里，也不在 env 块里。
func TestCommentedConfigHasNoCommentKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ark.json"), []byte(commentedProvider), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)

	cfg, err := m.Get("ark")
	if err != nil {
		t.Fatal(err)
	}
	for k := range cfg {
		if strings.Contains(k, "注释") || strings.Contains(k, "方舟 Agent Plan") {
			t.Errorf("注释变成了顶层键: %q", k)
		}
	}
	env := cfg["env"].(map[string]any)
	if len(env) != 2 {
		t.Errorf("env 块应只有 2 个键（注释不算），实际 %d: %v", len(env), env)
	}
}
