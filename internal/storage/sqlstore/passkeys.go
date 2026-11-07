package sqlstore

import (
	"encoding/json"
	"github.com/carvebloom/authova/internal/ent"
	"github.com/carvebloom/authova/internal/ent/passkey"
	"github.com/carvebloom/authova/internal/ent/passkeyceremony"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

// Frozen storage DTOs do not change when application domain types evolve.
type passkeyValueV1 struct {
	ID, UserID, CredentialHash, Name string
	Record                           []byte
	Version                          int64
	CreatedAt, UpdatedAt             time.Time
}
type ceremonyValueV1 struct {
	ID, Kind, FlowID, SessionID, UserID string
	Epoch, Expires                      int64
	Used                                bool
	Record                              []byte
}
type passkeyPayloadV1 struct {
	Version int            `json:"version"`
	Value   passkeyValueV1 `json:"value"`
}
type ceremonyPayloadV1 struct {
	Version int             `json:"version"`
	Value   ceremonyValueV1 `json:"value"`
}

func passkeyValue(p domain.Passkey) passkeyValueV1 {
	return passkeyValueV1{p.ID, p.UserID, p.CredentialHash, p.Name, p.Record, p.Version, p.CreatedAt, p.UpdatedAt}
}
func (p passkeyValueV1) domain() domain.Passkey {
	return domain.Passkey{ID: p.ID, UserID: p.UserID, CredentialHash: p.CredentialHash, Name: p.Name, Record: p.Record, Version: p.Version, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}
func ceremonyValue(c domain.PasskeyCeremony) ceremonyValueV1 {
	return ceremonyValueV1{c.ID, c.Kind, c.FlowID, c.SessionID, c.UserID, c.Epoch, c.Expires, c.Used, c.Record}
}
func (c ceremonyValueV1) domain() domain.PasskeyCeremony {
	return domain.PasskeyCeremony{ID: c.ID, Kind: c.Kind, FlowID: c.FlowID, SessionID: c.SessionID, UserID: c.UserID, Epoch: c.Epoch, Expires: c.Expires, Used: c.Used, Record: c.Record}
}
func validatePasskeyEnvelope(kind, id string, raw []byte) error {
	switch kind {
	case "passkey":
		var p passkeyPayloadV1
		if json.Unmarshal(raw, &p) != nil || p.Version != 1 || p.Value.ID != id || p.Value.UserID == "" || p.Value.Version < 1 || !json.Valid(p.Value.Record) {
			return domain.ErrUnavailable
		}
	case "passkey-ceremony":
		var c ceremonyPayloadV1
		if json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Value.ID != id || c.Value.Expires <= 0 || !json.Valid(c.Value.Record) {
			return domain.ErrUnavailable
		}
	default:
		return domain.ErrInvalid
	}
	return nil
}

func (t *tx) decodePasskey(id string, data []byte) (domain.Passkey, error) {
	raw, err := t.store.vault.Open(t.app+":passkey:"+id, data)
	if err != nil {
		return domain.Passkey{}, domain.ErrUnavailable
	}
	var value passkeyPayloadV1
	if validatePasskeyEnvelope("passkey", id, raw) != nil || json.Unmarshal(raw, &value) != nil {
		return domain.Passkey{}, domain.ErrUnavailable
	}
	return value.Value.domain(), nil
}
func (t *tx) Passkeys(user string) ([]domain.Passkey, error) {
	rows, e := t.entities().Passkey.Query().Where(passkey.AppIDEQ(t.app), passkey.UserIDEQ(user)).Order(ent.Asc(passkey.FieldID)).All(t.ctx)
	if e != nil {
		return nil, classify(e)
	}
	out := make([]domain.Passkey, 0, len(rows))
	for _, row := range rows {
		p, e := t.decodePasskey(row.ID, row.Payload)
		if e != nil {
			return nil, e
		}
		if p.UserID != user {
			return nil, domain.ErrUnavailable
		}
		out = append(out, p)
	}
	return out, nil
}

func (t *tx) Passkey(id string, lock bool) (domain.Passkey, error) {
	q := t.entities().Passkey.Query().Where(passkey.AppIDEQ(t.app), passkey.IDEQ(id))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return domain.Passkey{}, classify(e)
	}
	return t.decodePasskey(id, row.Payload)
}

func (t *tx) PasskeyByCredential(hash string, lock bool) (domain.Passkey, error) {
	q := t.entities().Passkey.Query().Where(passkey.AppIDEQ(t.app), passkey.CredentialHashEQ(hash))
	if lock && t.write {
		q.ForUpdate()
	}
	row, e := q.Only(t.ctx)
	if e != nil {
		return domain.Passkey{}, classify(e)
	}
	out, e := t.decodePasskey(row.ID, row.Payload)
	if e == nil && out.CredentialHash != hash {
		e = domain.ErrUnavailable
	}
	return out, e
}

func (t *tx) PutPasskey(p domain.Passkey) error {
	if !t.write {
		return domain.ErrForbidden
	}
	raw, err := json.Marshal(passkeyPayloadV1{1, passkeyValue(p)})
	if err != nil {
		return err
	}
	data, err := t.store.vault.Seal(t.app+":passkey:"+p.ID, raw)
	if err != nil {
		return err
	}
	if p.Version == 1 {
		_, err = t.entities().Passkey.Create().SetID(p.ID).SetAppID(t.app).SetUserID(p.UserID).SetCredentialHash(p.CredentialHash).SetConfigVersion(p.Version).SetCreatedAt(p.CreatedAt.Unix()).SetUpdatedAt(p.UpdatedAt.Unix()).SetPayload(data).Save(t.ctx)
		return classify(err)
	}
	n, err := t.entities().Passkey.Update().Where(passkey.AppIDEQ(t.app), passkey.IDEQ(p.ID), passkey.UserIDEQ(p.UserID), passkey.CredentialHashEQ(p.CredentialHash), passkey.ConfigVersionEQ(p.Version-1)).SetConfigVersion(p.Version).SetUpdatedAt(p.UpdatedAt.Unix()).SetPayload(data).Save(t.ctx)
	return expectOne(n, err)
}

func (t *tx) DeletePasskey(user, id string, version int64) error {
	if !t.write {
		return domain.ErrForbidden
	}
	n, e := t.entities().Passkey.Delete().Where(passkey.AppIDEQ(t.app), passkey.UserIDEQ(user), passkey.IDEQ(id), passkey.ConfigVersionEQ(version)).Exec(t.ctx)
	return expectOne(n, e)
}

func (t *tx) PasskeyCeremony(id string, lock bool) (domain.PasskeyCeremony, error) {
	q := t.entities().PasskeyCeremony.Query().Where(passkeyceremony.AppIDEQ(t.app), passkeyceremony.IDEQ(id))
	if lock && t.write {
		q.ForUpdate()
	}
	row, err := q.Only(t.ctx)
	if err != nil {
		return domain.PasskeyCeremony{}, classify(err)
	}
	data := row.Payload
	raw, err := t.store.vault.Open(t.app+":passkey-ceremony:"+id, data)
	if err != nil {
		return domain.PasskeyCeremony{}, domain.ErrUnavailable
	}
	var value ceremonyPayloadV1
	if validatePasskeyEnvelope("passkey-ceremony", id, raw) != nil || json.Unmarshal(raw, &value) != nil {
		return domain.PasskeyCeremony{}, domain.ErrUnavailable
	}
	return value.Value.domain(), nil
}
func (t *tx) PutPasskeyCeremony(c domain.PasskeyCeremony) error {
	raw, err := json.Marshal(ceremonyPayloadV1{1, ceremonyValue(c)})
	if err != nil {
		return err
	}
	data, err := t.store.vault.Seal(t.app+":passkey-ceremony:"+c.ID, raw)
	if err != nil {
		return err
	}
	q := t.entities().PasskeyCeremony.Create().SetAppID(t.app).SetID(c.ID).SetUserID(c.UserID).SetExpiresAt(c.Expires).SetPayload(data).OnConflict(t.conflictColumns([]string{"app_id", "id"}, []string{"user_id", "expires_at", "payload"})...)
	return t.typedUpsert(func() error { return q.Exec(t.ctx) }, func() (bool, error) {
		return t.entities().PasskeyCeremony.Query().Where(passkeyceremony.AppIDEQ(t.app), passkeyceremony.IDEQ(c.ID)).Exist(t.ctx)
	})
}

func (t *tx) EraseUserPasskeys(user string) error {
	if !t.write {
		return domain.ErrForbidden
	}
	if _, e := t.entities().PasskeyCeremony.Delete().Where(passkeyceremony.AppIDEQ(t.app), passkeyceremony.UserIDEQ(user)).Exec(t.ctx); e != nil {
		return classify(e)
	}
	_, e := t.entities().Passkey.Delete().Where(passkey.AppIDEQ(t.app), passkey.UserIDEQ(user)).Exec(t.ctx)
	return classify(e)
}
