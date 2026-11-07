package sdk

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLoginUsesHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"status":200,"code":"authentication_required"}`))
	}))
	defer server.Close()
	client, _ := New(server.URL)
	_, err := client.Login(context.Background(), "test@example.test", "test only", "000000")
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != 401 {
		t.Fatal("body overrode authoritative HTTP rejection")
	}
}

// Warm a persistent connection, then lose the response after the server has
// consumed the mutation. Idempotency-Key alone must not authorize the HTTP
// transport to replay an operation that its endpoint may not deduplicate.
func TestMutationsAreNotReplayedAfterCommitResponseLoss(t *testing.T) {
	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			var commits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if r.Method != "GET" && commits.Add(1) == 1 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{}`))
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.Request(context.Background(), "GET", "/api/v1/apps", "", "", nil); err != nil {
				t.Fatal(err)
			}
			_, err = client.Request(context.Background(), method, "/api/v1/apps/app/connections", "fixture-token", "fixture-idempotency", []byte(`{}`))
			if err == nil || commits.Load() != 1 {
				t.Fatal("mutation replayed after unknown outcome", commits.Load(), err)
			}
		})
	}
}

func TestIncompleteLoginIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201); w.Write([]byte(`{}`)) }))
	defer server.Close()
	client, _ := New(server.URL)
	if _, err := client.Login(context.Background(), "test@example.test", "test only", "000000"); err == nil {
		t.Fatal("incomplete management session accepted")
	}
}
