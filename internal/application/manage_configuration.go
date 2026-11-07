package application

import (
	"net/url"
	"strings"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/federation"
)

type clientEditor interface {
	AgentByClient(string) (domain.Agent, error)
	LockClient(string) (domain.Client, error)
	PutClient(domain.Client) error
	Audit(domain.Audit) error
}
type ClientPatch struct {
	Name          *string   `json:"name"`
	Status        *string   `json:"status"`
	ConfigVersion int64     `json:"config_version"`
	Redirects     *[]string `json:"redirect_uris"`
	Scopes        *[]string `json:"allowed_scopes"`
	Audiences     *[]string `json:"allowed_audiences"`
	Introspection *[]string `json:"allowed_introspection_audiences"`
	Grants        *[]string `json:"grant_types"`
}

func (s *Service) UpdateClient(t clientEditor, actor domain.Operator, app, clientID string, in ClientPatch) (domain.Client, error) {
	agent, err := agentClientManagement(t, actor, clientID)
	if err != nil {
		return domain.Client{}, err
	}
	c, e := t.LockClient(clientID)
	if e != nil {
		return c, e
	}
	if c.System {
		return c, domain.ErrForbidden
	}

	if in.ConfigVersion != c.Version {
		return c, domain.ErrConflict
	}
	if in.Name != nil {
		if *in.Name == "" || len(*in.Name) > 200 {
			return c, domain.ErrInvalid
		}
		c.Name = *in.Name
	}
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "disabled" {
			return c, domain.ErrInvalid
		}
		c.Status = *in.Status
	}
	if in.Redirects != nil {
		c.RedirectURIs = *in.Redirects
	}
	if in.Scopes != nil {
		c.Scopes = *in.Scopes
	}
	if in.Audiences != nil {
		c.Audiences = *in.Audiences
	}
	if in.Introspection != nil {
		c.IntrospectionAudiences = *in.Introspection
	}
	if in.Grants != nil {
		c.Grants = *in.Grants
	}
	if agent != nil && !agentClientWithinCeiling(*agent, c) {
		return c, domain.ErrForbidden
	}
	if !s.ValidateClient(c) {
		return c, domain.ErrInvalid
	}

	c.Version++
	if e = t.PutClient(c); e != nil {
		return c, e
	}
	return c, t.Audit(audit(app, actor.ID, "client.update", c.ClientID))
}

type connectionEditor interface {
	LockOwners() error
	Connections() ([]domain.Connection, error)
	LockConnection(string) (domain.Connection, error)
	PutConnection(domain.Connection) error
	Audit(domain.Audit) error
}

type ConnectionCreate struct {
	Name, Issuer      string
	ProviderType      string                        `json:"provider_type"`
	ClientID          string                        `json:"client_id"`
	ClientSecret      *string                       `json:"client_secret"`
	AlipayCredentials *federation.AlipayCredentials `json:"alipay_credentials"`
}

func (s *Service) CreateConnection(t connectionEditor, actor, app string, in ConnectionCreate) (c domain.Connection, err error) {
	if in.ProviderType == "" {
		in.ProviderType = "oidc"
	}
	fixed, supported := federation.FixedIssuer(in.ProviderType)
	if !supported {
		return c, domain.ErrInvalid
	}
	if fixed != "" {
		if in.Issuer != "" && in.Issuer != fixed {
			return c, domain.ErrInvalid
		}
		in.Issuer = fixed
	}
	secret := ""
	if in.ClientSecret != nil {
		secret = *in.ClientSecret
	}
	if in.ProviderType == "alipay" {
		if in.AlipayCredentials == nil || in.ClientSecret != nil || !federation.ValidAlipayAppID(in.ClientID) {
			return c, domain.ErrInvalid
		}
		secret, err = federation.EncodeAlipayCredentials(*in.AlipayCredentials)
		if err != nil {
			return c, domain.ErrInvalid
		}
	} else if in.AlipayCredentials != nil || secret == "" || len(secret) > 4096 {
		return c, domain.ErrInvalid
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || in.ClientID == "" || len(in.ClientID) > 512 || !validConnectionIssuer(in.Issuer, s.Insecure) {
		return c, domain.ErrInvalid
	}

	if e := t.LockOwners(); e != nil {
		return c, e
	}
	connections, e := t.Connections()
	if e != nil {
		return c, e
	}
	if len(connections) >= 100 {
		return c, domain.ErrConflict
	}
	c = domain.Connection{ID: ID(), Name: strings.TrimSpace(in.Name), Issuer: in.Issuer, ClientID: in.ClientID, Secret: secret, Status: "active", ProviderType: in.ProviderType, Version: 1, CreatedAt: s.Now().UTC()}
	if e = t.PutConnection(c); e != nil {
		return c, e
	}
	return c, t.Audit(audit(app, actor, "connection.create", c.ID))
}

type ConnectionPatch struct {
	Name, Status      *string
	ConfigVersion     int64                         `json:"config_version"`
	Secret            *string                       `json:"client_secret"`
	Reason            string                        `json:"reason"`
	AlipayCredentials *federation.AlipayCredentials `json:"alipay_credentials"`
}

func (s *Service) UpdateConnection(t connectionEditor, actor, app, connectionID string, in ConnectionPatch) (c domain.Connection, e error) {
	if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 {
		return c, domain.ErrInvalid
	}
	c, e = t.LockConnection(connectionID)
	if e != nil {
		return c, e
	}
	if c.Version != in.ConfigVersion {
		return c, domain.ErrConflict
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 200 {
			return c, domain.ErrInvalid
		}
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "disabled" {
			return c, domain.ErrInvalid
		}
		c.Status = *in.Status
	}
	if in.Secret != nil {
		if c.ProviderType == "alipay" || *in.Secret == "" || len(*in.Secret) > 4096 {
			return c, domain.ErrInvalid
		}
		c.Secret = *in.Secret
	}
	if in.AlipayCredentials != nil {
		if c.ProviderType != "alipay" {
			return c, domain.ErrInvalid
		}
		c.Secret, e = federation.EncodeAlipayCredentials(*in.AlipayCredentials)
		if e != nil {
			return c, domain.ErrInvalid
		}
	}
	if in.Name == nil && in.Status == nil && in.Secret == nil && in.AlipayCredentials == nil {
		return c, domain.ErrInvalid
	}
	c.Version++
	if e = t.PutConnection(c); e != nil {
		return c, e
	}
	return c, t.Audit(audit(app, actor, "connection.update", c.ID, in.Reason))
}
func validConnectionIssuer(raw string, insecure bool) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.Fragment == "" && u.RawQuery == "" && (u.Scheme == "https" || (insecure && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")))
}
