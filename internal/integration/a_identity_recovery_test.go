package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// Run before ordinary fixture-heavy suites: this offline operation intentionally
// quarantines every identity in the fresh disposable database, never a demo DB.
func TestIdentityRecoveryLifecycle(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			requireOwnedRecoveryDatabase(t, driver, dsn, "AUTHOVA_TEST_DISPOSABLE_DATABASE")
			f := setup(t, driver, dsn)
			ctx := context.Background()
			code, v := f.code()
			status, tokens := f.exchange(code, v)
			if status != 200 {
				t.Fatal(status)
			}
			oldFlow := f.securityFlow(t, "authorize")
			if e := f.service.PasswordLogin(ctx, f.scope1, oldFlow.ID, "email", f.user1.Email, password); e != nil {
				t.Fatal(e)
			}
			var session domain.Session
			if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				sessions, e := tx.Sessions(f.user1.ID, "", 100)
				if e != nil {
					return e
				}
				session = sessions[0]
				return tx.SetAdminRole(f.user1.ID, "owner")
			}); e != nil {
				t.Fatal(e)
			}
			stopped := *f.service
			stopped.Store = recoveryStopStore{Store: f.store, target: f.scope1.ID()}
			if _, e := stopped.QuarantineRecoveredIdentities(ctx, "inject interrupted user page"); !errors.Is(e, domain.ErrUnavailable) {
				t.Fatal("missing injected failure", e)
			}
			if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
				a, e := tx.App()
				if e == nil && a.Status != domain.Quarantined {
					t.Fatal("interruption did not leave a durable App fence")
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if e := f.service.ReleaseRecoveredApp(ctx, f.scope1, "incomplete recovery must stay fenced"); !errors.Is(e, domain.ErrConflict) {
				t.Fatal("released incomplete quarantine", e)
			}
			report, e := f.service.QuarantineRecoveredIdentities(ctx, "isolated recovery regression; restored facts are not current authority")
			if e != nil {
				t.Fatal(e)
			}
			if report.Apps < 2 || report.Users < 2 || report.Clients < 1 || report.Sessions < 1 || report.Families < 1 || report.Flows < 1 {
				t.Fatalf("incomplete quarantine counts: %+v", report)
			}
			if _, e = f.service.Operator(ctx, f.operatorToken, false); e == nil {
				t.Fatal("old management session resurrected")
			}
			if _, _, _, e = f.service.LoginOperator(ctx, f.operatorEmail, password, secure.TOTP(f.operatorTOTP, time.Now().Unix()/30)); e == nil {
				t.Fatal("old operator credential accepted")
			}
			if _, e = f.service.Complete(ctx, f.scope1, oldFlow.ID); e == nil {
				t.Fatal("old proof survived recovery")
			}
			if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				u, e := tx.User(f.user1.ID, true)
				if e != nil {
					return e
				}
				if u.Status != domain.Quarantined {
					t.Fatal("user not fenced")
				}
				if e = f.service.DisableUser(tx, "test", f.app1.ID, u.ID, "ordinary disable must not release recovery"); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("ordinary disable removed fence", e)
				}
				if _, e = f.service.EnableUser(tx, "test", f.app1.ID, u.ID, u.Epoch, "ordinary enable must not release recovery"); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("ordinary enable removed fence", e)
				}
				s, e := tx.Session(session.ID, false)
				if e != nil {
					return e
				}
				if s.Status != "revoked" {
					t.Fatal("session not revoked")
				}
				if _, e = tx.ActiveToken(decodeAgentClaims(t, tokens.Access)["jti"].(string)); e == nil {
					t.Fatal("restored access token active")
				}
				c, e := tx.Client(f.client.ClientID)
				if e != nil {
					return e
				}
				if c.Status != "disabled" || c.SecretDigest != "" || c.PreviousSecretDigest != "" {
					t.Fatal("restored client credential survived")
				}
				keys, e := tx.Keys()
				if e != nil {
					return e
				}
				active := 0
				for _, key := range keys {
					if key.Current && key.RevokedAt == 0 {
						active++
					}
				}
				if active != 1 {
					t.Fatal("fresh recovery signer missing")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			again, e := f.service.QuarantineRecoveredIdentities(ctx, "repeat completed recovery")
			if e != nil || again.Users != 0 || again.Keys != 0 || again.Clients != 0 || again.Sessions != 0 || again.Operators != 0 {
				t.Fatalf("repeat changed completed recovery: %+v %v", again, e)
			}
			newTOTP := secure.NewTOTP()
			if e = f.service.RecoverOperator(ctx, f.operatorEmail, "new approved operator recovery password", newTOTP); !errors.Is(e, domain.ErrConflict) {
				t.Fatal("unapproved restored role accepted", e)
			}
			if e = f.service.RecoverApprovedOperator(ctx, f.operatorEmail, "new approved operator recovery password", newTOTP, application.OperatorRecoveryApproval{Role: "owner", Reason: "identity and current platform owner authority verified"}); e != nil {
				t.Fatal(e)
			}
			token, _, _, e := f.service.LoginOperator(ctx, f.operatorEmail, "new approved operator recovery password", secure.TOTP(newTOTP, time.Now().Unix()/30))
			if e != nil {
				t.Fatal(e)
			}
			f.operatorToken = token
			status, _ = f.managementJSON("POST", "/resume", map[string]any{"reason": "ordinary resume must not bypass quarantine"})
			if status != 409 {
				t.Fatal("app resume bypassed quarantine", status)
			}
			if e = f.service.RecoverQuarantinedUser(ctx, f.scope1, f.user2.ID, "email", "wrong-app@example.test", "approved recovery user password", "", "cross-app recovery denied"); e == nil {
				t.Fatal("cross-App user recovered")
			}
			if e = f.service.RecoverQuarantinedUser(ctx, f.scope1, f.user1.ID, "email", "approved-new@example.test", "approved recovery user password", "owner", "out-of-band approved new identity and contact"); e != nil {
				t.Fatal(e)
			}
			if e = f.service.ReleaseRecoveredApp(ctx, f.scope1, "external directory, bans and credentials reviewed"); e != nil {
				t.Fatal(e)
			}
			status, _ = f.managementJSON("POST", "/resume", map[string]any{"reason": "activate reviewed recovery"})
			if status != 200 {
				t.Fatal("reviewed App not activatable", status)
			}
			if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
				u, e := tx.User(f.user1.ID, false)
				if e != nil {
					return e
				}
				if u.Status != "active" || u.Email != "approved-new@example.test" || u.TOTPSecret != "" || len(u.RecoveryDigests) != 0 {
					t.Fatal("old login data retained")
				}
				if _, e = tx.FindUser("email", f.service.Digest(f.app1.ID, "identifier:email", f.user1.Email)); !errors.Is(e, domain.ErrNotFound) {
					t.Fatal("old identifier remained usable", e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e = f.store.App(ctx, f.scope2, false, func(tx ports.AppTx) error {
				u, e := tx.User(f.user2.ID, false)
				if e == nil && u.Status != domain.Quarantined {
					t.Fatal("unapproved other App user reactivated")
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			// No old OAuth client/refresh proof becomes usable merely by releasing the App.
			status, _ = f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "refresh_token", "refresh_token", tokens.Refresh), true)
			if status == 200 {
				t.Fatal("old refresh/client secret resurrected")
			}
		})
	}
	if !configured {
		t.Skip("real PG/MySQL required")
	}
}

// Fail after the committed App fence, before its first user page. No mocked
// facts are persisted: all preceding and resumed writes use the real database.
type recoveryStopStore struct {
	ports.Store
	target string
}

func (s recoveryStopStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	return s.Store.App(ctx, scope, write, func(tx ports.AppTx) error {
		if write && scope.ID() == s.target {
			return fn(recoveryStopTx{AppTx: tx})
		}
		return fn(tx)
	})
}

type recoveryStopTx struct{ ports.AppTx }

func (recoveryStopTx) Users(string, int) ([]domain.User, error) { return nil, domain.ErrUnavailable }
