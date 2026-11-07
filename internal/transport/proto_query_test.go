package transport

import (
	"context"
	"github.com/carvebloom/authova/internal/runtimehttp"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeneratedQueryAdmissionOrder(t *testing.T) {
	api := new(Server)
	handler := api.protoQueryRouter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("canonical query bypassed generated binding") }))
	app := "019acc73-b8b8-710e-a933-b6fd041fc075"
	cases := []struct {
		path, auth string
		want       int
	}{
		{"/api/v1/apps/invalid/query/users?limit=bad", "", 401},
		{"/api/v1/apps/" + app + "/query/users?limit=bad", "", 401},
		{"/api/v1/apps/" + app + "/query/users?limit=bad", "Bearer untrusted", 400},
		{"/api/v1/apps/" + app + "/query/users?limit=1&limit=2", "Bearer untrusted", 400},
		{"/api/v1/apps/" + app + "/query/users?tenant_id=forbidden", "Bearer untrusted", 400},
		{"/api/v1/apps/" + app + "/query/users/a/sessions?user_id=other", "Bearer untrusted", 400},
		{"/api/v1/apps/" + app + "/query/grants?unknown=1", "Bearer untrusted", 400},
	}
	for _, test := range cases {
		t.Run(test.path+test.auth, func(t *testing.T) {
			r := httptest.NewRequest("GET", test.path, nil)
			if test.auth != "" {
				r.Header.Set("Authorization", test.auth)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("got %d, want %d: %s", w.Code, test.want, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatal("problem encoding changed")
			}
		})
	}
}

// Both Kratos layers must preserve the configured caller budget. Their default
// one-second timeout races Authova's one-second bounded database queue.
func TestProtocolFallbackKeepsRequestBudget(t *testing.T) {
	api := new(Server)
	inner := api.protoQueryRouter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 2*time.Second {
			t.Errorf("request budget shortened: %v %v", deadline, ok)
		}
		w.WriteHeader(204)
	}))
	handler := runtimehttp.NewServer(&http.Server{Handler: inner})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request := httptest.NewRequest("POST", "/a/key/oauth/introspect", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal(response.Code)
	}
}
