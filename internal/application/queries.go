package application

import (
	"slices"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

type QueryRequest struct {
	Kind, ID, UserID, Cursor, AgentID, InstallationID, TenantID string
	Limit                                                       int
}

type QueryUser struct {
	ID               string    `json:"id"`
	AppID            string    `json:"app_id"`
	DisplayName      string    `json:"display_name"`
	Status           string    `json:"status"`
	Email            string    `json:"email,omitempty"`
	Phone            string    `json:"phone,omitempty"`
	ContactsRedacted bool      `json:"contacts_redacted"`
	CreatedAt        time.Time `json:"created_at"`
}

type QueryAudit struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	ActorID   string    `json:"actor_id"`
	Action    string    `json:"action"`
	SubjectID string    `json:"subject_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ReadQuery exposes reviewed projections, never a raw repository or SQL escape
// hatch. Call only inside Provider.ReadQuery's authenticated read transaction.
func (s *Service) ReadQuery(t ports.AppTx, p domain.QueryPrincipal, q QueryRequest) (any, error) {
	required, tenantAllowed := domain.QueryCapability(q.Kind)
	if p.AppID == "" || required == "" || !slices.Contains(p.Scopes, required) || (p.TenantID != "" && !tenantAllowed) {
		return nil, domain.ErrForbidden
	}
	if q.Limit < 1 || q.Limit > 100 {
		return nil, domain.ErrInvalid
	}
	for _, value := range []string{q.ID, q.UserID, q.Cursor, q.AgentID, q.InstallationID} {
		if len(value) > 128 || strings.ContainsAny(value, "/\\\r\n\x00") {
			return nil, domain.ErrInvalid
		}
	}
	if !validAgentIdentifier(q.TenantID) {
		return nil, domain.ErrInvalid
	}
	if p.TenantID != "" {
		if q.TenantID != "" && q.TenantID != p.TenantID {
			return nil, domain.ErrForbidden
		}
		q.TenantID = p.TenantID
	}
	a, err := t.App()
	if err != nil {
		return nil, err
	}
	if a.ID != p.AppID || a.Status != "active" {
		return nil, domain.ErrForbidden
	}
	filter := domain.AgentFilter{After: q.Cursor, Limit: q.Limit + 1, AgentID: q.AgentID, InstallationID: q.InstallationID, UserID: q.UserID, TenantID: q.TenantID}
	switch q.Kind {
	case "context":
		return p, nil
	case "configuration":
		connections, err := t.ConnectionPage(q.Cursor, q.Limit+1)
		if err != nil {
			return nil, err
		}
		type connection struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			ProviderType string `json:"provider_type"`
			Status       string `json:"status"`
			Version      int64  `json:"config_version"`
		}
		items := make([]connection, 0, len(connections))
		for _, c := range connections {
			items = append(items, connection{c.ID, c.Name, c.ProviderType, c.Status, c.Version})
		}
		page := queryPage(items, q.Limit, func(c connection) string { return c.ID })
		out := map[string]any{"app": a, "policy": a.AuthenticationPolicy(), "resource_limits": a.ResourceLimits(), "passkey_available": s.PasskeyAvailable(a), "connections": page.Items}
		if page.NextCursor != nil {
			out["connections_next_cursor"] = *page.NextCursor
		}
		return out, nil
	case "users":
		users, err := t.Users(q.Cursor, q.Limit+1)
		if err != nil {
			return nil, err
		}
		items := make([]QueryUser, 0, len(users))
		for _, u := range users {
			items = append(items, queryUser(u, p))
		}
		return queryPage(items, q.Limit, func(u QueryUser) string { return u.ID }), nil
	case "user":
		u, err := t.User(q.ID, false)
		if err != nil {
			return nil, err
		}
		return queryUser(u, p), nil
	case "sessions":
		if _, err := t.User(q.UserID, false); err != nil {
			return nil, err
		}
		items, err := t.Sessions(q.UserID, q.Cursor, q.Limit+1)
		return queryPage(items, q.Limit, func(x domain.Session) string { return x.ID }), err
	case "agents":
		items, err := t.Agents(q.Cursor, q.Limit+1)
		return queryPage(items, q.Limit, func(x domain.Agent) string { return x.ID }), err
	case "agent":
		return t.Agent(q.ID, false)
	case "installations":
		items, err := t.AgentInstallations(filter)
		return queryPage(items, q.Limit, func(x domain.AgentInstallation) string { return x.ID }), err
	case "installation":
		i, err := t.AgentInstallation(q.ID, false)
		if err == nil && p.TenantID != "" && i.TenantID != p.TenantID {
			return nil, domain.ErrNotFound
		}
		return i, err
	case "grants":
		items, err := t.AgentGrants(filter)
		return queryPage(items, q.Limit, func(x domain.AgentGrant) string { return x.ID }), err
	case "grant":
		g, err := t.AgentGrant(q.ID, false)
		if err == nil && p.TenantID != "" && g.TenantID != p.TenantID {
			return nil, domain.ErrNotFound
		}
		return g, err
	case "audit":
		audits, err := t.Audits(q.Cursor, q.Limit+1)
		if err != nil {
			return nil, err
		}
		items := make([]QueryAudit, 0, len(audits))
		for _, v := range audits {
			items = append(items, QueryAudit{v.ID, v.AppID, v.ActorID, v.Action, v.SubjectID, v.CreatedAt})
		}
		return queryPage(items, q.Limit, func(x QueryAudit) string { return x.ID }), nil
	}
	return nil, domain.ErrInvalid
}

func queryUser(u domain.User, p domain.QueryPrincipal) QueryUser {
	redacted := !slices.Contains(p.Scopes, domain.QueryScopePrefix+"contacts")
	if redacted {
		if u.Email != "" {
			u.Email = secure.Mask(u.Email)
		}
		if u.Phone != "" {
			u.Phone = secure.Mask(u.Phone)
		}
	}
	return QueryUser{u.ID, u.AppID, u.DisplayName, u.Status, u.Email, u.Phone, redacted, u.CreatedAt}
}

func queryPage[T any](items []T, limit int, id func(T) string) domain.Page[T] {
	if items == nil {
		items = []T{}
	}
	p := domain.Page[T]{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		cursor := id(p.Items[limit-1])
		p.NextCursor = &cursor
	}
	return p
}
