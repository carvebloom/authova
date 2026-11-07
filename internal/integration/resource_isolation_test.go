package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/transport"
)

func TestAppResourceIsolation(t *testing.T) {
	configured := false
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(dialect, func(t *testing.T) {
			f := setup(t, dialect, dsn)
			ctx := context.Background()
			limits := domain.DefaultResourceLimits()
			limits.DBConcurrency = 1
			limits.HTTPConcurrency = 1
			limits.KDFConcurrency = 1
			limits.EmailChallengesPerHour = 1
			if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				a, e := tx.App()
				if e != nil {
					return e
				}
				a.Resources = &limits
				a.Version++
				return tx.UpdateAppConfig(a)
			}); e != nil {
				t.Fatal(e)
			}
			admission := resource.New(16, 4, 8, f.store.LoadResourceLimits)
			f.store.Admission = admission
			f.service.Resources = admission
			provider, e := protocol.New(f.service)
			if e != nil {
				t.Fatal(e)
			}
			api := (&transport.Server{Service: f.service, Provider: provider, Resources: admission, Limiter: transport.NewLocalLimiter(), EmailConfigured: true}).Handler()
			request := func(method, path string, body any, management bool) (int, []byte) {
				t.Helper()
				var reader io.Reader
				if body != nil {
					b, _ := json.Marshal(body)
					reader = bytes.NewReader(b)
				}
				r := httptest.NewRequest(method, path, reader)
				if body != nil {
					r.Header.Set("Content-Type", "application/json")
				}
				if management {
					r.Header.Set("Authorization", "Bearer "+f.operatorToken)
				}
				w := httptest.NewRecorder()
				api.ServeHTTP(w, r)
				return w.Code, w.Body.Bytes()
			}
			// Hold an actual App 1 transaction. App 2 and the control plane retain DB access.
			entered, finish := make(chan struct{}), make(chan struct{})
			result := make(chan error, 1)
			go func() {
				result <- f.store.App(resource.WithApp(ctx, f.app1.ID), f.scope1, false, func(tx ports.AppTx) error {
					if _, e := tx.App(); e != nil {
						return e
					}
					close(entered)
					<-finish
					return nil
				})
			}()
			<-entered
			if status, _ := request("GET", "/a/"+f.app1.Key+"/keys", nil, false); status != 429 {
				close(finish)
				t.Fatalf("App1 saturated DB status %d", status)
			}
			if status, b := request("GET", "/a/"+f.app2.Key+"/keys", nil, false); status != 200 {
				close(finish)
				t.Fatalf("App2 affected %d %s", status, b)
			}
			if status, b := request("GET", "/api/v1/apps/"+f.app1.ID+"/resource-policy", nil, true); status != 200 {
				close(finish)
				t.Fatalf("management reserve failed %d %s", status, b)
			}
			close(finish)
			if e = <-result; e != nil {
				t.Fatal(e)
			}
			// HTTP admission is independently scoped, without consuming App 2 permits.
			release, e := admission.Acquire(ctx, f.app1.ID, resource.HTTP)
			if e != nil {
				t.Fatal(e)
			}
			if status, _ := request("GET", "/a/"+f.app1.Key+"/keys", nil, false); status != 429 {
				release()
				t.Fatal("App1 HTTP cap", status)
			}
			if status, _ := request("GET", "/a/"+f.app2.Key+"/keys", nil, false); status != 200 {
				release()
				t.Fatal("App2 HTTP affected", status)
			}
			release()

			// App hourly email budget is separate from the per-destination cap.
			for i := range 2 {
				flow := f.securityFlow(t, "authorize")
				cookie := secure.Random(32)
				flow.CookieDigest = f.service.Digest(f.app1.ID, "flow-cookie", cookie)
				if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); e != nil {
					t.Fatal(e)
				}
				form := url.Values{"_csrf": {flow.CSRF}, "purpose": {"login"}, "channel": {"email"}, "destination": {"quota-" + secure.Random(8) + "@example.test"}}
				r := httptest.NewRequest("POST", "/a/"+f.app1.Key+"/ui/flows/"+flow.ID+"/challenges", strings.NewReader(form.Encode()))
				r.Header.Set("Origin", f.service.BaseURL)
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.AddCookie(&http.Cookie{Name: "av_flow_" + flow.ID, Value: cookie})
				w := httptest.NewRecorder()
				api.ServeHTTP(w, r)
				expected := 200
				if i == 1 {
					expected = 429
				}
				if w.Code != expected {
					t.Fatalf("hourly challenge cap request %d status %d body %s", i, w.Code, w.Body.String())
				}
			}
			// Full configuration and CAS; writes are explicit and auditable.
			limits.RequestsPerSecond = 3
			patch := map[string]any{"limits": limits, "config_version": 2, "reason": "App isolation acceptance"}
			if status, b := request("PATCH", "/api/v1/apps/"+f.app1.ID+"/resource-policy", patch, true); status != 200 {
				t.Fatalf("policy update %d %s", status, b)
			}
			if status, _ := request("PATCH", "/api/v1/apps/"+f.app1.ID+"/resource-policy", patch, true); status != 409 {
				t.Fatal("stale CAS accepted", status)
			}
			partial := map[string]any{"limits": map[string]int{"http_concurrency": 1}, "config_version": 3, "reason": "incomplete limits"}
			if status, _ := request("PATCH", "/api/v1/apps/"+f.app1.ID+"/resource-policy", partial, true); status != 400 {
				t.Fatal("partial budgets accepted", status)
			}
			if status, b := request("GET", "/api/v1/apps/"+f.app2.ID+"/resource-policy", nil, true); status != 200 || strings.Contains(string(b), `"requests_per_second":3,`) {
				t.Fatal("cross App policy changed", status, string(b))
			}
			rejected := false
			for range 5 {
				status, _ := request("GET", "/a/"+f.app1.Key+"/keys", nil, false)
				if status == 429 {
					rejected = true
				}
			}
			if !rejected {
				t.Fatal("App request budget not enforced")
			}
			if status, _ := request("GET", "/a/"+f.app2.Key+"/keys", nil, false); status != 200 {
				t.Fatal("App2 rate budget affected", status)
			}
		})
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL required")
	}
}
