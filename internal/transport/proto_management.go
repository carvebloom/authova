package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	v1 "github.com/carvebloom/authova/api/authova/v1"
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protojsoncompat"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type managementService struct{ server *Server }

var _ v1.ManagementHTTPServer = (*managementService)(nil)

type managementCall struct {
	service *managementService
	ctx     context.Context
	request *http.Request
	actor   domain.Operator
	app     domain.App
	scope   domain.Scope
	write   bool
}

// Admission keeps the existing order: current operator, App boundary, App
// existence, body, then the current role inside the transaction. A malformed
// command cannot disclose whether an unauthenticated caller guessed an App.
func (m *managementService) app(ctx context.Context, id string, write bool, deferBody ...bool) (*managementCall, error) {
	ctx, r, actor, err := (&inventoryService{server: m.server}).authorize(ctx, write)
	if err != nil {
		return nil, err
	}
	if (actor.AppID != "" && actor.AppID != id) || len(id) != 36 {
		return nil, domain.ErrNotFound
	}
	var app domain.App
	err = m.server.Service.Store.Platform(ctx, false, func(tx ports.PlatformTx) error {
		var err error
		app, err = tx.App(id, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	if meta := securityMeta(r); meta != nil {
		meta.AppID = id
	}
	if len(deferBody) == 0 {
		if err, ok := r.Context().Value(bodyDecodeError{}).(error); ok {
			return nil, err
		}
	}
	scope, err := domain.AppScope(id)
	return &managementCall{m, ctx, r, actor, app, scope, write}, err
}

func commandDTO(body proto.Message, out any) error {
	if body == nil || !body.ProtoReflect().IsValid() {
		return domain.ErrInvalid
	}
	data, err := protojsoncompat.Marshal(body)
	if err != nil || json.Unmarshal(data, out) != nil {
		return domain.ErrInvalid
	}
	return nil
}
func managementOutput[T proto.Message](out T, value any, err error) (T, error) {
	if err == nil {
		err = protojsoncompat.FromDTO(value, out)
	}
	return out, err
}
func appOutput[T proto.Message](call *managementCall, out T, private bool, command func(ports.AppTx, domain.Operator) (any, error)) (T, error) {
	var result any
	err := call.service.server.Service.AppManage(call.ctx, bearer(call.request), call.scope, call.write, private, func(tx ports.AppTx, actor domain.Operator) error {
		var err error
		result, err = command(tx, actor)
		return err
	})
	// Serialization and HTTP success occur only after AppManage confirms commit.
	return managementOutput(out, result, err)
}
func (m *managementService) rate(ctx context.Context, key string, maximum int, window time.Duration) error {
	r, rok := khttp.RequestFromServerContext(ctx)
	w, wok := khttp.ResponseWriterFromServerContext(ctx)
	if !rok || !wok {
		return domain.ErrUnavailable
	}
	if !m.server.limit(w, r, key, maximum, window) {
		return errResponseWritten
	}
	return nil
}
func validReason(reason string) bool { return strings.TrimSpace(reason) != "" && len(reason) <= 500 }

func (m *managementService) GetManagementPrincipal(ctx context.Context, _ *v1.GetManagementPrincipalRequest) (*v1.CurrentOperator, error) {
	_, _, actor, err := (&inventoryService{server: m.server}).authorize(ctx, false)
	return managementOutput(new(v1.CurrentOperator), map[string]any{"operator": actor}, err)
}
func (m *managementService) CreateManagementSession(ctx context.Context, in *v1.CreateManagementSessionRequest) (*v1.ManagementLoginResponse, error) {
	r, ok := khttp.RequestFromServerContext(ctx)
	if !ok {
		return nil, domain.ErrUnavailable
	}
	if meta := securityMeta(r); meta != nil {
		meta.Action = "management.request"
		meta.Success = true
	}
	if err := m.rate(ctx, "platform-login:"+remoteIP(r), 10, time.Minute); err != nil {
		return nil, err
	}
	if err, ok := r.Context().Value(bodyDecodeError{}).(error); ok {
		return nil, err
	}
	b := in.GetBody()
	token, actor, expires, err := m.server.Service.LoginOperator(ctx, b.GetEmail(), b.GetPassword(), b.GetTotp())
	return managementOutput(new(v1.ManagementLoginResponse), map[string]any{"token": token, "operator": actor, "expires_at": expires}, err)
}
func (m *managementService) RevokeManagementSession(ctx context.Context, _ *v1.RevokeManagementSessionRequest) (*emptypb.Empty, error) {
	r, ok := khttp.RequestFromServerContext(ctx)
	if !ok {
		return nil, domain.ErrUnavailable
	}
	if meta := securityMeta(r); meta != nil {
		meta.Action = "management.request"
		meta.Success = true
	}
	return new(emptypb.Empty), m.server.Service.LogoutOperator(ctx, bearer(r))
}
func (m *managementService) platform(ctx context.Context, write bool) (context.Context, *http.Request, error) {
	ctx, r, actor, err := (&inventoryService{server: m.server}).authorize(ctx, write)
	if err != nil {
		return ctx, r, err
	}
	if actor.AppID != "" {
		return ctx, r, domain.ErrForbidden
	}
	if err, ok := r.Context().Value(bodyDecodeError{}).(error); ok {
		return ctx, r, err
	}
	return ctx, r, nil
}
func (m *managementService) ListPlatformOperators(ctx context.Context, in *v1.ListPlatformOperatorsRequest) (*v1.OperatorPage, error) {
	ctx, _, err := m.platform(ctx, false)
	if err != nil {
		return nil, err
	}
	var items []domain.Operator
	err = m.server.Service.Store.Platform(ctx, false, func(tx ports.PlatformTx) error {
		var err error
		items, err = tx.OperatorPage(in.GetCursor(), int(in.GetLimit())+1)
		return err
	})
	return managementOutput(new(v1.OperatorPage), page(items, int(in.GetLimit()), func(o domain.Operator) string { return o.ID }), err)
}
func (m *managementService) CreatePlatformOperator(ctx context.Context, in *v1.CreatePlatformOperatorRequest) (*v1.OperatorCreated, error) {
	ctx, r, err := m.platform(ctx, true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	actor, secret, err := m.server.Service.CreateOperator(ctx, bearer(r), b.GetEmail(), b.GetPassword(), b.GetRole())
	return managementOutput(new(v1.OperatorCreated), map[string]any{"operator": actor, "totp_secret": secret}, err)
}
func (m *managementService) UpdatePlatformOperator(ctx context.Context, in *v1.UpdatePlatformOperatorRequest) (*v1.OperatorUpdated, error) {
	ctx, r, err := m.platform(ctx, true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	if !validReason(b.GetReason()) {
		return nil, domain.ErrInvalid
	}
	err = m.server.Service.SetOperator(ctx, bearer(r), in.GetOperatorId(), b.GetRole(), b.GetStatus(), b.GetReason())
	return managementOutput(new(v1.OperatorUpdated), map[string]bool{"updated": true}, err)
}
func (m *managementService) GetApp(ctx context.Context, in *v1.GetAppRequest) (*v1.App, error) {
	c, err := m.app(ctx, in.GetAppId(), false)
	if err != nil {
		return nil, err
	}
	return managementOutput(new(v1.App), c.app, nil)
}
func (m *managementService) UpdateApp(ctx context.Context, in *v1.UpdateAppRequest) (*v1.App, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	app := c.app
	update := func() error {
		if b.GetConfigVersion() != app.Version {
			return domain.ErrConflict
		}
		if b.Slug != nil {
			return domain.ErrInvalid
		}
		if b.Name != nil {
			if b.GetName() == "" || len(b.GetName()) > 200 {
				return domain.ErrInvalid
			}
			app.Name = b.GetName()
		}
		if b.RequireMfa != nil {
			app.RequireMFA = b.GetRequireMfa()
		}
		app.Version++
		return nil
	}
	if c.actor.AppID != "" {
		return appOutput(c, new(v1.App), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
			if actor.Role != "owner" {
				return nil, domain.ErrForbidden
			}
			var err error
			app, err = tx.App()
			if err != nil {
				return nil, err
			}
			if err = update(); err != nil {
				return nil, err
			}
			if err = tx.UpdateAppConfig(app); err != nil {
				return nil, err
			}
			return app, tx.Audit(event(app.ID, actor.ID, "app.update", app.ID))
		})
	}
	err = m.server.Service.Store.Platform(c.ctx, true, func(tx ports.PlatformTx) error {
		actor, err := tx.Operator(c.actor.ID, true)
		if err != nil {
			return err
		}
		if actor.Role == "auditor" || actor.Status != "active" {
			return domain.ErrForbidden
		}
		app, err = tx.App(app.ID, true)
		if err != nil {
			return err
		}
		if err = update(); err != nil {
			return err
		}
		if err = tx.PutApp(app); err != nil {
			return err
		}
		return tx.Audit(domain.Audit{ID: application.ID(), ActorID: actor.ID, Action: "app.update", SubjectID: app.ID, CreatedAt: time.Now().UTC()})
	})
	return managementOutput(new(v1.App), app, err)
}
func (m *managementService) appStatus(ctx context.Context, id, reason, status string) (*v1.App, error) {
	c, err := m.app(ctx, id, true, true)
	if err != nil {
		return nil, err
	}
	if c.actor.AppID != "" {
		return nil, domain.ErrForbidden
	}
	if err, ok := c.request.Context().Value(bodyDecodeError{}).(error); ok {
		return nil, err
	}
	if !validReason(reason) {
		return nil, domain.ErrInvalid
	}
	app := c.app
	action := "resume"
	if status == "suspended" {
		action = "suspend"
	}
	err = m.server.Service.Store.Platform(c.ctx, true, func(tx ports.PlatformTx) error {
		actor, err := tx.Operator(c.actor.ID, true)
		if err != nil {
			return err
		}
		if actor.Role == "auditor" || actor.Status != "active" {
			return domain.ErrForbidden
		}
		app, err = tx.App(id, true)
		if err != nil {
			return err
		}
		if app.Status == domain.Quarantined {
			return domain.ErrConflict
		}
		app.Status = status
		app.Version++
		if err = tx.PutApp(app); err != nil {
			return err
		}
		return tx.Audit(domain.Audit{ID: application.ID(), ActorID: actor.ID, Action: "app." + action, Reason: reason, SubjectID: id, CreatedAt: time.Now().UTC()})
	})
	return managementOutput(new(v1.App), app, err)
}
func (m *managementService) SuspendApp(ctx context.Context, in *v1.SuspendAppRequest) (*v1.App, error) {
	return m.appStatus(ctx, in.GetAppId(), in.GetBody().GetReason(), "suspended")
}
func (m *managementService) ResumeApp(ctx context.Context, in *v1.ResumeAppRequest) (*v1.App, error) {
	return m.appStatus(ctx, in.GetAppId(), in.GetBody().GetReason(), "active")
}
func (m *managementService) SetAppAdministrator(ctx context.Context, in *v1.SetAppAdministratorRequest) (*v1.AdministratorAssigned, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	b := in.GetBody()
	if !validReason(b.GetReason()) {
		return nil, domain.ErrInvalid
	}
	return appOutput(c, new(v1.AdministratorAssigned), true, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		return map[string]string{"user_id": in.GetUserId(), "role": b.GetRole()}, m.server.Service.GrantAdmin(tx, actor, c.app.ID, in.GetUserId(), b.GetRole(), b.GetReason())
	})
}
func (m *managementService) ProvisionSystemManagementClient(ctx context.Context, in *v1.ProvisionSystemManagementClientRequest) (*v1.ManagementClientProvisioned, error) {
	c, err := m.app(ctx, in.GetAppId(), true)
	if err != nil {
		return nil, err
	}
	clientID := "avm_" + c.app.Key
	return appOutput(c, new(v1.ManagementClientProvisioned), false, func(tx ports.AppTx, actor domain.Operator) (any, error) {
		if actor.Role != "owner" {
			return nil, domain.ErrForbidden
		}
		client, err := tx.Client(clientID)
		out := map[string]string{"client_id": clientID}
		if err == nil && client.System {
			return out, nil
		}
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		if err = tx.PutClient(m.server.Service.ManagementClient(c.app)); err != nil {
			return nil, err
		}
		return out, tx.Audit(event(c.app.ID, actor.ID, "client.management_provision", c.app.ID))
	})
}
