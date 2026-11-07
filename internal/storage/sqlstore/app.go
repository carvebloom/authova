package sqlstore

import (
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/carvebloom/authova/internal/ent"
	"github.com/carvebloom/authova/internal/ent/accesstoken"
	"github.com/carvebloom/authova/internal/ent/admingrant"
	"github.com/carvebloom/authova/internal/ent/app"
	"github.com/carvebloom/authova/internal/ent/auditevent"
	"github.com/carvebloom/authova/internal/ent/authorizationcode"
	"github.com/carvebloom/authova/internal/ent/challenge"
	"github.com/carvebloom/authova/internal/ent/connection"
	"github.com/carvebloom/authova/internal/ent/externalidentity"
	"github.com/carvebloom/authova/internal/ent/flow"
	"github.com/carvebloom/authova/internal/ent/idempotencyreceipt"
	"github.com/carvebloom/authova/internal/ent/identifier"
	"github.com/carvebloom/authova/internal/ent/oauthclient"
	"github.com/carvebloom/authova/internal/ent/oauthconsent"
	"github.com/carvebloom/authova/internal/ent/refreshfamily"
	"github.com/carvebloom/authova/internal/ent/refreshtoken"
	"github.com/carvebloom/authova/internal/ent/session"
	"github.com/carvebloom/authova/internal/ent/signingkey"
	"github.com/carvebloom/authova/internal/ent/user"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

func (t *tx) App() (domain.App, error) { return (&platformTx{t}).App(t.app, false) }
func (t *tx) UpdateAppConfig(a domain.App) error {
	if !t.write || a.ID != t.app {
		return domain.ErrForbidden
	}
	policy, e := json.Marshal(a.AuthenticationPolicy())
	if e != nil {
		return e
	}
	resources, e := json.Marshal(a.ResourceLimits())
	if e != nil {
		return e
	}
	n, e := t.entities().App.Update().Where(app.IDEQ(t.app), app.ConfigVersionEQ(a.Version-1)).
		SetName(a.Name).SetRequireMfa(int16(boolInt(a.RequireMFA))).SetConfigVersion(a.Version).
		SetAuthenticationPolicy(string(policy)).SetKeyEpoch(a.KeyEpoch).SetResourceLimits(string(resources)).SetStatus(a.Status).Save(t.ctx)
	if e != nil {
		return classify(e)
	}

	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}
func (t *tx) Client(id string) (domain.Client, error) {
	var c domain.Client
	row, e := t.entities().OAuthClient.Query().Where(oauthclient.AppIDEQ(t.app), oauthclient.ClientIDEQ(id)).Only(t.ctx)
	if e != nil {
		return c, classify(e)
	}
	e = t.open("client", row.Payload, &c)
	return c, e
}
func (t *tx) LockClient(id string) (domain.Client, error) {
	var out domain.Client
	if !t.write {
		return out, domain.ErrForbidden
	}
	row, e := t.entities().OAuthClient.Query().Where(oauthclient.AppIDEQ(t.app), oauthclient.ClientIDEQ(id)).ForUpdate().Only(t.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = t.open("client", row.Payload, &out)
	return out, e
}

func (t *tx) Clients(after string, n int) ([]domain.Client, error) {
	rows, e := t.entities().OAuthClient.Query().Where(oauthclient.AppIDEQ(t.app), oauthclient.IDGT(after)).Order(ent.Asc(oauthclient.FieldID)).Limit(n).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.Client, 0, len(rows))
	for _, row := range rows {
		var c domain.Client
		if e = t.open("client", row.Payload, &c); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}
func (t *tx) PutClient(c domain.Client) error {
	if c.AppID != t.app {
		return domain.ErrForbidden
	}
	b, e := t.seal("client", c)
	if e != nil {
		return e
	}
	q := t.entities().OAuthClient.Create().SetAppID(t.app).SetID(c.ID).SetClientID(c.ClientID).SetStatus(c.Status).SetPayload(b).SetKind(c.Kind).SetIntrospectionCount(len(c.IntrospectionAudiences)).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"status", "payload", "kind", "introspection_count"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().OAuthClient.Query().Where(oauthclient.AppIDEQ(t.app), oauthclient.IDEQ(c.ID)).Exist(t.ctx)
	})
}
func (t *tx) User(id string, lock bool) (domain.User, error) {
	var u domain.User
	q := t.entities().User.Query().Where(user.AppIDEQ(t.app), user.IDEQ(id))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return u, classify(e)
	}
	if e = t.open("user", row.Payload, &u); e != nil {
		return u, e
	}
	u.Status, u.Epoch = row.Status, row.SessionEpoch
	return u, nil
}
func (t *tx) FindUser(kind, digest string) (domain.User, error) {
	row, e := t.entities().Identifier.Query().Where(identifier.AppIDEQ(t.app), identifier.KindEQ(kind), identifier.IDEQ(digest)).Only(t.ctx)
	if e != nil {
		return domain.User{}, classify(e)
	}
	return t.User(row.UserID, false)
}

func (t *tx) Users(after string, n int) ([]domain.User, error) {
	rows, e := t.entities().User.Query().Where(user.AppIDEQ(t.app), user.IDGT(after)).Order(ent.Asc(user.FieldID)).Limit(n).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.User, 0, len(rows))
	for _, row := range rows {
		var u domain.User
		if e = t.open("user", row.Payload, &u); e != nil {
			return nil, e
		}
		u.Status, u.Epoch = row.Status, row.SessionEpoch
		out = append(out, u)
	}
	return out, nil
}
func (t *tx) CreateUser(u domain.User, kind, digest string) error {
	if u.AppID != t.app {
		return domain.ErrForbidden
	}
	b, e := t.seal("user", u)
	if e != nil {
		return e
	}
	if !t.write {
		return domain.ErrForbidden
	}
	if _, e = t.entities().User.Create().SetID(u.ID).SetAppID(t.app).SetStatus(u.Status).SetSessionEpoch(u.Epoch).SetCreatedAt(u.CreatedAt.Unix()).SetPayload(b).Save(t.ctx); e != nil {
		return classify(e)
	}

	if kind != "" {
		_, e = t.entities().Identifier.Create().SetAppID(t.app).SetKind(kind).SetID(digest).SetUserID(u.ID).Save(t.ctx)
		return classify(e)
	}
	return nil
}
func (t *tx) PutUser(u domain.User) error {
	if u.AppID != t.app {
		return domain.ErrForbidden
	}
	b, e := t.seal("user", u)
	if e != nil {
		return e
	}
	if !t.write {
		return domain.ErrForbidden
	}
	_, e = t.entities().User.Update().Where(user.AppIDEQ(t.app), user.IDEQ(u.ID)).SetStatus(u.Status).SetSessionEpoch(u.Epoch).SetPayload(b).Save(t.ctx)
	return classify(e)
}
func (t *tx) ReplaceIdentifier(user, kind, old, new string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	if _, e := t.entities().Identifier.Delete().Where(identifier.AppIDEQ(t.app), identifier.KindEQ(kind), identifier.IDEQ(old), identifier.UserIDEQ(user)).Exec(t.ctx); e != nil {
		return classify(e)
	}
	_, e := t.entities().Identifier.Create().SetAppID(t.app).SetKind(kind).SetID(new).SetUserID(user).Save(t.ctx)
	return classify(e)
}

func (t *tx) EraseIdentifiers(user string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	if _, e := t.entities().Identifier.Delete().Where(identifier.AppIDEQ(t.app), identifier.UserIDEQ(user)).Exec(t.ctx); e != nil {
		return classify(e)
	}
	_, e := t.entities().ExternalIdentity.Delete().Where(externalidentity.AppIDEQ(t.app), externalidentity.UserIDEQ(user)).Exec(t.ctx)
	return classify(e)
}

func (t *tx) UnbindExternal(user, connection string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	_, e := t.entities().ExternalIdentity.Delete().Where(externalidentity.AppIDEQ(t.app), externalidentity.UserIDEQ(user), externalidentity.ConnectionIDEQ(connection)).Exec(t.ctx)
	return classify(e)
}

func (t *tx) ExternalBindings(user string) ([]domain.ExternalBinding, error) {
	out := []domain.ExternalBinding{}
	e := t.entities().ExternalIdentity.Query().Where(externalidentity.AppIDEQ(t.app), externalidentity.UserIDEQ(user)).Order(ent.Asc(externalidentity.FieldConnectionID)).GroupBy(externalidentity.FieldConnectionID).Aggregate(ent.As(ent.Count(), "identity_count")).Scan(t.ctx, &out)
	return out, classify(e)
}

func (t *tx) AdminRole(id string) (string, error) {
	row, e := t.entities().AdminGrant.Query().Where(admingrant.AppIDEQ(t.app), admingrant.IDEQ(id), func(s *entsql.Selector) { s.Where(t.activeAdminUser(s)) }).Only(t.ctx)
	if e != nil {
		return "", classify(e)
	}
	return row.Role, nil
}
func (t *tx) activeAdminUser(s *entsql.Selector) *entsql.Predicate {
	u := entsql.Table(user.Table)
	sub := entsql.Select(u.C(user.FieldID)).From(u).Where(entsql.And(entsql.EQ(u.C(user.FieldAppID), t.app), entsql.EQ(u.C(user.FieldStatus), "active")))
	return entsql.In(s.C(admingrant.FieldID), sub)
}

func (t *tx) SetAdminRole(id, role string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	if role == "" {
		_, e := t.entities().AdminGrant.Delete().Where(admingrant.AppIDEQ(t.app), admingrant.IDEQ(id)).Exec(t.ctx)
		return classify(e)
	}
	q := t.entities().AdminGrant.Create().SetAppID(t.app).SetID(id).SetRole(role).OnConflict(t.conflictColumns([]string{"app_id", "user_id"}, []string{"role"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().AdminGrant.Query().Where(admingrant.AppIDEQ(t.app), admingrant.IDEQ(id)).Exist(t.ctx)
	})
}

func (t *tx) CountOwners() (int, error) {
	n, e := t.entities().AdminGrant.Query().Where(admingrant.AppIDEQ(t.app), admingrant.RoleEQ("owner"), func(s *entsql.Selector) { s.Where(t.activeAdminUser(s)) }).Count(t.ctx)
	return n, classify(e)
}

func (t *tx) LockOwners() error {
	var id string
	e := t.one("SELECT id FROM apps WHERE id=? FOR UPDATE", t.app).Scan(&id)
	return classify(e)
}
func (t *tx) Session(id string, lock bool) (domain.Session, error) {
	var out domain.Session
	q := t.entities().Session.Query().Where(session.AppIDEQ(t.app), session.IDEQ(id))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = t.open("session", row.Payload, &out)
	out.Status = row.Status
	return out, e
}
func (t *tx) SessionByHandle(digest string) (domain.Session, error) {
	row, e := t.entities().Session.Query().Where(session.AppIDEQ(t.app), session.HandleDigestEQ(digest)).Only(t.ctx)
	if e != nil {
		return domain.Session{}, classify(e)
	}
	return t.Session(row.ID, false)
}
func (t *tx) Sessions(userID, after string, limit int) ([]domain.Session, error) {
	if limit < 1 || limit > 101 {
		return nil, domain.ErrInvalid
	}
	rows, e := t.entities().Session.Query().Where(session.AppIDEQ(t.app), session.UserIDEQ(userID), session.IDGT(after)).Order(ent.Asc(session.FieldID)).Limit(limit).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.Session, 0, len(rows))
	for _, row := range rows {
		var v domain.Session
		if e = t.open("session", row.Payload, &v); e != nil {
			return nil, e
		}
		v.Status = row.Status
		out = append(out, v)
	}
	return out, nil
}
func (t *tx) PutSession(s domain.Session) error {
	b, e := t.seal("session", s)
	if e != nil {
		return e
	}
	q := t.entities().Session.Create().SetAppID(t.app).SetID(s.ID).SetUserID(s.UserID).SetHandleDigest(s.HandleDigest).SetStatus(s.Status).SetSessionEpoch(s.Epoch).SetExpiresAt(s.ExpiresAt.Unix()).SetPayload(b).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"status", "session_epoch", "expires_at", "payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().Session.Query().Where(session.AppIDEQ(t.app), session.IDEQ(s.ID)).Exist(t.ctx)
	})
}
func (t *tx) RevokeSessions(userID string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	_, e := t.entities().Session.Update().Where(session.AppIDEQ(t.app), session.UserIDEQ(userID)).SetStatus("revoked").Save(t.ctx)
	return classify(e)
}
func (t *tx) Flow(id string, lock bool) (domain.Flow, error) {
	var out domain.Flow
	q := t.entities().Flow.Query().Where(flow.AppIDEQ(t.app), flow.IDEQ(id))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = t.open("flow", row.Payload, &out)
	return out, e
}
func (t *tx) PutFlow(f domain.Flow) error {
	b, e := t.seal("flow", f)
	if e != nil {
		return e
	}
	q := t.entities().Flow.Create().SetAppID(t.app).SetID(f.ID).SetClientID(f.ClientID).SetExpiresAt(f.Expires).SetPayload(b).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"payload", "expires_at"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().Flow.Query().Where(flow.AppIDEQ(t.app), flow.IDEQ(f.ID)).Exist(t.ctx)
	})
}
func (t *tx) Consent(user, client string, scopes []string) error {
	payload, e := t.seal("consent", consentV1{user, client, scopes, time.Now().Unix()})
	if e != nil {
		return e
	}
	q := t.entities().OAuthConsent.Create().SetAppID(t.app).SetID(user).SetClientID(client).SetPayload(payload).OnConflict(t.conflictColumns([]string{"app_id", "user_id", "client_id"}, []string{"payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().OAuthConsent.Query().Where(oauthconsent.AppIDEQ(t.app), oauthconsent.IDEQ(user), oauthconsent.ClientIDEQ(client)).Exist(t.ctx)
	})
}

func (t *tx) Code(digest string, lock bool) (domain.Code, error) {
	c := domain.Code{Digest: digest}
	q := t.entities().AuthorizationCode.Query().Where(authorizationcode.AppIDEQ(t.app), authorizationcode.IDEQ(digest))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return c, classify(e)
	}
	c.FlowID, c.Expires, c.Used = row.FlowID, row.ExpiresAt, row.Used == 1
	return c, nil
}

func (t *tx) PutCode(c domain.Code) error {
	q := t.entities().AuthorizationCode.Create().SetAppID(t.app).SetID(c.Digest).SetFlowID(c.FlowID).SetExpiresAt(c.Expires).SetUsed(int16(boolInt(c.Used))).OnConflict(t.conflictColumns([]string{"app_id", "digest"}, []string{"used"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().AuthorizationCode.Query().Where(authorizationcode.AppIDEQ(t.app), authorizationcode.IDEQ(c.Digest)).Exist(t.ctx)
	})
}
func (t *tx) Token(id string) (domain.Token, error) {
	var out domain.Token
	row, e := t.entities().AccessToken.Query().Where(accesstoken.AppIDEQ(t.app), accesstoken.IDEQ(id)).Only(t.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = t.open("token", row.Payload, &out)
	out.Revoked = row.Revoked == 1
	return out, e
}

func (t *tx) PutToken(tok domain.Token) error {
	b, e := t.seal("token", tok)
	if e != nil {
		return e
	}
	q := t.entities().AccessToken.Create().SetAppID(t.app).SetID(tok.ID).SetClientID(tok.ClientID).SetNillableUserID(optionalString(tok.UserID)).SetNillableSessionID(optionalString(tok.SessionID)).SetNillableFamilyID(optionalString(tok.FamilyID)).SetExpiresAt(tok.Expires).SetRevoked(int16(boolInt(tok.Revoked))).SetPayload(b).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"revoked", "payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().AccessToken.Query().Where(accesstoken.AppIDEQ(t.app), accesstoken.IDEQ(tok.ID)).Exist(t.ctx)
	})
}

// ActiveToken performs one fresh primary snapshot read, including all revocation facts.
func (t *tx) ActiveToken(id string) (domain.Token, error) {
	var tok domain.Token
	var b, sessionData []byte
	var requiresMFA int
	e := t.one("SELECT x.payload,a.require_mfa,CASE WHEN a.require_mfa=1 AND x.user_id IS NOT NULL THEN s.payload ELSE NULL END FROM access_tokens x JOIN apps a ON a.id=x.app_id JOIN clients c ON c.app_id=x.app_id AND c.client_id=x.client_id LEFT JOIN users u ON u.app_id=x.app_id AND u.id=x.user_id LEFT JOIN sessions s ON s.app_id=x.app_id AND s.id=x.session_id LEFT JOIN refresh_families f ON f.app_id=x.app_id AND f.id=x.family_id "+agentJoins+" WHERE x.app_id=? AND x.id=? AND x.revoked=0 AND x.expires_at>? AND a.status='active' AND c.status='active' AND (x.user_id IS NULL OR (u.status='active' AND s.status='active' AND s.expires_at>? AND s.session_epoch=u.session_epoch AND s.user_id=u.id)) AND (x.family_id IS NULL OR (f.revoked=0 AND f.expires_at>? AND f.client_id=x.client_id AND f.session_id=x.session_id)) AND "+agentLiveness, t.app, id, time.Now().Unix(), time.Now().Unix(), time.Now().Unix()).Scan(&b, &requiresMFA, &sessionData)
	if e != nil {
		return tok, classify(e)
	}
	e = t.open("token", b, &tok)
	tok.RequiresMFA = requiresMFA == 1
	if e != nil {
		return tok, e
	}
	ok, e := t.tokenMFA(tok.RequiresMFA, tok.UserID, tok.SessionID, sessionData)
	if e != nil {
		return tok, e
	}
	if !ok {
		return tok, domain.ErrUnauthenticated
	}
	return tok, nil
}
func (t *tx) Refresh(digest string, lock bool) (domain.Refresh, error) {
	var out domain.Refresh
	q := t.entities().RefreshToken.Query().Where(refreshtoken.AppIDEQ(t.app), refreshtoken.IDEQ(digest))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = t.open("refresh", row.Payload, &out)
	out.Used = row.Used == 1
	return out, e
}

func (t *tx) PutRefresh(r domain.Refresh) error {
	b, e := t.seal("refresh", r)
	if e != nil {
		return e
	}
	q := t.entities().RefreshToken.Create().SetAppID(t.app).SetID(r.Digest).SetFamilyID(r.FamilyID).SetGeneration(r.Generation).SetUsed(int16(boolInt(r.Used))).SetExpiresAt(r.Expires).SetPayload(b).OnConflict(t.conflictColumns([]string{"app_id", "digest"}, []string{"used", "payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().RefreshToken.Query().Where(refreshtoken.AppIDEQ(t.app), refreshtoken.IDEQ(r.Digest)).Exist(t.ctx)
	})
}
func (t *tx) CreateFamily(id, client, session string, expires int64) error {
	if !t.write {
		return domain.ErrForbidden
	}
	_, e := t.entities().RefreshFamily.Create().SetID(id).SetAppID(t.app).SetClientID(client).SetSessionID(session).SetExpiresAt(expires).SetRevoked(0).Save(t.ctx)
	return classify(e)
}

func (t *tx) LockFamily(id string) (bool, error) {
	q := t.entities().RefreshFamily.Query().Where(refreshfamily.AppIDEQ(t.app), refreshfamily.IDEQ(id))
	if t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return false, classify(e)
	}
	return row.Revoked == 1 || row.ExpiresAt <= time.Now().Unix(), nil
}

func (t *tx) RevokeFamily(id string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	_, e := t.entities().RefreshFamily.Update().Where(refreshfamily.AppIDEQ(t.app), refreshfamily.IDEQ(id)).SetRevoked(1).Save(t.ctx)
	return classify(e)
}

func (t *tx) Challenge(id string, lock bool) (domain.Challenge, error) {
	var out domain.Challenge
	q := t.entities().Challenge.Query().Where(challenge.AppIDEQ(t.app), challenge.IDEQ(id))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = t.open("challenge", row.Payload, &out)
	return out, e
}

func (t *tx) PutChallenge(c domain.Challenge) error {
	b, e := t.seal("challenge", c)
	if e != nil {
		return e
	}
	q := t.entities().Challenge.Create().SetAppID(t.app).SetID(c.ID).SetFlowID(c.FlowID).SetExpiresAt(c.Expires).SetPayload(b).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().Challenge.Query().Where(challenge.AppIDEQ(t.app), challenge.IDEQ(c.ID)).Exist(t.ctx)
	})
}
func (t *tx) Connections() ([]domain.Connection, error) {
	return t.ConnectionPage("", 100)
}
func (t *tx) ConnectionPage(after string, limit int) ([]domain.Connection, error) {
	if limit < 1 || limit > 101 {
		return nil, domain.ErrInvalid
	}
	rows, e := t.entities().Connection.Query().Where(connection.AppIDEQ(t.app), connection.IDGT(after)).Order(ent.Asc(connection.FieldID)).Limit(limit).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.Connection, 0, len(rows))
	for _, row := range rows {
		var c domain.Connection
		if e = t.open("connection", row.Payload, &c); e != nil {
			return nil, e
		}
		normalizeConnection(&c)
		out = append(out, c)
	}
	return out, nil
}

func (t *tx) Connection(id string) (domain.Connection, error) { return t.readConnection(id, "") }

func (t *tx) LockConnection(id string) (domain.Connection, error) {
	return t.readConnection(id, "update")
}

func (t *tx) ShareConnection(id string) (domain.Connection, error) {
	return t.readConnection(id, "share")
}
func (t *tx) readConnection(id, lock string) (domain.Connection, error) {
	var out domain.Connection
	if lock != "" && !t.write {
		return out, domain.ErrForbidden
	}
	q := t.entities().Connection.Query().Where(connection.AppIDEQ(t.app), connection.IDEQ(id))
	if lock == "update" {
		q.ForUpdate()
	}
	if lock == "share" {
		q.ForShare()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return out, classify(e)
	}
	e = t.open("connection", row.Payload, &out)
	normalizeConnection(&out)
	return out, e
}

func normalizeConnection(c *domain.Connection) {
	if c.ProviderType == "" {
		c.ProviderType = "oidc"
	}
	if c.Version == 0 {
		c.Version = 1
	}
}
func (t *tx) PutConnection(c domain.Connection) error {
	b, e := t.seal("connection", c)
	if e != nil {
		return e
	}
	q := t.entities().Connection.Create().SetAppID(t.app).SetID(c.ID).SetPayload(b).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().Connection.Query().Where(connection.AppIDEQ(t.app), connection.IDEQ(c.ID)).Exist(t.ctx)
	})
}
func (t *tx) ExternalUser(connection, subject string) (string, error) {
	row, e := t.entities().ExternalIdentity.Query().Where(externalidentity.AppIDEQ(t.app), externalidentity.ConnectionIDEQ(connection), externalidentity.IDEQ(subject)).Only(t.ctx)
	if e != nil {
		return "", classify(e)
	}
	return row.UserID, nil
}

func (t *tx) BindExternal(connection, subject, user string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	_, e := t.entities().ExternalIdentity.Create().SetAppID(t.app).SetConnectionID(connection).SetID(subject).SetUserID(user).Save(t.ctx)
	return classify(e)
}

func (t *tx) Keys() ([]domain.SigningKey, error) { return t.signingKeys(false) }

// VerificationKeys excludes expired and revoked history before decryption.
func (t *tx) VerificationKeys() ([]domain.SigningKey, error) { return t.signingKeys(true) }

func (t *tx) signingKeys(verification bool) ([]domain.SigningKey, error) {
	q := t.entities().SigningKey.Query().Where(signingkey.AppIDEQ(t.app)).Order(ent.Desc(signingkey.FieldID))
	if verification {
		q.Where(signingkey.RevokedAtEQ(0), signingkey.Or(signingkey.CurrentKeyEQ(1), signingkey.RetireAtGT(time.Now().Unix())))
	}
	rows, e := q.All(t.ctx)
	return t.signingKeyRows(rows, e)
}

func (t *tx) RecoveryKeys(after string, limit int) ([]domain.SigningKey, error) {
	if limit < 1 || limit > 1000 {
		return nil, domain.ErrInvalid
	}
	rows, e := t.entities().SigningKey.Query().Where(signingkey.AppIDEQ(t.app), signingkey.IDGT(after)).Order(ent.Asc(signingkey.FieldID)).Limit(limit).All(t.ctx)
	return t.signingKeyRows(rows, e)
}

func (t *tx) signingKeyRows(rows []*ent.SigningKey, e error) ([]domain.SigningKey, error) {
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.SigningKey, 0, len(rows))
	for _, row := range rows {
		var k domain.SigningKey
		if e = t.open("key", row.Payload, &k); e != nil {
			return nil, e
		}
		k.Current, k.Retire, k.RevokedAt = row.CurrentKey == 1, row.RetireAt, row.RevokedAt
		out = append(out, k)
	}
	return out, nil
}

func (t *tx) PutKey(k domain.SigningKey) error {
	if k.AppID != t.app {
		return domain.ErrForbidden
	}
	b, e := t.seal("key", k)
	if e != nil {
		return e
	}
	q := t.entities().SigningKey.Create().SetAppID(t.app).SetID(k.ID).SetCurrentKey(int16(boolInt(k.Current))).SetRetireAt(k.Retire).SetRevokedAt(k.RevokedAt).SetPayload(b).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"current_key", "retire_at", "revoked_at", "payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().SigningKey.Query().Where(signingkey.AppIDEQ(t.app), signingkey.IDEQ(k.ID)).Exist(t.ctx)
	})
}
func (t *tx) Audit(a domain.Audit) error {
	if !t.write || a.AppID != t.app {
		return domain.ErrForbidden
	}
	data, e := t.seal("audit", a)
	if e != nil {
		return e
	}
	_, e = t.entities().AuditEvent.Create().SetAppID(t.app).SetID(a.ID).SetCreatedAt(a.CreatedAt.Unix()).SetPayload(data).Save(t.ctx)
	return classify(e)
}

func (t *tx) Audits(after string, n int) ([]domain.Audit, error) {
	rows, e := t.entities().AuditEvent.Query().Where(auditevent.AppIDEQ(t.app), auditevent.IDGT(after)).Order(ent.Asc(auditevent.FieldID)).Limit(n).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.Audit, 0, len(rows))
	for _, row := range rows {
		var a domain.Audit
		if e = t.open("audit", row.Payload, &a); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}

func (t *tx) Idempotency(key, fingerprint string) ([]byte, error) {
	q := t.entities().IdempotencyReceipt.Query().Where(idempotencyreceipt.AppIDEQ(t.app), idempotencyreceipt.IDEQ(key))
	if t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	if row.Fingerprint != fingerprint || row.ExpiresAt <= time.Now().Unix() {
		return nil, domain.ErrConflict
	}
	return t.store.vault.Open(t.app+":idempotency", row.Payload)
}

func (t *tx) PutIdempotency(key, fingerprint string, payload []byte, expires int64) error {
	if !t.write {
		return domain.ErrForbidden
	}
	data, e := t.store.vault.Seal(t.app+":idempotency", payload)
	if e != nil {
		return e
	}
	_, e = t.entities().IdempotencyReceipt.Create().SetAppID(t.app).SetID(key).SetFingerprint(fingerprint).SetExpiresAt(expires).SetPayload(data).Save(t.ctx)
	return classify(e)
}
