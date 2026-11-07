package observability

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestHistogramsAreValidAndRetainFailureOutcomes(t *testing.T) {
	m := NewMetrics()
	m.ObserveHTTP("secret-method", "authentication.passkey", 503, 25*time.Millisecond)
	m.ObserveKDF(time.Millisecond, errors.New("password SECRET"))
	m.ObserveDB(true, time.Millisecond, nil)
	m.ObserveDelivery("phone", "ack_failed", time.Millisecond)
	m.ObserveRejection("kdf", "app")
	r := httptest.NewRequest("GET", "/metrics", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	var reader io.Reader = w.Result().Body
	if w.Header().Get("Content-Encoding") == "gzip" {
		gz, e := gzip.NewReader(reader)
		if e != nil {
			t.Fatal(e)
		}
		defer gz.Close()
		reader = gz
	}
	raw, e := io.ReadAll(reader)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "secret-method") {
		t.Fatal("proof or raw method leaked")
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, e := parser.TextToMetricFamilies(strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	h := families["authova_http_request_duration_seconds"].Metric[0].Histogram
	if h.GetSampleCount() != 1 || h.GetSampleSum() != .025 {
		t.Fatal("histogram lost sample", h)
	}
	if families["authova_resource_rejections_total"].Metric[0].Counter.GetValue() != 1 {
		t.Fatal("rejection lost")
	}
}
func TestOutboxPollFailureHasFreshnessAndStops(t *testing.T) {
	m := NewMetrics()
	var calls atomic.Int64
	called := make(chan struct{}, 1)
	stop := m.PollOutbox(context.Background(), func(ctx context.Context) (OutboxFacts, error) {
		calls.Add(1)
		called <- struct{}{}
		return OutboxFacts{}, errors.New("database unavailable")
	})
	<-called
	stop()
	n := calls.Load()
	time.Sleep(5 * time.Millisecond)
	if calls.Load() != n {
		t.Fatal("poll survived shutdown")
	}
	families, e := m.registry.Gather()
	if e != nil {
		t.Fatal(e)
	}
	seen := false
	for _, f := range families {
		if f.GetName() == "authova_outbox_snapshot_valid" {
			seen = true
			if f.Metric[0].Gauge.GetValue() != 0 {
				t.Fatal("failed snapshot presented as current")
			}
		}
	}
	if !seen {
		t.Fatal("freshness missing")
	}
}
func TestOTLPWireExportAndNoSensitiveUpstreamAttributes(t *testing.T) {
	received := make(chan *collector.ExportTraceServiceRequest, 4)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("export path %s", r.URL.Path)
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, e := gzip.NewReader(r.Body)
			if e != nil {
				t.Error(e)
				return
			}
			defer gz.Close()
			reader = gz
		}
		data, e := io.ReadAll(reader)
		if e != nil {
			t.Error(e)
			return
		}
		var req collector.ExportTraceServiceRequest
		if e = proto.Unmarshal(data, &req); e != nil {
			t.Error(e)
			return
		}
		received <- &req
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}))
	defer sink.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	shutdown, e := ConfigureTracing(context.Background(), sink.URL+"/v1/traces", "authova-test", 1, true)
	if e != nil {
		t.Fatal(e)
	}
	defer otel.SetTracerProvider(noop.NewTracerProvider())
	ctx, span := Start(context.Background(), "authentication.test")
	r, _ := http.NewRequestWithContext(ctx, "GET", upstream.URL+"/token-SECRET?password=SECRET", nil)
	r.Header.Set("Authorization", "Bearer SECRET")
	response, e := HTTPTransport(nil).RoundTrip(r)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	End(span, errors.New("database SQL PASSWORD SECRET"))
	deadline, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = shutdown(deadline); e != nil {
		t.Fatal(e)
	}
	select {
	case request := <-received:
		data, _ := proto.Marshal(request)
		if strings.Contains(string(data), "SECRET") || strings.Contains(string(data), upstream.URL) {
			t.Fatal("private upstream data exported")
		}
		spans := 0
		failed := 0
		for _, rs := range request.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, s := range ss.Spans {
					spans++
					if s.Status.GetCode() == 2 {
						failed++
					}
				}
			}
		}
		if spans != 2 || failed != 2 {
			t.Fatalf("spans=%d errors=%d", spans, failed)
		}
	case <-time.After(time.Second):
		t.Fatal("no actual OTLP export")
	}
}
func TestExporterValidationAndUnavailableCollectorDoesNotBlockSpanProduction(t *testing.T) {
	for _, endpoint := range []string{"http://collector/v1/traces", "https://user:password@collector/v1/traces", "https://collector/v1/traces?token=secret"} {
		if _, e := ConfigureTracing(context.Background(), endpoint, "test", 1, false); e == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	for _, ratio := range []float64{math.NaN(), math.Inf(1), -1, 2} {
		if _, e := ConfigureTracing(context.Background(), "https://collector/v1/traces", "test", ratio, false); e == nil {
			t.Fatal("invalid ratio accepted")
		}
	}
	shutdown, e := ConfigureTracing(context.Background(), "http://127.0.0.1:1/v1/traces", "test", 1, true)
	if e != nil {
		t.Fatal(e)
	}
	defer otel.SetTracerProvider(noop.NewTracerProvider())
	started := time.Now()
	for range 1000 {
		_, span := Start(context.Background(), "auth.independent")
		End(span, nil)
	}
	if time.Since(started) > time.Second {
		t.Fatal("failed exporter blocked producer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = shutdown(ctx)
}
