package sqlstore

import (
	"encoding/json"
	"errors"
	"github.com/carvebloom/authova/internal/ent"
	"github.com/carvebloom/authova/internal/ent/app"
	"github.com/carvebloom/authova/internal/ent/managementsession"
	"github.com/carvebloom/authova/internal/ent/operator"
	"github.com/carvebloom/authova/internal/ent/platformreceipt"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

func (p *platformTx) ProvisionApp(a domain.App, k domain.SigningKey, client domain.Client, event domain.Audit) error {
	if e := p.PutApp(a); e != nil {
		return e
	}
	if p.store.dialect == "postgres" {
		if _, e := p.sql.ExecContext(p.ctx, "SELECT set_config('authova.app_id',$1,true)", a.ID); e != nil {
			return classify(e)
		}
	}
	u := *p.tx
	u.app = a.ID
	if e := u.PutKey(k); e != nil {
		return e
	}
	if e := u.PutClient(client); e != nil {
		return e
	}

	if p.store.dialect == "postgres" {
		if _, e := p.sql.ExecContext(p.ctx, "SELECT set_config('authova.app_id','',true)"); e != nil {
			return classify(e)
		}
	}
	return p.Audit(event)
}
func (t *tx) ManagementOperator(digest string, lock bool) (domain.Operator, error) {
	u := *t
	u.app = ""
	p := &platformTx{&u}
	m, e := p.ManagementSession(digest)
	if e != nil {
		if errors.Is(e, domain.ErrNotFound) {
			return domain.Operator{}, domain.ErrUnauthenticated
		}
		return domain.Operator{}, e
	}
	if m.Revoked || m.Expires <= time.Now().Unix() || (lock && m.MFATime < time.Now().Add(-10*time.Minute).Unix()) {
		return domain.Operator{}, domain.ErrUnauthenticated
	}
	o, e := p.Operator(m.OperatorID, lock)
	if e != nil {
		return o, e
	}
	if o.Status != "active" {
		return o, domain.ErrForbidden
	}
	return o, nil
}

type platformTx struct{ *tx }

func (p *platformTx) Idempotency(operator, key, fingerprint string) ([]byte, error) {
	row, e := p.entities().PlatformReceipt.Query().Where(platformreceipt.OperatorIDEQ(operator), platformreceipt.IDEQ(key)).Only(p.ctx)
	if e != nil {
		return nil, classify(e)
	}
	if row.Fingerprint != fingerprint || row.ExpiresAt <= time.Now().Unix() {
		return nil, domain.ErrConflict
	}
	return p.store.vault.Open("platform-idempotency:"+operator, row.Payload)
}
func (p *platformTx) PutIdempotency(operator, key, fingerprint string, data []byte, expires int64) error {
	if !p.write {
		return domain.ErrForbidden
	}
	encrypted, e := p.store.vault.Seal("platform-idempotency:"+operator, data)
	if e != nil {
		return e
	}
	_, e = p.entities().PlatformReceipt.Create().SetOperatorID(operator).SetID(key).SetFingerprint(fingerprint).SetExpiresAt(expires).SetPayload(encrypted).Save(p.ctx)
	return classify(e)
}

func (p *platformTx) App(id string, lock bool) (domain.App, error) {
	q := p.entities().App.Query().Where(app.IDEQ(id))
	if lock && p.write {
		q.ForUpdate()
	}
	row, e := q.Only(p.ctx)
	if e != nil {
		return domain.App{}, classify(e)
	}
	return appDomain(row)
}
func (p *platformTx) Apps(after string, n int) ([]domain.App, error) {
	rows, e := p.entities().App.Query().Where(app.IDGT(after)).Order(ent.Asc(app.FieldID)).Limit(n).All(p.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.App, 0, len(rows))
	for _, row := range rows {
		a, e := appDomain(row)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}
func (p *platformTx) PutApp(a domain.App) error {
	policy, e := json.Marshal(a.AuthenticationPolicy())
	if e != nil {
		return e
	}
	resources, e := json.Marshal(a.ResourceLimits())
	if e != nil {
		return e
	}
	if a.KeyEpoch == 0 {
		a.KeyEpoch = 1
	}
	q := p.entities().App.Create().SetID(a.ID).SetAppKey(a.Key).SetSlug(a.Slug).SetName(a.Name).SetStatus(a.Status).SetConfigVersion(a.Version).SetCreatedAt(a.CreatedAt.Unix()).SetRequireMfa(int16(boolInt(a.RequireMFA))).SetIssuer(a.Issuer).SetAuthenticationPolicy(string(policy)).SetKeyEpoch(a.KeyEpoch).SetResourceLimits(string(resources)).OnConflict(p.conflictColumns([]string{"id"}, []string{"slug", "name", "status", "config_version", "require_mfa", "authentication_policy", "key_epoch", "resource_limits"})...)
	return p.typedUpsert(func() error { return q.Exec(p.ctx) }, func() (bool, error) { return p.entities().App.Query().Where(app.IDEQ(a.ID)).Exist(p.ctx) })
}
func (p *platformTx) Operator(id string, lock bool) (domain.Operator, error) {
	var out domain.Operator
	q := p.entities().Operator.Query().Where(operator.IDEQ(id))
	if lock && p.write {
		q.ForUpdate()
	}
	row, e := q.Only(p.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = p.open("operator", row.Payload, &out)
	return out, e
}

func (p *platformTx) OperatorByEmail(email string) (domain.Operator, error) {
	var out domain.Operator
	q := p.entities().Operator.Query().Where(operator.EmailDigestEQ(p.store.vault.Digest("operator:email", email)))
	if p.write {
		q.ForUpdate()
	}
	row, e := q.Only(p.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = p.open("operator", row.Payload, &out)
	return out, e
}

func (p *platformTx) PutOperator(o domain.Operator) error {
	b, e := p.seal("operator", o)
	if e != nil {
		return e
	}
	q := p.entities().Operator.Create().SetID(o.ID).SetEmailDigest(p.store.vault.Digest("operator:email", o.Email)).SetStatus(o.Status).SetRole(o.Role).SetPayload(b).OnConflict(p.conflictColumns([]string{"id"}, []string{"status", "role", "payload"})...)
	return p.typedUpsert(func() error { return q.Exec(p.ctx) }, func() (bool, error) { return p.entities().Operator.Query().Where(operator.IDEQ(o.ID)).Exist(p.ctx) })
}
func (p *platformTx) Operators() ([]domain.Operator, error) { return p.OperatorPage("", 1000) }
func (p *platformTx) CountOperatorOwners() (int, error) {
	n, e := p.entities().Operator.Query().Where(operator.RoleEQ("owner"), operator.StatusEQ("active")).Count(p.ctx)
	return n, classify(e)
}

func (p *platformTx) OperatorPage(after string, limit int) ([]domain.Operator, error) {
	rows, e := p.entities().Operator.Query().Where(operator.IDGT(after)).Order(ent.Asc(operator.FieldID)).Limit(limit).All(p.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.Operator, 0, len(rows))
	for _, row := range rows {
		var o domain.Operator
		if e = p.open("operator", row.Payload, &o); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, nil
}

func (p *platformTx) CountOperators() (int, error) {
	if p.write {
		var singleton int
		if e := p.one("SELECT singleton FROM authova_schema WHERE singleton=1 FOR UPDATE").Scan(&singleton); e != nil {
			return 0, classify(e)
		}
	}
	n, e := p.entities().Operator.Query().Count(p.ctx)
	return n, classify(e)
}
func (p *platformTx) ManagementSession(digest string) (domain.ManagementSession, error) {
	var out domain.ManagementSession
	row, e := p.entities().ManagementSession.Query().Where(managementsession.IDEQ(digest)).Only(p.ctx)
	if e != nil {
		return out, classify(e)
	}
	if e = p.open("management-session", row.Payload, &out); e != nil {
		return out, e
	}
	out.Revoked = row.Revoked == 1
	return out, nil
}

func (p *platformTx) RevokeOperatorSessions(id string) error {
	if !p.write {
		return domain.ErrForbidden
	}
	_, e := p.entities().ManagementSession.Update().Where(managementsession.OperatorIDEQ(id)).SetRevoked(1).Save(p.ctx)
	return classify(e)
}

func (p *platformTx) PutManagementSession(m domain.ManagementSession) error {
	b, e := p.seal("management-session", m)
	if e != nil {
		return e
	}
	q := p.entities().ManagementSession.Create().SetID(m.Digest).SetOperatorID(m.OperatorID).SetExpiresAt(m.Expires).SetRevoked(int16(boolInt(m.Revoked))).SetPayload(b).OnConflict(p.conflictColumns([]string{"digest"}, []string{"revoked", "payload"})...)
	return p.typedUpsert(func() error { return q.Exec(p.ctx) }, func() (bool, error) {
		return p.entities().ManagementSession.Query().Where(managementsession.IDEQ(m.Digest)).Exist(p.ctx)
	})
}
func (p *platformTx) Audit(a domain.Audit) error { return p.tx.Audit(a) }
