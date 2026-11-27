package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commentedProviderContent 是带注释的 provider 配置。
const commentedProviderContent = `{
  // 额度走 Access Key 签名查（不是推理用的 ark-… key）
  "note": "火山方舟-AgentPlan",
  "env": {
    // Agent Plan 专用路径前缀
    "ANTHROPIC_BASE_URL": "https://ark.cn-beijing.volces.com/api/plan",
    "ANTHROPIC_AUTH_TOKEN": "ark-test-key"
  }
}`

// TestAPIProviderPut_PreservesComments 断言**网页保存后注释逐字节保留**。
//
// SaveRaw 是原样写盘（不重新序列化），所以注释不该丢——
// 这条是"用户在网页里改配置时不丢注释"的保障。
func TestAPIProviderPut_PreservesComments(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "ark", commentedProviderContent)

	// 读一次拿到 ETag
	_, body := e.do(t, http.MethodGet, "/api/providers/ark", nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)
	if !strings.Contains(env.Content, "额度走 Access Key") {
		t.Fatalf("读回来的内容应含注释: %q", env.Content)
	}

	// 改一个值（保留注释），存回去
	updated := strings.Replace(commentedProviderContent, "ark-test-key", "ark-new-key", 1)
	resp, respBody := e.do(t, http.MethodPut, "/api/providers/ark",
		writeEnvelope{Content: updated},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, respBody)
	}

	data, err := os.ReadFile(filepath.Join(e.providersDir, "ark.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != updated {
		t.Errorf("落盘内容不对（注释丢了？）:\n got: %s\nwant: %s", data, updated)
	}
	if !strings.Contains(string(data), "额度走 Access Key") {
		t.Errorf("注释丢了: %s", data)
	}
}

// TestAPIProviderPut_RejectsBrokenJSON 断言**坏 JSON 仍然被拒**。
//
// 容忍注释 ≠ 容忍任何东西。
func TestAPIProviderPut_RejectsBrokenJSON(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "ark", commentedProviderContent)

	_, body := e.do(t, http.MethodGet, "/api/providers/ark", nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	resp, respBody := e.do(t, http.MethodPut, "/api/providers/ark",
		writeEnvelope{Content: `{"env": {"A": "1",}}`},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 400 {
		t.Fatalf("尾随逗号应被拒（状态 = %d），body = %s", resp.StatusCode, respBody)
	}
}

// TestAPIProviderPut_CommentSyntaxErrorPointsAtLine 断言**报错带行号**。
func TestAPIProviderPut_CommentSyntaxErrorPointsAtLine(t *testing.T) {
	e := newConsoleServer(t)
	e.writeProvider(t, "ark", commentedProviderContent)

	_, body := e.do(t, http.MethodGet, "/api/providers/ark", nil, nil)
	var env fileEnvelope
	_ = json.Unmarshal(body, &env)

	bad := "{\n  // 注释\n  \"env\": @\n}"
	resp, respBody := e.do(t, http.MethodPut, "/api/providers/ark",
		writeEnvelope{Content: bad},
		map[string]string{"If-Match": env.ETag})
	if resp.StatusCode != 400 {
		t.Fatalf("状态 = %d，body = %s", resp.StatusCode, respBody)
	}
	if !strings.Contains(string(respBody), "第 3 行") {
		t.Errorf("错误信息应指出第 3 行: %s", respBody)
	}
}
