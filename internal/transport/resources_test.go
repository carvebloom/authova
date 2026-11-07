package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/ports"
)

type blockedHealth struct{ ports.Store }

func (blockedHealth) Health(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
func (blockedHealth) Stats() ports.PoolStats           { return ports.PoolStats{} }

func TestRequestBudgetAndFailureMetrics(t *testing.T) {
	server := &Server{Service: &application.Service{Store: blockedHealth{}}, RequestTimeout: 20 * time.Millisecond}
	handler := server.Handler()
	response := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil))
	if response.Code != 503 || time.Since(started) > time.Second {
		t.Fatal("dependency wait exceeded the request budget")
	}
	if server.failures.Load() != 1 || server.inflight.Load() != 0 {
		t.Fatal("failure/inflight counters are incorrect")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(response.Body.String(), `authova_http_responses_total{class="5xx"} 1`) {
		t.Fatal("HTTP dependency errors missing from metrics")
	}
}

type heldHealth struct {
	ports.Store
	entered chan struct{}
	release chan struct{}
}

func (s heldHealth) Health(ctx context.Context) error {
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (heldHealth) Stats() ports.PoolStats { return ports.PoolStats{} }

func TestAdmissionBoundsBlockedRequestsAndKeepsLiveness(t *testing.T) {
	store := heldHealth{entered: make(chan struct{}, 2), release: make(chan struct{})}
	api := &Server{Service: &application.Service{Store: store}, MaxInflight: 2}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	done := make(chan error, 2)
	for range 2 {
		go func() {
			response, err := client.Get(server.URL + "/readyz")
			if err == nil {
				response.Body.Close()
			}
			done <- err
		}()
	}
	for range 2 {
		select {
		case <-store.entered:
		case <-time.After(time.Second):
			t.Fatal("requests did not block")
		}
	}
	response, err := client.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") == "" {
		t.Fatal("admission did not reject excess work", response.StatusCode)
	}
	response, err = client.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("liveness unavailable during overload")
	}
	close(store.release)
	for range 2 {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	response, err = client.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("admission did not recover")
	}
	if api.failures.Load() != 1 || api.inflight.Load() != 0 {
		t.Fatal("overload metrics incorrect")
	}
}

func TestStrictJSONMediaAndSingleValue(t *testing.T) {
	for _, item := range []struct {
		media, body string
		valid       bool
	}{
		{"application/json", `{"value":"ok"}`, true},
		{"application/json; charset=utf-8", `{"value":"ok"}`, true},
		{"application/json-extra", `{"value":"ok"}`, false},
		{"text/plain", `{"value":"ok"}`, false},
		{"application/json", `{"value":"ok"} {}`, false},
		{"application/json", `{"unknown":"ok"}`, false},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(item.body))
		r.Header.Set("Content-Type", item.media)
		var data struct {
			Value string `json:"value"`
		}
		if err := decode(r, &data); (err == nil) != item.valid {
			t.Errorf("media=%s body=%s accepted=%v", item.media, item.body, err == nil)
		}
	}
}
