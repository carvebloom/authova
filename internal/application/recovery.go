package application

import (
	"context"
	"slices"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// RecoverOperator is a local break-glass operation, never an unauthenticated HTTP API.
// Possession of the deployment's database credential and master key is required.
type OperatorRecoveryApproval struct {
	Role   string
	Reason string
}

func (s *Service) RecoverOperator(ctx context.Context, email, password, secret string) error {
	return s.recoverOperator(ctx, email, password, secret, nil)
}

// RecoverApprovedOperator requires explicit approval of identity AND authority.
// A restored operator role is not used as proof of its current authorization.
func (s *Service) RecoverApprovedOperator(ctx context.Context, email, password, secret string, approval OperatorRecoveryApproval) error {
	if !ValidReason(approval.Reason) || !slices.Contains([]string{"owner", "admin", "auditor"}, approval.Role) {
		return domain.ErrInvalid
	}
	return s.recoverOperator(ctx, email, password, secret, &approval)
}

func (s *Service) recoverOperator(ctx context.Context, email, password, secret string, approval *OperatorRecoveryApproval) error {
	email, e := secure.Normalize("email", email)
	if e != nil || !secure.ValidTOTPSecret(secret) {
		return domain.ErrInvalid
	}
	hash, e := s.HashPassword(ctx, password)
	if e != nil {
		return e
	}
	return s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		operator, e := t.OperatorByEmail(email)
		if e != nil {
			return e
		}
		if operator.Status == domain.Quarantined {
			if approval == nil {
				return domain.ErrConflict
			}
			operator.Status = "active"
		}
		if approval != nil {
			operator.Role = approval.Role
		}
		operator.Password = hash
		operator.TOTP = secret
		operator.TOTPLast = 0
		if e = t.PutOperator(operator); e != nil {
			return e
		}
		if e = t.RevokeOperatorSessions(operator.ID); e != nil {
			return e
		}
		reason := "local credential recovery"
		if approval != nil {
			reason = approval.Reason
		}
		return t.Audit(audit("", "local-recovery", "platform.operator_recovered", operator.ID, reason))
	})
}
