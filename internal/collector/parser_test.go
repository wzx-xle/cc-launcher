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

// 构造一个最简 ExportMetricsServiceRequest：1 resource × 1 scope × 1 Sum × 2 datapoints
func makeTestRequest(t *testing.T) []byte {
	t.Helper()

	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				Resource: resourcepbResource(),
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Scope: &commonpb.InstrumentationScope{Name: "claude_code"},
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
													kv("type", "input"),
													kv("model", "deepseek-chat"),
													kv("provider", "deepseek"),
												},
											},
											{
												TimeUnixNano: 1_700_000_001_000_000_000,
												Value:        &metricpb.NumberDataPoint_AsInt{AsInt: 50},
												Attributes: []*commonpb.KeyValue{
													kv("type", "output"),
													kv("model", "deepseek-chat"),
													kv("provider", "deepseek"),
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

// resourcepbResource 单独抽出来因为循环引用问题——不要在 makeTestRequest 里 new
func resourcepbResource() *resourcepb.Resource {
	return &resourcepb.Resource{
		Attributes: []*commonpb.KeyValue{
			kv("session.id", "sess-1"),
			kv("service.name", "claude-code"),
		},
	}
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
	if got := res.Metrics[0].Labels["provider"]; got != "deepseek" {
		t.Errorf("label provider: got %q", got)
	}
	// session.id 不应进入 metric labels（spec：去重）
	if _, ok := res.Metrics[0].Labels["session.id"]; ok {
		t.Errorf("session.id should not be in metric labels")
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
				Resource: resourcepbResource(),
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Scope: &commonpb.InstrumentationScope{Name: "claude_code"},
						Metrics: []*metricpb.Metric{
							{
								Name: "claude_code.session.count",
								Data: &metricpb.Metric_Gauge{
									Gauge: &metricpb.Gauge{
										DataPoints: []*metricpb.NumberDataPoint{
											{
												TimeUnixNano: 1_700_000_002_000_000_000,
												Value:        &metricpb.NumberDataPoint_AsDouble{AsDouble: 1.0},
												Attributes:   []*commonpb.KeyValue{kv("provider", "anthropic")},
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
}