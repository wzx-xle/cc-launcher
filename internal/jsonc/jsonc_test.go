package jsonc

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestStripCommentsKeepsPositions 断言剥注释**不改变任何字节位置**。
//
// 这是整个实现的地基：位置不变 → 标准库给的字节偏移仍然对得上原文件 →
// 报出来的行号才是原文件的行号。删掉注释会让后面所有行往上移，用户看到
// "第 3 行出错"实际是第 47 行写的注释出了问题。
func TestStripCommentsKeepsPositions(t *testing.T) {
	in := []byte(`{
  "env": {
    // 一条注释
    "A": "1"
  },
  /* 块
     注释 */
  "b": 2
}`)
	out, err := StripComments(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("长度变了: %d → %d", len(in), len(out))
	}
	// 换行数必须一样（行号才对得上）
	if strings.Count(string(in), "\n") != strings.Count(string(out), "\n") {
		t.Errorf("换行数变了")
	}
	// 注释被换成空白，其它字符原样保留
	if strings.Contains(string(out), "一条注释") || strings.Contains(string(out), "块") {
		t.Errorf("注释没被替换掉: %s", out)
	}
	if !strings.Contains(string(out), `"A": "1"`) {
		t.Errorf("正常内容被破坏了: %s", out)
	}
}

// TestStripCommentsIdentityOnPlainJSON 断言无注释的 JSON 剥离后**逐字节相同**。
//
// 这是"零迁移"的前提：不带注释的配置解析行为必须完全不变。
func TestStripCommentsIdentityOnPlainJSON(t *testing.T) {
	in := []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://ark.cn-beijing.volces.com/api/plan","N":1}}`)
	out, err := StripComments(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("无注释的 JSON 被改动了:\n got: %s\nwant: %s", out, in)
	}
}

// TestStringContentIsNotComment 是**护栏测试**：字符串里的注释标记不是注释。
//
// provider 配置里全是 URL —— `https://ark.cn-beijing.volces.com/api/plan` 里的
// `//` 若被当注释，整行就废了。这是本实现最容易写错的地方。
func TestStringContentIsNotComment(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"URL 里的双斜杠", `{"base":"https://ark.cn-beijing.volces.com/api/plan"}`},
		{"URL 里的双斜杠带端口", `{"base":"http://127.0.0.1:8317/api/usage"}`},
		{"字符串里的块注释标记", `{"note":"use /* this */ literally"}`},
		{"字符串里的双斜杠", `{"note":"see // docs"}`},
		{"转义引号后的双斜杠", `{"note":"say \"hi\" // now"}`},
		{"转义反斜杠后的双斜杠", `{"note":"path\\\\ // not a comment"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			if err := Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			// 关键：字符串的值必须原样保留
			for k, v := range got {
				if s, ok := v.(string); ok {
					if !strings.Contains(s, "//") && !strings.Contains(s, "/*") {
						t.Errorf("%s 的值被破坏了: %q", k, s)
					}
				}
			}
		})
	}
}

// TestStringValuesPreserved 断言字符串的值**逐字节**保留。
func TestStringValuesPreserved(t *testing.T) {
	in := `{"a":"https://x.example/p","b":"use /* this */ literally","c":"say \"hi\" // now"}`
	var got map[string]string
	if err := Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"a": "https://x.example/p",
		"b": "use /* this */ literally",
		"c": `say "hi" // now`,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q，期望 %q", k, got[k], w)
		}
	}
}

// TestCommentsInEveryPosition 断言注释出现在**任何位置**都能处理。
func TestCommentsInEveryPosition(t *testing.T) {
	in := `{
  // 键之前
  "env": { /* 冒号后面 */ "A": "1" // 值后面
  },
  // 成员之间
  "b": [1, /* 数组里 */ 2],
  "c": 3 // 最后一行
}`
	var got map[string]any
	if err := Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if got["b"].([]any)[1].(float64) != 2 {
		t.Errorf("数组解析错了: %v", got["b"])
	}
	if got["c"].(float64) != 3 {
		t.Errorf("最后一个成员解析错了: %v", got["c"])
	}
}

// TestErrorLinePointsToOriginalFile 是**护栏测试**：报的行号是**原文件**的行号。
//
// 上面前 9 行都有注释，第 10 行有语法错误——剥注释若动了位置，报出来的
// 就会是别的行。
func TestErrorLinePointsToOriginalFile(t *testing.T) {
	in := "{\n" +
		"  // 注释 1\n" +
		"  // 注释 2\n" +
		"  /* 块注释 */\n" +
		"  // 注释 3\n" +
		"  // 注释 4\n" +
		"  // 注释 5\n" +
		"  // 注释 6\n" +
		"  // 注释 7\n" +
		"  \"a\": @\n" +
		"  // 注释 8\n" +
		"}"
	var got map[string]any
	err := Unmarshal([]byte(in), &got)
	if err == nil {
		t.Fatal("应该报错")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("应该返回 *SyntaxError，实际 %T: %v", err, err)
	}
	if se.Line != 10 {
		t.Errorf("行号 = %d，期望 10（错误信息: %v）", se.Line, err)
	}
	if !strings.Contains(err.Error(), "第 10 行") {
		t.Errorf("错误信息应含行号: %v", err)
	}
}

// TestSyntaxErrorUnwraps 保证 errors.As 仍能拿到原始的 *json.SyntaxError。
//
// 包装错误不能破坏错误链——调用方可能按标准类型判断。
func TestSyntaxErrorUnwraps(t *testing.T) {
	var got map[string]any
	err := Unmarshal([]byte(`{"a": @}`), &got)
	var se *json.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("包装后仍应能拿到 *json.SyntaxError，实际 %T", err)
	}
}

// TestTrailingCommaRejected 断言**尾随逗号仍然报错**。
//
// JSONC 规范（jsonc.org）不允许尾随逗号；而且尾随逗号是"删键忘删逗号"的
// 高发区，报错比容忍更有帮助。
func TestTrailingCommaRejected(t *testing.T) {
	var got map[string]any
	if err := Unmarshal([]byte(`{"env": {"A": "1"},}`), &got); err == nil {
		t.Error("尾随逗号应报错")
	}
}

// TestUnclosedBlockComment 断言块注释没闭合时报错。
func TestUnclosedBlockComment(t *testing.T) {
	var got map[string]any
	err := Unmarshal([]byte(`{"a": 1} /* 没闭合`), &got)
	if err == nil {
		t.Fatal("没闭合的块注释应报错")
	}
	if !strings.Contains(err.Error(), "闭合") {
		t.Errorf("错误信息应说清是注释没闭合: %v", err)
	}
}

// TestUnclosedStringIsJSONError 断言字符串没闭合时**交给 json.Unmarshal 报错**。
//
// 它的错误信息更准（带位置），而且能区分"是没闭合的字符串"还是"没闭合的注释"。
func TestUnclosedStringIsJSONError(t *testing.T) {
	var got map[string]any
	err := Unmarshal([]byte(`{"a": "未闭合`), &got)
	if err == nil {
		t.Fatal("未闭合的字符串应报错")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Errorf("应返回带位置的错误，实际 %T: %v", err, err)
	}
}

// TestLineCommentAtEndOfFile 断言行注释在**文件末尾**（没有换行）也能处理。
func TestLineCommentAtEndOfFile(t *testing.T) {
	var got map[string]any
	if err := Unmarshal([]byte(`{"a": 1} // 末尾注释`), &got); err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if got["a"].(float64) != 1 {
		t.Errorf("解析错了: %v", got)
	}
}

// TestUnterminatedStringWithSlash 断言字符串没闭合时，其中的 `//` 不会被当注释。
//
// 这是状态机的边界：字符串态一路走到文件末尾，后面的 `//` 必须仍然被当字符串
// 内容（否则会"意外"闭合字符串或吃掉内容）。
func TestUnterminatedStringWithSlash(t *testing.T) {
	var got map[string]any
	err := Unmarshal([]byte(`{"a": "未闭合 // 里面有双斜杠`), &got)
	if err == nil {
		t.Fatal("未闭合的字符串应报错")
	}
}
