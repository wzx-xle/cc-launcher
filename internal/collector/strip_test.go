package collector

import (
	"testing"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// makePayloadWithAttrs 造一个带指定 resource attributes 的 payload。
func makePayloadWithAttrs(t *testing.T, attrs []*commonpb.KeyValue) []byte {
	t.Helper()
	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{Attributes: attrs},
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Metrics: []*metricpb.Metric{
							{
								Name: "claude_code.token.usage",
								Data: &metricpb.Metric_Sum{
									Sum: &metricpb.Sum{
										DataPoints: []*metricpb.NumberDataPoint{
											{
												TimeUnixNano: 1_700_000_000_000_000_000,
												Value:        &metricpb.NumberDataPoint_AsInt{AsInt: 100},
												Attributes: []*commonpb.KeyValue{
													kv("session.id", "sess-1"),
													kv("type", "input"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	b, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// attrsOf 解析 payload 里的 resource attribute 键值。
func attrsOf(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var req collectormetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := map[string]string{}
	for _, rm := range req.GetResourceMetrics() {
		for _, kv := range rm.GetResource().GetAttributes() {
			out[kv.GetKey()] = kv.GetValue().GetStringValue()
		}
	}
	return out
}

// TestStripCclAttributes_RemovesCclKeys 断言 ccl.* 三个键被删掉、用户的键保留。
func TestStripCclAttributes_RemovesCclKeys(t *testing.T) {
	body := makePayloadWithAttrs(t, []*commonpb.KeyValue{
		kv("service.name", "claude-code"),
		kv("team", "platform"),
		kv("ccl.provider", "minimax"),
		kv("ccl.cwd", "/home/u/proj"),
		kv("ccl.provider_endpoint", "api.minimax.cn"),
	})

	out, err := stripCclAttributes(body)
	if err != nil {
		t.Fatalf("stripCclAttributes: %v", err)
	}
	got := attrsOf(t, out)
	for _, k := range cclAttributeKeys {
		if _, ok := got[k]; ok {
			t.Errorf("ccl 属性 %q 应被剥掉: %v", k, got)
		}
	}
	if got["team"] != "platform" {
		t.Errorf("用户的属性应保留: %v", got)
	}
	if got["service.name"] != "claude-code" {
		t.Errorf("Claude Code 自带的属性应保留: %v", got)
	}
}

// TestStripCclAttributes_NoCclKeysUnchanged 断言没有 ccl.* 时 body 原样返回
// （不做无谓的 unmarshal/marshal）。
func TestStripCclAttributes_NoCclKeysUnchanged(t *testing.T) {
	body := makePayloadWithAttrs(t, []*commonpb.KeyValue{
		kv("service.name", "claude-code"),
		kv("team", "platform"),
	})
	out, err := stripCclAttributes(body)
	if err != nil {
		t.Fatalf("stripCclAttributes: %v", err)
	}
	if len(out) != len(body) {
		t.Fatalf("无 ccl.* 时 body 应原样返回")
	}
	got := attrsOf(t, out)
	if got["team"] != "platform" {
		t.Errorf("属性应保留: %v", got)
	}
}

// TestStripCclAttributes_Malformed 断言畸形 payload 不 panic、按原样返回。
func TestStripCclAttributes_Malformed(t *testing.T) {
	body := []byte{0xff, 0x00, 0xff}
	out, err := stripCclAttributes(body)
	if err == nil {
		t.Fatal("畸形 payload 应报错")
	}
	if len(out) != len(body) {
		t.Fatalf("失败时应原样返回 body")
	}
}

// TestStripCclAttributes_KeepsOtherFields 断言剥属性不影响 payload 的其它部分
// （session.id / metric 名 / 值）。
func TestStripCclAttributes_KeepsOtherFields(t *testing.T) {
	body := makePayloadWithAttrs(t, []*commonpb.KeyValue{
		kv("ccl.provider", "minimax"),
	})
	out, err := stripCclAttributes(body)
	if err != nil {
		t.Fatalf("stripCclAttributes: %v", err)
	}
	var req collectormetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rms := req.GetResourceMetrics()
	if len(rms) != 1 {
		t.Fatalf("want 1 resource metrics, got %d", len(rms))
	}
	ms := rms[0].GetScopeMetrics()[0].GetMetrics()
	if len(ms) != 1 || ms[0].GetName() != "claude_code.token.usage" {
		t.Fatalf("metric 丢失: %+v", ms)
	}
	dp := ms[0].GetSum().GetDataPoints()[0]
	if v, ok := dp.GetValue().(*metricpb.NumberDataPoint_AsInt); !ok || v.AsInt != 100 {
		t.Errorf("metric 值被改: %v", dp.GetValue())
	}
}

// TestStripCclAttributes_RemovesFromDataPoints 断言 ccl.* 也从 **datapoint 的
// attributes** 里剥掉。
//
// 2026-10-28 真机实测发现：Claude Code 的 OTel SDK 把 OTEL_RESOURCE_ATTRIBUTES
// 的内容**同时**放进 Resource 的 attributes 和**每个 data point 的 attributes**
// ——只剥 resource 的话，datapoint 里那份会原样转发出去 → ccl.* 仍泄漏到远程。
func TestStripCclAttributes_RemovesFromDataPoints(t *testing.T) {
	body := makePayloadWithAttrs(t, []*commonpb.KeyValue{
		kv("service.name", "claude-code"),
		kv("team", "platform"),
	})
	// 手动往 datapoint attrs 里塞 ccl.*（模拟 Claude Code 的真实形态）
	var req collectormetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	dp := req.GetResourceMetrics()[0].GetScopeMetrics()[0].GetMetrics()[0].GetSum().GetDataPoints()[0]
	dp.Attributes = append(dp.Attributes,
		kv("ccl.provider", "minimax"),
		kv("ccl.cwd", "/home/u/proj"),
		kv("ccl.provider_endpoint", "api.minimax.cn"),
		kv("session.id", "sess-1"),
	)
	body, err := proto.Marshal(&req)
	if err != nil {
		t.Fatal(err)
	}

	out, err := stripCclAttributes(body)
	if err != nil {
		t.Fatalf("stripCclAttributes: %v", err)
	}
	var got collectormetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	gotDP := got.GetResourceMetrics()[0].GetScopeMetrics()[0].GetMetrics()[0].GetSum().GetDataPoints()[0]
	seen := map[string]bool{}
	for _, kv := range gotDP.GetAttributes() {
		seen[kv.GetKey()] = true
	}
	for _, k := range cclAttributeKeys {
		if seen[k] {
			t.Errorf("datapoint 里的 ccl 属性 %q 应被剥掉: %v", k, seen)
		}
	}
	if !seen["session.id"] {
		t.Errorf("datapoint 的其它属性应保留: %v", seen)
	}
}
