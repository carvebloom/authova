package sqlstore

import (
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/carvebloom/authova/internal/ent"
	"github.com/carvebloom/authova/internal/ent/agent"
	"github.com/carvebloom/authova/internal/ent/agentgrant"
	"github.com/carvebloom/authova/internal/ent/agentinstallation"
	"github.com/carvebloom/authova/internal/ent/agenttoken"
	"github.com/carvebloom/authova/internal/ent/idempotencyreceipt"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

// Frozen persistence DTOs are independent of HTTP JSON names. Add a new version
// for incompatible changes. Each ciphertext is bound to App, kind and row ID.
type agentValueV1 struct {
	ID          string
	AppID       string
	ClientID    string
	Name        string
	Description string
	Kind        string
	Status      string
	Modes       []string
	Scopes      []string
	Resources   []string
	TokenTTL    int64
	GrantTTL    int64
	Version     int64
	CreatedAt   time.Time
}
type installationValueV1 struct {
	ID                string
	AppID             string
	AgentID           string
	TenantID          string
	UserID            string
	ResponsibleParty  string
	Resource          string
	Scopes            []string
	Modes             []string
	Status            string
	Version           int64
	CreatedAt         time.Time
	Expires           int64
	AutonomousGrantID string
}
type grantValueV1 struct {
	ID             string
	AppID          string
	AgentID        string
	InstallationID string
	ClientID       string
	UserID         string
	TenantID       string
	Mode           string
	Resource       string
	Scopes         []string
	Status         string
	Version        int64
	CreatedAt      time.Time
	Expires        int64
	RevokedAt      int64
	SessionID      string
}

type agentEnvelope[V any] struct {
	Version int `json:"version"`
	Value   V   `json:"value"`
}

func (t *tx) sealAgent(kind, id string, value any) ([]byte, error) {
	raw, err := json.Marshal(agentEnvelope[any]{1, value})
	if err != nil {
		return nil, err
	}
	return t.store.vault.Seal(t.app+":"+kind+":"+id, raw)
}
func openAgent[V any](t *tx, kind, id string, data []byte) (V, error) {
	var out agentEnvelope[V]
	raw, err := t.store.vault.Open(t.app+":"+kind+":"+id, data)
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Version != 1 {
		return out.Value, domain.ErrUnavailable
	}
	return out.Value, nil
}
func validateAgentEnvelope(kind, id string, raw []byte) error {
	switch kind {
	case "agent":
		var v agentEnvelope[agentValueV1]
		if json.Unmarshal(raw, &v) != nil || v.Version != 1 || v.Value.ID != id || v.Value.AppID == "" {
			return domain.ErrUnavailable
		}
	case "agent-installation":
		var v agentEnvelope[installationValueV1]
		if json.Unmarshal(raw, &v) != nil || v.Version != 1 || v.Value.ID != id || v.Value.AppID == "" {
			return domain.ErrUnavailable
		}
	case "agent-grant":
		var v agentEnvelope[grantValueV1]
		if json.Unmarshal(raw, &v) != nil || v.Version != 1 || v.Value.ID != id || v.Value.AppID == "" {
			return domain.ErrUnavailable
		}
	default:
		return domain.ErrInvalid
	}
	return nil
}
func (t *tx) Agent(id string, lock bool) (domain.Agent, error) { return t.loadAgent(id, t.lock(lock)) }

// SharedAgent permits concurrent issuers while excluding configuration writes.
func (t *tx) SharedAgent(id string) (domain.Agent, error) {
	if !t.write {
		return domain.Agent{}, domain.ErrForbidden
	}
	return t.loadAgent(id, " FOR SHARE")
}
func (t *tx) loadAgent(id, suffix string) (domain.Agent, error) {
	q := t.entities().Agent.Query().Where(agent.AppIDEQ(t.app), agent.IDEQ(id))
	switch suffix {
	case " FOR UPDATE":
		q.ForUpdate()
	case " FOR SHARE":
		q.ForShare()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return domain.Agent{}, classify(e)
	}
	v, e := openAgent[agentValueV1](t, "agent", id, row.Payload)
	out := domain.Agent(v)
	if e == nil && (out.ID != id || out.AppID != t.app) {
		e = domain.ErrUnavailable
	}

	return out, e
}

func (t *tx) AgentByClient(client string) (domain.Agent, error) {
	row, e := t.entities().Agent.Query().Where(agent.AppIDEQ(t.app), agent.ClientIDEQ(client)).Only(t.ctx)
	if e != nil {
		return domain.Agent{}, classify(e)
	}
	return t.Agent(row.ID, false)
}

func (t *tx) Agents(after string, n int) ([]domain.Agent, error) {
	rows, e := t.entities().Agent.Query().Where(agent.AppIDEQ(t.app), agent.IDGT(after)).Order(ent.Asc(agent.FieldID)).Limit(n).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.Agent, 0, len(rows))
	for _, row := range rows {
		v, e := openAgent[agentValueV1](t, "agent", row.ID, row.Payload)
		if e != nil {
			return nil, e
		}
		if v.ID != row.ID || v.AppID != t.app {
			return nil, domain.ErrUnavailable
		}
		out = append(out, domain.Agent(v))
	}
	return out, nil
}

func (t *tx) PutAgent(a domain.Agent) error {
	if !t.write || a.AppID != t.app {
		return domain.ErrForbidden
	}
	data, err := t.sealAgent("agent", a.ID, agentValueV1(a))
	if err != nil {
		return err
	}
	if a.Version == 1 {
		_, err = t.entities().Agent.Create().SetAppID(t.app).SetID(a.ID).SetClientID(a.ClientID).SetStatus(a.Status).SetConfigVersion(a.Version).SetPayload(data).Save(t.ctx)
		return classify(err)
	}
	n, err := t.entities().Agent.Update().Where(agent.AppIDEQ(t.app), agent.IDEQ(a.ID), agent.ConfigVersionEQ(a.Version-1)).SetStatus(a.Status).SetConfigVersion(a.Version).SetPayload(data).Save(t.ctx)
	return expectOne(n, err)
}

func (t *tx) AgentInstallation(id string, lock bool) (domain.AgentInstallation, error) {
	return t.loadAgentInstallation(id, t.lock(lock))
}

// SharedAgentInstallation permits concurrent issuers while excluding configuration writes.
func (t *tx) SharedAgentInstallation(id string) (domain.AgentInstallation, error) {
	if !t.write {
		return domain.AgentInstallation{}, domain.ErrForbidden
	}
	return t.loadAgentInstallation(id, " FOR SHARE")
}
func (t *tx) loadAgentInstallation(id, suffix string) (domain.AgentInstallation, error) {
	q := t.entities().AgentInstallation.Query().Where(agentinstallation.AppIDEQ(t.app), agentinstallation.IDEQ(id))
	switch suffix {
	case " FOR UPDATE":
		q.ForUpdate()
	case " FOR SHARE":
		q.ForShare()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return domain.AgentInstallation{}, classify(e)
	}
	v, e := openAgent[installationValueV1](t, "agent-installation", id, row.Payload)
	out := domain.AgentInstallation(v)
	if e == nil && (out.ID != id || out.AppID != t.app) {
		e = domain.ErrUnavailable
	}

	return out, e
}

func (t *tx) AgentInstallations(f domain.AgentFilter) ([]domain.AgentInstallation, error) {
	q := t.entities().AgentInstallation.Query().Where(agentinstallation.AppIDEQ(t.app), agentinstallation.IDGT(f.After))
	if f.AgentID != "" {
		q.Where(agentinstallation.AgentIDEQ(f.AgentID))
	}
	if f.UserID != "" {
		q.Where(agentinstallation.UserIDEQ(f.UserID))
	}
	if f.TenantID != "" {
		q.Where(agentinstallation.TenantIDEQ(f.TenantID))
	}
	rows, e := q.Order(ent.Asc(agentinstallation.FieldID)).Limit(f.Limit).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.AgentInstallation, 0, len(rows))
	for _, row := range rows {
		v, e := openAgent[installationValueV1](t, "agent-installation", row.ID, row.Payload)
		if e != nil {
			return nil, e
		}
		if v.ID != row.ID || v.AppID != t.app {
			return nil, domain.ErrUnavailable
		}
		out = append(out, domain.AgentInstallation(v))
	}
	return out, nil
}

func (t *tx) PutAgentInstallation(a domain.AgentInstallation) error {
	if !t.write || a.AppID != t.app {
		return domain.ErrForbidden
	}
	data, err := t.sealAgent("agent-installation", a.ID, installationValueV1(a))
	if err != nil {
		return err
	}
	if a.Version == 1 {
		_, err = t.entities().AgentInstallation.Create().SetAppID(t.app).SetID(a.ID).SetAgentID(a.AgentID).SetTenantID(a.TenantID).SetNillableUserID(optionalString(a.UserID)).SetExpiresAt(a.Expires).SetStatus(a.Status).SetConfigVersion(a.Version).SetPayload(data).Save(t.ctx)
		return classify(err)
	}
	n, err := t.entities().AgentInstallation.Update().Where(agentinstallation.AppIDEQ(t.app), agentinstallation.IDEQ(a.ID), agentinstallation.ConfigVersionEQ(a.Version-1)).SetStatus(a.Status).SetConfigVersion(a.Version).SetExpiresAt(a.Expires).SetPayload(data).Save(t.ctx)
	return expectOne(n, err)
}

func (t *tx) AgentGrant(id string, lock bool) (domain.AgentGrant, error) {
	return t.loadAgentGrant(id, t.lock(lock))
}

// SharedAgentGrant permits concurrent issuers while excluding configuration writes.
func (t *tx) SharedAgentGrant(id string) (domain.AgentGrant, error) {
	if !t.write {
		return domain.AgentGrant{}, domain.ErrForbidden
	}
	return t.loadAgentGrant(id, " FOR SHARE")
}
func (t *tx) loadAgentGrant(id, suffix string) (domain.AgentGrant, error) {
	q := t.entities().AgentGrant.Query().Where(agentgrant.AppIDEQ(t.app), agentgrant.IDEQ(id))
	switch suffix {
	case " FOR UPDATE":
		q.ForUpdate()
	case " FOR SHARE":
		q.ForShare()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return domain.AgentGrant{}, classify(e)
	}
	v, e := openAgent[grantValueV1](t, "agent-grant", id, row.Payload)
	out := domain.AgentGrant(v)
	if e == nil && (out.ID != id || out.AppID != t.app) {
		e = domain.ErrUnavailable
	}
	out.Status, out.Version = row.Status, row.ConfigVersion
	return out, e
}

func (t *tx) AgentGrants(f domain.AgentFilter) ([]domain.AgentGrant, error) {
	q := t.entities().AgentGrant.Query().Where(agentgrant.AppIDEQ(t.app), agentgrant.IDGT(f.After))
	if f.AgentID != "" {
		q.Where(agentgrant.AgentIDEQ(f.AgentID))
	}
	if f.UserID != "" {
		q.Where(agentgrant.UserIDEQ(f.UserID))
	}
	if f.InstallationID != "" {
		q.Where(agentgrant.InstallationIDEQ(f.InstallationID))
	}
	// Use one statement/snapshot and bind both sides of the installation relation.
	q.Where(func(s *entsql.Selector) {
		i := entsql.Table(agentinstallation.Table)
		sub := entsql.Select(i.C(agentinstallation.FieldID)).From(i).Where(entsql.EQ(i.C(agentinstallation.FieldAppID), t.app))
		if f.TenantID != "" {
			sub.Where(entsql.EQ(i.C(agentinstallation.FieldTenantID), f.TenantID))
		}
		s.Where(entsql.In(s.C(agentgrant.FieldInstallationID), sub))
	})
	rows, e := q.Order(ent.Asc(agentgrant.FieldID)).Limit(f.Limit).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.AgentGrant, 0, len(rows))
	for _, row := range rows {
		v, e := openAgent[grantValueV1](t, "agent-grant", row.ID, row.Payload)
		if e != nil {
			return nil, e
		}
		if v.ID != row.ID || v.AppID != t.app {
			return nil, domain.ErrUnavailable
		}
		a := domain.AgentGrant(v)
		a.Status, a.Version = row.Status, row.ConfigVersion
		out = append(out, a)
	}
	return out, nil
}

func (t *tx) PutAgentGrant(a domain.AgentGrant) error {
	if !t.write || a.AppID != t.app {
		return domain.ErrForbidden
	}
	data, err := t.sealAgent("agent-grant", a.ID, grantValueV1(a))
	if err != nil {
		return err
	}
	if a.Version == 1 {
		_, err = t.entities().AgentGrant.Create().SetAppID(t.app).SetID(a.ID).SetAgentID(a.AgentID).SetInstallationID(a.InstallationID).SetNillableUserID(optionalString(a.UserID)).SetExpiresAt(a.Expires).SetStatus(a.Status).SetConfigVersion(a.Version).SetPayload(data).Save(t.ctx)
		return classify(err)
	}
	n, err := t.entities().AgentGrant.Update().Where(agentgrant.AppIDEQ(t.app), agentgrant.IDEQ(a.ID), agentgrant.ConfigVersionEQ(a.Version-1)).SetStatus(a.Status).SetConfigVersion(a.Version).SetExpiresAt(a.Expires).SetPayload(data).Save(t.ctx)
	return expectOne(n, err)
}

func (t *tx) AgentToken(id string) (domain.AgentTokenBinding, error) {
	b := domain.AgentTokenBinding{TokenID: id}
	row, e := t.entities().AgentToken.Query().Where(agenttoken.AppIDEQ(t.app), agenttoken.IDEQ(id)).Only(t.ctx)
	if e != nil {
		return b, classify(e)
	}
	b.AgentID, b.InstallationID, b.GrantID, b.TenantID, b.Mode, b.UserID = row.AgentID, row.InstallationID, row.GrantID, row.TenantID, row.Mode, row.UserID
	b.AgentVersion, b.InstallationVersion, b.GrantVersion = row.AgentVersion, row.InstallationVersion, row.GrantVersion
	return b, nil
}
func (t *tx) PutAgentToken(b domain.AgentTokenBinding) error {
	if !t.write {
		return domain.ErrForbidden
	}
	_, e := t.entities().AgentToken.Create().SetID(b.TokenID).SetAppID(t.app).SetAgentID(b.AgentID).SetInstallationID(b.InstallationID).SetGrantID(b.GrantID).SetTenantID(b.TenantID).SetMode(b.Mode).SetUserID(b.UserID).SetAgentVersion(b.AgentVersion).SetInstallationVersion(b.InstallationVersion).SetGrantVersion(b.GrantVersion).Save(t.ctx)
	return classify(e)
}
func (t *tx) EraseUserAgentGrants(user string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	// Preserve immutable attribution and encrypted payload; authoritative columns
	// tombstone grants and advance the version within this transaction.
	_, e := t.entities().AgentGrant.Update().Where(agentgrant.AppIDEQ(t.app), agentgrant.UserIDEQ(user), agentgrant.StatusEQ("active")).SetStatus("revoked").AddConfigVersion(1).Save(t.ctx)
	return classify(e)
}

func (t *tx) AgentOperation(key string) ([]byte, error) {
	row, e := t.entities().IdempotencyReceipt.Query().Where(idempotencyreceipt.AppIDEQ(t.app), idempotencyreceipt.IDEQ(key)).Only(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	if row.ExpiresAt <= time.Now().Unix() {
		return nil, domain.ErrExpired
	}
	return t.store.vault.Open(t.app+":idempotency", row.Payload)
}
