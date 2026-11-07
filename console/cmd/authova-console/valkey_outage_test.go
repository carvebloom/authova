package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsoleValkeyTLSOutageRecovery(t *testing.T) {
	control := os.Getenv("AUTHOVA_TEST_VALKEY_CONTROL")
	if control == "" {
		t.Skip("isolated TLS lifecycle runner required")
	}
	var calls atomic.Int32
	b, _ := fixtureBFF(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[]}`))
	}))
	_, request := fixtureSession(t, b, session{Token: "test-only", CSRF: "test-csrf", Expires: time.Now().Add(time.Hour)})
	step := func(request, ready string) {
		t.Helper()
		if os.WriteFile(filepath.Join(control, request), nil, 0600) != nil {
			t.Fatal("cannot signal lifecycle")
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(control, ready)); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("lifecycle timeout", ready)
	}
	step("stop", "stopped")
	response := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	b.ServeHTTP(response, request.WithContext(ctx))
	cancel()
	if response.Code != 503 || calls.Load() != 0 {
		t.Fatal("cache outage did not fail closed", response.Code, calls.Load())
	}
	step("restart", "restarted")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response = httptest.NewRecorder()
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		b.ServeHTTP(response, request.WithContext(ctx))
		cancel()
		if response.Code == 401 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if response.Code != 401 || calls.Load() != 0 {
		t.Fatal("lost cache data must require fresh authentication", response.Code, calls.Load())
	}
	_, fresh := fixtureSession(t, b, session{Token: "fresh-test-only", CSRF: "fresh-csrf", Expires: time.Now().Add(time.Hour)})
	response = httptest.NewRecorder()
	b.ServeHTTP(response, fresh)
	if response.Code != 200 || calls.Load() != 1 {
		t.Fatal("BFF failed to recover after fresh login", response.Code)
	}
}
