package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func TestSecurityReview(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			f := setup(t, driver, dsn)
			t.Run("cancelled-flow-never-issues-code", f.cancelledFlowCheck)
			t.Run("credential-change-invalidates-pending-login", f.staleLoginCheck)
			t.Run("current-mfa-policy-before-consent", f.currentMFAPolicyCheck)
			t.Run("superseded-challenge-is-rejected", f.supersededChallengeCheck)
			t.Run("primary-client-permission-after-parse-cache", f.clientCacheCheck)
		})
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL DSNs required")
	}
}

func (f *fixture) cancelledFlowCheck(t *testing.T) {
	verifier := secure.Random(32)
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {f.client.ClientID}, "redirect_uri": {f.server.URL + "/test-callback"}, "response_type": {"code"}, "scope": {"api.read"}, "state": {"cancel-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	response, err := f.http.Get(f.app1.Issuer + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	csrf := csrfPattern.FindSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal("flow CSRF unavailable")
	}
	flowURL := response.Request.URL.String()
	flowID := strings.TrimPrefix(flowURL, f.app1.Issuer+"/ui/flows/")
	status, _ := f.post(flowURL+"/cancel", mapForm("_csrf", string(csrf[1])), false)
	if status != 200 {
		t.Fatal("cancel request failed", status)
	}
	response, err = f.http.Get(f.app1.Issuer + "/authorize/callback?id=" + url.QueryEscape(flowID))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location, _ := url.Parse(response.Header.Get("Location"))
	if location != nil && location.Query().Get("code") != "" {
		status, tokens := f.exchange(location.Query().Get("code"), verifier)
		if status == 200 && f.active(tokens.Access) {
			t.Fatal("cancelled unauthenticated flow produced an active resource access token")
		}
		t.Fatal("cancelled unauthenticated flow produced an authorization code; exchange status", status)
	}
}

func (f *fixture) pendingPasswordFlow(t *testing.T) (domain.Flow, domain.User) {
	t.Helper()
	ctx := context.Background()
	user := f.user1
	user.ID = application.ID()
	user.Email = "review-" + user.ID + "@example.test"
	data, _ := json.Marshal(map[string]any{"RedirectURI": f.server.URL + "/test-callback", "Scopes": []string{"openid"}, "Audiences": f.client.Audiences})
	flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: "choose_method", Purpose: "authorize", Expires: time.Now().Add(10 * time.Minute).Unix(), Data: data}
	err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		if err := tx.CreateUser(user, "email", f.service.Digest(f.app1.ID, "identifier:email", user.Email)); err != nil {
			return err
		}
		return tx.PutFlow(flow)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.PasswordLogin(ctx, f.scope1, flow.ID, "email", user.Email, password); err != nil {
		t.Fatal(err)
	}
	return flow, user
}

func (f *fixture) staleLoginCheck(t *testing.T) {
	ctx := context.Background()
	flow, user := f.pendingPasswordFlow(t)
	err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		user.Epoch++ // Password reset/disable/MFA enrollment all change this fact.
		if err := tx.PutUser(user); err != nil {
			return err
		}
		return tx.RevokeSessions(user.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Complete(ctx, f.scope1, flow.ID); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal("pending password proof survived credential epoch change", err)
	}
}

func (f *fixture) currentMFAPolicyCheck(t *testing.T) {
	ctx := context.Background()
	flow, _ := f.pendingPasswordFlow(t)
	err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		app, err := tx.App()
		if err != nil {
			return err
		}
		app.RequireMFA = true
		app.Version++
		return tx.UpdateAppConfig(app)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			app, err := tx.App()
			if err != nil {
				return err
			}
			app.RequireMFA = false
			app.Version++
			return tx.UpdateAppConfig(app)
		})
	})
	if _, err = f.service.Complete(ctx, f.scope1, flow.ID); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal("current MFA policy did not stop password-only session creation", err)
	}
}

func (f *fixture) supersededChallengeCheck(t *testing.T) {
	ctx := context.Background()
	flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: "choose_method", Purpose: "authorize", Expires: time.Now().Add(time.Minute).Unix()}
	if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); err != nil {
		t.Fatal(err)
	}
	first, err := f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", "email", "first@example.test", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		challenge, err := tx.Challenge(first, true)
		if err != nil {
			return err
		}
		challenge.Digest = f.service.Digest(f.app1.ID, "otp:"+first, "123456")
		return tx.PutChallenge(challenge)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", "email", "second@example.test", true); err != nil {
		t.Fatal(err)
	}
	if err = f.service.VerifyChallenge(ctx, f.scope1, flow.ID, first, "123456"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal("superseded challenge accepted", err)
	}
}

func (f *fixture) clientCacheCheck(t *testing.T) {
	ctx := context.Background()
	status, body := f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
	if status != 200 {
		t.Fatal("machine fixture failed")
	}
	var token tokens
	if json.Unmarshal(body, &token) != nil || !f.active(token.Access) {
		t.Fatal("initial token inactive")
	}
	if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		client, err := tx.LockClient(f.client.ClientID)
		if err != nil {
			return err
		}
		client.IntrospectionAudiences = []string{}
		return tx.PutClient(client)
	}); err != nil {
		t.Fatal(err)
	}
	status, _ = f.post(f.app1.Issuer+"/oauth/introspect", mapForm("token", token.Access), true)
	if status != 403 {
		t.Fatal("parsed client cache preserved removed introspection permission", status)
	}
	if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		client, err := tx.LockClient(f.client.ClientID)
		if err != nil {
			return err
		}
		client.IntrospectionAudiences = f.client.IntrospectionAudiences
		client.SecretDigest = f.service.Digest(f.app1.ID, "client-secret", secure.Random(32))
		return tx.PutClient(client)
	}); err != nil {
		t.Fatal(err)
	}
	status, _ = f.post(f.app1.Issuer+"/oauth/introspect", mapForm("token", token.Access), true)
	if status != 401 {
		t.Fatal("parsed client cache preserved retired credentials", status)
	}
}
