package settingsschema

import (
	"strings"
	"testing"
)

// TestValidate_KnownKeyWrongTypeStops 断言：已知键类型不对 → 报错。
func TestValidate_KnownKeyWrongTypeStops(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]any
		want string // 期望出现在错误里的片段
	}{
		{"model 给对象", map[string]any{"model": map[string]any{"name": "x"}}, "model: 期望字符串"},
		{"model 给数字", map[string]any{"model": 123}, "model: 期望字符串"},
		{"effortLevel 给布尔", map[string]any{"effortLevel": true}, "effortLevel: 期望字符串"},
		{"verbose 给字符串", map[string]any{"verbose": "yes"}, "verbose: 期望布尔值"},
		{"cleanupPeriodDays 给字符串", map[string]any{"cleanupPeriodDays": "30"}, "cleanupPeriodDays: 期望数字"},
		{"permissions 给字符串", map[string]any{"permissions": "all"}, "permissions: 期望对象"},
		{"env 给数组", map[string]any{"env": []any{"A=B"}}, "env: 期望对象"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(tc.obj)
			if res.OK() {
				t.Fatalf("应该报错，实际通过: %+v", res)
			}
			joined := strings.Join(res.Errors, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("错误里应含 %q，实际:\n%s", tc.want, joined)
			}
			// 有错时不应交付该键
			if _, ok := res.Cleaned["model"]; ok && strings.Contains(tc.name, "model") {
				t.Errorf("校验失败时不应交付该键")
			}
		})
	}
}

// TestValidate_NonStringEnvValueNotFatal 断言：env 块里的非字符串值**不阻断**。
//
// 实测（2026-10-30，Claude Code 2.1.308）：env 里的值不是字符串时，Claude Code
// 不会丢弃整个 settings 文件，而是把那个值转成字符串写进进程环境。阻断在这里
// 是过严的——值由交付层统一转字符串 + 给警告即可。
func TestValidate_NonStringEnvValueNotFatal(t *testing.T) {
	res := Validate(map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL": "https://api.example.com",
			"API_TIMEOUT_MS":     3000000,
		},
	})
	if !res.OK() {
		t.Fatalf("env 里的非字符串值不该阻断: %+v", res.Errors)
	}
	env, ok := res.Cleaned["env"].(map[string]any)
	if !ok {
		t.Fatalf("env 块应保留: %+v", res.Cleaned)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.example.com" {
		t.Errorf("env 的字符串值应原样保留: %+v", env)
	}
}

// TestValidate_UnknownKeyPasses 断言：未知顶层键放行且原样交付。
func TestValidate_UnknownKeyPasses(t *testing.T) {
	obj := map[string]any{
		"someNewKey": map[string]any{"a": 1},
		"env":        map[string]any{"A": "B"},
	}
	res := Validate(obj)
	if !res.OK() {
		t.Fatalf("未知键不应报错: %+v", res.Errors)
	}
	got, ok := res.Cleaned["someNewKey"].(map[string]any)
	if !ok {
		t.Fatalf("未知键应原样交付: %+v", res.Cleaned)
	}
	if got["a"] != 1 {
		t.Errorf("未知键的值应原样保留: %+v", got)
	}
}

// TestValidate_BadArrayElementDropped 断言：字符串数组里的坏元素被剔除 + 警告，
// 其余元素保留（Claude Code 自己也只丢这个元素）。
func TestValidate_BadArrayElementDropped(t *testing.T) {
	res := Validate(map[string]any{
		"permissions": map[string]any{
			"allow": []any{123, "Bash(ls:*)"},
		},
	})
	if !res.OK() {
		t.Fatalf("坏元素不该阻断启动: %+v", res.Errors)
	}
	perms := res.Cleaned["permissions"].(map[string]any)
	allow := perms["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Bash(ls:*)" {
		t.Errorf("坏元素应被剔除、其余保留: %+v", allow)
	}
	if len(res.Warnings) == 0 {
		t.Error("剔除坏元素应产生警告")
	}
}

// TestValidate_ValidConfigUnchanged 断言：合法配置原样通过，形状不变。
func TestValidate_ValidConfigUnchanged(t *testing.T) {
	obj := map[string]any{
		"model":       "some-model",
		"env":         map[string]any{"ANTHROPIC_BASE_URL": "https://api.example.com"},
		"permissions": map[string]any{"allow": []any{"Bash(ls:*)"}},
		"note":        "ccl 的元数据（未知键 → 放行）",
	}
	res := Validate(obj)
	if !res.OK() {
		t.Fatalf("合法配置不该报错: %+v", res.Errors)
	}
	if res.Cleaned["model"] != "some-model" {
		t.Errorf("model 应原样保留: %+v", res.Cleaned["model"])
	}
	env := res.Cleaned["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "https://api.example.com" {
		t.Errorf("env 应原样保留: %+v", env)
	}
	if res.Cleaned["note"] != "ccl 的元数据（未知键 → 放行）" {
		t.Errorf("未知键应原样保留")
	}
}

// TestValidate_PermissionsChildTypeMismatchTolerated 断言：permissions 的子键
// 类型不符不阻断（Claude Code 对 permissions 子键写错不会丢整个文件）。
func TestValidate_PermissionsChildTypeMismatchTolerated(t *testing.T) {
	res := Validate(map[string]any{
		"permissions": map[string]any{
			"allow": "Bash(ls:*)", // 应该是数组
		},
	})
	if !res.OK() {
		t.Fatalf("permissions 子键类型不符不该阻断: %+v", res.Errors)
	}
}

// TestValidate_ErrorsSorted 断言：错误输出顺序稳定（map 遍历顺序随机）。
func TestValidate_ErrorsSorted(t *testing.T) {
	obj := map[string]any{
		"model":   1,
		"verbose": "x",
		"theme":   2,
	}
	res := Validate(obj)
	if len(res.Errors) != 3 {
		t.Fatalf("应有 3 条错误: %+v", res.Errors)
	}
	for i := 1; i < len(res.Errors); i++ {
		if res.Errors[i-1] > res.Errors[i] {
			t.Errorf("错误应按字典序: %+v", res.Errors)
			break
		}
	}
}

// TestKnownKeys_TableSanity 断言：键表本身没坏（白名单自检）。
func TestKnownKeys_TableSanity(t *testing.T) {
	keys := KnownKeys()
	if len(keys) < 150 {
		t.Errorf("顶层键表条目偏少（%d），可能漏抄: %+v", len(keys), keys)
	}
	// 关键键必须在表里（漏了会让类型写错不被拦）。
	for _, k := range []string{"env", "model", "permissions", "hooks", "statusLine", "outputStyle"} {
		if _, ok := topLevel[k]; !ok {
			t.Errorf("键表缺少 %q", k)
		}
	}
	// 表里的每个键都要能构造出节点（防止 nil 节点 panic）。
	for k, n := range topLevel {
		if n == nil {
			t.Errorf("键 %q 的节点为 nil", k)
			continue
		}
		switch n.Kind {
		case KindString, KindNumber, KindBool, KindArray, KindObject, KindAny:
		default:
			t.Errorf("键 %q 的类型非法: %v", k, n.Kind)
		}
	}
}
