package application

import (
	"context"
	"encoding/json"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

type AgentQuarantineReport struct {
	AppsScanned         int `json:"apps_scanned"`
	AgentsDisabled      int `json:"agents_disabled"`
	InstallationsClosed int `json:"installations_disabled"`
	GrantsRevoked       int `json:"grants_revoked"`
	CredentialsCleared  int `json:"credentials_cleared"`
}

// QuarantineRecoveredAgents is a local maintenance operation requiring the DB
// credential and encryption key. Stop every writer and isolate ingress first.
// Restoring an older backup can otherwise resurrect approvals/revocations and
// rotated secrets. Each Agent is fenced atomically; a failed run must finish
// before traffic resumes. Repeating a completed run is a no-op.
//
// This does not recover an external membership directory or quarantine human
// login/operator credentials. Those require a separate deployment recovery plan.
func (s *Service) QuarantineRecoveredAgents(ctx context.Context, reason string) (AgentQuarantineReport, error) {
	var report AgentQuarantineReport
	if !ValidReason(reason) {
		return report, domain.ErrInvalid
	}
	appAfter := ""
	for {
		var apps []domain.App
		err := s.Store.Platform(ctx, false, func(tx ports.PlatformTx) error {
			var e error
			apps, e = tx.Apps(appAfter, 100)
			return e
		})
		if err != nil {
			return report, err
		}
		if len(apps) == 0 {
			return report, nil
		}
		for _, app := range apps {
			scope, err := domain.AppScope(app.ID)
			if err != nil {
				return report, err
			}
			done, err := s.QuarantineAppAgents(ctx, scope, reason)
			report.AgentsDisabled += done.AgentsDisabled
			report.InstallationsClosed += done.InstallationsClosed
			report.GrantsRevoked += done.GrantsRevoked
			report.CredentialsCleared += done.CredentialsCleared
			if err != nil {
				return report, err
			}
			report.AppsScanned++
			appAfter = app.ID
		}
	}
}

// QuarantineAppAgents is the App-bound maintenance capability used by full
// deployment recovery. It is not exposed as an unauthenticated HTTP operation.
func (s *Service) QuarantineAppAgents(ctx context.Context, scope domain.Scope, reason string) (AgentQuarantineReport, error) {
	var report AgentQuarantineReport
	if scope.ID() == "" || !ValidReason(reason) {
		return report, domain.ErrInvalid
	}
	after := ""
	for {
		var agents []domain.Agent
		err := s.Store.App(ctx, scope, false, func(tx ports.AppTx) error {
			var e error
			agents, e = tx.Agents(after, 100)
			return e
		})
		if err != nil {
			return report, err
		}
		if len(agents) == 0 {
			report.AppsScanned = 1
			return report, nil
		}
		for _, agent := range agents {
			done, err := s.quarantineAgent(ctx, scope, agent.ID, reason)
			report.AgentsDisabled += done.AgentsDisabled
			report.InstallationsClosed += done.InstallationsClosed
			report.GrantsRevoked += done.GrantsRevoked
			report.CredentialsCleared += done.CredentialsCleared
			if err != nil {
				return report, err
			}
			after = agent.ID
		}
	}
}

// Fence before draining pages. Every page is independently atomic and bounded;
// an interrupted run never rolls a successfully committed fence back to active.
func (s *Service) quarantineAgent(ctx context.Context, scope domain.Scope, id, reason string) (AgentQuarantineReport, error) {
	var report, fenced AgentQuarantineReport
	err := s.Store.App(ctx, scope, true, func(tx ports.AppTx) error {
		var e error
		fenced, e = s.fenceRecoveredAgent(tx, id, reason)
		return e
	})
	if err != nil {
		return report, err
	}
	report = fenced
	for _, grants := range []bool{false, true} {
		after := ""
		for {
			var next string
			changed, records := 0, 0
			err = s.Store.App(ctx, scope, true, func(tx ports.AppTx) error {
				a, e := tx.SharedAgent(id)
				if e != nil {
					return e
				}
				if a.Status != "disabled" {
					return domain.ErrConflict
				}
				filter := domain.AgentFilter{AgentID: id, After: after, Limit: 100}
				if grants {
					page, e := tx.AgentGrants(filter)
					if e != nil {
						return e
					}
					records = len(page)
					for _, g := range page {
						if g.Status == "active" {
							g.Status, g.RevokedAt = "revoked", s.Now().Unix()
							g.Version++
							if e = tx.PutAgentGrant(g); e != nil {
								return e
							}
							if e = tx.Audit(agentGrantAudit(g, "local-recovery", "agent.grant.revoke", reason)); e != nil {
								return e
							}
							changed++
						}
						next = g.ID
					}
				} else {
					page, e := tx.AgentInstallations(filter)
					if e != nil {
						return e
					}
					records = len(page)
					for _, i := range page {
						if i.Status != "disabled" {
							i.Status = "disabled"
							i.Version++
							if e = tx.PutAgentInstallation(i); e != nil {
								return e
							}
							if e = tx.Audit(audit(i.AppID, "local-recovery", "agent.installation.update", i.ID, reason)); e != nil {
								return e
							}
							changed++
						}
						next = i.ID
					}
				}
				return nil
			})
			if err != nil {
				return report, err
			}
			if grants {
				report.GrantsRevoked += changed
			} else {
				report.InstallationsClosed += changed
			}
			if records == 0 {
				break
			}
			after = next
		}
	}
	return report, nil
}

func (s *Service) fenceRecoveredAgent(tx ports.AppTx, id, reason string) (AgentQuarantineReport, error) {
	var done AgentQuarantineReport
	a, err := tx.Agent(id, true)
	if err != nil {
		return done, err
	}
	if a.Status != "disabled" {
		a.Status = "disabled"
		a.Version++
		if err = tx.PutAgent(a); err != nil {
			return done, err
		}
		done.AgentsDisabled++
	}
	c, err := tx.LockClient(a.ClientID)
	if err != nil {
		return done, err
	}
	if c.Kind == "confidential" && (c.SecretDigest != "" || c.PreviousSecretDigest != "" || c.PreviousSecretExpires != 0) {
		c.SecretDigest, c.PreviousSecretDigest, c.PreviousSecretExpires = "", "", 0
		c.Version++
		if err = tx.PutClient(c); err != nil {
			return done, err
		}
		done.CredentialsCleared++
	}
	if done.AgentsDisabled+done.CredentialsCleared > 0 {
		raw, _ := json.Marshal(struct {
			Reason string `json:"reason"`
			AgentQuarantineReport
		}{reason, done})
		if err = tx.Audit(audit(a.AppID, "local-recovery", "agent.recovery.fence", id, string(raw))); err != nil {
			return done, err
		}
	}
	return done, nil
}
