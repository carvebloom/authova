package transport

import (
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) { s.Metrics.Handler().ServeHTTP(w, r) }

func (s *Server) registerLegacyMetrics() {
	counter := func(name, help string, value func() float64) {
		s.Metrics.Register(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, value))
	}
	gauge := func(name, help string, value func() float64) {
		s.Metrics.Register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, value))
	}
	counter("authova_requests_total", "All HTTP requests.", func() float64 { return float64(s.requests.Load()) })
	counter("authova_failures_total", "HTTP server failures.", func() float64 { return float64(s.failures.Load()) })
	counter("authova_panics_total", "Recovered HTTP panics.", func() float64 { return float64(s.panics.Load()) })
	gauge("authova_inflight", "Current HTTP requests.", func() float64 { return float64(s.inflight.Load()) })
	counter("authova_http_duration_seconds_sum", "Legacy cumulative HTTP duration.", func() float64 { return float64(s.durationNanos.Load()) / float64(time.Second) })
	counter("authova_http_duration_seconds_count", "Legacy completed HTTP requests.", func() float64 { return float64(s.completed.Load()) })
	for class := 1; class <= 5; class++ {
		s.Metrics.Register(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "authova_http_responses_total", Help: "HTTP responses by status class.", ConstLabels: prometheus.Labels{"class": strconv.Itoa(class) + "xx"}}, func() float64 { return float64(s.responses[class].Load()) }))
	}
	gauge("authova_go_goroutines", "Current goroutines.", func() float64 { return float64(runtime.NumGoroutine()) })
	memory := func() runtime.MemStats { var m runtime.MemStats; runtime.ReadMemStats(&m); return m }
	gauge("authova_go_heap_bytes", "Go heap bytes.", func() float64 { return float64(memory().HeapAlloc) })
	gauge("authova_go_heap_objects", "Go heap objects.", func() float64 { return float64(memory().HeapObjects) })
	counter("authova_go_gc_total", "Completed Go collections.", func() float64 { return float64(memory().NumGC) })
	gauge("authova_db_max_connections", "Configured SQL pool maximum.", func() float64 { return float64(s.Service.Store.Stats().MaxOpen) })
	gauge("authova_db_open_connections", "Open SQL connections.", func() float64 { return float64(s.Service.Store.Stats().Open) })
	gauge("authova_db_in_use_connections", "In-use SQL connections.", func() float64 { return float64(s.Service.Store.Stats().InUse) })
	gauge("authova_db_idle_connections", "Idle SQL connections.", func() float64 { return float64(s.Service.Store.Stats().Idle) })
	counter("authova_db_wait_total", "SQL pool waits.", func() float64 { return float64(s.Service.Store.Stats().WaitCount) })
	counter("authova_db_wait_seconds_total", "Time waiting for SQL pool.", func() float64 { return s.Service.Store.Stats().WaitSeconds })
	if s.SecurityEvents != nil {
		counter("authova_security_events_written_total", "Written security events.", func() float64 { return float64(s.SecurityEvents.Stats().Written) })
		counter("authova_security_events_dropped_total", "Dropped security events.", func() float64 { return float64(s.SecurityEvents.Stats().QueueDropped) })
		counter("authova_security_events_recorded_total", "Recorded security events.", func() float64 { return float64(s.SecurityEvents.Stats().Recorded) })
		counter("authova_security_events_attempted_total", "Attempted security event writes.", func() float64 { return float64(s.SecurityEvents.Stats().Attempted) })
		counter("authova_security_events_write_failed_total", "Failed security event writes.", func() float64 { return float64(s.SecurityEvents.Stats().WriteFailed) })
	}
}

// Only finite classes are exported; never label a raw App/user/flow ID or URL.
func metricMethod(method string) string {
	switch method {
	case "GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS", "HEAD":
		return method
	default:
		return "OTHER"
	}
}
func metricRoute(path string) string {
	switch path {
	case "/healthz", "/readyz", "/metrics":
		return path
	}
	if strings.HasPrefix(path, "/api/v1/") {
		if strings.Contains(path, "/query/") {
			return "agent.query"
		}
		if strings.HasSuffix(path, "/resource-policy") {
			return "management.resource_policy"
		}
		if strings.HasSuffix(path, "/authentication-config") {
			return "management.authentication_config"
		}
		if path == "/api/v1/platform/sessions" {
			return "management.session"
		}
		return "management.other"
	}
	if strings.HasPrefix(path, "/a/") {
		if strings.Contains(path, "/passkey/") || strings.Contains(path, "/passkeys") {
			return "authentication.passkey"
		}
		if strings.Contains(path, "/ui/") {
			return "authentication.ui"
		}
		for _, suffix := range []string{"/oauth/token", "/oauth/introspect", "/userinfo", "/authorize", "/authorize/callback", "/keys", "/revoke", "/.well-known/openid-configuration", "/.well-known/oauth-authorization-server"} {
			if strings.HasSuffix(path, suffix) {
				return "oauth" + suffix
			}
		}
	}
	return "other"
}
