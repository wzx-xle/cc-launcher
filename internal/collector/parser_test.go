package collector

import (
	"testing"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func kv(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: k,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
	}
}

// makeTestRequest 模拟 Claude Code v2.1.x 真实 OTel payload 形态：
//   - resource attrs 不含 session.id
//   - session.id 在 datapoint attrs
//   - type ∈ {input, output, cacheRead, cacheCreation}
func makeTestRequest(t *testing.T) []byte {
	t.Helper()

	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kv("service.name", "claude-code"),
						kv("service.version", "2.1.267"),
					},
				},
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Scope: &commonpb.InstrumentationScope{Name: "com.anthropic.claude_code"},
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
													kv("model", "deepseek-chat"),
												},
											},
											{
												TimeUnixNano: 1_700_000_001_000_000_000,
												Value:        &metricpb.NumberDataPoint_AsInt{AsInt: 50},
												Attributes: []*commonpb.KeyValue{
													kv("session.id", "sess-1"),
													kv("type", "output"),
													kv("model", "deepseek-chat"),
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
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestParseMetricsRequest_BasicSum(t *testing.T) {
	body := makeTestRequest(t)
	res, err := ParseMetricsRequest(body)
	if err != nil {
		t.Fatalf("ParseMetricsRequest: %v", err)
	}

	if len(res.Metrics) != 2 {
		t.Fatalf("want 2 metrics, got %d", len(res.Metrics))
	}

	if res.Metrics[0].Name != "claude_code.token.usage" {
		t.Errorf("name: got %q", res.Metrics[0].Name)
	}
	if res.Metrics[0].Value != 100 {
		t.Errorf("value: got %v", res.Metrics[0].Value)
	}
	if got := res.Metrics[0].Labels["type"]; got != "input" {
		t.Errorf("label type: got %q", got)
	}
	if got := res.Metrics[0].Labels["model"]; got != "deepseek-chat" {
		t.Errorf("label model: got %q", got)
	}
	// session.id 应被提到 SessionID 字段，不在 labels 里
	if res.Metrics[0].SessionID != "sess-1" {
		t.Errorf("session id: got %q", res.Metrics[0].SessionID)
	}
	if _, ok := res.Metrics[0].Labels["session.id"]; ok {
		t.Errorf("session.id should not be in metric labels")
	}

	// session 行被创建
	if len(res.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(res.Sessions))
	}
	if res.Sessions[0].ID != "sess-1" {
		t.Errorf("session row id: %s", res.Sessions[0].ID)
	}
	if res.Sessions[0].Model != "deepseek-chat" {
		t.Errorf("session row model: %s", res.Sessions[0].Model)
	}
}

func TestParseMetricsRequest_EmptyBody(t *testing.T) {
	res, err := ParseMetricsRequest(nil)
	if err != nil {
		t.Fatalf("nil body should be no-op, got %v", err)
	}
	if len(res.Metrics) != 0 || len(res.Sessions) != 0 {
		t.Errorf("empty body should produce empty result")
	}
}

func TestParseMetricsRequest_Malformed(t *testing.T) {
	_, err := ParseMetricsRequest([]byte{0xff, 0x00, 0xff})
	if err == nil {
		t.Fatal("malformed bytes should error")
	}
	if !IsParseError(err) {
		t.Errorf("expected ParseError, got %T", err)
	}
}

func TestParseMetricsRequest_Gauge(t *testing.T) {
	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kv("service.name", "claude-code"),
					},
				},
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Metrics: []*metricpb.Metric{
							{
								Name: "claude_code.session.count",
								Data: &metricpb.Metric_Gauge{
									Gauge: &metricpb.Gauge{
										DataPoints: []*metricpb.NumberDataPoint{
											{
												TimeUnixNano: 1_700_000_002_000_000_000,
												Value:        &metricpb.NumberDataPoint_AsDouble{AsDouble: 1.0},
												Attributes: []*commonpb.KeyValue{
													kv("session.id", "sess-X"),
													kv("model", "claude-opus-4"),
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
	body, _ := proto.Marshal(req)
	res, err := ParseMetricsRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Metrics) != 1 {
		t.Fatalf("want 1, got %d", len(res.Metrics))
	}
	if res.Metrics[0].Value != 1.0 {
		t.Errorf("gauge value: got %v", res.Metrics[0].Value)
	}
	if res.Metrics[0].SessionID != "sess-X" {
		t.Errorf("session id: got %q", res.Metrics[0].SessionID)
	}
	if len(res.Sessions) != 1 || res.Sessions[0].ID != "sess-X" {
		t.Errorf("session row missing: %+v", res.Sessions)
	}
}

func TestParseMetricsRequest_CacheLabelValues(t *testing.T) {
	// 验证 cacheRead / cacheCreation 被正确识别
	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Metrics: []*metricpb.Metric{
							{
								Name: "claude_code.token.usage",
								Data: &metricpb.Metric_Sum{
									Sum: &metricpb.Sum{
										DataPoints: []*metricpb.NumberDataPoint{
											{Value: &metricpb.NumberDataPoint_AsInt{AsInt: 5}, Attributes: []*commonpb.KeyValue{kv("session.id", "s"), kv("type", "cacheRead")}},
											{Value: &metricpb.NumberDataPoint_AsInt{AsInt: 7}, Attributes: []*commonpb.KeyValue{kv("session.id", "s"), kv("type", "cacheCreation")}},
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
	body, _ := proto.Marshal(req)
	res, _ := ParseMetricsRequest(body)
	if len(res.Metrics) != 2 {
		t.Fatalf("want 2, got %d", len(res.Metrics))
	}
	types := []string{res.Metrics[0].Labels["type"], res.Metrics[1].Labels["type"]}
	if types[0] != "cacheRead" || types[1] != "cacheCreation" {
		t.Errorf("labels not preserved: %v", types)
	}
}

// TestParseMetricsRequest_SessionModelFromLaterMetric 验证：session.count
// 先到（没 model）→ cost.usage 后到（有 model）→ session 行应有 model。
//
// 这模拟 Claude Code 实际推送顺序（实测 session.count 是 Gauge 第一个 datapoint，
// cost/token 是后续 Sum）。如果 session row 的 model 仍是空，说明逻辑没生效。
func TestParseMetricsRequest_SessionModelFromLaterMetric(t *testing.T) {
	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Metrics: []*metricpb.Metric{
							{
								Name: "claude_code.session.count",
								Data: &metricpb.Metric_Gauge{
									Gauge: &metricpb.Gauge{
										DataPoints: []*metricpb.NumberDataPoint{
											{Value: &metricpb.NumberDataPoint_AsInt{AsInt: 1}, Attributes: []*commonpb.KeyValue{kv("session.id", "S1")}},
										},
									},
								},
							},
							{
								Name: "claude_code.cost.usage",
								Data: &metricpb.Metric_Sum{
									Sum: &metricpb.Sum{
										DataPoints: []*metricpb.NumberDataPoint{
											{Value: &metricpb.NumberDataPoint_AsDouble{AsDouble: 0.01}, Attributes: []*commonpb.KeyValue{kv("session.id", "S1"), kv("model", "claude-haiku-4-5")}},
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
	body, _ := proto.Marshal(req)
	res, err := ParseMetricsRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(res.Sessions))
	}
	if res.Sessions[0].Model != "claude-haiku-4-5" {
		t.Errorf("session row model: want %q, got %q (session.count came first without model; cost.usage later should fill in)", "claude-haiku-4-5", res.Sessions[0].Model)
	}
}