package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

type humanRecoverySnapshot struct {
	AgentFixture                                                agentRecoveryFixture
	Access, Refresh, OperatorToken, OperatorEmail, OperatorTOTP string
}

func eachHumanSnapshot(t *testing.T, fn func(*testing.T, string, string, string)) {
	dir := os.Getenv("AUTHOVA_IDENTITY_RECOVERY_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("explicit private disposable recovery campaign required")
	}
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			t.Fatal("disposable source DB required")
		}
		t.Run(driver, func(t *testing.T) {
			requireOwnedRecoveryDatabase(t, driver, dsn, "AUTHOVA_IDENTITY_RECOVERY_SOURCE_DATABASE")
			fn(t, driver, dsn, filepath.Join(dir, driver+"-human.json"))
		})
	}
}
func TestIdentityRecoverySnapshotFixture(t *testing.T) {
	eachHumanSnapshot(t, func(t *testing.T, driver, dsn, file string) {
		f := setup(t, driver, dsn)
		code, v := f.code()
		status, tok := f.exchange(code, v)
		if status != 200 || !f.active(tok.Access) {
			t.Fatal("snapshot credential setup failed")
		}
		saved := humanRecoverySnapshot{AgentFixture: agentRecoveryFixture{App: f.app1, OtherApp: f.app2, Client: f.client, Secret: f.secret, User: f.user1}, Access: tok.Access, Refresh: tok.Refresh, OperatorToken: f.operatorToken, OperatorEmail: f.operatorEmail, OperatorTOTP: f.operatorTOTP}
		data, e := json.Marshal(saved)
		if e != nil || os.WriteFile(file, data, 0600) != nil {
			t.Fatal("cannot save private fixture")
		}
	})
}
func readHumanSnapshot(t *testing.T, file string) humanRecoverySnapshot {
	t.Helper()
	var saved humanRecoverySnapshot
	data, e := os.ReadFile(file)
	if e != nil || json.Unmarshal(data, &saved) != nil {
		t.Fatal("private fixture unavailable")
	}
	return saved
}
func TestIdentityRecoverySnapshotMutation(t *testing.T) {
	eachHumanSnapshot(t, func(t *testing.T, driver, dsn, file string) {
		saved := readHumanSnapshot(t, file)
		f := restoredAgentFixture(t, driver, dsn, saved.AgentFixture)
		ctx := context.Background()
		hash, e := f.service.HashPassword(ctx, "post backup replaced password phrase")
		if e != nil {
			t.Fatal(e)
		}
		if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			u, e := tx.User(saved.AgentFixture.User.ID, true)
			if e != nil {
				return e
			}
			u.PasswordHash = hash
			u.TOTPSecret = secure.NewTOTP()
			u.Status = "disabled"
			u.Epoch++
			if e = tx.PutUser(u); e != nil {
				return e
			}
			if e = tx.RevokeSessions(u.ID); e != nil {
				return e
			}
			c, e := tx.LockClient(f.client.ClientID)
			if e != nil {
				return e
			}
			c.SecretDigest = f.service.Digest(f.app1.ID, "client-secret", secure.Random(32))
			c.Version++
			return tx.PutClient(c)
		}); e != nil {
			t.Fatal(e)
		}
		if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			if _, e := tx.ActiveToken(decodeAgentClaims(t, saved.Access)["jti"].(string)); e == nil {
				return errors.New("post-backup access revocation absent")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	})
}
func TestIdentityRecoverySnapshotRestored(t *testing.T) {
	eachHumanSnapshot(t, func(t *testing.T, driver, _, file string) {
		saved := readHumanSnapshot(t, file)
		dsn := os.Getenv("AUTHOVA_RESTORE_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			t.Fatal("isolated restored DB required")
		}
		requireOwnedRecoveryDatabase(t, driver, dsn, "AUTHOVA_IDENTITY_RECOVERY_RESTORED_DATABASE")
		f := restoredAgentFixture(t, driver, dsn, saved.AgentFixture)
		ctx := context.Background()
		// The actual restored old snapshot resurrects both token and old password;
		// neither is trusted merely because dump/restore succeeded.
		if !f.active(saved.Access) {
			t.Fatal("fixture did not demonstrate backup rollback")
		}
		flow := f.securityFlow(t, "authorize")
		if e := f.service.PasswordLogin(ctx, f.scope1, flow.ID, "email", f.user1.Email, password); e != nil {
			t.Fatal("old snapshot password not present", e)
		}
		if _, e := f.service.QuarantineRecoveredIdentities(ctx, "restored old snapshot; no authoritative post-backup credentials or ban facts"); e != nil {
			t.Fatal(e)
		}
		if e := f.service.PasswordLogin(ctx, f.scope1, flow.ID, "email", f.user1.Email, password); e == nil {
			t.Fatal("quarantined old password accepted")
		}
		if _, e := f.service.Operator(ctx, saved.OperatorToken, false); e == nil {
			t.Fatal("restored management token accepted")
		}
		secret := secure.NewTOTP()
		if e := f.service.RecoverApprovedOperator(ctx, saved.OperatorEmail, "approved new recovery operator password", secret, application.OperatorRecoveryApproval{Role: "auditor", Reason: "out-of-band identity approved for auditor only"}); e != nil {
			t.Fatal(e)
		}
		if _, operator, _, e := f.service.LoginOperator(ctx, saved.OperatorEmail, "approved new recovery operator password", secure.TOTP(secret, time.Now().Unix()/30)); e != nil || operator.Role != "auditor" {
			t.Fatal("restored owner authority was not replaced by explicit auditor approval", e)
		}
		if e := f.service.RecoverQuarantinedUser(ctx, f.scope1, f.user1.ID, "email", "approved-recovered@example.test", "approved new recovery user password", "", "independent identity and contact verification completed"); e != nil {
			t.Fatal(e)
		}
		if e := f.service.ReleaseRecoveredApp(ctx, f.scope1, "configuration and external facts reconciled"); e != nil {
			t.Fatal(e)
		}
		if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			u, e := tx.User(f.user1.ID, false)
			if e != nil {
				return e
			}
			if u.TOTPSecret != "" || u.Email != "approved-recovered@example.test" {
				t.Fatal("restored credentials survived approval")
			}
			if e = f.service.CheckPassword(ctx, password, u.PasswordHash); e == nil {
				t.Fatal("old password survived approved replacement")
			}
			if _, e = tx.ActiveToken(decodeAgentClaims(t, saved.Access)["jti"].(string)); e == nil {
				t.Fatal("old access token resurrected")
			}
			c, e := tx.Client(f.client.ClientID)
			if e != nil {
				return e
			}
			if c.Status != "disabled" || c.SecretDigest != "" {
				t.Fatal("old client secret survived recovery")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		t.Log("actual old backup rollback observed; fenced recovery rejected old identity and client credentials")
	})
}
