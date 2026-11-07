package integration

import (
	"context"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// Long-lived acceptance fixtures must satisfy the same recent-MFA requirement
// as an operator. Never extend MFA timestamps or bypass AppManage in a soak.
func reauthenticateAcceptanceOwner(t *testing.T, f *fixture) {
	t.Helper()
	if f.operatorToken != "" && time.Since(f.operatorAuthAt) < 5*time.Minute {
		return
	}
	if f.operatorEmail == "" || f.operatorTOTP == "" {
		authenticateAcceptanceOwner(t, f)
		return
	}
	token, _, _, err := f.service.LoginOperator(context.Background(), f.operatorEmail, password, secure.TOTP(f.operatorTOTP, time.Now().Unix()/30))
	if err != nil {
		t.Fatal("legitimate operator MFA reauthentication failed", err)
	}
	f.operatorToken, f.operatorAuthAt = token, time.Now()
	f.operatorReauth++
	t.Log("acceptance operator reauthenticated with current TOTP")
}

// Resume/setup is host-owned synthetic preparation, outside measured traffic.
func authenticateAcceptanceOwner(t *testing.T, f *fixture) {
	t.Helper()
	var user domain.User
	if err := f.store.App(context.Background(), f.scope1, false, func(tx ports.AppTx) error {
		var err error
		user, err = tx.User(f.user1.ID, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	secret := secure.NewTOTP()
	owner := domain.Operator{ID: application.ID(), Email: "acceptance-" + application.ID() + "@example.test", Role: "owner", Status: "active", Password: user.PasswordHash, TOTP: secret}
	if err := f.store.Platform(context.Background(), true, func(tx ports.PlatformTx) error { return tx.PutOperator(owner) }); err != nil {
		t.Fatal(err)
	}
	f.operatorEmail, f.operatorTOTP, f.operatorToken = owner.Email, secret, ""
	reauthenticateAcceptanceOwner(t, f)
}
