package transport

import (
	"context"
	"time"

	v1 "github.com/carvebloom/authova/api/authova/v1"
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func (m *managementService) GetAuthenticationConfig(ctx context.Context, in *v1.GetAuthenticationConfigRequest) (*v1.AuthenticationConfig, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AuthenticationConfig), false, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		app, err := tx.App()
		return m.server.authenticationConfig(app), err
	})
}
func (m *managementService) UpdateAuthenticationPolicy(ctx context.Context, in *v1.UpdateAuthenticationPolicyRequest) (*v1.AuthenticationConfig, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	if !application.ValidReason(b.GetReason()) || b.GetConfigVersion() < 1 {
		return nil, domain.ErrInvalid
	}
	var command struct {
		Policy *domain.AuthenticationPolicy `json:"policy"`
	}
	if err = commandDTO(b, &command); err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.AuthenticationConfig), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if actor.AppID != "" && actor.Role != "owner" {
			return nil, domain.ErrForbidden
		}
		app, err := tx.App()
		if err != nil {
			return nil, err
		}
		if command.Policy == nil {
			return nil, domain.ErrInvalid
		}
		if b.GetConfigVersion() != app.Version {
			return nil, domain.ErrConflict
		}
		app.Policy = command.Policy
		app.Version++
		if err = tx.UpdateAppConfig(app); err != nil {
			return nil, err
		}
		if err = tx.Audit(event(c.scope.ID(), actor.ID, "app.authentication_policy_update", app.ID, b.GetReason())); err != nil {
			return nil, err
		}
		return m.server.authenticationConfig(app), nil
	})
}
func (m *managementService) GetResourcePolicy(ctx context.Context, in *v1.GetResourcePolicyRequest) (*v1.ResourceConfig, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.ResourceConfig), false, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		app, err := tx.App()
		return m.server.resourceConfig(app), err
	})
}
func (m *managementService) UpdateResourcePolicy(ctx context.Context, in *v1.UpdateResourcePolicyRequest) (*v1.ResourceConfig, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	var command struct {
		Limits *domain.ResourceLimits `json:"limits"`
	}
	if err = commandDTO(b, &command); err != nil || command.Limits == nil || !command.Limits.Valid() || b.GetConfigVersion() < 1 || !application.ValidReason(b.GetReason()) {
		return nil, domain.ErrInvalid
	}
	out, err := appOutput(c, new(v1.ResourceConfig), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		app, err := tx.App()
		if err != nil {
			return nil, err
		}
		if actor.AppID != "" && actor.Role != "owner" {
			return nil, domain.ErrForbidden
		}
		if app.Version != b.GetConfigVersion() {
			return nil, domain.ErrConflict
		}
		app.Resources = command.Limits
		app.Version++
		if err = tx.UpdateAppConfig(app); err != nil {
			return nil, err
		}
		if err = tx.Audit(event(c.scope.ID(), actor.ID, "app.resource_policy_update", app.ID, b.GetReason())); err != nil {
			return nil, err
		}
		return m.server.resourceConfig(app), nil
	})
	if err == nil && m.server.Resources != nil {
		m.server.Resources.Invalidate(c.scope.ID())
	}
	return out, err
}
func (m *managementService) EnableUser(ctx context.Context, in *v1.EnableUserRequest) (*v1.User, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	if !application.ValidReason(b.GetReason()) || b.GetSessionEpoch() < 1 {
		return nil, domain.ErrInvalid
	}
	return appOutput(c, new(v1.User), true, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		user, err := m.server.Service.EnableUser(tx, actor.ID, c.scope.ID(), in.GetUserId(), b.GetSessionEpoch(), b.GetReason())
		maskUser(&user)
		return user, err
	})
}
func (m *managementService) RotateClientSecret(ctx context.Context, in *v1.RotateClientSecretRequest) (*v1.ClientSecretRotated, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	if !application.ValidReason(b.GetReason()) || b.GetConfigVersion() < 1 || b.GraceSeconds == nil {
		return nil, domain.ErrInvalid
	}
	return appOutput(c, new(v1.ClientSecretRotated), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		client, secret, err := m.server.Service.RotateClientSecret(tx, actor, c.scope.ID(), in.GetClientId(), b.GetConfigVersion(), b.GetGraceSeconds(), b.GetReason())
		return map[string]any{"client": client, "client_secret": secret, "previous_secret_expires_at": client.PreviousSecretExpires}, err
	})
}
func (m *managementService) RevokePreviousClientSecret(ctx context.Context, in *v1.RevokePreviousClientSecretRequest) (*v1.Client, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	if !application.ValidReason(b.GetReason()) || b.GetConfigVersion() < 1 {
		return nil, domain.ErrInvalid
	}
	return appOutput(c, new(v1.Client), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		return m.server.Service.RevokePreviousClientSecret(tx, actor, c.scope.ID(), in.GetClientId(), b.GetConfigVersion(), b.GetReason())
	})
}
func (m *managementService) ListSigningKeys(ctx context.Context, in *v1.ListSigningKeysRequest) (*v1.SigningKeyPage, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.SigningKeyPage), false, func(tx ports.AppTx, _ domain.Operator) (any, error) {
		app, err := tx.App()
		if err != nil {
			return nil, err
		}
		keys, err := tx.Keys()
		if err != nil {
			return nil, err
		}
		items := []map[string]any{}
		for _, key := range keys {
			items = append(items, map[string]any{"key_id": key.ID, "current": key.Current, "created_at": key.Created, "retire_at": key.Retire, "revoked_at": key.RevokedAt})
		}
		return map[string]any{"items": items, "config_version": app.Version}, nil
	})
}
func (m *managementService) signingReplacement(c *managementCall) (domain.SigningKey, error) {
	err := m.server.Service.AppManage(c.ctx, bearer(c.request), c.scope, true, false, func(_ ports.AppTx, actor domain.Operator) error {
		if actor.AppID != "" && actor.Role != "owner" {
			return domain.ErrForbidden
		}
		return nil
	})
	if err != nil {
		return domain.SigningKey{}, err
	}
	if err = m.rate(c.ctx, "signing-rotation:"+c.scope.ID(), 5, time.Minute); err != nil {
		return domain.SigningKey{}, err
	}
	// Expensive key generation stays outside a transaction, after current role checks.
	return m.server.Service.NewSigningKey(c.scope.ID())
}
func (m *managementService) RotateAppSigningKey(ctx context.Context, in *v1.RotateAppSigningKeyRequest) (*v1.SigningKeyRotated, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	reason := in.GetBody().GetReason()
	if !validReason(reason) {
		return nil, domain.ErrInvalid
	}
	key, err := m.signingReplacement(c)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.SigningKeyRotated), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if actor.AppID != "" && actor.Role != "owner" {
			return nil, domain.ErrForbidden
		}
		return map[string]string{"key_id": key.ID}, m.server.Service.RotateKey(tx, actor.ID, c.scope.ID(), key, reason)
	})
}
func (m *managementService) RevokeSigningKey(ctx context.Context, in *v1.RevokeSigningKeyRequest) (*v1.KeyRevoked, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	if !application.ValidReason(b.GetReason()) || b.GetConfigVersion() < 1 {
		return nil, domain.ErrInvalid
	}
	key, err := m.signingReplacement(c)
	if err != nil {
		return nil, err
	}
	return appOutput(c, new(v1.KeyRevoked), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if actor.AppID != "" && actor.Role != "owner" {
			return nil, domain.ErrForbidden
		}
		next, version, err := m.server.Service.RevokeSigningKey(tx, actor.ID, c.scope.ID(), in.GetKeyId(), b.GetConfigVersion(), key, b.GetReason())
		return map[string]any{"key_id": in.GetKeyId(), "new_key_id": next, "config_version": version}, err
	})
}
