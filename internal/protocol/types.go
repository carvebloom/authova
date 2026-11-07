// Package protocol adapts the upstream OIDC engine to Authova's scoped transactions.
package protocol

import (
	"context"
	"net/url"
	"slices"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

type transactionKey struct{}
type requestContext struct {
	Tx             ports.AppTx
	Introspection  ports.IntrospectionReader
	App            domain.App
	ClientID       string
	Cookie         string
	Failure        error
	InstallationID string
	Resource       string
	AgentBinding   *domain.AgentTokenBinding
}

func WithTransaction(ctx context.Context, t ports.AppTx, app domain.App, client, cookie string) context.Context {
	return context.WithValue(ctx, transactionKey{}, &requestContext{Tx: t, App: app, ClientID: client, Cookie: cookie})
}
func req(ctx context.Context) *requestContext {
	r, ok := ctx.Value(transactionKey{}).(*requestContext)
	if !ok {
		panic("protocol request without scoped transaction")
	}
	return r
}
func TransactionFailure(ctx context.Context) error { return req(ctx).Failure }
func dependency(ctx context.Context, e error) error {
	if e != nil {
		req(ctx).Failure = e
	}
	return e
}

type Request struct {
	AgentGrantExpires                                                    int64
	AgentInstallationID, AgentGrantID                                    string
	ID, ClientID, RedirectURI, State, Nonce, UserID, SessionID, FamilyID string
	Scopes, Audiences, AMR                                               []string
	CodeChallenge                                                        string
	CodeMethod                                                           oidc.CodeChallengeMethod
	ResponseMode                                                         oidc.ResponseMode
	AuthTime, MFATime, Epoch                                             int64
	Ready                                                                bool
}

func (a *Request) GetID() string { return a.ID }
func (a *Request) GetACR() string {
	if a.MFATime > 0 {
		return "urn:authova:acr:mfa"
	}
	return "urn:authova:acr:single"
}
func (a *Request) GetAMR() []string       { return a.AMR }
func (a *Request) GetAudience() []string  { return a.Audiences }
func (a *Request) GetAuthTime() time.Time { return time.Unix(a.AuthTime, 0) }
func (a *Request) GetClientID() string    { return a.ClientID }
func (a *Request) GetCodeChallenge() *oidc.CodeChallenge {
	if a.CodeChallenge == "" {
		return nil
	}
	return &oidc.CodeChallenge{Challenge: a.CodeChallenge, Method: a.CodeMethod}
}
func (a *Request) GetNonce() string                   { return a.Nonce }
func (a *Request) GetRedirectURI() string             { return a.RedirectURI }
func (a *Request) GetResponseType() oidc.ResponseType { return oidc.ResponseTypeCode }
func (a *Request) GetResponseMode() oidc.ResponseMode { return a.ResponseMode }
func (a *Request) GetScopes() []string                { return a.Scopes }
func (a *Request) GetState() string                   { return a.State }
func (a *Request) GetSubject() string                 { return a.UserID }
func (a *Request) Done() bool                         { return a.Ready }

type RefreshRequest struct{ Value Request }

func (r *RefreshRequest) GetAMR() []string                 { return r.Value.AMR }
func (r *RefreshRequest) GetAudience() []string            { return r.Value.Audiences }
func (r *RefreshRequest) GetAuthTime() time.Time           { return time.Unix(r.Value.AuthTime, 0) }
func (r *RefreshRequest) GetClientID() string              { return r.Value.ClientID }
func (r *RefreshRequest) GetScopes() []string              { return r.Value.Scopes }
func (r *RefreshRequest) GetSubject() string               { return r.Value.UserID }
func (r *RefreshRequest) SetCurrentScopes(scopes []string) { r.Value.Scopes = scopes }

type machineRequest struct {
	client            string
	scopes, audiences []string
}

func (r *machineRequest) GetSubject() string    { return r.client }
func (r *machineRequest) GetScopes() []string   { return r.scopes }
func (r *machineRequest) GetAudience() []string { return r.audiences }

type client struct {
	domain.Client
	app      domain.App
	insecure bool
}

func (c *client) GetID() string                    { return c.ClientID }
func (c *client) RedirectURIs() []string           { return c.Client.RedirectURIs }
func (c *client) PostLogoutRedirectURIs() []string { return c.Client.RedirectURIs }
func (c *client) ApplicationType() op.ApplicationType {
	if c.Kind == "confidential" {
		return op.ApplicationTypeWeb
	}
	return op.ApplicationTypeUserAgent
}
func (c *client) AuthMethod() oidc.AuthMethod {
	if c.Kind == "confidential" {
		return oidc.AuthMethodBasic
	}
	return oidc.AuthMethodNone
}
func (c *client) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}
func (c *client) GrantTypes() []oidc.GrantType {
	r := []oidc.GrantType{}
	for _, g := range c.Grants {
		r = append(r, oidc.GrantType(g))
	}
	return r
}
func (c *client) LoginURL(id string) string           { return c.app.Issuer + "/ui/flows/" + url.PathEscape(id) }
func (c *client) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeJWT }
func (c *client) IDTokenLifetime() time.Duration      { return 10 * time.Minute }
func (c *client) DevMode() bool                       { return c.insecure }
func (c *client) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}
func (c *client) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}
func (c *client) IsScopeAllowed(s string) bool         { return slices.Contains(c.Scopes, s) }
func (c *client) IDTokenUserinfoClaimsAssertion() bool { return false }
func (c *client) ClockSkew() time.Duration             { return 0 }

type key struct {
	id              string
	private, public any
}

func (k key) ID() string                                  { return k.id }
func (k key) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k key) Key() any {
	if k.private != nil {
		return k.private
	}
	return k.public
}
func (k key) Algorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k key) Use() string                        { return "sig" }
