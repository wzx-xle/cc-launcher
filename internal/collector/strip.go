package collector

import (
	"fmt"
	"slices"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// cclAttributeKeys 是 ccl 追加的 resource attribute 键。
//
// 这三个键是 launch 上下文（provider / cwd / 端点 host）的载体，**只对本地
// 有意义**——它们描述的是"这次 ccl 启动用了什么"，对用户的远程 collector 没有
// 价值，还可能泄露本机的工作目录。
//
// 转发前剥掉它们（launch-context-settings-injection §"the daemon strips ccl's
// attributes before forwarding"）→ 远程 collector 收到的资源属性恢复成
// "用户没装 ccl 时的样子"。
var cclAttributeKeys = []string{
	"ccl.provider",
	"ccl.cwd",
	"ccl.provider_endpoint",
}

// stripCclAttributes 从 OTLP metrics payload 里删掉 ccl 追加的那几个键，
// 返回新的 protobuf bytes。
//
// **要剥两处**（2026-10-28 真机实测发现）：Claude Code 的 OTel SDK 会把
// `OTEL_RESOURCE_ATTRIBUTES` 的内容**同时**放进
//   - Resource 的 attributes
//   - **每个 data point 的 attributes**（每个 metric 的每个 datapoint 都重复一遍）
//
// 只剥 resource attributes 的话，datapoint 里那份会原样转发出去 → ccl.* 仍然
// 泄漏到远程 collector。
//
// 语义：
//   - payload 里没有 ccl.* → 原样返回（不做无谓的 unmarshal/marshal）
//   - unmarshal 失败 → 返回原 body + error（调用方应**照常转发原 body**，
//     不能因为剥不掉就把转发丢了）
//
// 注意：本地存的 labels **保留** ccl.*——stats 查询层的 providerName() 兜底
// 读的就是 labels 里的 ccl.provider。剥掉的只有转发出去的那一份。
func stripCclAttributes(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}

	var req collectormetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return body, fmt.Errorf("解析 payload 失败（转发时剥 ccl 属性）: %w", err)
	}

	changed := false
	for _, rm := range req.GetResourceMetrics() {
		if rm == nil {
			continue
		}
		// 1) Resource 的 attributes
		if rm.GetResource() != nil {
			if attrs, ch := stripCclFromAttrs(rm.GetResource().GetAttributes()); ch {
				rm.Resource.Attributes = attrs
				changed = true
			}
		}
		// 2) Scope 的 attributes
		for _, sm := range rm.GetScopeMetrics() {
			if sm == nil {
				continue
			}
			if sm.GetScope() != nil {
				if attrs, ch := stripCclFromAttrs(sm.GetScope().GetAttributes()); ch {
					sm.Scope.Attributes = attrs
					changed = true
				}
			}
			// 3) 每个 data point 的 attributes
			for _, m := range sm.GetMetrics() {
				if m == nil {
					continue
				}
				switch v := m.GetData().(type) {
				case *metricpb.Metric_Sum:
					for _, dp := range v.Sum.GetDataPoints() {
						if attrs, ch := stripCclFromAttrs(dp.GetAttributes()); ch {
							dp.Attributes = attrs
							changed = true
						}
					}
				case *metricpb.Metric_Gauge:
					for _, dp := range v.Gauge.GetDataPoints() {
						if attrs, ch := stripCclFromAttrs(dp.GetAttributes()); ch {
							dp.Attributes = attrs
							changed = true
						}
					}
				case *metricpb.Metric_Histogram:
					for _, dp := range v.Histogram.GetDataPoints() {
						if attrs, ch := stripCclFromAttrs(dp.GetAttributes()); ch {
							dp.Attributes = attrs
							changed = true
						}
					}
				case *metricpb.Metric_Summary:
					for _, dp := range v.Summary.GetDataPoints() {
						if attrs, ch := stripCclFromAttrs(dp.GetAttributes()); ch {
							dp.Attributes = attrs
							changed = true
						}
					}
				case *metricpb.Metric_ExponentialHistogram:
					for _, dp := range v.ExponentialHistogram.GetDataPoints() {
						if attrs, ch := stripCclFromAttrs(dp.GetAttributes()); ch {
							dp.Attributes = attrs
							changed = true
						}
					}
				}
			}
		}
	}
	if !changed {
		return body, nil
	}

	out, err := proto.Marshal(&req)
	if err != nil {
		return body, fmt.Errorf("重新序列化 payload 失败（转发时剥 ccl 属性）: %w", err)
	}
	return out, nil
}

// stripCclFromAttrs 从 attribute 列表里剔除 ccl 的键，返回新列表 + 是否变化。
func stripCclFromAttrs(attrs []*commonpb.KeyValue) ([]*commonpb.KeyValue, bool) {
	if len(attrs) == 0 {
		return attrs, false
	}
	kept := make([]*commonpb.KeyValue, 0, len(attrs))
	changed := false
	for _, kv := range attrs {
		if kv != nil && isCclAttribute(kv.GetKey()) {
			changed = true
			continue
		}
		kept = append(kept, kv)
	}
	if !changed {
		return attrs, false
	}
	return kept, true
}

// isCclAttribute 报告 key 是否是 ccl 追加的属性键。
func isCclAttribute(key string) bool {
	return slices.Contains(cclAttributeKeys, key)
}
