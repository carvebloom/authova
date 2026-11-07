package transport

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/carvebloom/authova/api/authova/v1"
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (m *managementService) CreateClient(ctx context.Context, in *v1.CreateClientRequest) (*v1.CreatedClient, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command domain.Client
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	key := in.GetIdempotencyKey()
	if len(key) < 8 || len(key) > 128 {
		return nil, domain.ErrInvalid
	}
	client, secret, err := m.server.Service.CreateClient(c.ctx, bearer(c.request), c.scope, command, key)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(client)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if secret != "" {
		out["client_secret"] = secret
	}
	return managementOutput(new(v1.CreatedClient), out, nil)
}
func (m *managementService) ListClients(ctx context.Context, in *v1.ListClientsRequest) (*v1.ClientPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	after, limit := paging(c.request)
	return appOutput(c, new(v1.ClientPage), false, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		items, err := tx.Clients(after, limit+1)
		return page(items, limit, func(a domain.Client) string { return a.ID }), err
	})
}
func (m *managementService) GetClient(ctx context.Context, in *v1.GetClientRequest) (*v1.Client, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.Client), false, func(tx ports.AppTx, _ domain.Operator) (any, error) { return tx.Client(in.GetClientId()) })
}
func (m *managementService) UpdateClient(ctx context.Context, in *v1.UpdateClientRequest) (*v1.Client, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.ClientPatch
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.Client), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if _, err := tx.Client(in.GetClientId()); err != nil {
			return nil, err
		}
		return m.server.Service.UpdateClient(tx, actor, c.scope.ID(), in.GetClientId(), command)
	})
}
func (m *managementService) ListConnections(ctx context.Context, in *v1.ListConnectionsRequest) (*v1.ConnectionPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.ConnectionPage), false, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		items, err := tx.Connections()
		return domain.Page[domain.Connection]{Items: items}, err
	})
}
func (m *managementService) GetConnection(ctx context.Context, in *v1.GetConnectionRequest) (*v1.Connection, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.Connection), false, func(tx ports.AppTx, _ domain.Operator) (any, error) { return tx.Connection(in.GetConnectionId()) })
}
func (m *managementService) CreateConnection(ctx context.Context, in *v1.CreateConnectionRequest) (*v1.Connection, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.ConnectionCreate
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.Connection), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		return m.server.Service.CreateConnection(tx, actor.ID, c.scope.ID(), command)
	})
}
func (m *managementService) UpdateConnection(ctx context.Context, in *v1.UpdateConnectionRequest) (*v1.Connection, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	var command application.ConnectionPatch
	if err = commandDTO(in.GetBody(), &command); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.Connection), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		return m.server.Service.UpdateConnection(tx, actor.ID, c.scope.ID(), in.GetConnectionId(), command)
	})
}
func (m *managementService) ListUsers(ctx context.Context, in *v1.ListUsersRequest) (*v1.UserPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	after, limit := paging(c.request)
	return appOutput(c, new(v1.UserPage), true, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		if q := in.GetQ(); q != "" {
			kind := "email"
			if strings.HasPrefix(q, "+") {
				kind = "phone"
			}
			value, err := normalizeQuery(kind, q)
			if err != nil {
				return nil, err
			}
			user, err := tx.FindUser(kind, m.server.Service.Digest(c.scope.ID(), "identifier:"+kind, value))
			if errors.Is(err, domain.ErrNotFound) {
				return domain.Page[domain.User]{Items: []domain.User{}}, nil
			}
			if err != nil {
				return nil, err
			}
			maskUser(&user)
			return domain.Page[domain.User]{Items: []domain.User{user}}, nil
		}
		items, err := tx.Users(after, limit+1)
		for i := range items {
			maskUser(&items[i])
		}
		return page(items, limit, func(u domain.User) string { return u.ID }), err
	})
}
func (m *managementService) GetUser(ctx context.Context, in *v1.GetUserRequest) (*v1.User, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.User), true, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		user, err := tx.User(in.GetUserId(), false)
		if err != nil {
			return nil, err
		}
		maskUser(&user)
		user.AdminRole, _ = tx.AdminRole(user.ID)
		return user, nil
	})
}
func (m *managementService) DisableUser(ctx context.Context, in *v1.DisableUserRequest) (*v1.UserDisabled, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	reason := in.GetBody().GetReason()
	if !validReason(reason) {
		return nil, domain.ErrInvalid
	}
	return appOutput(c, new(v1.UserDisabled), true, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		user, err := tx.User(in.GetUserId(), false)
		if err != nil {
			return nil, err
		}
		err = m.server.Service.DisableUser(tx, actor.ID, c.scope.ID(), user.ID, reason)
		return map[string]bool{"disabled": err == nil}, err
	})
}
func (m *managementService) ListUserSessions(ctx context.Context, in *v1.ListUserSessionsRequest) (*v1.SessionPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	after, limit := paging(c.request)
	return appOutput(c, new(v1.SessionPage), true, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		user, err := tx.User(in.GetUserId(), false)
		if err != nil {
			return nil, err
		}
		items, err := tx.Sessions(user.ID, after, limit+1)
		return page(items, limit, func(s domain.Session) string { return s.ID }), err
	})
}
func (m *managementService) RevokeUserSession(ctx context.Context, in *v1.RevokeUserSessionRequest) (*emptypb.Empty, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	reason := in.GetBody().GetReason()
	if !validReason(reason) {
		return nil, domain.ErrInvalid
	}
	err = m.server.Service.AppManage(c.ctx, bearer(c.request), c.scope, true, true, func(tx ports.AppTx, actor domain.Operator) error {
		user, err := tx.User(in.GetUserId(), false)
		if err != nil {
			return err
		}
		session, err := tx.Session(in.GetSessionId(), true)
		if err != nil {
			return err
		}
		if session.UserID != user.ID {
			return domain.ErrNotFound
		}
		session.Status = "revoked"
		if err = tx.PutSession(session); err != nil {
			return err
		}
		return tx.Audit(event(c.scope.ID(), actor.ID, "session.revoke", session.ID, reason))
	})
	return new(emptypb.Empty), err
}
func (m *managementService) ListAppAudit(ctx context.Context, in *v1.ListAppAuditRequest) (*v1.AuditPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	after, limit := paging(c.request)
	return appOutput(c, new(v1.AuditPage), true, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		items, err := tx.Audits(after, limit+1)
		return page(items, limit, func(a domain.Audit) string { return a.ID }), err
	})
}
