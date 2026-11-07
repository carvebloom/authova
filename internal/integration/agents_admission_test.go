package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/carvebloom/authova/internal/transport"
)

func TestAgentRevocationUnderAdmissionPressure(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := acceptanceAgent(t, f)
		delegated := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		refreshControl := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		status, machine := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}})
		if status != 200 {
			t.Fatal("autonomous control failed", status)
		}
		machineToken := machine["access_token"].(string)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			app, err := tx.App()
			if err != nil {
				return err
			}
			limits := app.ResourceLimits()
			limits.DBConcurrency = 1
			app.Resources, app.Version = &limits, app.Version+1
			return tx.UpdateAppConfig(app)
		}); err != nil {
			t.Fatal(err)
		}
		admission := resource.New(16, 4, 4, f.store.LoadResourceLimits)
		f.store.Admission, f.service.Resources = admission, admission
		provider, err := protocol.New(f.service)
		if err != nil {
			t.Fatal(err)
		}
		api := (&transport.Server{Service: f.service, Provider: provider, Resources: admission, Limiter: transport.NewLocalLimiter()}).Handler()
		introspection := func() *http.Request {
			r := httptest.NewRequest("POST", f.app1.Issuer+"/oauth/introspect", bytes.NewBufferString(url.Values{"token": {delegated.Access}}.Encode())).WithContext(ctx)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetBasicAuth(f.client.ClientID, f.secret)
			return r
		}
		warm := httptest.NewRecorder()
		api.ServeHTTP(warm, introspection())
		if warm.Code != 200 {
			t.Fatal("could not warm routing metadata before SQL pressure", warm.Code)
		}
		entered, results := make(chan struct{}, 3), make(chan error, 3)
		release := make(chan struct{})
		var once sync.Once
		finish := func() { once.Do(func() { close(release) }) }
		defer finish()
		for _, scope := range []domain.Scope{f.scope1, f.scope2, f.scope2} {
			go func(scope domain.Scope) {
				results <- f.store.App(resource.WithApp(ctx, scope.ID()), scope, false, func(tx ports.AppTx) error {
					if _, err := tx.App(); err != nil {
						return err
					}
					entered <- struct{}{}
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}(scope)
		}
		for n := 0; n < 3; n++ {
			select {
			case <-entered:
			case err := <-results:
				t.Fatal("pressure fixture failed", err)
			case <-ctx.Done():
				t.Fatal("pressure fixture timed out")
			}
		}
		// Real SQL transactions fill all business DB permits. Handler-level calls
		// exercise production routing/admission/authorization without fake tokens.
		denied := httptest.NewRecorder()
		api.ServeHTTP(denied, introspection())
		if denied.Code != 429 {
			t.Fatal("saturated business request did not reject", denied.Code)
		}
		status, _, err = agentRefreshHTTP(f, a, refreshControl.Refresh)
		if err != nil || (status != 429 && status != 503) {
			t.Fatal("DB admission did not reject refresh before its transaction", status, err)
		}
		body, _ := json.Marshal(application.AgentVersionedReason{Version: 1, Reason: "revoke while all business DB permits are occupied"})
		request := func(app string, authenticated bool) int {
			r := httptest.NewRequest(http.MethodPost, f.server.URL+"/api/v1/apps/"+app+"/agent-grants/"+delegated.GrantID+"/revoke", bytes.NewReader(body)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/json")
			if authenticated {
				r.Header.Set("Authorization", "Bearer "+f.operatorToken)
			}
			w := httptest.NewRecorder()
			api.ServeHTTP(w, r)
			return w.Code
		}
		if status := request(f.app1.ID, false); status != 401 {
			t.Fatal("management reserve bypassed authentication", status)
		}
		if status := request(f.app2.ID, true); status != 404 {
			t.Fatal("management reserve bypassed App isolation", status)
		}
		if status := request(f.app1.ID, true); status != 200 {
			t.Fatal("reserved capacity could not revoke Agent grant", status)
		}
		finish()
		for n := 0; n < 3; n++ {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		requireActive(t, f, delegated.Access, false)
		requireActive(t, f, machineToken, true)
		status, refreshed, err := agentRefreshHTTP(f, a, refreshControl.Refresh)
		if err != nil || status != 200 {
			t.Fatal("pre-transaction admission failure consumed a valid refresh", status, err)
		}
		fresh := recoveryCredentialFrom(t, f, refreshed)
		if fresh.GrantID != refreshControl.GrantID {
			t.Fatal("budget rejection rebound the refresh grant")
		}
	})
}
