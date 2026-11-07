package application

import (
	"context"
	"slices"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

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
