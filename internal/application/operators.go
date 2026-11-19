package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// SetOperatorPassword changes only the password, leaving the target's MFA intact.
// A platform admin can change their own password; an owner can reset another
// platform operator's password. Both must prove their own current password.
func (s *Service) SetOperatorPassword(ctx context.Context, token, id, currentPassword, password, reason string) error {
	if !ValidReason(reason) || id == "" || currentPassword == "" {
		return domain.ErrInvalid
	}
	actor, err := s.Operator(ctx, token, true)
	if err != nil {
		return err
	}
	if actor.AppID != "" || (actor.Role != "owner" && actor.Role != "admin") || (actor.ID != id && actor.Role != "owner") {
		return domain.ErrForbidden
	}
	if err = s.CheckPassword(ctx, currentPassword, actor.Password); err != nil {
		if errors.Is(err, domain.ErrUnauthenticated) {
			return domain.ErrForbidden
		}
		return err
	}
	hash, err := s.HashPassword(ctx, password)
	if err != nil {
		return err
	}
	return s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		// Use the same serialization lock as role changes. Recheck after KDF so
		// a concurrent password/role change cannot authorize with stale proof.
		if _, err := t.CountOperators(); err != nil {
			return err
		}
		current, err := t.Operator(actor.ID, true)
		if err != nil {
			return err
		}
		if current.AppID != "" || current.Status != "active" || current.Password != actor.Password ||
			(current.Role != "owner" && current.Role != "admin") || (id != current.ID && current.Role != "owner") {
			return domain.ErrForbidden
		}
		// Platform operators authenticate with a management session, not an App
		// JWT. Check its current liveness and MFA again in the write transaction.
		if strings.Count(token, ".") == 2 {
			return domain.ErrForbidden
		}
		session, err := t.ManagementSession(s.Vault.Digest("management-token", token))
		if err != nil {
			return err
		}
		if session.OperatorID != actor.ID || session.Revoked || session.Expires <= s.Now().Unix() || session.MFATime < s.Now().Add(-10*time.Minute).Unix() {
			return domain.ErrUnauthenticated
		}
		target := current
		if id != current.ID {
			target, err = t.Operator(id, true)
			if err != nil {
				return err
			}
		}
		if target.AppID != "" {
			return domain.ErrForbidden
		}
		if target.Status == domain.Quarantined {
			return domain.ErrConflict
		}
		target.Password = hash
		if err = t.PutOperator(target); err != nil {
			return err
		}
		if err = t.RevokeOperatorSessions(id); err != nil {
			return err
		}
		return t.Audit(audit("", actor.ID, "platform.operator_password_change", id, reason))
	})
}

func (s *Service) CreateOperator(ctx context.Context, token, email, password, role string) (operator domain.Operator, secret string, err error) {
	actor, e := s.Operator(ctx, token, true)
	if e != nil {
		return operator, "", e
	}
	if actor.AppID != "" || actor.Role != "owner" {
		return operator, "", domain.ErrForbidden
	}
	email, e = secure.Normalize("email", email)
	if e != nil || !slices.Contains([]string{"owner", "admin", "auditor"}, role) {
		return operator, "", domain.ErrInvalid
	}
	hash, e := s.HashPassword(ctx, password)
	if e != nil {
		return operator, "", e
	}
	secret = secure.NewTOTP()
	operator = domain.Operator{ID: ID(), Email: email, Role: role, Status: "active", Password: hash, TOTP: secret}
	err = s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		current, e := t.Operator(actor.ID, true)
		if e != nil {
			return e
		}
		if current.Role != "owner" || current.Status != "active" {
			return domain.ErrForbidden
		}
		if e = t.PutOperator(operator); e != nil {
			return e
		}
		return t.Audit(audit("", actor.ID, "platform.operator_create", operator.ID))
	})
	return
}
func (s *Service) SetOperator(ctx context.Context, token, id, role, status string, reasons ...string) error {
	actor, e := s.Operator(ctx, token, true)
	if e != nil {
		return e
	}
	if actor.AppID != "" || actor.Role != "owner" {
		return domain.ErrForbidden
	}
	if !slices.Contains([]string{"owner", "admin", "auditor"}, role) || !slices.Contains([]string{"active", "disabled"}, status) {
		return domain.ErrInvalid
	}
	return s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		if _, e := t.CountOperators(); e != nil {
			return e
		}
		current, e := t.Operator(actor.ID, true)
		if e != nil {
			return e
		}
		if current.Role != "owner" || current.Status != "active" {
			return domain.ErrForbidden
		}
		owners, e := t.CountOperatorOwners()
		if e != nil {
			return e
		}

		target, e := t.Operator(id, true)
		if e != nil {
			return e
		}
		if target.Status == domain.Quarantined {
			return domain.ErrConflict
		}
		if target.Role == "owner" && target.Status == "active" && (role != "owner" || status != "active") && owners <= 1 {
			return domain.ErrConflict
		}
		target.Role, target.Status = role, status
		if e = t.PutOperator(target); e != nil {
			return e
		}
		if e = t.RevokeOperatorSessions(id); e != nil {
			return e
		}
		return t.Audit(audit("", actor.ID, "platform.operator_update", id, reasons...))
	})
}
