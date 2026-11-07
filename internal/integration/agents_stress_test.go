package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func agentHTTP(client *http.Client, endpoint, id, secret string, form url.Values) (int, map[string]any, error) {
	r, err := http.NewRequest("POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(id, secret)
	resp, err := client.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	return resp.StatusCode, out, err
}

func agentRefreshHTTP(f *fixture, a application.CreatedAgent, refresh string) (int, map[string]any, error) {
	return agentHTTP(f.http, f.app1.Issuer+"/oauth/token", a.Agent.ClientID, a.Secret, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

func agentActiveHTTP(f *fixture, token string) (map[string]any, error) {
	status, out, err := agentHTTP(f.http, f.app1.Issuer+"/oauth/introspect", f.client.ClientID, f.secret, url.Values{"token": {token}})
	if err != nil || status != 200 {
		return nil, fmt.Errorf("introspection transport/status: %d %v", status, err)
	}
	return out, nil
}

func requireAgentBinding(out map[string]any, f *fixture, a application.CreatedAgent, i domain.AgentInstallation, mode, user, grant string) error {
	if out["active"] != true || out["app_id"] != f.app1.ID || out["agent_id"] != a.Agent.ID || out["installation_id"] != i.ID || out["tenant_id"] != i.TenantID || out["grant_id"] != grant || out["agent_mode"] != mode {
		return errors.New("active principal lost its App/Agent/installation/tenant/grant/mode binding")
	}
	aud, ok := out["aud"].([]any)
	if !ok || len(aud) != 1 || aud[0] != i.Resource {
		return errors.New("principal lost its exact resource binding")
	}
	wantScopes := append([]string{}, i.Scopes...)
	if mode == domain.AgentDelegated {
		wantScopes = append(wantScopes, "openid", "offline_access")
	}
	scope, ok := out["scope"].(string)
	gotScopes := strings.Fields(scope)
	if !ok || len(gotScopes) != len(wantScopes) {
		return errors.New("principal lost its exact scope set")
	}
	for index, value := range gotScopes {
		if !slices.Contains(wantScopes, value) || slices.Contains(gotScopes[:index], value) {
			return errors.New("principal expanded or duplicated scopes")
		}
	}
	expires, expOK := out["exp"].(float64)
	issued, iatOK := out["iat"].(float64)
	if !expOK || !iatOK || issued <= 0 || expires <= issued || expires-issued > float64(a.Agent.TokenTTL) || expires <= float64(time.Now().Unix()) {
		return errors.New("active principal lost its normal lifetime bound")
	}
	if mode == domain.AgentDelegated {
		actor, ok := out["act"].(map[string]any)
		if !ok || actor["sub"] != a.Agent.ID || actor["client_id"] != a.Agent.ClientID || out["sub"] != user || out["principal_type"] != "user" {
			return errors.New("delegated actor/user binding changed")
		}
	} else if out["sub"] != a.Agent.ClientID || out["act"] != nil || out["principal_type"] != "client" {
		return errors.New("autonomous token impersonated a user")
	}
	return nil
}

func acceptanceUser(t *testing.T, f *fixture) *fixture {
	t.Helper()
	copy := *f
	u := f.user1
	u.ID = application.ID()
	u.Email = "acceptance-" + u.ID + "@example.test"
	u.CreatedAt = time.Now().UTC()
	if err := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
		return tx.CreateUser(u, "email", f.service.Digest(f.app1.ID, "identifier:email", u.Email))
	}); err != nil {
		t.Fatal(err)
	}
	copy.user1 = u
	return &copy
}

func TestAgentConcurrentRefreshReplay(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := acceptanceAgent(t, f)
		initial := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		control := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		const callers = 24
		start := make(chan struct{})
		type response struct {
			status int
			out    map[string]any
			err    error
		}
		results := make(chan response, callers)
		for n := 0; n < callers; n++ {
			go func() {
				<-start
				s, out, err := agentRefreshHTTP(f, a, initial.Refresh)
				results <- response{s, out, err}
			}()
		}
		close(start)
		winners := 0
		var winner recoveryCredential
		for n := 0; n < callers; n++ {
			r := <-results
			if r.err != nil || (r.status != 200 && r.status != 400) {
				t.Fatalf("concurrent refresh status=%d error=%v", r.status, r.err)
			}
			if r.status == 200 {
				winners++
				winner.Access, _ = r.out["access_token"].(string)
				winner.Refresh, _ = r.out["refresh_token"].(string)
			}
		}
		if winners != 1 || winner.Access == "" || winner.Refresh == "" {
			t.Fatal("refresh did not have exactly one rotation winner", winners)
		}
		requireActive(t, f, winner.Access, false)
		requireActive(t, f, initial.Access, false)
		requireActive(t, f, control.Access, true)
		status, _, err := agentRefreshHTTP(f, a, winner.Refresh)
		if err != nil || status != 400 {
			t.Fatal("replayed family produced a new generation", status, err)
		}
		t.Logf("same refresh concurrent callers=%d winners=1 rejected=%d; winner family inactive; independent consent active", callers, callers-1)
	})
}

// The acknowledgement is an observable commit boundary. Requests started after
// it must reject. An overlapping success is allowed only if it is inactive once
// the revocation has committed; it is never counted as a bypass on arrival time.
func agentRevocationRound(t *testing.T, parent *fixture, kind string) int {
	t.Helper()
	f := acceptanceUser(t, parent)
	a, i := acceptanceAgent(t, f)
	delegated := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
	controlIn, err := f.service.CreateAgentInstallation(context.Background(), f.operatorToken, f.scope1, application.AgentInstallationCreate{
		AgentID: a.Agent.ID, TenantID: "independent-tenant", UserID: f.user1.ID, ResponsibleParty: "trusted acceptance provisioner", Resource: i.Resource,
		Modes: i.Modes, Scopes: i.Scopes, Expires: i.Expires, Reason: "independent tenant control",
	}, "acceptance-"+application.ID())
	if err != nil {
		t.Fatal(err)
	}
	status, machine := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {controlIn.ID}, "scope": {"api.read"}})
	if status != 200 {
		t.Fatal("control issuance failed", status)
	}
	controlToken, _ := machine["access_token"].(string)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var acknowledged atomic.Bool
	var mu sync.Mutex
	tokens := []string{delegated.Access}
	latestRefresh := delegated.Refresh
	var firstError error
	count := 0
	fail := func(err error) {
		mu.Lock()
		if firstError == nil {
			firstError = err
		}
		mu.Unlock()
		cancel()
	}
	for worker := 0; worker < 7; worker++ {
		wg.Add(1)
		go func(refreshWorker bool) {
			defer wg.Done()
			refresh := delegated.Refresh
			for ctx.Err() == nil {
				after := acknowledged.Load()
				var s int
				var out map[string]any
				var err error
				if refreshWorker {
					s, out, err = agentRefreshHTTP(f, a, refresh)
				} else {
					s, out, err = agentHTTP(f.http, f.app1.Issuer+"/oauth/token", a.Agent.ClientID, a.Secret, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}})
				}
				denied := refreshWorker || kind != "grant"
				if err != nil || (s != 200 && s != 400) || (after && denied && s == 200) || (after && !denied && s != 200) {
					fail(fmt.Errorf("%s concurrent issuance: status=%d after_ack=%v refresh=%v err=%v", kind, s, after, refreshWorker, err))
					return
				}
				if s == 200 {
					token, _ := out["access_token"].(string)
					if token == "" {
						fail(errors.New("successful issuance omitted token"))
						return
					}
					if refreshWorker {
						refresh, _ = out["refresh_token"].(string)
					}
					mu.Lock()
					count++
					if refreshWorker {
						latestRefresh = refresh
					}
					if refreshWorker || kind != "grant" {
						tokens = append(tokens, token)
					}
					mu.Unlock()
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(worker == 0)
	}
	time.Sleep(250 * time.Millisecond)
	switch kind {
	case "grant":
		status, _ = agentJSON(t, f, "POST", "/agent-grants/"+delegated.GrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "concurrent grant revocation"})
	case "installation":
		status, _ = agentJSON(t, f, "PATCH", "/agent-installations/"+i.ID, application.AgentInstallationPatch{Version: 1, Status: "disabled", Reason: "concurrent installation disable"})
	case "agent":
		disabled := "disabled"
		status, _ = agentJSON(t, f, "PATCH", "/agents/"+a.Agent.ID, application.AgentPatch{Version: 1, Status: &disabled, Reason: "concurrent Agent disable"})
	default:
		cancel()
		wg.Wait()
		t.Fatal("unknown revocation scope")
	}
	if status == 200 {
		acknowledged.Store(true)
	}
	time.Sleep(150 * time.Millisecond)
	cancel()
	wg.Wait()
	if status != 200 || firstError != nil || count == 0 {
		t.Fatalf("revocation round %s status=%d issued=%d error=%v", kind, status, count, firstError)
	}
	for _, token := range tokens {
		requireActive(t, f, token, false)
	}
	for n := 0; n < 4; n++ {
		status, _, err := agentRefreshHTTP(f, a, latestRefresh)
		if status != 400 || err != nil {
			t.Fatal("post-ack delegated refresh escaped revocation", status, err)
		}
	}
	requireActive(t, f, controlToken, kind != "agent")
	return count
}

func TestAgentConcurrentRevocationScopes(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		for _, kind := range []string{"grant", "installation", "agent"} {
			t.Run(kind, func(t *testing.T) {
				count := agentRevocationRound(t, f, kind)
				t.Logf("overlapping successful issuances=%d; all affected tokens inactive after committed %s revocation", count, kind)
			})
		}
	})
}

func TestAgentConcurrentRevocationCAS(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := acceptanceAgent(t, f)
		initial := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		const callers = 16
		start := make(chan struct{})
		results := make(chan int, callers)
		for n := 0; n < callers; n++ {
			go func() {
				<-start
				status, _ := agentJSON(t, f, "POST", "/agent-grants/"+initial.GrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "competing CAS revocation"})
				results <- status
			}()
		}
		close(start)
		winners := 0
		for n := 0; n < callers; n++ {
			status := <-results
			if status == 200 {
				winners++
			} else if status != 409 {
				t.Error("unexpected CAS status", status)
			}
		}
		if winners != 1 {
			t.Fatal("competing revocations did not have one winner", winners)
		}
		requireActive(t, f, initial.Access, false)
	})
}

func TestAgentDelegationSoak(t *testing.T) {
	duration, err := time.ParseDuration(os.Getenv("AUTHOVA_AGENT_SOAK_DURATION"))
	if err != nil || duration < time.Minute {
		t.Skip("opt in with AUTHOVA_AGENT_SOAK_DURATION>=1m; formal long gate requires >=1h")
	}
	if duration > 24*time.Hour {
		t.Fatal("local soak limit exceeded")
	}
	agentDatabases(t, func(t *testing.T, f *fixture) {
		t.Parallel()
		a, i := acceptanceAgent(t, f)
		initial := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		ctx, cancel := context.WithCancel(context.Background())
		var background sync.WaitGroup
		var machineIssued atomic.Int64
		failures := make(chan error, 1)
		for worker := 0; worker < 8; worker++ {
			background.Add(1)
			go func() {
				defer background.Done()
				for ctx.Err() == nil {
					status, out, err := agentHTTP(f.http, f.app1.Issuer+"/oauth/token", a.Agent.ClientID, a.Secret, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}, "tenant_id": {"untrusted-tenant"}, "user_id": {"untrusted-user"}})
					if err == nil && status == 200 {
						token, _ := out["access_token"].(string)
						var active map[string]any
						active, err = agentActiveHTTP(f, token)
						if err == nil {
							err = requireAgentBinding(active, f, a, i, domain.AgentAutonomous, "", i.AutonomousGrantID)
						}
					} else if err == nil {
						err = fmt.Errorf("background issuance status %d", status)
					}
					if err != nil {
						select {
						case failures <- err:
						default:
						}
						cancel()
						return
					}
					machineIssued.Add(1)
					select {
					case <-ctx.Done():
						return
					case <-time.After(100 * time.Millisecond):
					}
				}
			}()
		}
		defer func() { cancel(); background.Wait() }()
		deadline := time.Now().Add(duration)
		started := time.Now()
		refreshAt := time.Now().Add(40 * time.Second)
		churnAt := time.Now()
		var log *os.File
		if directory := os.Getenv("AUTHOVA_AGENT_ACCEPTANCE_DIR"); directory != "" {
			var err error
			log, err = os.OpenFile(filepath.Join(directory, "agent-soak-"+f.store.Dialect()+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
		}
		current := initial
		checks, rotations, revokedRounds, issued, expiryChecks := 0, 0, 0, 0, 0
		type expiring struct {
			token string
			check time.Time
		}
		expirations := []expiring{{initial.Access, time.Now().Add(125 * time.Second)}}
		kinds := []string{"grant", "installation", "agent"}
		for time.Now().Before(deadline) {
			select {
			case err := <-failures:
				t.Fatal("continuous Agent workload failed", err)
			default:
			}
			claims := requireActive(t, f, current.Access, true)
			if err := requireAgentBinding(claims, f, a, i, domain.AgentDelegated, f.user1.ID, initial.GrantID); err != nil {
				t.Fatal(err)
			}
			checks++
			if time.Now().After(refreshAt) {
				reauthenticateAcceptanceOwner(t, f)
				// Rejected rebinding must never consume the next valid generation.
				status, _, err := agentHTTP(f.http, f.app1.Issuer+"/oauth/token", a.Agent.ClientID, a.Secret, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {current.Refresh}, "installation_id": {application.ID()}})
				if err != nil || status != 400 {
					t.Fatal("invalid refresh substitution not rejected", status, err)
				}
				status, out, err := agentRefreshHTTP(f, a, current.Refresh)
				if err != nil || status != 200 {
					t.Fatal("live refresh chain failed", status, err)
				}
				current = recoveryCredentialFrom(t, f, out)
				if current.GrantID != initial.GrantID {
					t.Fatal("refresh changed delegated grant")
				}
				expirations = append(expirations, expiring{current.Access, time.Now().Add(125 * time.Second)})
				rotations++
				refreshAt = time.Now().Add(40 * time.Second)
			}
			if time.Now().After(churnAt) {
				issued += agentRevocationRound(t, f, kinds[revokedRounds%len(kinds)])
				requireActive(t, f, current.Access, true)
				revokedRounds++
				churnAt = time.Now().Add(20 * time.Second)
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				row := map[string]any{"elapsed_seconds": time.Since(started).Seconds(), "checks": checks, "refresh_rotations": rotations, "revocation_rounds": revokedRounds, "concurrent_issued": issued, "continuous_machine_issued_and_verified": machineIssued.Load(), "expired_token_checks": expiryChecks, "heap_bytes": mem.HeapAlloc, "goroutines": runtime.NumGoroutine(), "pool": f.store.Stats()}
				if log != nil {
					if err := json.NewEncoder(log).Encode(row); err != nil {
						t.Fatal(err)
					}
				}
				t.Logf("Agent soak elapsed=%s checks=%d rotations=%d revoke_rounds=%d issued=%d", time.Since(started).Round(time.Second), checks, rotations, revokedRounds, issued)
			}
			for len(expirations) > 0 && time.Now().After(expirations[0].check) {
				requireActive(t, f, expirations[0].token, false)
				expirations = expirations[1:]
				expiryChecks++
			}
			time.Sleep(25 * time.Millisecond)
		}
		if duration >= 10*time.Minute && (rotations < 10 || expiryChecks < 5 || revokedRounds < 10) {
			t.Fatal("soak did not cross actual TTLs and revocation boundaries")
		}
		cancel()
		background.Wait()
		select {
		case err := <-failures:
			t.Fatal("continuous Agent workload failed", err)
		default:
		}
		if f.store.Stats().InUse != 0 {
			t.Fatal("database connections leaked after workload drained")
		}
		t.Logf("Agent soak complete duration=%s checks=%d rotations=%d revoke_rounds=%d issued=%d expired=%d continuous_machine=%d", time.Since(started), checks, rotations, revokedRounds, issued, expiryChecks, machineIssued.Load())
	})
}
