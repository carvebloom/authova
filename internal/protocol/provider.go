package protocol

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

type Provider struct {
	cache          verificationCache
	service        *application.Service
	storage        *Storage
	engine         *op.Provider
	failureCount   atomic.Uint64
	lastFailureLog atomic.Int64
}

func New(s *application.Service) (*Provider, error) {
	st := &Storage{Service: s}
	config := &op.Config{CryptoKey: s.Vault.Derive("authorization-code"), CryptoKeyId: "v1", CodeMethodS256: true, GrantTypeRefreshToken: true, SupportedScopes: []string{"openid", "profile", "email", "phone", "offline_access"}}
	issuer := func(insecure bool) (op.IssuerFromRequest, error) {
		return func(r *http.Request) string { return req(r.Context()).App.Issuer }, nil
	}
	opts := []op.Option{op.WithCrypto(op.NewAES256GCMCrypto(config.CryptoKey, "v1"))}
	if s.Insecure {
		opts = append(opts, op.WithAllowInsecure())
	}
	engine, e := op.NewProvider(config, st, issuer, opts...)
	if e != nil {
		return nil, e
	}
	return &Provider{service: s, storage: st, engine: engine, cache: verificationCache{sets: map[string]verificationSet{}}}, nil
}
func failure(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}
func (p *Provider) ServeApp(w http.ResponseWriter, r *http.Request, app domain.App) {
	scope, e := domain.AppScope(app.ID)
	if e != nil {
		failure(w, 404, "invalid_request")
		return
	}
	prefix := "/a/" + app.Key
	relative := strings.TrimPrefix(r.URL.Path, prefix)
	if relative == "/.well-known/oauth-authorization-server" {
		relative = "/.well-known/openid-configuration"
		r = r.Clone(r.Context())
		r.URL.Path = prefix + relative
	}
	if relative != "/.well-known/openid-configuration" && relative != "/keys" && relative != "/userinfo" && relative != "/oauth/token" && relative != "/oauth/introspect" && relative != "/revoke" && relative != "/authorize" && relative != "/authorize/callback" {
		failure(w, 404, "invalid_request")
		return
	}
	write := relative == "/oauth/token" || relative == "/revoke" || relative == "/authorize" || relative == "/authorize/callback"
	if (relative == "/oauth/token" || relative == "/revoke" || relative == "/oauth/introspect") && r.Method != "POST" {
		failure(w, 405, "invalid_request")
		return
	}
	if e = r.ParseForm(); e != nil {
		failure(w, 400, "invalid_request")
		return
	}
	caller := r.Form.Get("client_id")
	if basic, _, ok := r.BasicAuth(); ok {
		caller, _ = url.QueryUnescape(basic)
	}
	flowID := r.URL.Query().Get("id")
	cookie := ""
	if c, e := r.Cookie("av_flow_" + flowID); e == nil {
		cookie = c.Value
	}
	var record *httptest.ResponseRecorder
	serve := func(ctx context.Context) error {
		req(ctx).InstallationID = r.Form.Get("installation_id")
		req(ctx).Resource = r.Form.Get("resource")
		if len(r.Form["installation_id"]) > 1 || len(r.Form["resource"]) > 1 {
			record = httptest.NewRecorder()
			failure(record, 400, "invalid_request")
			return nil
		}
		record = httptest.NewRecorder()
		switch relative {
		case "/authorize":
			op.Authorize(record, r.WithContext(op.ContextWithIssuer(ctx, app.Issuer)), authorizeValidator{p.engine})
		case "/oauth/introspect":
			p.introspect(record, r.WithContext(ctx))
		case "/userinfo":
			p.userinfo(record, r.WithContext(ctx))
		case "/revoke":
			p.revoke(record, r.WithContext(ctx))
		default:
			http.StripPrefix(prefix, p.engine).ServeHTTP(record, r.WithContext(ctx))
		}
		if e := TransactionFailure(ctx); e != nil {
			return e
		}
		if record.Code >= 500 {
			return domain.ErrUnavailable
		}
		if relative == "/oauth/token" && record.Code == 200 {
			if e := p.profile(ctx, record); e != nil {
				return e
			}
		}
		if relative == "/.well-known/openid-configuration" && record.Code == 200 {
			p.discovery(app, record)
		}
		return nil
	}
	var err error
	if reader, ok := p.service.Store.(ports.IntrospectionReader); ok && relative == "/oauth/introspect" {
		ctx := WithTransaction(r.Context(), nil, app, caller, cookie)
		req(ctx).Introspection = reader
		err = serve(ctx)
	} else {
		err = p.service.Store.App(r.Context(), scope, write, func(t ports.AppTx) error {
			return serve(WithTransaction(r.Context(), t, app, caller, cookie))
		})
	}
	if err != nil {
		if errors.Is(err, domain.ErrRateLimit) {
			w.Header().Set("Retry-After", "1")
			failure(w, 429, "rate_limited")
			return
		}
		p.logTransactionFailure(relative, err)
		w.Header().Set("Retry-After", "1")
		failure(w, 503, "server_error")
		return
	}
	for key, values := range record.Header() {
		if key == "Content-Length" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(record.Code)
	w.Write(record.Body.Bytes())
}

// Keep outage logs bounded while HTTP metrics retain every failed request.
// Audit facts are stored separately and are never sampled here.
func (p *Provider) logTransactionFailure(path string, err error) {
	p.failureCount.Add(1)
	now := time.Now().UnixNano()
	previous := p.lastFailureLog.Load()
	if now-previous >= int64(time.Second) && p.lastFailureLog.CompareAndSwap(previous, now) {
		slog.Error("protocol transaction failed", "path", path, "error", err, "failures_since_log", p.failureCount.Swap(0))
	}
}
func (p *Provider) discovery(a domain.App, rec *httptest.ResponseRecorder) {
	var m map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &m) != nil {
		return
	}
	m["grant_types_supported"] = []string{"authorization_code", "refresh_token", "client_credentials"}
	m["response_types_supported"] = []string{"code"}
	m["response_modes_supported"] = []string{"query"}
	m["token_endpoint_auth_methods_supported"] = []string{"none", "client_secret_basic"}
	m["code_challenge_methods_supported"] = []string{"S256"}
	delete(m, "device_authorization_endpoint")
	delete(m, "end_session_endpoint")
	m["scopes_supported"] = []string{"openid", "profile", "email", "phone", "offline_access"}
	rec.Body.Reset()
	json.NewEncoder(rec.Body).Encode(m)
}

// profile keeps the upstream code/PKCE engine but applies RFC 9068 typ/audience.
// Re-signing the ID token also updates at_hash to the final access token.
func (p *Provider) profile(ctx context.Context, rec *httptest.ResponseRecorder) error {
	var response map[string]json.RawMessage
	if e := json.Unmarshal(rec.Body.Bytes(), &response); e != nil {
		return e
	}
	var access, id string
	if e := json.Unmarshal(response["access_token"], &access); e != nil {
		return e
	}
	if rawID, exists := response["id_token"]; exists {
		if e := json.Unmarshal(rawID, &id); e != nil {
			return e
		}
		if id == "" {
			return domain.ErrUnavailable
		}
	}
	if access == "" {
		return domain.ErrUnavailable
	}
	k, e := p.storage.SigningKey(ctx)
	if e != nil {
		return e
	}
	at, e := jose.ParseSigned(access, []jose.SignatureAlgorithm{jose.RS256})
	if e != nil {
		return e
	}
	raw, e := at.Verify(&k.Key().(*rsa.PrivateKey).PublicKey)
	if e != nil {
		return e
	}
	var claims map[string]any
	if e = json.Unmarshal(raw, &claims); e != nil {
		return e
	}
	c, e := req(ctx).Tx.Client(req(ctx).ClientID)
	if e != nil {
		return e
	}
	idClaim, ok := claims["jti"].(string)
	if !ok || idClaim == "" {
		return domain.ErrUnavailable
	}
	stored, e := req(ctx).Tx.Token(idClaim)
	if e != nil {
		return e
	}
	claims["aud"] = stored.Audiences
	if binding, err := req(ctx).Tx.AgentToken(stored.ID); err == nil {
		addAgentClaims(claims, binding)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: &jose.JSONWebKey{Key: k.Key(), KeyID: k.ID()}}, (&jose.SignerOptions{}).WithType("at+jwt"))
	if e != nil {
		return e
	}
	b, _ := json.Marshal(claims)
	signed, e := signer.Sign(b)
	if e != nil {
		return e
	}
	access, e = signed.CompactSerialize()
	if e != nil {
		return e
	}
	response["access_token"], _ = json.Marshal(access)
	if id != "" {
		j, e := jose.ParseSigned(id, []jose.SignatureAlgorithm{jose.RS256})
		if e != nil {
			return e
		}
		raw, e = j.Verify(&k.Key().(*rsa.PrivateKey).PublicKey)
		if e != nil {
			return e
		}
		var ic map[string]any
		if e = json.Unmarshal(raw, &ic); e != nil {
			return e
		}
		ic["aud"] = []string{c.ClientID}
		hash, e := oidc.ClaimHash(access, jose.RS256)
		if e != nil {
			return e
		}
		ic["at_hash"] = hash
		signer, e = jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: &jose.JSONWebKey{Key: k.Key(), KeyID: k.ID()}}, (&jose.SignerOptions{}).WithType("JWT"))
		if e != nil {
			return e
		}
		b, _ = json.Marshal(ic)
		signed, e = signer.Sign(b)
		if e != nil {
			return e
		}
		id, e = signed.CompactSerialize()
		if e != nil {
			return e
		}
		response["id_token"], _ = json.Marshal(id)
	}
	rec.Body.Reset()
	return json.NewEncoder(rec.Body).Encode(response)
}

type accessClaims struct {
	AgentID        string      `json:"agent_id"`
	InstallationID string      `json:"installation_id"`
	GrantID        string      `json:"grant_id"`
	TenantID       string      `json:"tenant_id"`
	AgentMode      string      `json:"agent_mode"`
	Actor          *agentActor `json:"act"`
	Issuer         string      `json:"iss"`
	Subject        string      `json:"sub"`
	Audience       []string    `json:"aud"`
	Expires        int64       `json:"exp"`
	Issued         int64       `json:"iat"`
	ID             string      `json:"jti"`
	ClientID       string      `json:"client_id"`
	Scope          string      `json:"scope"`
	Principal      string      `json:"principal_type"`
	SessionID      string      `json:"sid"`
}

func (p *Provider) verify(ctx context.Context, token string) (accessClaims, error) {
	var c accessClaims
	if len(token) > 16384 {
		return c, domain.ErrUnauthenticated
	}
	j, e := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if e != nil || len(j.Signatures) != 1 {
		return c, domain.ErrUnauthenticated
	}
	h := j.Signatures[0].Header
	if h.ExtraHeaders[jose.HeaderType] != "at+jwt" || h.KeyID == "" {
		return c, domain.ErrUnauthenticated
	}
	keys, e := req(ctx).Tx.Keys()
	if e != nil {
		return c, dependency(ctx, e)
	}
	for _, k := range keys {
		if k.RevokedAt != 0 || k.ID != h.KeyID || (!k.Current && k.Retire < time.Now().Unix()) {
			continue
		}
		priv, e := x509.ParsePKCS1PrivateKey(k.Private)
		if e != nil {
			return c, dependency(ctx, e)
		}
		b, e := j.Verify(&priv.PublicKey)
		if e != nil {
			return c, domain.ErrUnauthenticated
		}
		if e = json.Unmarshal(b, &c); e != nil {
			return c, domain.ErrUnauthenticated
		}
		if c.Issuer != req(ctx).App.Issuer || c.Expires <= time.Now().Unix() || c.Issued > time.Now().Add(30*time.Second).Unix() || c.Subject == "" || c.ID == "" || len(c.Audience) == 0 || c.ClientID == "" || !slices.Contains([]string{"user", "client"}, c.Principal) {
			return c, domain.ErrUnauthenticated
		}
		return c, nil
	}
	return c, domain.ErrUnauthenticated
}
func (p *Provider) introspect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, secret, ok := r.BasicAuth()
	id, _ = url.QueryUnescape(id)
	secret, _ = url.QueryUnescape(secret)
	if !ok {
		failure(w, 401, "invalid_client")
		return
	}
	raw := r.Form.Get("token")
	hint := ""
	var signed *jose.JSONWebSignature
	if len(raw) <= 16384 {
		if parsed, e := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256}); e == nil {
			signed = parsed
			var c accessClaims
			if json.Unmarshal(parsed.UnsafePayloadWithoutVerification(), &c) == nil {
				hint = c.ID
			}
		}
	}
	var client domain.Client
	var tok domain.TokenState
	var live bool
	var e error
	if reader := req(ctx).Introspection; reader != nil {
		scope, err := domain.AppScope(req(ctx).App.ID)
		if err != nil {
			dependency(ctx, err)
			return
		}
		client, tok, live, e = reader.ReadIntrospection(ctx, scope, id, hint)
	} else {
		client, tok, live, e = req(ctx).Tx.IntrospectionState(id, hint)
	}
	if e != nil {
		if !errors.Is(e, domain.ErrNotFound) {
			dependency(ctx, e)
		}
		failure(w, 401, "invalid_client")
		return
	}
	if client.AppID != req(ctx).App.ID || client.ClientID != id || client.Status != "active" || client.Kind != "confidential" || !p.service.ClientSecretMatches(req(ctx).App.ID, client, secret) {
		failure(w, 401, "invalid_client")
		return
	}
	if len(client.IntrospectionAudiences) == 0 {
		failure(w, 403, "unauthorized_client")
		return
	}
	inactive := func() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"active": false})
	}
	// The authenticated caller and authoritative snapshot already establish an
	// inactive token. Untrusted jti/kid must not trigger key loading in that case.
	if !live {
		inactive()
		return
	}
	claims, e := p.cachedVerify(ctx, signed, tok.KeyEpoch)
	if e != nil {
		inactive()
		return
	}
	if !tokenStateMatches(claims, tok) {
		inactive()
		return
	}
	allowed := false
	for _, audience := range claims.Audience {
		if slices.Contains(client.IntrospectionAudiences, audience) {
			allowed = true
		}
	}
	if !allowed {
		inactive()
		return
	}
	// Preserve the original transaction adapter contract: only the optional
	// atomic reader guarantees that the MFA proof is already in its snapshot.
	if req(ctx).Introspection == nil && tok.RequiresMFA && tok.UserID != "" {
		session, err := req(ctx).Tx.Session(tok.SessionID, false)
		if err != nil {
			dependency(ctx, err)
			return
		}
		if session.ID != tok.SessionID || session.UserID != tok.UserID || session.MFATime <= 0 {
			inactive()
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"active": true, "sub": claims.Subject, "client_id": claims.ClientID, "aud": claims.Audience, "iss": claims.Issuer, "exp": tok.Expires, "iat": claims.Issued, "jti": tok.ID, "scope": claims.Scope, "token_type": "Bearer", "principal_type": claims.Principal, "app_id": req(ctx).App.ID}
	if tok.Agent != nil {
		addAgentClaims(out, *tok.Agent)
	}
	json.NewEncoder(w).Encode(out)
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c, e := p.verify(ctx, token)
	if e != nil {
		failure(w, 401, "invalid_token")
		return
	}
	info := &oidc.UserInfo{}
	if e = p.storage.SetUserinfoFromToken(ctx, info, c.ID, c.Subject, ""); e != nil {
		if !errors.Is(e, domain.ErrForbidden) && !errors.Is(e, domain.ErrNotFound) {
			dependency(ctx, e)
		}
		failure(w, 401, "invalid_token")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}
func (p *Provider) revoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, secret, basic := r.BasicAuth()
	id, _ = url.QueryUnescape(id)
	secret, _ = url.QueryUnescape(secret)
	if basic {
		if p.storage.AuthorizeClientIDSecret(ctx, id, secret) != nil {
			failure(w, 401, "invalid_client")
			return
		}
	} else {
		id = r.Form.Get("client_id")
		client, e := req(ctx).Tx.Client(id)
		if e != nil && !errors.Is(e, domain.ErrNotFound) {
			dependency(ctx, e)
		}
		if e != nil || client.Kind != "public" {
			failure(w, 401, "invalid_client")
			return
		}
	}

	token := r.Form.Get("token")
	if c, e := p.verify(ctx, token); e == nil {
		if c.ClientID == id {
			tok, e := req(ctx).Tx.Token(c.ID)
			if e == nil {
				tok.Revoked = true
				dependency(ctx, req(ctx).Tx.PutToken(tok))
			} else if !errors.Is(e, domain.ErrNotFound) {
				dependency(ctx, e)
			}
		}
	} else {
		p.storage.RevokeToken(ctx, token, "", id)
	}
	w.WriteHeader(200)
}
