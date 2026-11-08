// Package transport implements versioned management and same-origin browser endpoints.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/carvebloom/authova/internal/diagnostic"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/observability"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/carvebloom/authova/internal/securityevents"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Limiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}
type Server struct {
	Metrics                          *observability.Metrics
	metricsOnce                      sync.Once
	Resources                        *resource.Manager
	SecurityEvents                   securityevents.Sink
	routeMu                          sync.Mutex
	routes                           map[string]routeEntry
	routeLoads                       map[string]*routeLoad
	Service                          *application.Service
	Provider                         *protocol.Provider
	Limiter                          Limiter
	EmailConfigured, PhoneConfigured bool
	requests, failures               atomic.Uint64
	panics, completed, durationNanos atomic.Uint64
	inflight                         atomic.Int64
	responses                        [6]atomic.Uint64
	RequestTimeout                   time.Duration
	MaxInflight                      int64
}

func (s *Server) Handler() http.Handler {
	s.metricsOnce.Do(func() {
		if s.Metrics == nil {
			s.Metrics = observability.NewMetrics()
		}
		s.registerLegacyMetrics()
	})
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if e := s.Service.Store.Health(ctx); e != nil {
			problem(w, r, diagnostic.Wrap(errors.Join(domain.ErrUnavailable, e), "storage", "connect"))
			return
		}
		jsonReply(w, 200, map[string]string{"status": "ready"})
	})
	m.HandleFunc("GET /metrics", s.metrics)
	m.HandleFunc("/api/v1/", s.management)
	m.HandleFunc("/a/{app_key}/", s.app)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	return s.wrapHTTP(s.protoQueryRouter(m))
}

func (s *Server) wrapHTTP(m http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		budget := s.RequestTimeout
		if budget <= 0 {
			budget = 15 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		route := metricRoute(r.URL.Path)
		ctx, span := observability.Start(ctx, "HTTP "+route, attribute.String("http.route", route), attribute.String("http.request.method", metricMethod(r.Method)))
		defer span.End()
		r = r.WithContext(withSecurityRequest(ctx))
		started := time.Now()
		recorded := &responseStatus{ResponseWriter: w}
		w = recorded
		s.requests.Add(1)
		concurrency := s.inflight.Add(1)
		defer s.inflight.Add(-1)
		id := application.ID()
		w.Header().Set("X-Request-ID", id)
		span.SetAttributes(attribute.String("authova.request_id", id))
		r = r.WithContext(diagnostic.WithRequest(r.Context(), id))
		if span.SpanContext().IsValid() {
			w.Header().Set("X-Trace-ID", span.SpanContext().TraceID().String())
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		defer func() {
			panicValue := recover()
			panicked := panicValue != nil
			if panicked {
				if panicValue == http.ErrAbortHandler {
					panic(http.ErrAbortHandler)
				}
				s.panics.Add(1)
				diagnostic.Panic(r.Context(), "http", panicValue)
				if recorded.status != 0 {
					panic(http.ErrAbortHandler)
				}
				problem(w, r, domain.ErrUnavailable)
			}
			status := recorded.status
			if status == 0 {
				status = http.StatusOK
			}
			if status >= 500 || panicked {
				s.failures.Add(1)
			}
			if status/100 > 0 && status/100 < len(s.responses) {
				s.responses[status/100].Add(1)
			}
			s.emitSecurity(r, id, status)
			s.Metrics.ObserveHTTP(r.Method, route, status, time.Since(started))
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= 500 {
				span.SetStatus(codes.Error, "http_error")
			}
			s.completed.Add(1)
			s.durationNanos.Add(uint64(time.Since(started)))
		}()
		maximum := s.MaxInflight
		if maximum <= 0 {
			maximum = 1024
		}
		if r.URL.Path != "/healthz" && concurrency > maximum {
			w.Header().Set("Retry-After", "1")
			problem(w, r, domain.ErrUnavailable)
			return
		}
		m.ServeHTTP(w, r)
	})
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, r *http.Request, e error) {
	switch diagnostic.Inspect(e).Kind {
	case "internal", "dependency", "cancelled", "deadline", "outcome_unknown":
		diagnostic.Log(r.Context(), "http", "request", e)
	}
	if meta := securityMeta(r); meta != nil {
		meta.Reason = securityFailure(e)
	}
	status, code := 503, "dependency_unavailable"
	switch {
	case diagnostic.Has(e, domain.ErrCommitUnknown):
		status, code = 503, "outcome_unknown"
	case diagnostic.Has(e, domain.ErrNotFound):
		status, code = 404, "not_found"
	case diagnostic.Has(e, domain.ErrInvalid):
		status, code = 400, "invalid_request"
	case diagnostic.Has(e, domain.ErrForbidden):
		status, code = 403, "forbidden"
	case diagnostic.Has(e, domain.ErrUnauthenticated):
		status, code = 401, "authentication_required"
	case diagnostic.Has(e, domain.ErrConflict):
		status, code = 409, "conflict"
	case diagnostic.Has(e, domain.ErrExpired):
		status, code = 410, "flow_expired"
	case diagnostic.Has(e, domain.ErrRateLimit):
		status, code = 429, "rate_limited"
		if hint := diagnostic.Inspect(e).RetryAfter; hint > 0 && w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", strconv.Itoa(hint))
		}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"type": "urn:authova:problem:" + code, "title": http.StatusText(status), "status": status, "detail": "The request could not be completed.", "code": code, "request_id": w.Header().Get("X-Request-ID")})
}
func decode(r *http.Request, v any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return domain.ErrInvalid
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return domain.ErrInvalid
	}
	var trailing any
	if e := d.Decode(&trailing); !errors.Is(e, io.EOF) {
		return domain.ErrInvalid
	}
	return nil
}
func bearer(r *http.Request) string {
	a := r.Header.Get("Authorization")
	if !strings.HasPrefix(a, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(a, "Bearer ")
}
func (s *Server) limit(w http.ResponseWriter, r *http.Request, key string, maximum int, window time.Duration) bool {
	if s.Limiter == nil {
		problem(w, r, domain.ErrUnavailable)
		return false
	}
	ok, e := s.Limiter.Allow(r.Context(), key, maximum, window)
	if e != nil {
		problem(w, r, diagnostic.Wrap(errors.Join(domain.ErrUnavailable, e), "http", "upstream"))
		return false
	}
	if !ok {
		if s.Metrics != nil {
			kind := "rate"
			if strings.HasPrefix(key, "app-challenges:") {
				kind = "challenge_quota"
			}
			s.Metrics.ObserveRejection(kind, "quota")
		}
		if meta := securityMeta(r); meta != nil && meta.Action == "" {
			meta.Action = "request.rate_limit"
		}
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(window.Seconds()))))
		problem(w, r, domain.ErrRateLimit)
		return false
	}
	return true
}
func remoteIP(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return host /* forwarded headers are never trusted */
}
func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	a, e := s.routeApp(r)
	if e != nil {
		problem(w, r, e)
		return
	}
	if meta := securityMeta(r); meta != nil {
		meta.AppID = a.ID
		meta.Action = "oauth.request"
	}
	trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("authova.app_id", a.ID))
	r = r.WithContext(resource.WithApp(r.Context(), a.ID))
	if s.Resources != nil {
		release, err := s.Resources.Acquire(r.Context(), a.ID, resource.HTTP)
		if err != nil {
			problem(w, r, err)
			return
		}
		defer release()
		limits, err := s.Resources.Limits(r.Context(), a.ID)
		if err != nil {
			problem(w, r, err)
			return
		}
		if !s.limit(w, r, "app-requests:"+a.ID, limits.RequestsPerSecond, time.Second) {
			return
		}
	}
	if strings.HasSuffix(r.URL.Path, "/.well-known/authova-management") && r.Method == "GET" {
		scope, _ := domain.AppScope(a.ID)
		var c domain.Client
		e = s.Service.Store.App(r.Context(), scope, false, func(t ports.AppTx) error { var err error; c, err = t.Client("avm_" + a.Key); return err })
		if e != nil {
			problem(w, r, e)
			return
		}
		if !c.System {
			problem(w, r, domain.ErrNotFound)
			return
		}
		jsonReply(w, 200, map[string]string{"issuer": a.Issuer, "client_id": c.ClientID})
		return
	}
	if meta := securityMeta(r); meta != nil {
		meta.AppID = a.ID
		meta.Action = "oauth.request"
	}
	if strings.Contains(r.URL.Path, "/ui/") {
		s.loginUI(w, r, a)
		return
	}
	if !s.limit(w, r, "protocol:"+a.ID+":"+s.Service.Digest(a.ID, "ip", remoteIP(r)), 20000, time.Second) {
		return
	}
	s.Provider.ServeApp(w, r, a)
}

type routeEntry struct {
	app     domain.App
	expires time.Time
}

type routeLoad struct {
	done chan struct{}
	app  domain.App
	err  error
}

func (s *Server) routeApp(r *http.Request) (domain.App, error) {
	key := r.PathValue("app_key")
	cachedPath := strings.HasSuffix(r.URL.Path, "/oauth/introspect")
	if cachedPath {
		s.routeMu.Lock()
		cached, ok := s.routes[key]
		if ok && cached.expires.After(time.Now()) {
			s.routeMu.Unlock()
			return cached.app, nil
		}
		if load := s.routeLoads[key]; load != nil {
			s.routeMu.Unlock()
			select {
			case <-load.done:
				return load.app, load.err
			case <-r.Context().Done():
				return domain.App{}, r.Context().Err()
			}
		}
		if len(s.routeLoads) >= 2048 {
			s.routeMu.Unlock()
			return domain.App{}, domain.ErrUnavailable
		}
		if s.routeLoads == nil {
			s.routeLoads = map[string]*routeLoad{}
		}
		load := &routeLoad{done: make(chan struct{})}
		s.routeLoads[key] = load
		s.routeMu.Unlock()
		app, err := s.Service.Store.RouteApp(r.Context(), key)
		s.routeMu.Lock()
		if err == nil {
			if s.routes == nil {
				s.routes = map[string]routeEntry{}
			}
			s.routes[key] = routeEntry{app: app, expires: time.Now().Add(time.Minute)}
			for len(s.routes) > 2048 {
				for old := range s.routes {
					delete(s.routes, old)
					break
				}
			}
		}
		load.app, load.err = app, err
		delete(s.routeLoads, key)
		close(load.done)
		s.routeMu.Unlock()
		return app, err
	}
	return s.Service.Store.RouteApp(r.Context(), key)
}
