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
type ParseResult struct {
	Sessions []Session // 唯一化的 session 列表（按 ID 去重）
	Metrics  []Metric  // 所有 number data points
}

// ParseMetricsRequest 解析 ExportMetricsServiceRequest protobuf bytes。
//
// Claude Code 实际 payload 形态（v2.1.x 验证）：
//   - resource attrs: host.arch / os.type / os.version / service.name / service.version / terminal.type / user.id
//   - datapoint attrs: session.id / model / type (input|output|cacheRead|cacheCreation) / start_type / query_source
//   - session.id 出现在 datapoint attrs（不是 resource attrs）
//   - provider / cwd Claude Code 推送中 **不存在**
//   - 已知 metric names: claude_code.session.count / claude_code.token.usage /
//     claude_code.cost.usage / claude_code.active_time.total
//
// 设计要点：
//   - 只处理 Sum / Gauge 的 NumberDataPoint；Histogram / Summary 跳过
//   - session.id 从 datapoint attrs 取（per-datapoint 覆盖 → 同一 resource 里多个 session 也能区分）
//   - model 从 datapoint attrs 取
//   - 不抛 panic：malformed protobuf → ParseError
func ParseMetricsRequest(body []byte) (*ParseResult, error) {
	var req collectormetrics.ExportMetricsServiceRequest
	if len(body) == 0 {
		return &ParseResult{}, nil
	}
	if err := proto.Unmarshal(body, &req); err != nil {
		return nil, &ParseError{Reason: "protobuf 解析失败: " + err.Error()}
	}

	result := &ParseResult{}
	sessionSeen := make(map[string]bool)
	sessionModels := make(map[string]string) // session.id → model（首个非空值留下）

	for _, rm := range req.GetResourceMetrics() {
		if rm == nil {
			continue
		}
		resAttrs := attrsToMap(rm.GetResource().GetAttributes())

		for _, sm := range rm.GetScopeMetrics() {
			if sm == nil {
				continue
			}
			scopeAttrs := attrsToMap(sm.GetScope().GetAttributes())

			for _, m := range sm.GetMetrics() {
				if m == nil {
					continue
				}
				extractFromMetric(m, resAttrs, scopeAttrs, sessionModels, result)
			}
		}
	}

	// 基于累计的 sessionModels 创建 session 行
	for sid, model := range sessionModels {
		if sid == "" || sessionSeen[sid] {
			continue
		}
		result.Sessions = append(result.Sessions, Session{
			ID:        sid,
			StartedAt: time.Now(),
			Provider:  "", // Claude Code 不推送 provider；ccl 留空
			Cwd:       "", // Claude Code 不推送 cwd
			Model:     model,
		})
		sessionSeen[sid] = true
	}

	return result, nil
}

// extractFromMetric 处理单个 Metric（Sum / Gauge / 其他类型）。
//
// sessionModels 由 caller 持有（跨 metric 累积）：首个非空 model 值留下。
func extractFromMetric(
	m *metricpb.Metric,
	resAttrs, scopeAttrs map[string]string,
	sessionModels map[string]string,
	out *ParseResult,
) {
	name := m.GetName()

	trackModel := func(dp *metricpb.NumberDataPoint) {
		dpAttrs := mapToAttrs(dp.GetAttributes())
		mp := dataPointToMetric(dp, name, resAttrs, scopeAttrs, dpAttrs)
		if sid := mp.SessionID; sid != "" {
			if cur, ok := sessionModels[sid]; !ok || cur == "" {
				sessionModels[sid] = mp.Labels["model"]
			}
		}
		out.Metrics = append(out.Metrics, mp)
	}

	switch v := m.Data.(type) {
	case *metricpb.Metric_Sum:
		for _, dp := range v.Sum.GetDataPoints() {
			trackModel(dp)
		}
	case *metricpb.Metric_Gauge:
		for _, dp := range v.Gauge.GetDataPoints() {
			trackModel(dp)
		}
	default:
		// Histogram / Summary / ExponentialHistogram 暂不处理。
	}
}

// mapToAttrs 是 []KeyValue → map[string]string 的便捷包装。
func mapToAttrs(attrs []*commonpb.KeyValue) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	return attrsToMap(attrs)
}

// dataPointToMetric 把单个 NumberDataPoint 转成 Metric。
//
// labels 包含：
//   - resource attrs（除 session.id）
//   - scope attrs
//   - datapoint attrs
//
// session.id 单独提取到 Metric.SessionID（不进入 labels，避免冗余）。
func dataPointToMetric(
	dp *metricpb.NumberDataPoint,
	metricName string,
	resAttrs, scopeAttrs, dpAttrs map[string]string,
) Metric {
	labels := make(map[string]string, len(resAttrs)+len(scopeAttrs)+len(dpAttrs))

	maps.Copy(labels, resAttrs)
	maps.Copy(labels, scopeAttrs)
	maps.Copy(labels, dpAttrs)

	// session.id 不放进 labels；从 dpAttrs 提取到顶层字段
	sessionID := labels["session.id"]
	delete(labels, "session.id")

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
		SessionID:  sessionID,
		Name:       metricName,
		Value:      value,
		Labels:     labels,
		CapturedAt: ts,
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