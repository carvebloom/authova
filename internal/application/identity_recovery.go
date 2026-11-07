package application

import (
	"context"
	"errors"
	"strconv"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

const recoveryPage = 100

type IdentityQuarantineReport struct {
	Apps               int                   `json:"apps_completed"`
	Operators          int                   `json:"operators_quarantined"`
	Users              int                   `json:"users_quarantined"`
	Clients            int                   `json:"clients_disabled"`
	Keys               int                   `json:"signing_keys_revoked"`
	Sessions           int                   `json:"sessions_revoked"`
	Families           int                   `json:"refresh_families_revoked"`
	Flows              int                   `json:"flows_expired"`
	ManagementSessions int                   `json:"management_sessions_revoked"`
	Agents             AgentQuarantineReport `json:"agents"`
}

func recoveryMarker(a domain.App) string {
	return "recovery-complete:" + strconv.FormatInt(a.KeyEpoch, 10)
}
func recoveryComplete(t ports.AppTx, a domain.App) (bool, error) {
	data, err := t.Idempotency(recoveryMarker(a), "local-recovery")
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(data) == "complete", nil
}

// QuarantineRecoveredIdentities is a local, offline maintenance operation. Stop
// every writer and isolate ingress first. It never trusts restored passwords,
// MFA, external bindings, roles or client secrets as current authority. Completed
// pages remain fenced after interruption; rerun before approving any identity.
func (s *Service) QuarantineRecoveredIdentities(ctx context.Context, reason string) (report IdentityQuarantineReport, err error) {
	if !ValidReason(reason) {
		return report, domain.ErrInvalid
	}
	for after := ""; ; {
		var next string
		count, changed := 0, 0
		err = s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
			operators, e := t.OperatorPage(after, recoveryPage)
			if e != nil {
				return e
			}
			count = len(operators)
			for _, item := range operators {
				o, e := t.Operator(item.ID, true)
				if e != nil {
					return e
				}
				if o.Status != domain.Quarantined {
					o.Status = domain.Quarantined
					o.Password = ""
					o.TOTP = ""
					o.TOTPLast = 0
					if e = t.PutOperator(o); e != nil {
						return e
					}
					if e = t.Audit(audit("", "local-recovery", "platform.operator_quarantine", o.ID, reason)); e != nil {
						return e
					}
					changed++
				}
				next = o.ID
			}
			return nil
		})
		if err != nil {
			return report, err
		}
		report.Operators += changed
		if count == 0 {
			break
		}
		after = next
	}
	for after := ""; ; {
		var batch domain.RecoveryBatch
		err = s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
			var e error
			batch, e = t.RecoveryManagementSessions(after, recoveryPage)
			return e
		})
		if err != nil {
			return report, err
		}
		report.ManagementSessions += batch.Changed
		if batch.Scanned == 0 {
			break
		}
		after = batch.After
	}
	for after := ""; ; {
		var apps []domain.App
		err = s.Store.Platform(ctx, false, func(t ports.PlatformTx) error { var e error; apps, e = t.Apps(after, recoveryPage); return e })
		if err != nil {
			return report, err
		}
		if len(apps) == 0 {
			return report, nil
		}
		for _, app := range apps {
			scope, _ := domain.AppScope(app.ID)
			err = s.quarantineRecoveredApp(ctx, scope, reason, &report)
			if err != nil {
				return report, err
			}
			report.Apps++
			after = app.ID
		}
	}
}

func (s *Service) quarantineRecoveredApp(ctx context.Context, scope domain.Scope, reason string, report *IdentityQuarantineReport) error {
	complete := false
	err := s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		if e := t.LockOwners(); e != nil {
			return e
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		if a.Status == domain.Quarantined {
			complete, e = recoveryComplete(t, a)
			return e
		}
		a.Status = domain.Quarantined
		a.Version++
		a.KeyEpoch++
		if e = t.UpdateAppConfig(a); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), "local-recovery", "app.recovery_fence", a.ID, reason))
	})
	if err != nil || complete {
		return err
	}
	for after := ""; ; {
		var next string
		count, changed := 0, 0
		err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
			users, e := t.Users(after, recoveryPage)
			if e != nil {
				return e
			}
			count = len(users)
			for _, item := range users {
				u, e := t.User(item.ID, true)
				if e != nil {
					return e
				}
				if u.Status != "deleted" && u.Status != domain.Quarantined {
					u.Status = domain.Quarantined
					u.Epoch++
					if e = t.PutUser(u); e != nil {
						return e
					}
					if e = t.SetAdminRole(u.ID, ""); e != nil {
						return e
					}
					if e = t.Audit(audit(scope.ID(), "local-recovery", "user.recovery_fence", u.ID, reason)); e != nil {
						return e
					}
					changed++
				}
				next = u.ID
			}
			return nil
		})
		if err != nil {
			return err
		}
		report.Users += changed
		if count == 0 {
			break
		}
		after = next
	}
	for after := ""; ; {
		var next string
		count, changed := 0, 0
		err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
			clients, e := t.Clients(after, recoveryPage)
			if e != nil {
				return e
			}
			count = len(clients)
			for _, item := range clients {
				c, e := t.LockClient(item.ClientID)
				if e != nil {
					return e
				}
				// Fixed management clients have no secret or client-credentials grant;
				// their human subjects and roles are separately fenced above.
				if !c.System && (c.Status != "disabled" || c.SecretDigest != "" || c.PreviousSecretDigest != "" || c.PreviousSecretExpires != 0) {
					c.Status = "disabled"
					c.SecretDigest = ""
					c.PreviousSecretDigest = ""
					c.PreviousSecretExpires = 0
					c.Version++
					if e = t.PutClient(c); e != nil {
						return e
					}
					if e = t.Audit(audit(scope.ID(), "local-recovery", "client.recovery_fence", c.ClientID, reason)); e != nil {
						return e
					}
					changed++
				}
				next = c.ID
			}
			return nil
		})
		if err != nil {
			return err
		}
		report.Clients += changed
		if count == 0 {
			break
		}
		after = next
	}
	for _, kind := range []domain.RecoveryFact{domain.RecoverySessions, domain.RecoveryFamilies, domain.RecoveryFlows} {
		for after := ""; ; {
			var batch domain.RecoveryBatch
			err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
				var e error
				batch, e = t.RecoveryBatch(kind, after, recoveryPage)
				return e
			})
			if err != nil {
				return err
			}
			switch kind {
			case domain.RecoverySessions:
				report.Sessions += batch.Changed
			case domain.RecoveryFamilies:
				report.Families += batch.Changed
			case domain.RecoveryFlows:
				report.Flows += batch.Changed
			}
			if batch.Scanned == 0 {
				break
			}
			after = batch.After
		}
	}
	for after := ""; ; {
		var next string
		count, changed := 0, 0
		err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
			keys, e := t.RecoveryKeys(after, recoveryPage)
			if e != nil {
				return e
			}
			count = len(keys)
			for _, key := range keys {
				if key.RevokedAt == 0 {
					key.RevokedAt = s.Now().Unix()
					key.Retire = key.RevokedAt
					key.Current = false
					if e = t.PutKey(key); e != nil {
						return e
					}
					changed++
				}
				next = key.ID
			}
			return nil
		})
		if err != nil {
			return err
		}
		report.Keys += changed
		if count == 0 {
			break
		}
		after = next
	}
	agents, err := s.QuarantineAppAgents(ctx, scope, reason)
	report.Agents.AgentsDisabled += agents.AgentsDisabled
	report.Agents.InstallationsClosed += agents.InstallationsClosed
	report.Agents.GrantsRevoked += agents.GrantsRevoked
	report.Agents.CredentialsCleared += agents.CredentialsCleared
	report.Agents.AppsScanned++
	if err != nil {
		return err
	}
	key, err := s.NewSigningKey(scope.ID())
	if err != nil {
		return err
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		if e := t.LockOwners(); e != nil {
			return e
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		if a.Status != domain.Quarantined {
			return domain.ErrConflict
		}
		if e = t.PutKey(key); e != nil {
			return e
		}
		a.KeyEpoch++
		a.Version++
		if e = t.UpdateAppConfig(a); e != nil {
			return e
		}
		if e = t.PutIdempotency(recoveryMarker(a), "local-recovery", []byte("complete"), s.Now().AddDate(100, 0, 0).Unix()); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), "local-recovery", "app.recovery_quarantined", a.ID, reason))
	})
}

// RecoverQuarantinedUser requires an out-of-band identity/contact check by the
// deployment owner. It destroys all restored login bindings before installing
// new credentials. Other users remain quarantined, including formerly disabled
// users whose post-backup ban status cannot be established from the old dump.
func (s *Service) RecoverQuarantinedUser(ctx context.Context, scope domain.Scope, id, kind, value, password, role, reason string) error {
	if !ValidReason(reason) || (role != "" && role != "owner" && role != "admin" && role != "auditor") {
		return domain.ErrInvalid
	}
	value, err := secure.Normalize(kind, value)
	if err != nil {
		return domain.ErrInvalid
	}
	hash, err := s.HashPassword(ctx, password)
	if err != nil {
		return err
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		if e := t.LockOwners(); e != nil {
			return e
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		complete, e := recoveryComplete(t, a)
		if e != nil {
			return e
		}
		if a.Status != domain.Quarantined || !complete {
			return domain.ErrConflict
		}
		u, e := t.User(id, true)
		if e != nil {
			return e
		}
		if u.Status != domain.Quarantined {
			return domain.ErrConflict
		}
		if e = t.EraseIdentifiers(u.ID); e != nil {
			return e
		}
		if e = t.EraseUserPasskeys(u.ID); e != nil {
			return e
		}
		if e = t.RevokeSessions(u.ID); e != nil {
			return e
		}
		u.Email = ""
		u.Phone = ""
		if kind == "email" {
			u.Email = value
		} else {
			u.Phone = value
		}
		u.PasswordHash = hash
		u.TOTPSecret = ""
		u.TOTPLast = 0
		u.RecoveryDigests = nil
		u.Epoch++
		u.Status = "active"
		if e = t.PutUser(u); e != nil {
			return e
		}
		if e = t.ReplaceIdentifier(u.ID, kind, s.Digest(scope.ID(), "identifier:"+kind, ""), s.Digest(scope.ID(), "identifier:"+kind, value)); e != nil {
			return e
		}
		if e = t.SetAdminRole(u.ID, role); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), "local-recovery", "user.recovery_approved", u.ID, reason))
	})
}

// ReleaseRecoveredApp only changes the recovery fence into a suspended App.
// The platform operator must separately review configuration and activate it.
func (s *Service) ReleaseRecoveredApp(ctx context.Context, scope domain.Scope, reason string) error {
	if !ValidReason(reason) {
		return domain.ErrInvalid
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		if e := t.LockOwners(); e != nil {
			return e
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		complete, e := recoveryComplete(t, a)
		if e != nil {
			return e
		}
		if a.Status != domain.Quarantined || !complete {
			return domain.ErrConflict
		}
		a.Status = "suspended"
		a.Version++
		if e = t.UpdateAppConfig(a); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), "local-recovery", "app.recovery_released", a.ID, reason))
	})
}
