package collector

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// telemetry-config-compat §5.1 场景 A 端到端：典型公司远程 collector 配置
//
// 起真实 daemon + 真实 store + 真实 forwarder（target 指向 mock remote collector），
// 验证 POST /v1/metrics 后：SQLite 有 session/metric + remote 收到 1 次 POST。

func TestServer_ScenarioA_PerSignalMetrics_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}

	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	fwd := NewForwarder(remote.URL+"/v1/metrics", "")
	if !fwd.Enabled() {
		t.Fatal("forwarder must be enabled")
	}

	srv := NewServer(ServerOptions{Addr: ":0", Store: store, Forward: fwd})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	time.Sleep(100 * time.Millisecond)
	addr := srv.Addr()

	req := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricpb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "session.id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "scenario-A"}}},
					},
				},
				ScopeMetrics: []*metricpb.ScopeMetrics{
					{
						Metrics: []*metricpb.Metric{
							{
								Name: "claude_code.session.count",
								Data: &metricpb.Metric_Sum{
									Sum: &metricpb.Sum{
										DataPoints: []*metricpb.NumberDataPoint{
											{TimeUnixNano: 1_700_000_000_000_000_000, Value: &metricpb.NumberDataPoint_AsInt{AsInt: 1}},
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

	httpResp, err := http.Post("http://127.0.0.1"+addr+"/v1/metrics", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/metrics: %v", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	if httpResp.StatusCode != 200 {
		t.Errorf("status: want 200, got %d", httpResp.StatusCode)
	}

	// 等异步 forward + store worker
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && remoteCalls.Load() == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if got := remoteCalls.Load(); got != 1 {
		t.Errorf("remote should receive 1 POST, got %d", got)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, "scenario-A").Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 1 {
		t.Errorf("sessions: want 1, got %d", n)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM metrics WHERE session_id=?`, "scenario-A").Scan(&n); err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if n < 1 {
		t.Errorf("metrics: want >=1, got %d", n)
	}
}