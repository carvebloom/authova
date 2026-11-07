package transport

import (
	"context"
	"encoding/json"

	v1 "github.com/carvebloom/authova/api/authova/v1"
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func (c *managementCall) agentFilter() (domain.AgentFilter, int, error) {
	after, limit := paging(c.request)
	q := c.request.URL.Query()
	f := domain.AgentFilter{After: after, Limit: limit + 1, AgentID: q.Get("agent_id"), InstallationID: q.Get("installation_id"), UserID: q.Get("user_id"), TenantID: q.Get("tenant_id")}
	for _, value := range []string{f.AgentID, f.InstallationID, f.UserID, f.TenantID} {
		if len(value) > 128 {
			return f, limit, domain.ErrInvalid
		}
	}
	return f, limit, nil
}
func agentOwner(actor domain.Operator) error {
	if actor.Role != "owner" {
		return domain.ErrForbidden
	}
	return nil
}
func (m *managementService) CreateAgent(ctx context.Context, in *v1.CreateAgentRequest) (*v1.CreatedAgent, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.AgentCreate
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	out, err := m.server.Service.CreateAgent(c.ctx, bearer(c.request), c.scope, command, in.GetIdempotencyKey())
	return managementOutput(new(v1.CreatedAgent), out, err)
}
func (m *managementService) CreateAgentInstallation(ctx context.Context, in *v1.CreateAgentInstallationRequest) (*v1.AgentInstallation, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.AgentInstallationCreate
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	out, err := m.server.Service.CreateAgentInstallation(c.ctx, bearer(c.request), c.scope, command, in.GetIdempotencyKey())
	return managementOutput(new(v1.AgentInstallation), out, err)
}
func (m *managementService) ListAgents(ctx context.Context, in *v1.ListAgentsRequest) (*v1.AgentPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	f, limit, err := c.agentFilter()
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentPage), false, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		items, err := tx.Agents(f.After, f.Limit)
		return page(items, limit, func(a domain.Agent) string { return a.ID }), err
	})
}
func (m *managementService) GetAgent(ctx context.Context, in *v1.GetAgentRequest) (*v1.Agent, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.Agent), false, func(tx ports.AppTx, _ domain.Operator) (any, error) { return tx.Agent(in.GetAgentId(), false) })
}
func (m *managementService) UpdateAgent(ctx context.Context, in *v1.UpdateAgentRequest) (*v1.Agent, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.AgentPatch
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.Agent), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if err := agentOwner(actor); err != nil {
			return nil, err
		}
		return m.server.Service.UpdateAgent(tx, actor, in.GetAgentId(), command)
	})
}
func (m *managementService) ListAgentInstallations(ctx context.Context, in *v1.ListAgentInstallationsRequest) (*v1.AgentInstallationPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	f, limit, err := c.agentFilter()
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentInstallationPage), true, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		items, err := tx.AgentInstallations(f)
		return page(items, limit, func(a domain.AgentInstallation) string { return a.ID }), err
	})
}
func (m *managementService) GetAgentInstallation(ctx context.Context, in *v1.GetAgentInstallationRequest) (*v1.AgentInstallation, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentInstallation), true, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		return tx.AgentInstallation(in.GetInstallationId(), false)
	})
}
func (m *managementService) UpdateAgentInstallation(ctx context.Context, in *v1.UpdateAgentInstallationRequest) (*v1.AgentInstallation, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.AgentInstallationPatch
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentInstallation), true, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if err := agentOwner(actor); err != nil {
			return nil, err
		}
		return m.server.Service.UpdateAgentInstallation(tx, actor, in.GetInstallationId(), command)
	})
}
func (m *managementService) RenewAutonomousAgentGrant(ctx context.Context, in *v1.RenewAutonomousAgentGrantRequest) (*v1.AgentInstallation, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.AgentVersionedReason
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentInstallation), true, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if err := agentOwner(actor); err != nil {
			return nil, err
		}
		return m.server.Service.RenewAutonomousAgent(tx, actor, in.GetInstallationId(), command)
	})
}
func (m *managementService) ListAgentGrants(ctx context.Context, in *v1.ListAgentGrantsRequest) (*v1.AgentGrantPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	f, limit, err := c.agentFilter()
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentGrantPage), true, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		items, err := tx.AgentGrants(f)
		return page(items, limit, func(a domain.AgentGrant) string { return a.ID }), err
	})
}
func (m *managementService) GetAgentGrant(ctx context.Context, in *v1.GetAgentGrantRequest) (*v1.AgentGrant, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentGrant), true, func(tx ports.AppTx, _ domain.Operator) (any, error) { return tx.AgentGrant(in.GetGrantId(), false) })
}
func (m *managementService) RevokeAgentGrant(ctx context.Context, in *v1.RevokeAgentGrantRequest) (*v1.AgentGrant, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.AgentVersionedReason
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentGrant), true, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if err := agentOwner(actor); err != nil {
			return nil, err
		}
		return m.server.Service.RevokeAgentGrant(tx, actor.ID, in.GetGrantId(), command, "")
	})
}
func (m *managementService) GetAgentOperation(ctx context.Context, in *v1.GetAgentOperationRequest) (*v1.AgentOperation, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	if _, _, err = c.agentFilter(); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AgentOperation), true, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if !application.ValidAgentOperationKey(in.GetOperationKey()) || actor.Role != "owner" {
			return nil, domain.ErrForbidden
		}
		key := m.server.Service.Digest(c.scope.ID(), "idempotency-actor", actor.ID+":"+in.GetOperationKey())
		raw, err := tx.AgentOperation(key)
		if err != nil {
			return nil, err
		}
		var created application.CreatedAgent
		var installation domain.AgentInstallation
		if json.Unmarshal(raw, &created) == nil && created.Agent.AppID == c.scope.ID() && created.Agent.ID != "" {
			return map[string]string{"kind": "agent", "resource_id": created.Agent.ID}, nil
		}
		if json.Unmarshal(raw, &installation) == nil && installation.AppID == c.scope.ID() && installation.AgentID != "" && installation.ID != "" {
			return map[string]string{"kind": "installation", "resource_id": installation.ID}, nil
		}
		return nil, domain.ErrNotFound
	})
}
