package collector

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// ParseError 表示解析失败：HTTP 4xx（bad request），daemon 不应被击垮。
type ParseError struct{ Reason string }

func (e *ParseError) Error() string { return "parse: " + e.Reason }

// ParseResult 是单个 OTLP 导出请求的解析输出。
// 包含本次请求中识别出的 session 元数据 + 所有 data points。
type ParseResult struct {
	Sessions []Session // 唯一化的 session 列表（按 ID 去重）
	Metrics  []Metric  // 所有 number data points
}

// ParseMetricsRequest 解析 ExportMetricsServiceRequest protobuf bytes。
//
// 设计要点：
//   - 只处理 Sum / Gauge 的 NumberDataPoint；Histogram / Summary / ExponentialHistogram 跳过
//     （Claude Code 当前只发 Sum/Gauge；未来若发 Histogram 再补）
//   - Resource attributes 提取 session.id / provider / cwd / model
//   - Scope attributes + data point attributes 合并为 labels
//   - 不抛 panic：malformed protobuf → ParseError
func ParseMetricsRequest(body []byte) (*ParseResult, error) {
	var req collectormetrics.ExportMetricsServiceRequest
	if len(body) == 0 {
		return &ParseResult{}, nil // 空 body 视为空 export，返回 200
	}
	if err := proto.Unmarshal(body, &req); err != nil {
		return nil, &ParseError{Reason: "protobuf 解析失败: " + err.Error()}
	}

	result := &ParseResult{}
	sessionSeen := make(map[string]bool)

	for _, rm := range req.GetResourceMetrics() {
		if rm == nil {
			continue
		}
		resAttrs := attrsToMap(rm.GetResource().GetAttributes())

		// 从 resource attrs 提取 session 候选。
		// Claude Code 推送时通常带 session.id；其它字段从 label 反推。
		sessID := resAttrs["session.id"]
		if sessID == "" {
			// 没有 session.id 的 resource 不创建 session 行；metric 仍可记录。
			// 简化起见：用 resource 本身的某种"指纹"做 ID（这里直接用 service.name + 时间），
			// 但为了避免无意义 session 噪音，直接跳过 session 创建。
		}

		for _, sm := range rm.GetScopeMetrics() {
			if sm == nil {
				continue
			}
			scopeAttrs := attrsToMap(sm.GetScope().GetAttributes())

			for _, m := range sm.GetMetrics() {
				if m == nil {
					continue
				}
				extractFromMetric(m, resAttrs, scopeAttrs, sessID, result)
			}
		}

		// session 记录（按 resource 维度去重）。
		if sessID != "" && !sessionSeen[sessID] {
			result.Sessions = append(result.Sessions, buildSession(sessID, resAttrs))
			sessionSeen[sessID] = true
		}
	}

	return result, nil
}

// extractFromMetric 处理单个 Metric（Sum / Gauge / 其他类型）。
func extractFromMetric(
	m *metricpb.Metric,
	resAttrs, scopeAttrs map[string]string,
	sessID string,
	out *ParseResult,
) {
	name := m.GetName()

	switch v := m.Data.(type) {
	case *metricpb.Metric_Sum:
		for _, dp := range v.Sum.GetDataPoints() {
			out.Metrics = append(out.Metrics, dataPointToMetric(dp, name, resAttrs, scopeAttrs, sessID))
		}
	case *metricpb.Metric_Gauge:
		for _, dp := range v.Gauge.GetDataPoints() {
			out.Metrics = append(out.Metrics, dataPointToMetric(dp, name, resAttrs, scopeAttrs, sessID))
		}
	default:
		// Histogram / Summary / ExponentialHistogram 暂不处理。
		// claude_code 当前只发 Sum/Gauge，未来若发 Histogram 再补。
	}
}

// dataPointToMetric 把单个 NumberDataPoint 转成 Metric。
func dataPointToMetric(
	dp *metricpb.NumberDataPoint,
	metricName string,
	resAttrs, scopeAttrs map[string]string,
	sessID string,
) Metric {
	labels := make(map[string]string, len(resAttrs)+len(scopeAttrs)+8)

	// resource attrs：service.name / service.version 等
	for k, v := range resAttrs {
		// session.id 已经在 sessions 行；不在 metric labels 里重复
		if k == "session.id" {
			continue
		}
		labels[k] = v
	}
	// scope attrs
	maps.Copy(labels, scopeAttrs)
	// data point attrs（model / provider / session.id / cwd 都在这里）
	for _, kv := range dp.GetAttributes() {
		labels[kv.GetKey()] = anyValueToString(kv.GetValue())
	}

	ts := time.Unix(0, int64(dp.GetTimeUnixNano()))
	if dp.GetTimeUnixNano() == 0 {
		ts = time.Now()
	}

	value := 0.0
	switch v := dp.Value.(type) {
	case *metricpb.NumberDataPoint_AsDouble:
		value = v.AsDouble
	case *metricpb.NumberDataPoint_AsInt:
		value = float64(v.AsInt)
	}

	return Metric{
		SessionID:  sessID,
		Name:       metricName,
		Value:      value,
		Labels:     labels,
		CapturedAt: ts,
	}
}

// buildSession 从 resource attrs 构建 Session 行。
func buildSession(sessID string, resAttrs map[string]string) Session {
	return Session{
		ID:        sessID,
		StartedAt: time.Now(), // 由 caller 收到多个 resource 时保持唯一；
		// 精确 started_at 由 metrics 第一帧覆盖（store 层 UPDATE 处理）。
		Provider: resAttrs["provider"],
		Cwd:      resAttrs["cwd"],
		Model:    resAttrs["model"],
	}
}

// attrsToMap 把 []KeyValue 转为 string map；AnyValue 转 string 表示。
func attrsToMap(attrs []*commonpb.KeyValue) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		if kv == nil {
			continue
		}
		m[kv.GetKey()] = anyValueToString(kv.GetValue())
	}
	return m
}

// anyValueToString 把 AnyValue 转 string；非字符串类型走 fmt。
func anyValueToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch val := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", val.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", val.DoubleValue)
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprintf("%t", val.BoolValue)
	default:
		return ""
	}
}

// IsParseError 报告 err 是否为 ParseError（4xx 信号）。
func IsParseError(err error) bool {
	var pe *ParseError
	return errors.As(err, &pe)
}

// summarizeLabels 用于错误日志（避免大对象刷屏）。
func summarizeLabels(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	var parts []string
	for k, v := range m {
		parts = append(parts, k+"="+v)
		if len(parts) >= 6 {
			break
		}
	}
	s := strings.Join(parts, ",")
	if len(m) > 6 {
		s += ",..."
	}
	return s
}