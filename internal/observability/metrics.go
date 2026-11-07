// Package observability records bounded operational facts. Secrets, request
// bodies, query strings, identifiers and raw SQL never become metric labels.
package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry                                                *prometheus.Registry
	httpDuration, kdfDuration, dbDuration, deliveryDuration *prometheus.HistogramVec
	rejections                                              *prometheus.CounterVec
	resourceWait                                            *prometheus.HistogramVec
}

func NewMetrics() *Metrics {
	r := prometheus.NewRegistry()
	buckets := []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 20}
	hist := func(name, help string, labels []string) *prometheus.HistogramVec {
		return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
	}
	m := &Metrics{registry: r, httpDuration: hist("authova_http_request_duration_seconds", "HTTP request duration by bounded route class.", []string{"method", "route", "status_class"}), kdfDuration: hist("authova_kdf_duration_seconds", "Password proof work, including bounded admission.", []string{"outcome"}), dbDuration: hist("authova_db_transaction_duration_seconds", "Database transaction duration, not SQL text.", []string{"mode", "outcome"}), deliveryDuration: hist("authova_delivery_duration_seconds", "Outbox processing including provider and acknowledgement.", []string{"channel", "outcome"}), rejections: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "authova_resource_rejections_total", Help: "Rejected resource admissions without unbounded App labels."}, []string{"resource", "reason"})}
	m.resourceWait = hist("authova_resource_wait_duration_seconds", "Bounded admission wait before owning a DB connection or permit.", []string{"resource", "outcome"})
	r.MustRegister(m.httpDuration, m.kdfDuration, m.dbDuration, m.deliveryDuration, m.rejections, m.resourceWait, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}
func (m *Metrics) Register(cs ...prometheus.Collector) { m.registry.MustRegister(cs...) }

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
func (m *Metrics) ObserveHTTP(method, route string, status int, duration time.Duration) {
	switch method {
	case "GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS", "HEAD":
	default:
		method = "OTHER"
	}
	m.httpDuration.WithLabelValues(method, route, strconv.Itoa(status/100)+"xx").Observe(duration.Seconds())
}
func (m *Metrics) ObserveKDF(d time.Duration, err error) {
	m.kdfDuration.WithLabelValues(outcome(err)).Observe(d.Seconds())
}
func (m *Metrics) ObserveDB(write bool, d time.Duration, err error) {
	mode := "read"
	if write {
		mode = "write"
	}
	m.dbDuration.WithLabelValues(mode, outcome(err)).Observe(d.Seconds())
}
func (m *Metrics) ObserveDelivery(channel, result string, d time.Duration) {
	if channel != "email" && channel != "phone" {
		channel = "other"
	}
	m.deliveryDuration.WithLabelValues(channel, result).Observe(d.Seconds())
}
func (m *Metrics) ObserveRejection(kind, reason string) {
	m.rejections.WithLabelValues(kind, reason).Inc()
}
func (m *Metrics) ObserveResourceWait(kind, result string, duration time.Duration) {
	m.resourceWait.WithLabelValues(kind, result).Observe(duration.Seconds())
}
func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}
