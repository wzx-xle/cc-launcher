package quota

import (
	"testing"
	"time"
)

// TestUsedPct 断言已用百分比是**派生**的，且各种边界都返回 nil 而不是 0。
func TestUsedPct(t *testing.T) {
	cases := []struct {
		name string
		d    Dimension
		want *float64
	}{
		{
			name: "有分母",
			d:    Dimension{Used: f64(4), Limit: f64(100)},
			want: f64(4),
		},
		{
			name: "无分母",
			d:    Dimension{Used: f64(4)},
			want: nil,
		},
		{
			name: "零分母",
			d:    Dimension{Used: f64(4), Limit: f64(0)},
			want: nil,
		},
		{
			name: "无限额度",
			d:    Dimension{Used: f64(4), Limit: f64(100), Unlimited: true},
			want: nil,
		},
		{
			name: "没有已用量",
			d:    Dimension{Limit: f64(100)},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.d.UsedPct()
			if tc.want == nil {
				if got != nil {
					t.Fatalf("应返回 nil, got %v", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("应有值, got nil")
			}
			if *got != *tc.want {
				t.Errorf("UsedPct = %v, want %v", *got, *tc.want)
			}
		})
	}
}

// TestUsedPct_NewAPI 断言 New API 的真实数值能派生出正确百分比
// （200000000 分母 / 2576 已用 → 0.001288%）。
func TestUsedPct_NewAPI(t *testing.T) {
	d := Dimension{Used: f64(2576), Limit: f64(200000000)}
	pct := d.UsedPct()
	if pct == nil {
		t.Fatal("应有百分比")
	}
	if *pct < 0.00128 || *pct > 0.00130 {
		t.Errorf("UsedPct = %v, 期望约 0.001288", *pct)
	}
}

// TestDeriveLabel 断言 label 的派生规则：窗口型从时长来，其它型用 unit。
func TestDeriveLabel(t *testing.T) {
	cases := []struct {
		kind     Kind
		duration time.Duration
		unit     string
		want     string
	}{
		{KindWindow, 18000 * time.Second, "%", "5h"},
		{KindWindow, 604800 * time.Second, "%", "7d"},
		{KindWindow, 2592000 * time.Second, "%", "30d"},
		{KindWindow, 3600 * time.Second, "%", "1h"},
		{KindWindow, 120 * time.Second, "%", "2m"},
		{KindWindow, 90 * time.Second, "%", "90s"},
		{KindWindow, 45 * time.Second, "%", "45s"},
		{KindQuota, 0, "USD", "USD"},
		{KindBalance, 0, "CNY", "CNY"},
		{KindQuota, 0, "tokens", "tokens"},
	}
	for _, tc := range cases {
		got := DeriveLabel(tc.kind, tc.duration, tc.unit)
		if got != tc.want {
			t.Errorf("DeriveLabel(%s, %v, %q) = %q, want %q", tc.kind, tc.duration, tc.unit, got, tc.want)
		}
	}
}

// TestFinalize_SanitizesUnit 断言 unit 被清洗成纯 ASCII，且 label 跟着变。
//
// 厂商可能返回中文货币名或含控制字符的字符串——状态栏渲染到终端一行里，
// 非 ASCII 直接没法读。
func TestFinalize_SanitizesUnit(t *testing.T) {
	d := Dimension{Kind: KindBalance, Unit: "人民币\x00"}
	d.Finalize()
	// 非 ASCII 字符被删掉 → unit 清空 → 兜底成 "amount"（不能留空）。
	if d.Unit != "amount" {
		t.Errorf("unit 应被清成 ASCII 并兜底: %q", d.Unit)
	}
	if d.Label != d.Unit {
		t.Errorf("非窗口型 label 应等于 unit: %q vs %q", d.Label, d.Unit)
	}

	// 全是控制字符 → 兜底成 "amount"，不能留空
	d2 := Dimension{Kind: KindBalance, Unit: "\x00\x01"}
	d2.Finalize()
	if d2.Unit == "" || d2.Label == "" {
		t.Errorf("兜底后不该为空: %+v", d2)
	}
}

// TestFinalize_LabelNeverFromVendor 断言窗口型 label 永远来自时长，
// 不来自厂商的原始字符串。
func TestFinalize_LabelNeverFromVendor(t *testing.T) {
	d := Dimension{Kind: KindWindow, Duration: 18000 * time.Second, Unit: "%"}
	d.Finalize()
	if d.Label != "5h" {
		t.Errorf("窗口型 label 应为 5h, got %q", d.Label)
	}
}

// TestSanitizeASCII 断言清洗规则。
func TestSanitizeASCII(t *testing.T) {
	if got := SanitizeASCII("CNY"); got != "CNY" {
		t.Errorf("ASCII 应原样保留: %q", got)
	}
	if got := SanitizeASCII("C¥Y"); got != "CY" {
		t.Errorf("非 ASCII 应被删掉: %q", got)
	}
	if got := SanitizeASCII("a\tb\nc"); got != "abc" {
		t.Errorf("控制字符应被删掉: %q", got)
	}
}
