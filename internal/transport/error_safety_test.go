package transport

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/observability"
	"github.com/carvebloom/authova/internal/runtimehttp"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnknownPublicProjectionAndPanic(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	for _, e := range []error{errors.New("CANARY-DSN"), kerrors.New(400, "invalid_request", "CANARY-SECRET").WithMetadata(map[string]string{"token": "CANARY"}), fmt.Errorf("CANARY: %w", status.Error(codes.Internal, "CANARY"))} {
		server := &Server{Metrics: observability.NewMetrics()}
		h := server.wrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { problem(w, r, e) }))
		w := httptest.NewRecorder()
		runtimehttp.NewServer(&http.Server{Handler: h}).Server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/apps", nil))
		if w.Code != 503 || strings.Contains(w.Body.String(), "CANARY") || !strings.Contains(w.Body.String(), w.Header().Get("X-Request-ID")) {
			t.Fatal(w.Body.String())
		}
	}
	server := &Server{Metrics: observability.NewMetrics()}
	h := server.wrapHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("CANARY-PASSWORD") }))
	w := httptest.NewRecorder()
	runtimehttp.NewServer(&http.Server{Handler: h}).Server.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/?password=CANARY", strings.NewReader("CANARY")))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	if strings.Contains(logs.String(), "CANARY") || !strings.Contains(logs.String(), "panic_unwind") || !strings.Contains(logs.String(), "frames") {
		t.Fatal("panic diagnostic unsafe/missing")
	}
}
func TestAbortAndPartialPanicKeepAbort(t *testing.T) {
	for _, write := range []bool{false, true} {
		func() {
			defer func() {
				if recover() != http.ErrAbortHandler {
					t.Fatal("lost abort semantics")
				}
			}()
			s := &Server{Metrics: observability.NewMetrics()}
			s.wrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if write {
					w.Write([]byte("partial"))
					panic("secret")
				}
				panic(http.ErrAbortHandler)
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()
	}
}
