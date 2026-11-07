package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	sdk "github.com/carvebloom/authova/console/internal/coreapi"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/valkey-io/valkey-go"
	"golang.org/x/oauth2"
)

type appFlow struct{ Key, ClientID, State, Nonce, Verifier string }

var appKey = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (b *bff) upstreamContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, b.api.HTTP)
}
func (b *bff) appOAuth(ctx context.Context, key, client string) (*oidc.Provider, *oauth2.Config, error) {
	provider, e := oidc.NewProvider(b.upstreamContext(ctx), b.api.BaseURL+"/a/"+key)
	if e != nil {
		return nil, nil, e
	}
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInParams // Public Client: never probe/replay a rotation.
	return provider, &oauth2.Config{ClientID: client, RedirectURL: b.origin + "/app/callback", Endpoint: endpoint, Scopes: []string{"openid", "profile", "email", "offline_access", "authova.admin"}}, nil
}
func (b *bff) appLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AppKey string `json:"app_key"`
	}
	if r.Method != "POST" || r.Header.Get("Origin") != b.origin || decodeJSON(r, &in) != nil || !appKey.MatchString(in.AppKey) {
		problem(w, 400, "invalid_request")
		return
	}
	req, e := http.NewRequestWithContext(r.Context(), "GET", b.api.BaseURL+"/a/"+in.AppKey+"/.well-known/authova-management", nil)
	if e != nil {
		problem(w, 400, "invalid_request")
		return
	}
	resp, e := b.api.HTTP.Do(req)
	if e != nil {
		problem(w, 503, "dependency_unavailable")
		return
	}
	defer resp.Body.Close()
	var metadata struct {
		Issuer   string `json:"issuer"`
		ClientID string `json:"client_id"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(http.MaxBytesReader(w, resp.Body, 32*1024)).Decode(&metadata) != nil || metadata.Issuer != b.api.BaseURL+"/a/"+in.AppKey {
		problem(w, 404, "not_found")
		return
	}
	_, config, e := b.appOAuth(r.Context(), in.AppKey, metadata.ClientID)
	if e != nil {
		problem(w, 503, "dependency_unavailable")
		return
	}
	flow := appFlow{Key: in.AppKey, ClientID: metadata.ClientID, State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier()}
	raw, _ := json.Marshal(flow)
	encrypted, e := b.crypt(raw, true)
	if e != nil {
		problem(w, 503, "dependency_unavailable")
		return
	}
	if e = b.cache.Do(r.Context(), b.cache.B().Set().Key("authova:console:flow:"+flow.State).Value(string(encrypted)).ExSeconds(300).Build()).Error(); e != nil {
		problem(w, 503, "dependency_unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "av_console_flow", Value: flow.State, Path: "/app/", HttpOnly: true, Secure: !b.development, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	response(w, 200, map[string]string{"redirect_to": config.AuthCodeURL(flow.State, oauth2.SetAuthURLParam("nonce", flow.Nonce), oauth2.S256ChallengeOption(flow.Verifier))})
}
func (b *bff) appCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	cookie, e := r.Cookie("av_console_flow")
	if e != nil || len(state) != 43 || cookie.Value != state {
		problem(w, 403, "forbidden")
		return
	}
	raw, e := b.cache.Do(r.Context(), b.cache.B().Getdel().Key("authova:console:flow:"+state).Build()).AsBytes()
	if e != nil {
		if !valkey.IsValkeyNil(e) {
			problem(w, 503, "dependency_unavailable")
			return
		}
		problem(w, 403, "flow_expired")
		return
	}
	plain, e := b.crypt(raw, false)
	if e != nil {
		problem(w, 403, "forbidden")
		return
	}
	var flow appFlow
	if json.Unmarshal(plain, &flow) != nil || flow.State != state {
		problem(w, 403, "forbidden")
		return
	}
	provider, config, e := b.appOAuth(r.Context(), flow.Key, flow.ClientID)
	if e != nil {
		problem(w, 503, "dependency_unavailable")
		return
	}
	token, e := config.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
	if e != nil {
		problem(w, 401, "authentication_required")
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		problem(w, 401, "authentication_required")
		return
	}
	identity, e := provider.Verifier(&oidc.Config{ClientID: flow.ClientID, SupportedSigningAlgs: []string{"RS256"}}).Verify(r.Context(), rawID)
	if e != nil || identity.Nonce != flow.Nonce || identity.VerifyAccessToken(token.AccessToken) != nil {
		problem(w, 401, "authentication_required")
		return
	}
	resp, e := b.api.Request(r.Context(), "GET", "/api/v1/platform/me", token.AccessToken, "", nil)
	if e != nil || resp.Status != 200 {
		problem(w, 403, "forbidden")
		return
	}
	var current struct {
		Operator sdk.Operator `json:"operator"`
	}
	if json.Unmarshal(resp.Body, &current) != nil || current.Operator.AppID == "" {
		problem(w, 403, "forbidden")
		return
	}
	session := session{Token: token.AccessToken, Refresh: token.RefreshToken, TokenExpires: token.Expiry, AppKey: flow.Key, ClientID: flow.ClientID, CSRF: random(), Operator: current.Operator, Expires: time.Now().Add(8 * time.Hour)}
	id := random()
	if e = b.saveSession(r.Context(), id, session); e != nil {
		problem(w, 503, "dependency_unavailable")
		return
	}
	b.cookie(w, id, 8*3600)
	http.SetCookie(w, &http.Cookie{Name: "av_console_flow", Value: "", Path: "/app/", HttpOnly: true, Secure: !b.development, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/#/apps", http.StatusSeeOther)
}
func (b *bff) saveSession(ctx context.Context, id string, s session) error {
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	encrypted, e := b.crypt(raw, true)
	if e != nil {
		return e
	}
	ttl := int64(time.Until(s.Expires).Seconds())
	if ttl <= 0 {
		return errors.New("session expired")
	}
	return b.cache.Do(ctx, b.cache.B().Set().Key("authova:console:"+id).Value(string(encrypted)).ExSeconds(ttl).Build()).Error()
}

// refreshSession serializes rotation across BFF replicas, then reloads the shared state.
func (b *bff) refreshSession(r *http.Request, id string, s session) (session, error) {
	if s.Refresh == "" || s.TokenExpires.After(time.Now().Add(30*time.Second)) {
		return s, nil
	}
	lock := "authova:console:refresh-lock:" + id
	nonce := random()
	deadline := time.Now().Add(5 * time.Second)
	acquired := false
	for time.Now().Before(deadline) {
		result, e := b.cache.Do(r.Context(), b.cache.B().Set().Key(lock).Value(nonce).Nx().ExSeconds(30).Build()).ToString()
		if e == nil && result == "OK" {
			acquired = true
			break
		}
		select {
		case <-r.Context().Done():
			return s, r.Context().Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !acquired {
		return s, errors.New("refresh lock unavailable")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		b.cache.Do(release, b.cache.B().Eval().Script("if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) else return 0 end").Numkeys(1).Key(lock).Arg(nonce).Build())
	}()
	current, _, e := b.load(r)
	if e != nil {
		return s, e
	}
	if current.RefreshPending {
		return current, errSessionMissing
	}
	if current.TokenExpires.After(time.Now().Add(30 * time.Second)) {
		return current, nil
	}
	_, config, e := b.appOAuth(r.Context(), current.AppKey, current.ClientID)
	if e != nil {
		return s, e
	}
	current.RefreshPending = true
	if e = b.replaceSession(r.Context(), id, current); e != nil {
		return current, e
	}
	token, e := config.TokenSource(r.Context(), &oauth2.Token{RefreshToken: current.Refresh, Expiry: time.Now().Add(-time.Hour)}).Token()
	if e != nil {
		return s, e
	}
	if token.AccessToken == "" || token.RefreshToken == "" || !token.Expiry.After(time.Now()) {
		return current, errors.New("invalid refresh response")
	}
	current.Token, current.Refresh, current.TokenExpires = token.AccessToken, token.RefreshToken, token.Expiry
	current.RefreshPending = false
	return current, b.replaceSession(r.Context(), id, current)
}
func (b *bff) revokeAppSession(ctx context.Context, s session) error {
	form := url.Values{"client_id": {s.ClientID}, "token": {s.Refresh}, "token_type_hint": {"refresh_token"}}
	req, e := http.NewRequestWithContext(ctx, "POST", b.api.BaseURL+"/a/"+s.AppKey+"/revoke", strings.NewReader(form.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := b.api.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("app logout failed")
	}
	return nil
}

// replaceSession never resurrects a session removed by logout. A pending refresh
// persists before network I/O, so an interrupted/unknown rotation is not replayed.
func (b *bff) replaceSession(ctx context.Context, id string, s session) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	ciphertext, err := b.crypt(data, true)
	if err != nil {
		return err
	}
	ttl := int64(time.Until(s.Expires).Seconds())
	if ttl <= 0 {
		return errSessionMissing
	}
	result, err := b.cache.Do(ctx, b.cache.B().Set().Key("authova:console:"+id).Value(string(ciphertext)).Xx().ExSeconds(ttl).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return errSessionMissing
	}
	if err != nil {
		return err
	}
	if result != "OK" {
		return errSessionMissing
	}
	return nil
}
