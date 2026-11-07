package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
	"github.com/carvebloom/authova/internal/transport"
)

// These fixtures contain real credentials and must remain in a private directory.
// All measured authorization operations use the real HTTP protocol and SQL store.
type recoveryCredential struct {
	Access, Refresh, GrantID string
}
type agentRecoveryFixture struct {
	App          domain.App               `json:"app"`
	OtherApp     domain.App               `json:"other_app"`
	Users        []string                 `json:"sample_user_ids"`
	Actual       int64                    `json:"actual_users"`
	Client       domain.Client            `json:"client"`
	Secret       string                   `json:"client_secret"`
	User         domain.User              `json:"user"`
	Agent        application.CreatedAgent `json:"agent"`
	Installation domain.AgentInstallation `json:"installation"`
	Live         recoveryCredential       `json:"live"`
	UsedRefresh  string                   `json:"used_refresh"`
	Revoked      recoveryCredential       `json:"revoked"`
	Replayed     recoveryCredential       `json:"replayed"`
	OtherClient  domain.Client            `json:"other_client"`
	OtherSecret  string                   `json:"other_secret"`
}

func recoveryCredentialFrom(t *testing.T, f *fixture, out map[string]any) recoveryCredential {
	t.Helper()
	access, _ := out["access_token"].(string)
	refresh, _ := out["refresh_token"].(string)
	claims := requireActive(t, f, access, true)
	grant, _ := claims["grant_id"].(string)
	if access == "" || refresh == "" || grant == "" {
		t.Fatal("missing delegated recovery fixture")
	}
	return recoveryCredential{access, refresh, grant}
}

func acceptanceAgent(t *testing.T, f *fixture) (application.CreatedAgent, domain.AgentInstallation) {
	t.Helper()
	ctx := context.Background()
	a, err := f.service.CreateAgent(ctx, f.operatorToken, f.scope1, application.AgentCreate{
		Name: "Acceptance Agent", Kind: "confidential", Modes: []string{domain.AgentAutonomous, domain.AgentDelegated},
		Scopes: []string{"api.read"}, Resources: []string{"urn:authova:integration"},
		RedirectURIs: []string{f.server.URL + "/test-callback"}, TokenTTL: 120, GrantTTL: 86400,
	}, "acceptance-"+application.ID())
	if err != nil {
		t.Fatal(err)
	}
	i, err := f.service.CreateAgentInstallation(ctx, f.operatorToken, f.scope1, application.AgentInstallationCreate{
		AgentID: a.Agent.ID, TenantID: "shared-tenant-name", UserID: f.user1.ID, ResponsibleParty: "trusted acceptance provisioner",
		Resource: "urn:authova:integration", Scopes: []string{"api.read"}, Modes: a.Agent.Modes,
		Expires: time.Now().Add(48 * time.Hour).Unix(), Reason: "explicit synthetic fixture approval",
	}, "acceptance-"+application.ID())
	if err != nil {
		t.Fatal(err)
	}
	return a, i
}

func TestAgentCapacityFixture(t *testing.T) {
	directory := os.Getenv("AUTHOVA_AGENT_ACCEPTANCE_DIR")
	count, err := strconv.Atoi(os.Getenv("AUTHOVA_AGENT_CAPACITY_USERS"))
	if directory == "" || err != nil || count < 1 {
		t.Skip("requires private acceptance directory and explicit synthetic user count")
	}
	if count > 20000000 {
		t.Fatal("local fixture limit exceeded")
	}
	agentDatabases(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		started := time.Now()
		users, err := f.store.LoadSyntheticUsers(ctx, f.scope1, count, f.user1.PasswordHash, func(done int) {
			t.Logf("synthetic users=%d elapsed=%s", done, time.Since(started).Round(time.Second))
		})
		if err != nil {
			t.Fatal(err)
		}
		actual, err := f.store.UserCount(ctx, f.scope1)
		if err != nil || actual != int64(count) {
			t.Fatal("unexpected real user cardinality", actual, err)
		}
		reauthenticateAcceptanceOwner(t, f)
		finishAgentCapacityFixture(t, f, users, actual, directory)
		t.Logf("capacity fixture complete: users=%d samples=%d elapsed=%s", actual, len(users), time.Since(started).Round(time.Second))
	})
}

func finishAgentCapacityFixture(t *testing.T, f *fixture, users []string, actual int64, directory string) {
	t.Helper()
	ctx := context.Background()
	a, i := acceptanceAgent(t, f)
	live := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
	old := live.Refresh
	status, rotated := agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}})
	if status != 200 {
		t.Fatal("fixture rotation failed", status)
	}
	live = recoveryCredentialFrom(t, f, rotated)
	revoked := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
	status, _ = agentJSON(t, f, "POST", "/agent-grants/"+revoked.GrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "retained revoked recovery fixture"})
	if status != 200 {
		t.Fatal("fixture revoke failed", status)
	}
	replayed := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
	status, winner := agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {replayed.Refresh}})
	if status != 200 {
		t.Fatal("fixture replay preparation failed", status)
	}
	status, _ = agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {replayed.Refresh}})
	if status != 400 {
		t.Fatal("replay fixture did not reject reuse", status)
	}
	replayed.Access, _ = winner["access_token"].(string)
	replayed.Refresh, _ = winner["refresh_token"].(string)
	requireActive(t, f, replayed.Access, false)
	other, secret, err := f.service.CreateClient(ctx, f.operatorToken, f.scope2, domain.Client{Name: "Other App resource", Kind: "confidential", Scopes: []string{"api.read"}, Audiences: []string{i.Resource}, IntrospectionAudiences: []string{i.Resource}, Grants: []string{"client_credentials"}}, "acceptance-"+application.ID())
	if err != nil {
		t.Fatal(err)
	}
	out := agentRecoveryFixture{f.app1, f.app2, users, actual, f.client, f.secret, f.user1, a, i, live, old, revoked, replayed, other, secret}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(directory, f.store.Dialect()+"-fixture.json"), raw, 0600) != nil {
		t.Fatal("cannot save private recovery fixture")
	}
}

func restoredAgentFixture(t *testing.T, driver, dsn string, saved agentRecoveryFixture) *fixture {
	t.Helper()
	base := strings.TrimSuffix(saved.App.Issuer, "/a/"+saved.App.Key)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the exact issuer and redirect origin, as a real DNS/service restore
	// would. The original fixture server has stopped before the backup stage.
	listener, err := net.Listen("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Listener.Close()
	server.Listener = listener
	key, err := os.ReadFile(os.Getenv("AUTHOVA_MASTER_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	vault, err := secure.NewVault(key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlstore.Open(context.Background(), driver, dsn, base, vault, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err = store.CheckRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	service, err := application.New(store, vault, base, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := protocol.New(service)
	if err != nil {
		t.Fatal(err)
	}
	api := &transport.Server{Service: service, Provider: provider, Limiter: transport.NewLocalLimiter(), EmailConfigured: true}
	server.Config.Handler = api.Handler()
	server.Start()
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar, client.Timeout = jar, 10*time.Second
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Path == "/test-callback" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	scope, _ := domain.AppScope(saved.App.ID)
	other, _ := domain.AppScope(saved.OtherApp.ID)
	return &fixture{t: t, store: store, service: service, server: server, app1: saved.App, app2: saved.OtherApp, scope1: scope, scope2: other, client: saved.Client, secret: saved.Secret, user1: saved.User, http: client}
}

func TestAgentRecoveredAuthority(t *testing.T) {
	directory := os.Getenv("AUTHOVA_AGENT_ACCEPTANCE_DIR")
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_RESTORE_" + strings.ToUpper(driver) + "_DSN")
		if directory == "" || dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			var saved agentRecoveryFixture
			raw, err := os.ReadFile(filepath.Join(directory, driver+"-fixture.json"))
			if err != nil || json.Unmarshal(raw, &saved) != nil {
				t.Fatal("private recovery fixture unavailable")
			}
			f := restoredAgentFixture(t, driver, dsn, saved)
			ctx := context.Background()
			if os.Getenv("AUTHOVA_AGENT_POST_BACKUP_REVOKE") == "1" {
				key, _ := os.ReadFile(os.Getenv("AUTHOVA_MASTER_KEY_FILE"))
				vault, _ := secure.NewVault(key)
				source, err := sqlstore.Open(ctx, driver, os.Getenv("AUTHOVA_TEST_"+strings.ToUpper(driver)+"_DSN"), f.server.URL, vault, 2)
				if err != nil {
					t.Fatal(err)
				}
				defer source.Close()
				if err = source.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					g, err := tx.AgentGrant(saved.Live.GrantID, false)
					if err != nil || g.Status != "revoked" {
						return errors.New("post-backup source revocation not present")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			err = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
				a, e := tx.Agent(saved.Agent.Agent.ID, false)
				if e != nil || !reflect.DeepEqual(a, saved.Agent.Agent) {
					return errors.New("decrypted Agent changed")
				}
				i, e := tx.AgentInstallation(saved.Installation.ID, false)
				if e != nil || !reflect.DeepEqual(i, saved.Installation) {
					return errors.New("decrypted installation changed")
				}
				for _, record := range []struct{ id, status string }{{saved.Live.GrantID, "active"}, {saved.Revoked.GrantID, "revoked"}, {saved.Replayed.GrantID, "active"}} {
					g, e := tx.AgentGrant(record.id, false)
					if e != nil || g.Status != record.status || g.UserID != saved.User.ID || g.TenantID != saved.Installation.TenantID || g.InstallationID != saved.Installation.ID || g.SessionID == "" {
						return errors.New("restored delegated grant lost identity or revocation")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, cred := range []recoveryCredential{saved.Revoked, saved.Replayed} {
				requireActive(t, f, cred.Access, false)
				status, _ := agentTokenRequest(t, f, saved.Agent, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.Refresh}})
				if status != 400 {
					t.Fatal("restore resurrected denied refresh", status)
				}
			}
			status, out := agentTokenRequest(t, f, saved.Agent, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {saved.Live.Refresh}, "installation_id": {saved.Installation.ID}, "tenant_id": {"forged-tenant"}, "user_id": {"forged-user"}})
			if status != 200 {
				t.Fatal("valid restored grant cannot refresh", status)
			}
			if os.Getenv("AUTHOVA_AGENT_POST_BACKUP_REVOKE") == "1" {
				t.Log("observed backup rollback: later source revocation is absent from unfenced snapshot; quarantine is mandatory before reopening Agent traffic")
			}
			fresh := recoveryCredentialFrom(t, f, out)
			claims := requireActive(t, f, fresh.Access, true)
			if claims["sub"] != saved.User.ID || claims["tenant_id"] != saved.Installation.TenantID || claims["grant_id"] != saved.Live.GrantID {
				t.Fatal("restore rebound delegated identity")
			}
			r, _ := http.NewRequest("POST", saved.OtherApp.Issuer+"/oauth/introspect", strings.NewReader(url.Values{"token": {fresh.Access}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetBasicAuth(saved.OtherClient.ClientID, saved.OtherSecret)
			resp, err := f.http.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			var result struct{ Active bool }
			err = json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || result.Active {
				t.Fatal("restored token accepted in another App")
			}
			status, _ = agentTokenRequest(t, f, saved.Agent, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {saved.UsedRefresh}})
			if status != 400 {
				t.Fatal("restore forgot consumed refresh", status)
			}
			requireActive(t, f, fresh.Access, false)
			badVault, _ := secure.NewVault(bytes.Repeat([]byte{0x7d}, 32))
			bad, err := sqlstore.Open(ctx, driver, dsn, f.server.URL, badVault, 2)
			if err != nil {
				t.Fatal(err)
			}
			defer bad.Close()
			if err = bad.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
				_, e := tx.Agent(saved.Agent.Agent.ID, false)
				return e
			}); !errors.Is(err, domain.ErrUnavailable) {
				t.Fatal("wrong master key did not fail closed while decrypting Agent facts", err)
			}
			t.Log("restored live delegation refreshed; revoked/replayed/consumed facts retained; cross-App and wrong-key denied")
		})
	}
	if !configured {
		t.Skip("requires private fixture and actual restored databases")
	}
}

func TestAgentRevokeAfterBackup(t *testing.T) {
	directory := os.Getenv("AUTHOVA_AGENT_ACCEPTANCE_DIR")
	if directory == "" || os.Getenv("AUTHOVA_AGENT_POST_BACKUP_REVOKE") != "1" {
		t.Skip("requires explicit post-backup revocation rehearsal")
	}
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			var saved agentRecoveryFixture
			raw, err := os.ReadFile(filepath.Join(directory, driver+"-fixture.json"))
			if err != nil || json.Unmarshal(raw, &saved) != nil {
				t.Fatal("private fixture unavailable")
			}
			f := restoredAgentFixture(t, driver, os.Getenv("AUTHOVA_TEST_"+strings.ToUpper(driver)+"_DSN"), saved)
			if err := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
				g, e := tx.AgentGrant(saved.Live.GrantID, true)
				if e != nil || g.Status == "revoked" {
					return e // Rehearsal can be resumed without repeating the mutation.
				}
				_, e = f.service.RevokeAgentGrant(tx, saved.User.ID, g.ID, application.AgentVersionedReason{Version: g.Version, Reason: "revoked AFTER immutable backup snapshot"}, saved.User.ID)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			status, _, err := agentRefreshHTTP(f, saved.Agent, saved.Live.Refresh)
			if err != nil || status != 400 {
				t.Fatal("source post-backup revocation was ineffective", status, err)
			}
			t.Log("source grant revoked after backup; old snapshot cannot include this later fact")
		})
	}
}

func TestAgentRestoredQuarantineCLI(t *testing.T) {
	directory := os.Getenv("AUTHOVA_AGENT_ACCEPTANCE_DIR")
	binary := os.Getenv("AUTHOVA_TEST_CORE_BINARY")
	if directory == "" || binary == "" {
		t.Skip("requires actual restored databases and compiled Core CLI")
	}
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			var saved agentRecoveryFixture
			raw, err := os.ReadFile(filepath.Join(directory, driver+"-fixture.json"))
			if err != nil || json.Unmarshal(raw, &saved) != nil {
				t.Fatal("private fixture unavailable")
			}
			dsn := os.Getenv("AUTHOVA_RESTORE_" + strings.ToUpper(driver) + "_DSN")
			f := restoredAgentFixture(t, driver, dsn, saved)
			delegated := recoveryCredentialFrom(t, f, issueDelegated(t, f, saved.Agent, saved.Installation))
			status, machine := agentTokenRequest(t, f, saved.Agent, url.Values{"grant_type": {"client_credentials"}, "installation_id": {saved.Installation.ID}, "scope": {"api.read"}})
			if status != 200 {
				t.Fatal("pre-quarantine machine issuance failed", status)
			}
			machineToken := machine["access_token"].(string)
			machineClaims := requireActive(t, f, machineToken, true)
			delegatedClaims := requireActive(t, f, delegated.Access, true)
			f.server.Close() // No restored copy writer or ingress remains during maintenance.
			f.store.Close()
			invoke := func(confirm string) (application.AgentQuarantineReport, error) {
				cmd := exec.Command(binary, "quarantine-agents")
				base := strings.TrimSuffix(saved.App.Issuer, "/a/"+saved.App.Key)
				cmd.Env = append(os.Environ(), "AUTHOVA_DATABASE_DRIVER="+driver, "AUTHOVA_DATABASE_URL="+dsn, "AUTHOVA_BASE_URL="+base)
				input, _ := json.Marshal(map[string]string{"reason": "post-backup revocation rollback quarantine rehearsal", "confirm": confirm})
				cmd.Stdin = bytes.NewReader(input)
				out, err := cmd.Output()
				var report application.AgentQuarantineReport
				if err == nil {
					err = json.Unmarshal(out, &report)
				}
				return report, err
			}
			if _, err := invoke("incorrect"); err == nil {
				t.Fatal("maintenance command accepted missing typed confirmation")
			}
			report, err := invoke("disable_all_agents")
			if err != nil || report.AgentsDisabled == 0 || report.InstallationsClosed == 0 || report.CredentialsCleared == 0 || report.GrantsRevoked == 0 {
				t.Fatal("actual restore CLI failed", report, err)
			}
			repeated, err := invoke("disable_all_agents")
			if err != nil || repeated.AgentsDisabled+repeated.InstallationsClosed+repeated.CredentialsCleared+repeated.GrantsRevoked != 0 {
				t.Fatal("actual maintenance CLI was not idempotent", repeated, err)
			}
			f = restoredAgentFixture(t, driver, dsn, saved)
			if time.Now().Unix() >= int64(machineClaims["exp"].(float64)) || time.Now().Unix() >= int64(delegatedClaims["exp"].(float64)) {
				t.Fatal("maintenance exceeded sample token TTL; cannot attribute inactive to quarantine instead of natural expiry")
			}
			requireActive(t, f, delegated.Access, false)
			requireActive(t, f, machineToken, false)
			status, _, err = agentRefreshHTTP(f, saved.Agent, delegated.Refresh)
			if err != nil || status != 401 {
				t.Fatal("old secret survived restored quarantine", status, err)
			}
			t.Logf("actual restored CLI: agents=%d installations=%d grants=%d credentials=%d; repeat no-op; old delegated/autonomous tokens and secrets denied", report.AgentsDisabled, report.InstallationsClosed, report.GrantsRevoked, report.CredentialsCleared)
		})
	}
}
