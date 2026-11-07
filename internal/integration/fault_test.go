package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

type unavailableStore struct {
	ports.Store
	failed atomic.Bool
}

func (s *unavailableStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	if s.failed.Load() {
		return domain.ErrUnavailable
	}
	return s.Store.App(ctx, scope, write, fn)
}
func (f *fixture) faultChecks(t *testing.T) {
	ctx := context.Background()
	status, data := f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
	if status != 200 {
		t.Fatal("machine token fixture failed")
	}
	var token tokens
	json.Unmarshal(data, &token)
	if !f.active(token.Access) {
		t.Fatal("fixture inactive")
	}
	wrapped := &unavailableStore{Store: f.store}
	f.service.Store = wrapped
	wrapped.failed.Store(true)
	status, data = f.post(f.app1.Issuer+"/oauth/introspect", mapForm("token", token.Access), true)
	if status != 503 || strings.Contains(string(data), `"active":true`) {
		t.Fatal("dependency failure returned reliable authorization")
	}
	wrapped.failed.Store(false)
	f.service.Store = f.store
	if !f.active(token.Access) {
		t.Fatal("recovered dependency remains inactive")
	}
	flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: "choose_method", Purpose: "authorize", Expires: time.Now().Add(time.Minute).Unix()}
	if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); e != nil {
		t.Fatal(e)
	}
	id, e := f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", "email", "attempt-"+application.ID()+"@example.test", true)
	if e != nil {
		t.Fatal(e)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if e = f.service.VerifyChallenge(ctx, f.scope1, flow.ID, id, "invalid"); e == nil {
			t.Fatal("incorrect OTP accepted")
		}
	}
	e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		challenge, e := tx.Challenge(id, false)
		if e != nil {
			return e
		}
		if challenge.Attempts != 5 {
			t.Fatal("failed OTP counters rolled back")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func mapForm(values ...string) url.Values {
	out := url.Values{}
	for i := 0; i < len(values); i += 2 {
		out.Set(values[i], values[i+1])
	}
	return out
}
