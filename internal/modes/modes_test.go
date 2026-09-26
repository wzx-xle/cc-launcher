package modes

import (
	"strings"
	"testing"
)

// Names() 不重不漏，且顺序与 all 一致。
func TestNamesMatchAll(t *testing.T) {
	got := Names()
	if len(got) != len(all) {
		t.Fatalf("Names() 长度 %d 与 all 长度 %d 不一致", len(got), len(all))
	}
	for i := range all {
		if got[i] != all[i].Name {
			t.Errorf("Names()[%d] = %q，期望 %q（顺序应与 all 一致）", i, got[i], all[i].Name)
		}
	}
}

// 每个模式都必须有非空中文描述——描述是用户做选择的唯一依据，空描述等于误导。
func TestModesHaveDescriptions(t *testing.T) {
	for _, m := range all {
		if strings.TrimSpace(m.Description) == "" {
			t.Errorf("模式 %q 缺少说明文案", m.Name)
		}
		if strings.TrimSpace(m.Name) == "" {
			t.Error("出现空 Name 条目")
		}
	}
}

// 名称必须全局唯一，否则 ByName 的语义无定义。
func TestNamesAreUnique(t *testing.T) {
	seen := make(map[string]struct{}, len(all))
	for _, m := range all {
		if _, dup := seen[m.Name]; dup {
			t.Errorf("重复的模式名 %q", m.Name)
		}
		seen[m.Name] = struct{}{}
	}
}

// ByName 的命中与未命中两条路径都要覆盖。
func TestByName(t *testing.T) {
	if _, ok := ByName("plan"); !ok {
		t.Error("ByName(\"plan\") 应命中")
	}
	if _, ok := ByName("definitely-not-A-mode"); ok {
		t.Error("ByName(\"definitely-not-A-mode\") 应返回 ok=false")
	}
}