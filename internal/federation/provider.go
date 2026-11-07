// Package federation adapts upstream protocols to a verified, stable subject.
// Upstream access tokens are used transiently and are never persisted as local credentials.
package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/carvebloom/authova/internal/domain"
	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Identity struct {
	Subject, Name, Email string
	EmailVerified        bool
}
type Provider interface {
	Authorization(context.Context, domain.Connection, string, string, string, string) (string, error)
	Authenticate(context.Context, domain.Connection, string, string, string, string) (Identity, error)
}

func New(kind string, client *http.Client) (Provider, error) {
	if client == nil {
		return nil, domain.ErrUnavailable
	}
	switch kind {
	case "", "oidc":
		return oidcProvider{client}, nil
	case "github":
		return githubProvider{client}, nil
	case "wechat":
		return wechatProvider{client}, nil
	case "qq":
		return qqProvider{client}, nil
	case "alipay":
		return alipayProvider{client}, nil
	default:
		return nil, domain.ErrInvalid
	}
}

type oidcProvider struct{ client *http.Client }

func (p oidcProvider) config(ctx context.Context, c domain.Connection, redirect string) (context.Context, *coreoidc.Provider, *oauth2.Config, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
	provider, err := coreoidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return ctx, nil, nil, domain.ErrUnavailable
	}
	var metadata struct {
		AuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	if provider.Claims(&metadata) != nil {
		return ctx, nil, nil, domain.ErrUnavailable
	}
	ep := provider.Endpoint()
	if len(metadata.AuthMethods) == 0 || slices.Contains(metadata.AuthMethods, "client_secret_basic") {
		ep.AuthStyle = oauth2.AuthStyleInHeader
	} else if slices.Contains(metadata.AuthMethods, "client_secret_post") {
		ep.AuthStyle = oauth2.AuthStyleInParams
	} else {
		return ctx, nil, nil, domain.ErrInvalid
	}
	return ctx, provider, &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.Secret, Endpoint: ep, RedirectURL: redirect, Scopes: []string{"openid", "profile", "email"}}, nil
}
func (p oidcProvider) Authorization(ctx context.Context, c domain.Connection, redirect, state, nonce, verifier string) (string, error) {
	_, _, cfg, err := p.config(ctx, c, redirect)
	if err != nil {
		return "", err
	}
	return cfg.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.S256ChallengeOption(verifier)), nil
}
func (p oidcProvider) Authenticate(ctx context.Context, c domain.Connection, redirect, code, nonce, verifier string) (Identity, error) {
	ctx, provider, cfg, err := p.config(ctx, c, redirect)
	if err != nil {
		return Identity{}, err
	}
	token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, domain.ErrUnauthenticated
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return Identity{}, domain.ErrUnauthenticated
	}
	id, err := provider.Verifier(&coreoidc.Config{ClientID: c.ClientID, SupportedSigningAlgs: []string{"RS256", "ES256"}}).Verify(ctx, raw)
	if err != nil || id.Nonce != nonce || id.Subject == "" {
		return Identity{}, domain.ErrUnauthenticated
	}
	var identity Identity
	identity.Subject = id.Subject
	var claims struct {
		Name, Email   string
		EmailVerified bool `json:"email_verified"`
	}
	if id.Claims(&claims) != nil {
		return Identity{}, domain.ErrUnauthenticated
	}
	identity.Name, identity.Email, identity.EmailVerified = claims.Name, claims.Email, claims.EmailVerified
	var metadata struct {
		UserInfo string `json:"userinfo_endpoint"`
	}
	if provider.Claims(&metadata) != nil {
		return Identity{}, domain.ErrUnavailable
	}
	if metadata.UserInfo != "" {
		info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err != nil || info.Subject != id.Subject {
			return Identity{}, domain.ErrUnauthenticated
		}
		var profile struct {
			Name, Email   string
			EmailVerified bool `json:"email_verified"`
		}
		if info.Claims(&profile) != nil {
			return Identity{}, domain.ErrUnauthenticated
		}
		if profile.Name != "" {
			identity.Name = profile.Name
		}
		if profile.Email != "" {
			identity.Email, identity.EmailVerified = profile.Email, profile.EmailVerified
		}
	}
	return identity, nil
}

// GitHub.com OAuth Apps use OAuth2 + the authenticated /user API, not OIDC.
// Endpoints are fixed: user-controlled API URLs could leak provider credentials.
type githubProvider struct{ client *http.Client }

func (p githubProvider) config(c domain.Connection, redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.Secret, RedirectURL: redirect, Scopes: []string{"read:user", "user:email"}, Endpoint: oauth2.Endpoint{AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", AuthStyle: oauth2.AuthStyleInParams}}
}
func (p githubProvider) Authorization(_ context.Context, c domain.Connection, redirect, state, _, verifier string) (string, error) {
	return p.config(c, redirect).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("prompt", "select_account")), nil
}
func (p githubProvider) Authenticate(ctx context.Context, c domain.Connection, redirect, code, _, verifier string) (Identity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
	token, err := p.config(c, redirect).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "bearer") {
		return Identity{}, domain.ErrUnauthenticated
	}
	var user struct {
		ID          int64 `json:"id"`
		Name, Login string
	}
	if err = p.get(ctx, "/user", token.AccessToken, &user); err != nil {
		return Identity{}, err
	}
	if user.ID <= 0 || user.Login == "" {
		return Identity{}, domain.ErrUnauthenticated
	}
	name := user.Name
	if name == "" {
		name = user.Login
	}
	identity := Identity{Subject: strconv.FormatInt(user.ID, 10), Name: name}
	var emails []struct {
		Email             string
		Primary, Verified bool
	}
	if err = p.get(ctx, "/user/emails", token.AccessToken, &emails); err != nil {
		return Identity{}, err
	}
	for _, email := range emails {
		if email.Primary && email.Verified {
			identity.Email, identity.EmailVerified = email.Email, true
			break
		}
	}
	return identity, nil
}
func (p githubProvider) get(ctx context.Context, path, token string, out any) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return domain.ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	r.Header.Set("User-Agent", "Authova")
	resp, err := p.client.Do(r)
	if err != nil {
		return domain.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return domain.ErrUnauthenticated
	}
	if resp.StatusCode != 200 {
		return domain.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, out) != nil {
		return domain.ErrUnavailable
	}
	return nil
}
