package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func (f *fixture) managementToken() string {
	return f.managementTokenWithScopes("openid profile offline_access authova.admin")
}

func (f *fixture) managementTokenWithScopes(scopes string) string {
	f.t.Helper()
	ctx := context.Background()
	var system domain.Client
	e := f.store.App(ctx, f.scope1, true, func(t ports.AppTx) error {
		if e := t.SetAdminRole(f.user1.ID, "owner"); e != nil {
			return e
		}
		var err error
		system, err = t.Client("avm_" + f.app1.Key)
		if err != nil {
			return err
		}
		system.RedirectURIs = []string{f.server.URL + "/test-callback"}
		return t.PutClient(system)
	})
	if e != nil {
		f.t.Fatal(e)
	}
	verifier := secure.Random(32)
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {system.ClientID}, "redirect_uri": {f.server.URL + "/test-callback"}, "response_type": {"code"}, "scope": {scopes}, "state": {"management-state"}, "nonce": {"management-nonce"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
	resp, e := f.http.Get(f.app1.Issuer + "/authorize?" + q.Encode())
	if e != nil {
		f.t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	flowURL := resp.Request.URL.String()
	csrf := csrfPattern.FindSubmatch(b)
	if len(csrf) != 2 {
		f.t.Fatal("management flow CSRF")
	}
	status, b := f.post(flowURL+"/password", url.Values{"_csrf": {string(csrf[1])}, "kind": {"email"}, "identifier": {"same@example.com"}, "password": {password}}, false)
	if status != 200 {
		f.t.Fatal("management password failed")
	}
	flowID := strings.TrimPrefix(flowURL, f.app1.Issuer+"/ui/flows/")
	secret, e := f.service.EnrollMFA(ctx, f.scope1, flowID)
	if e != nil {
		f.t.Fatal(e)
	}
	codes, e := f.service.ConfirmMFA(ctx, f.scope1, flowID, secure.TOTP(secret, time.Now().Unix()/30))
	if e != nil || len(codes) != 10 {
		f.t.Fatal("MFA enrollment/recovery failed", e)
	}
	csrf = csrfPattern.FindSubmatch(b)
	r, _ := http.NewRequest("POST", flowURL+"/complete", strings.NewReader(url.Values{"_csrf": {string(csrf[1])}}.Encode()))
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e = f.http.Do(r)
	if e != nil {
		f.t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		f.t.Fatalf("management callback status %d", resp.StatusCode)
	}
	location, _ := url.Parse(resp.Header.Get("Location"))
	status, b = f.post(f.app1.Issuer+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {system.ClientID}, "code": {location.Query().Get("code")}, "code_verifier": {verifier}, "redirect_uri": {f.server.URL + "/test-callback"}}, false)
	if status != 200 {
		f.t.Fatalf("management token status %d", status)
	}
	var response tokens
	json.Unmarshal(b, &response)
	return response.Access
}
func (f *fixture) managementChecks(t *testing.T) {
	ctx := context.Background()
	token := f.managementToken()
	operator, e := f.service.Operator(ctx, token, false)
	if e != nil || operator.AppID != f.app1.ID || operator.Role != "owner" {
		t.Fatal("App management principal invalid", e)
	}
	e = f.service.AppManage(ctx, token, f.scope2, false, true, func(ports.AppTx, domain.Operator) error { return nil })
	if e == nil {
		t.Fatal("management token accepted in another App")
	}
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.SetAdminRole(f.user1.ID, "auditor") })
	if e != nil {
		t.Fatal(e)
	}
	e = f.service.AppManage(ctx, token, f.scope1, true, false, func(ports.AppTx, domain.Operator) error { return nil })
	if e == nil {
		t.Fatal("revoked write role cached")
	}
	e = f.service.AppManage(ctx, token, f.scope1, false, true, func(ports.AppTx, domain.Operator) error { return nil })
	if e != nil {
		t.Fatal("App auditor cannot read masked data", e)
	}
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.SetAdminRole(f.user1.ID, "") })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Operator(ctx, token, false); e == nil {
		t.Fatal("removed admin grant remains authorized")
	}
}
