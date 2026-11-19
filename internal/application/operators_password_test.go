package application

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// Each callback has an isolated snapshot, committed only on success, like Store.
type passwordStore struct {
	ports.Store
	operators   map[string]domain.Operator
	sessions    map[string]domain.ManagementSession
	audits      []domain.Audit
	calls       int
	beforeWrite func()
	failure     string
}
type passwordTx struct {
	ports.PlatformTx
	store *passwordStore
}

func (s *passwordStore) Platform(_ context.Context, _ bool, fn func(ports.PlatformTx) error) error {
	s.calls++
	if s.calls == 2 && s.beforeWrite != nil {
		s.beforeWrite()
	}
	snapshot := *s
	snapshot.operators = maps.Clone(s.operators)
	snapshot.sessions = maps.Clone(s.sessions)
	snapshot.audits = append([]domain.Audit(nil), s.audits...)
	if err := fn(&passwordTx{store: &snapshot}); err != nil {
		return err
	}
	s.operators, s.sessions, s.audits = snapshot.operators, snapshot.sessions, snapshot.audits
	return nil
}
func (t *passwordTx) ManagementSession(digest string) (domain.ManagementSession, error) {
	m, ok := t.store.sessions[digest]
	if !ok {
		return m, domain.ErrNotFound
	}
	return m, nil
}
func (t *passwordTx) Operator(id string, _ bool) (domain.Operator, error) {
	o, ok := t.store.operators[id]
	if !ok {
		return o, domain.ErrNotFound
	}
	return o, nil
}
func (t *passwordTx) CountOperators() (int, error)        { return len(t.store.operators), nil }
func (t *passwordTx) PutOperator(o domain.Operator) error { t.store.operators[o.ID] = o; return nil }
func (t *passwordTx) RevokeOperatorSessions(id string) error {
	if t.store.failure == "revoke" {
		return domain.ErrUnavailable
	}
	for key, m := range t.store.sessions {
		if m.OperatorID == id {
			m.Revoked = true
			t.store.sessions[key] = m
		}
	}
	return nil
}
func (t *passwordTx) Audit(a domain.Audit) error {
	if t.store.failure == "audit" {
		return domain.ErrUnavailable
	}
	t.store.audits = append(t.store.audits, a)
	return nil
}

func TestOperatorPasswordAuthorizationAndAtomicRevocation(t *testing.T) {
	const oldPassword = "old correct password phrase"
	const newPassword = "new independent password phrase"
	oldHash, err := secure.HashPassword(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name, role, app, target, current, reason, failure string
		mutate                                            func(*passwordStore, string)
		want                                              error
	}{
		{name: "admin self", role: "admin", target: "actor", current: oldPassword, reason: "routine change"},
		{name: "owner reset", role: "owner", target: "target", current: oldPassword, reason: "approved reset"},
		{name: "admin other forbidden", role: "admin", target: "target", current: oldPassword, reason: "reset", want: domain.ErrForbidden},
		{name: "app owner forbidden", role: "owner", app: "app", target: "actor", current: oldPassword, reason: "reset", want: domain.ErrForbidden},
		{name: "auditor forbidden", role: "auditor", target: "actor", current: oldPassword, reason: "reset", want: domain.ErrForbidden},
		{name: "wrong actor password", role: "owner", target: "target", current: "not the actor password", reason: "reset", want: domain.ErrForbidden},
		{name: "reason required", role: "owner", target: "actor", current: oldPassword, want: domain.ErrInvalid},
		{name: "quarantined target", role: "owner", target: "target", current: oldPassword, reason: "reset", mutate: func(s *passwordStore, _ string) {
			o := s.operators["target"]
			o.Status = domain.Quarantined
			s.operators[o.ID] = o
		}, want: domain.ErrConflict},
		{name: "password changed during KDF", role: "owner", target: "target", current: oldPassword, reason: "reset", mutate: func(s *passwordStore, _ string) {
			o := s.operators["actor"]
			o.Password = "newer proof"
			s.operators[o.ID] = o
		}, want: domain.ErrForbidden},
		{name: "role changed during KDF", role: "owner", target: "target", current: oldPassword, reason: "reset", mutate: func(s *passwordStore, _ string) { o := s.operators["actor"]; o.Role = "admin"; s.operators[o.ID] = o }, want: domain.ErrForbidden},
		{name: "session revoked during KDF", role: "owner", target: "target", current: oldPassword, reason: "reset", mutate: func(s *passwordStore, d string) { m := s.sessions[d]; m.Revoked = true; s.sessions[d] = m }, want: domain.ErrUnauthenticated},
		{name: "MFA expired during KDF", role: "owner", target: "target", current: oldPassword, reason: "reset", mutate: func(s *passwordStore, d string) {
			m := s.sessions[d]
			m.MFATime = now.Add(-11 * time.Minute).Unix()
			s.sessions[d] = m
		}, want: domain.ErrUnauthenticated},
		{name: "revoke failure rolls back", role: "owner", target: "target", current: oldPassword, reason: "reset", failure: "revoke", want: domain.ErrUnavailable},
		{name: "audit failure rolls back", role: "owner", target: "target", current: oldPassword, reason: "reset", failure: "audit", want: domain.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vault, e := secure.NewVault(bytes.Repeat([]byte{7}, 32))
			if e != nil {
				t.Fatal(e)
			}
			digest := vault.Digest("management-token", "session")
			store := &passwordStore{operators: map[string]domain.Operator{
				"actor":  {ID: "actor", Email: "actor@example.test", Role: tc.role, AppID: tc.app, Status: "active", Password: oldHash, TOTP: "unchanged", TOTPLast: 42},
				"target": {ID: "target", Role: "admin", Status: "active", Password: oldHash, TOTP: "target-mfa", TOTPLast: 24},
			}, sessions: map[string]domain.ManagementSession{
				digest:     {OperatorID: "actor", Expires: now.Add(time.Hour).Unix(), MFATime: now.Unix()},
				"target-a": {OperatorID: "target"}, "target-b": {OperatorID: "target"},
				"unrelated": {OperatorID: "other"},
			}, failure: tc.failure}
			store.beforeWrite = func() {
				if tc.mutate != nil {
					tc.mutate(store, digest)
				}
			}
			service := &Service{Store: store, Vault: vault, now: func() time.Time { return now }, kdf: make(chan struct{}, 1), kdfQueue: make(chan struct{}, 1)}
			e = service.SetOperatorPassword(context.Background(), "session", tc.target, tc.current, newPassword, tc.reason)
			if !errors.Is(e, tc.want) {
				t.Fatalf("got %v, want %v", e, tc.want)
			}
			target := store.operators[tc.target]
			if tc.want != nil {
				if target.Password != oldHash && !(tc.target == "actor" && tc.mutate != nil) {
					t.Fatal("password changed on rejected operation")
				}
				if store.sessions["target-a"].Revoked || len(store.audits) != 0 {
					t.Fatal("partial revocation/audit escaped failed transaction")
				}
				return
			}
			if target.Password == oldHash || !secure.CheckPassword(newPassword, target.Password) || secure.CheckPassword(oldPassword, target.Password) {
				t.Fatal("new password not effective")
			}
			originalMFA := "target-mfa"
			last := int64(24)
			if tc.target == "actor" {
				originalMFA = "unchanged"
				last = 42
			}
			if target.TOTP != originalMFA || target.TOTPLast != last || target.Role != tc.role && tc.target == "actor" || target.Status != "active" {
				t.Fatal("unrelated account policy changed")
			}
			for _, m := range store.sessions {
				if m.Revoked != (m.OperatorID == tc.target) {
					t.Fatal("session revocation escaped target boundary")
				}
			}
			if len(store.audits) != 1 || store.audits[0].Reason != tc.reason || store.audits[0].SubjectID != tc.target || store.audits[0].ActorID != "actor" {
				t.Fatal("missing bounded audit")
			}
		})
	}
}
