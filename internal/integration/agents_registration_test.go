package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// Registration changes the flow purpose before rendering consent in the same
// POST response. The user must still see the Agent and exact target resource.
func TestAgentRegistrationConsentIdentifiesDelegate(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, _ := newAgentFixture(t, f, []string{domain.AgentDelegated})
		status, body := agentJSON(t, f, "POST", "/agent-installations", application.AgentInstallationCreate{
			AgentID: a.Agent.ID, ResponsibleParty: "registration-test", Resource: "urn:authova:integration",
			Scopes: []string{"api.read"}, Modes: []string{domain.AgentDelegated},
			Expires: time.Now().Add(time.Hour).Unix(), Reason: "explicit App-wide installation",
		})
		var install domain.AgentInstallation
		if status != 201 || json.Unmarshal(body, &install) != nil {
			t.Fatalf("create installation: HTTP %d", status)
		}
		challenge := sha256.Sum256([]byte(secure.Random(32)))
		q := url.Values{"client_id": {a.Agent.ClientID}, "installation_id": {install.ID}, "resource": {install.Resource},
			"redirect_uri": {f.server.URL + "/test-callback"}, "response_type": {"code"}, "scope": {"openid api.read"},
			"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
		resp, err := f.http.Get(f.app1.Issuer + "/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		body, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("start flow: HTTP %d", resp.StatusCode)
		}
		flowURL := resp.Request.URL.String()
		flowID := flowURL[strings.LastIndex(flowURL, "/")+1:]
		csrf := csrfPattern.FindSubmatch(body)
		if len(csrf) != 2 {
			t.Fatal("missing browser CSRF binding")
		}
		// OTP proof is tested independently; seed only the already verified state.
		err = f.service.Store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
			flow, e := tx.Flow(flowID, true)
			if e != nil {
				return e
			}
			flow.Purpose, flow.Step = domain.PurposeRegister, domain.StepSetPassword
			flow.VerifiedKind, flow.VerifiedValue = "email", application.ID()+"@example.test"
			return tx.PutFlow(flow)
		})
		if err != nil {
			t.Fatal(err)
		}
		status, body = f.post(flowURL+"/register", url.Values{"_csrf": {string(csrf[1])}, "password": {password}, "display_name": {"New Agent user"}}, false)
		for _, required := range []string{"确认授权", a.Agent.Name, install.Resource, "授权到期时间", "撤销此 Agent"} {
			if status != 200 || !bytes.Contains(body, []byte(required)) {
				t.Fatalf("registration consent omitted %q (HTTP %d)", required, status)
			}
		}
	})
}
