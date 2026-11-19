package integration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	mysql "github.com/go-sql-driver/mysql"
)

// This is an explicit release gate, never an optional local-database test.
// The issuer must separately prove that the named fresh fixture is owned.
func passwordStorageFixture(t *testing.T) (*fixture, *sql.DB) {
	t.Helper()
	name := os.Getenv("AUTHOVA_TEST_DISPOSABLE_DATABASE")
	dsn := os.Getenv("AUTHOVA_TEST_MYSQL_DSN")
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !regexp.MustCompile(`^authova_quality_[0-9a-f]{12}$`).MatchString(name) || cfg.DBName != name || os.Getenv("AUTHOVA_TEST_POSTGRES_DSN") != "" {
		t.Fatal("release password gate requires its isolated MySQL fixture")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("open isolated audit reader failed")
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error("audit reader close failed")
		}
	})
	return setup(t, "mysql", dsn), db
}

type passwordStorageProbe struct {
	ports.Store
	fail        string
	beforeWrite func(context.Context) error
}

func (p passwordStorageProbe) Platform(ctx context.Context, write bool, fn func(ports.PlatformTx) error) error {
	return p.Store.Platform(ctx, write, func(tx ports.PlatformTx) error {
		return fn(passwordStorageTx{PlatformTx: tx, fail: p.fail, beforeWrite: p.beforeWrite, ctx: ctx})
	})
}

type passwordStorageTx struct {
	ports.PlatformTx
	fail        string
	beforeWrite func(context.Context) error
	ctx         context.Context
}

func (p passwordStorageTx) CountOperators() (int, error) {
	if p.beforeWrite != nil {
		if err := p.beforeWrite(p.ctx); err != nil {
			return 0, err
		}
	}
	return p.PlatformTx.CountOperators()
}

func (p passwordStorageTx) RevokeOperatorSessions(id string) error {
	if err := p.PlatformTx.RevokeOperatorSessions(id); err != nil {
		return err
	}
	if p.fail == "revoke" {
		return domain.ErrUnavailable
	}
	return nil
}

func (p passwordStorageTx) Audit(a domain.Audit) error {
	if err := p.PlatformTx.Audit(a); err != nil {
		return err
	}
	if p.fail == "audit" {
		return domain.ErrUnavailable
	}
	return nil
}

func TestPlatformPasswordStorage(t *testing.T) {
	f, db := passwordStorageFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	auditCount := func() int {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE app_id='' ").Scan(&n); err != nil {
			t.Fatal("audit count failed")
		}
		return n
	}
	for _, fault := range []string{"revoke", "audit", "success"} {
		t.Run(fault, func(t *testing.T) {
			id := application.ID()
			hash, err := f.service.HashPassword(ctx, password)
			if err != nil {
				t.Fatal("fixture KDF failed")
			}
			otp := secure.NewTOTP()
			actor := domain.Operator{ID: id, Email: id + "@example.test", Password: hash, Role: "admin", Status: "active", TOTP: otp}
			token := secure.Random(32)
			digest := f.service.Vault.Digest("management-token", token)
			other := secure.Random(32)
			if err := f.store.Platform(ctx, true, func(tx ports.PlatformTx) error {
				if err := tx.PutOperator(actor); err != nil {
					return err
				}
				for _, d := range []string{digest, other} {
					if err := tx.PutManagementSession(domain.ManagementSession{Digest: d, OperatorID: id, Expires: time.Now().Add(time.Hour).Unix(), MFATime: time.Now().Unix()}); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal("fixture seed failed")
			}
			before := auditCount()
			svc := *f.service
			svc.Store = passwordStorageProbe{Store: f.store, fail: fault}
			newPassword := "replacement password for owned release fixture"
			err = svc.SetOperatorPassword(ctx, token, id, password, newPassword, "owned release storage gate")
			if fault == "success" && err != nil || fault != "success" && !errors.Is(err, domain.ErrUnavailable) {
				t.Fatal("unexpected mutation outcome")
			}
			if err := f.store.Platform(ctx, false, func(tx ports.PlatformTx) error {
				current, err := tx.Operator(id, false)
				if err != nil {
					return err
				}
				changed := fault == "success"
				if (current.Password != hash) != changed || current.TOTP != otp || current.TOTPLast != 0 || current.Role != "admin" || current.Status != "active" {
					t.Fatal("password or unrelated account policy mismatch")
				}
				if changed && !secure.CheckPassword(newPassword, current.Password) {
					t.Fatal("replacement password not effective")
				}
				for _, d := range []string{digest, other} {
					m, err := tx.ManagementSession(d)
					if err != nil {
						return err
					}
					if m.Revoked != changed {
						t.Fatal("session rollback/commit mismatch")
					}
				}
				return nil
			}); err != nil {
				t.Fatal("stored result read failed")
			}
			want := before
			if fault == "success" {
				want++
			}
			if auditCount() != want {
				t.Fatal("audit escaped rollback or missing on commit")
			}
			if fault == "success" {
				if _, err := f.service.Operator(ctx, token, false); err == nil {
					t.Fatal("revoked self-session accepted")
				}
				if _, _, _, err := f.service.LoginOperator(ctx, actor.Email, password, secure.TOTP(otp, time.Now().Unix()/30)); err == nil {
					t.Fatal("old password accepted")
				}
				if _, _, _, err := f.service.LoginOperator(ctx, actor.Email, newPassword, secure.TOTP(otp, time.Now().Unix()/30)); err != nil {
					t.Fatal("new password plus original TOTP cannot re-login")
				}
			}
		})
	}
	t.Run("stale-current-password-proof", func(t *testing.T) {
		reached, resume := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(resume) })
		svc := *f.service
		svc.Store = passwordStorageProbe{Store: f.store, beforeWrite: func(ctx context.Context) error {
			close(reached)
			select {
			case <-resume:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		actor, err := f.service.Operator(ctx, f.operatorToken, true)
		if err != nil {
			t.Fatal("fixture actor missing")
		}
		done := make(chan error, 1)
		go func() {
			done <- svc.SetOperatorPassword(ctx, f.operatorToken, actor.ID, password, "stale attempted replacement password", "owned race gate")
		}()
		select {
		case <-reached:
		case <-time.After(10 * time.Second):
			t.Fatal("write proof barrier not reached")
		}
		replacement, err := f.service.HashPassword(ctx, "concurrent owned replacement password")
		if err != nil {
			t.Fatal("concurrent KDF failed")
		}
		before := auditCount()
		if err := f.store.Platform(ctx, true, func(tx ports.PlatformTx) error {
			o, err := tx.Operator(actor.ID, true)
			if err != nil {
				return err
			}
			o.Password = replacement
			return tx.PutOperator(o)
		}); err != nil {
			t.Fatal("concurrent password fixture write failed")
		}
		once.Do(func() { close(resume) })
		if err := <-done; !errors.Is(err, domain.ErrForbidden) {
			t.Fatal("stale proof authorized password write")
		}
		if auditCount() != before {
			t.Fatal("stale proof wrote audit")
		}
		if err := f.store.Platform(ctx, false, func(tx ports.PlatformTx) error {
			o, err := tx.Operator(actor.ID, false)
			if err != nil {
				return err
			}
			if o.Password != replacement {
				t.Fatal("stale proof overwrote concurrent password")
			}
			m, err := tx.ManagementSession(f.service.Vault.Digest("management-token", f.operatorToken))
			if err != nil {
				return err
			}
			if m.Revoked {
				t.Fatal("stale proof revoked target sessions")
			}
			return nil
		}); err != nil {
			t.Fatal("stale result read failed")
		}
	})
}

func TestPlatformPasswordReproofStorage(t *testing.T) {
	for _, mode := range []string{"role", "session"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := passwordStorageFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			actor, err := f.service.Operator(ctx, f.operatorToken, true)
			if err != nil {
				t.Fatal("fixture actor unavailable")
			}
			reached, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(resume) })
			service := *f.service
			service.Store = passwordStorageProbe{Store: f.store, beforeWrite: func(ctx context.Context) error {
				close(reached)
				select {
				case <-resume:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			done := make(chan error, 1)
			go func() {
				done <- service.SetOperatorPassword(ctx, f.operatorToken, actor.ID, password, "reproof attempted replacement password", "owned reproof gate")
			}()
			select {
			case <-reached:
			case <-ctx.Done():
				t.Fatal("reproof barrier not reached")
			}
			if err := f.store.Platform(ctx, true, func(tx ports.PlatformTx) error {
				if mode == "role" {
					o, err := tx.Operator(actor.ID, true)
					if err != nil {
						return err
					}
					o.Role = "auditor"
					return tx.PutOperator(o)
				}
				m, err := tx.ManagementSession(f.service.Vault.Digest("management-token", f.operatorToken))
				if err != nil {
					return err
				}
				m.Revoked = true
				return tx.PutManagementSession(m)
			}); err != nil {
				t.Fatal("concurrent proof invalidation failed")
			}
			once.Do(func() { close(resume) })
			want := domain.ErrForbidden
			if mode == "session" {
				want = domain.ErrUnauthenticated
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatal("invalidated proof accepted")
			}
			if err := f.store.Platform(ctx, false, func(tx ports.PlatformTx) error {
				o, err := tx.Operator(actor.ID, false)
				if err != nil {
					return err
				}
				if o.Password != actor.Password || o.TOTP != actor.TOTP {
					t.Fatal("rejected proof changed credential")
				}
				return nil
			}); err != nil {
				t.Fatal("reproof result unavailable")
			}
		})
	}
}
