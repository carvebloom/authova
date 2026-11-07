// Package application owns identity and management use cases and transaction boundaries.
package application

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/google/uuid"
)

type Service struct {
	ChallengeLimiter      ports.RateLimiter
	ObserveChallengeLimit func(string)
	ObserveKDF            func(time.Duration, error)
	Resources             *resource.Manager
	ConsoleOrigin         string
	UpstreamClient        *http.Client
	Store                 ports.Store
	Vault                 *secure.Vault
	BaseURL               string
	Insecure              bool
	kdf                   chan struct{}
	kdfQueue              chan struct{}
	now                   func() time.Time
	dummy                 string
}

func New(store ports.Store, vault *secure.Vault, base string, insecure bool, budget int, options ...Option) (*Service, error) {
	if budget < 1 || budget > 512 {
		return nil, domain.ErrInvalid
	}
	dummy, e := secure.HashPassword(secure.Random(32))
	if e != nil {
		return nil, e
	}
	service := &Service{Store: store, Vault: vault, BaseURL: strings.TrimRight(base, "/"), ConsoleOrigin: "http://127.0.0.1:18090", Insecure: insecure, kdf: make(chan struct{}, budget), kdfQueue: make(chan struct{}, budget*4), dummy: dummy, UpstreamClient: secure.PublicHTTPClient(insecure)}
	for _, option := range options {
		option(service)
	}
	return service, nil
}
func ID() string { return uuid.Must(uuid.NewV7()).String() }
func (s *Service) Digest(app, purpose, value string) string {
	return s.Vault.Digest(app+":"+purpose, value)
}
func (s *Service) withKDF(ctx context.Context, fn func() error) (err error) {
	started := time.Now()
	defer func() {
		if s.ObserveKDF != nil {
			s.ObserveKDF(time.Since(started), err)
		}
	}()
	if s.Resources != nil {
		release, err := s.Resources.Acquire(ctx, resource.App(ctx), resource.KDF)
		if err != nil {
			return err
		}
		defer release()
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", domain.ErrUnavailable, ctx.Err())
	}
	select {
	case s.kdfQueue <- struct{}{}:
		defer func() { <-s.kdfQueue }()
	default:
		return domain.ErrUnavailable
	}
	select {
	case s.kdf <- struct{}{}:
		defer func() { <-s.kdf }()
		if ctx.Err() != nil {
			return fmt.Errorf("%w: %w", domain.ErrUnavailable, ctx.Err())
		}
		return fn()
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", domain.ErrUnavailable, ctx.Err())
	}
}
func (s *Service) HashPassword(ctx context.Context, p string) (h string, e error) {
	if !secure.ValidPassword(p) {
		return "", domain.ErrInvalid
	}
	e = s.withKDF(ctx, func() error { h, e = secure.HashPassword(p); return e })
	return
}
func (s *Service) CheckPassword(ctx context.Context, p, h string) error {
	if h == "" {
		h = s.dummy
	}
	return s.withKDF(ctx, func() error {
		if !secure.CheckPassword(p, h) {
			return domain.ErrUnauthenticated
		}
		return nil
	})
}
func audit(app, actor, action, subject string, reasons ...string) domain.Audit {
	event := domain.Audit{ID: ID(), AppID: app, ActorID: actor, Action: action, SubjectID: subject, CreatedAt: time.Now().UTC()}
	if len(reasons) > 0 {
		event.Reason = reasons[0]
	}
	return event
}
func (s *Service) Bootstrap(ctx context.Context, email, password, totp string) error {
	email, e := secure.Normalize("email", email)
	if e != nil {
		return domain.ErrInvalid
	}
	if !secure.ValidTOTPSecret(totp) {
		return domain.ErrInvalid
	}
	h, e := s.HashPassword(ctx, password)
	if e != nil {
		return e
	}
	return s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		n, e := t.CountOperators()
		if e != nil {
			return e
		}
		if n != 0 {
			return domain.ErrConflict
		}
		o := domain.Operator{ID: ID(), Email: email, Role: "owner", Status: "active", Password: h, TOTP: totp}
		if e = t.PutOperator(o); e != nil {
			return e
		}
		return t.Audit(audit("", o.ID, "platform.bootstrap", o.ID))
	})
}
func (s *Service) LoginOperator(ctx context.Context, email, password, code string) (token string, o domain.Operator, expires time.Time, err error) {
	email, e := secure.Normalize("email", email)
	if e != nil {
		email = "invalid"
	}
	err = s.Store.Platform(ctx, false, func(t ports.PlatformTx) error { o, e = t.OperatorByEmail(email); return e })
	h := o.Password
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", o, expires, err
	}
	if e = s.CheckPassword(ctx, password, h); e != nil {
		return "", domain.Operator{}, expires, e
	}
	if err != nil {
		return "", domain.Operator{}, expires, domain.ErrUnauthenticated
	}
	token = secure.Random(32)
	expires = s.Now().Add(8 * time.Hour).UTC()
	err = s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		current, e := t.Operator(o.ID, true)
		if e != nil {
			return e
		}
		if current.Status != "active" || current.Password != h {
			return domain.ErrUnauthenticated
		}
		step, ok := secure.VerifyTOTP(current.TOTP, code, current.TOTPLast, s.Now())
		if !ok {
			return domain.ErrUnauthenticated
		}
		current.TOTPLast = step
		if e = t.PutOperator(current); e != nil {
			return e
		}
		o = current
		if e = t.PutManagementSession(domain.ManagementSession{Digest: s.Vault.Digest("management-token", token), OperatorID: o.ID, Created: s.Now().Unix(), Expires: expires.Unix(), MFATime: s.Now().Unix()}); e != nil {
			return e
		}
		return t.Audit(audit("", o.ID, "platform.login", o.ID))
	})
	return
}
func (s *Service) Operator(ctx context.Context, token string, write bool) (o domain.Operator, err error) {
	if strings.Count(token, ".") == 2 {
		return s.JWTManager(ctx, token, write)
	}
	if token == "" {
		return o, domain.ErrUnauthenticated
	}
	err = s.Store.Platform(ctx, write, func(t ports.PlatformTx) error {
		m, e := t.ManagementSession(s.Vault.Digest("management-token", token))
		if e != nil {
			if errors.Is(e, domain.ErrNotFound) {
				return domain.ErrUnauthenticated
			}
			return e
		}
		if m.Revoked || m.Expires <= s.Now().Unix() || (write && m.MFATime < s.Now().Add(-10*time.Minute).Unix()) {
			return domain.ErrUnauthenticated
		}
		o, e = t.Operator(m.OperatorID, write)
		if e != nil {
			return e
		}
		if o.Status != "active" {
			return domain.ErrForbidden
		}
		if write && o.Role == "auditor" {
			return domain.ErrForbidden
		}
		return nil
	})
	return
}
func (s *Service) LogoutOperator(ctx context.Context, token string) error {
	return s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		m, e := t.ManagementSession(s.Vault.Digest("management-token", token))
		if e != nil {
			return e
		}
		m.Revoked = true
		if e = t.PutManagementSession(m); e != nil {
			return e
		}
		return t.Audit(audit("", m.OperatorID, "platform.logout", m.OperatorID))
	})
}
func (s *Service) NewSigningKey(app string) (domain.SigningKey, error) {
	k, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return domain.SigningKey{}, e
	}
	return domain.SigningKey{ID: ID(), AppID: app, Private: x509.MarshalPKCS1PrivateKey(k), Current: true, Created: s.Now().Unix()}, nil
}

var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

func (s *Service) CreateApp(ctx context.Context, token, name, slug string, idem ...string) (a domain.App, e error) {
	o, e := s.Operator(ctx, token, true)
	if e != nil {
		return a, e
	}
	if strings.TrimSpace(name) == "" || len(name) > 200 || !slugPattern.MatchString(slug) {
		return a, domain.ErrInvalid
	}
	key := ""
	if len(idem) > 0 {
		key = idem[0]
	}
	fingerprint := s.Vault.Digest("app-create", name+"\x00"+slug)
	if key != "" {
		found := false
		e = s.Store.Platform(ctx, false, func(t ports.PlatformTx) error {
			data, err := t.Idempotency(o.ID, key, fingerprint)
			if errors.Is(err, domain.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			found = true
			return json.Unmarshal(data, &a)
		})
		if e != nil || found {
			return a, e
		}
	}

	a = domain.App{ID: ID(), Key: strings.ToLower(strings.ReplaceAll(ID(), "-", "")), Name: name, Slug: slug, Status: "active", Version: 1, CreatedAt: s.Now().UTC()}
	a.Issuer = s.BaseURL + "/a/" + a.Key
	k, e := s.NewSigningKey(a.ID)
	if e != nil {
		return a, e
	}
	e = s.Store.Platform(ctx, true, func(t ports.PlatformTx) error {
		current, e := t.Operator(o.ID, true)
		if e != nil {
			return e
		}
		if current.Status != "active" || current.Role == "auditor" {
			return domain.ErrForbidden
		}
		if key != "" {
			data, err := t.Idempotency(o.ID, key, fingerprint)
			if err == nil {
				return json.Unmarshal(data, &a)
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		}
		if err := t.ProvisionApp(a, k, s.ManagementClient(a), audit("", o.ID, "app.create", a.ID)); err != nil {
			return err
		}
		if key != "" {
			data, _ := json.Marshal(a)
			return t.PutIdempotency(o.ID, key, fingerprint, data, s.Now().Add(24*time.Hour).Unix())
		}
		return nil
	})
	return
}

// AppManage is the narrow, audited platform support path; auditors see metadata only.
// The current session and operator are rechecked in the App transaction.
func (s *Service) AppManage(ctx context.Context, token string, scope domain.Scope, write, private bool, fn func(ports.AppTx, domain.Operator) error) error {
	if token == "" {
		return domain.ErrUnauthenticated
	}
	return s.Store.App(ctx, scope, write, func(t ports.AppTx) error {
		var o domain.Operator
		var e error
		if strings.Count(token, ".") == 2 {
			a, err := t.App()
			if err != nil {
				return err
			}
			o, e = s.appOperator(t, a, token, write)
		} else {
			o, e = t.ManagementOperator(s.Vault.Digest("management-token", token), write)
		}
		if e != nil {
			return e
		}
		if write && o.Role == "auditor" {
			return domain.ErrForbidden
		}
		if private && o.Role == "auditor" && o.AppID == "" {
			return domain.ErrForbidden
		}
		return fn(t, o)
	})
}

func (s *Service) CreateClient(ctx context.Context, token string, scope domain.Scope, c domain.Client, key string) (result domain.Client, secret string, err error) {
	if c.System || c.SecretDigest != "" || c.PreviousSecretDigest != "" || c.PreviousSecretExpires != 0 || !validClient(c, s.Insecure) {
		return c, "", domain.ErrInvalid
	}
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	if c.IntrospectionAudiences == nil {
		c.IntrospectionAudiences = []string{}
	}

	c.ID = ID()
	c.AppID = scope.ID()
	c.ClientID = "avc_" + strings.ToLower(strings.ReplaceAll(ID(), "-", ""))[:26]
	c.Status = "active"
	c.Version = 1
	c.CreatedAt = s.Now().UTC()
	if c.Kind == "confidential" {
		secret = secure.Random(32)
		c.SecretDigest = s.Digest(scope.ID(), "client-secret", secret)
	}
	fp := s.Digest(scope.ID(), "idem-client", mustJSON(struct{ Client domain.Client }{cWithoutIDs(c)}))
	var replay struct {
		Client domain.Client `json:"client"`
		Secret string        `json:"client_secret,omitempty"`
	}
	err = s.AppManage(ctx, token, scope, true, false, func(t ports.AppTx, o domain.Operator) error {
		if key != "" {
			key = s.Digest(scope.ID(), "idempotency-actor", o.ID+":"+key)
		}
		if key != "" {
			b, e := t.Idempotency(key, fp)
			if e == nil {
				if e = json.Unmarshal(b, &replay); e != nil {
					return e
				}
				result, secret = replay.Client, replay.Secret
				return nil
			}
			if !errors.Is(e, domain.ErrNotFound) {
				return e
			}
		}
		if e := t.PutClient(c); e != nil {
			return e
		}
		if e := t.Audit(audit(scope.ID(), o.ID, "client.create", c.ClientID)); e != nil {
			return e
		}
		result = c
		if key != "" {
			b, _ := json.Marshal(struct {
				Client domain.Client `json:"client"`
				Secret string        `json:"client_secret,omitempty"`
			}{c, secret})
			return t.PutIdempotency(key, fp, b, s.Now().Add(10*time.Minute).Unix())
		}
		return nil
	})
	return
}
func cWithoutIDs(c domain.Client) domain.Client {
	c.ID = ""
	c.AppID = ""
	c.ClientID = ""
	c.SecretDigest = ""
	c.PreviousSecretDigest = ""
	c.PreviousSecretExpires = 0
	c.Status = ""
	c.Version = 0
	c.CreatedAt = time.Time{}
	return c
}
func mustJSON(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func validClient(c domain.Client, insecure bool) bool {
	if c.Name == "" || len(c.Name) > 200 || (c.Kind != "public" && c.Kind != "confidential") || len(c.Scopes) == 0 || len(c.Scopes) > 32 || len(c.Audiences) == 0 || len(c.Audiences) > 16 || len(c.RedirectURIs) > 16 {
		return false
	}
	if c.Kind == "public" && (len(c.IntrospectionAudiences) > 0 || slices.Contains(c.Grants, "client_credentials")) {
		return false
	}
	for _, g := range c.Grants {
		if !slices.Contains([]string{"authorization_code", "refresh_token", "client_credentials"}, g) {
			return false
		}
	}
	for _, raw := range c.RedirectURIs {
		u, e := url.Parse(raw)
		if e != nil || u.Host == "" || u.Fragment != "" || u.User != nil || (u.Scheme != "https" && !(insecure && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
			return false
		}
	}
	for _, a := range append(append([]string{}, c.Audiences...), c.IntrospectionAudiences...) {
		u, e := url.Parse(a)
		if e != nil || u.Scheme == "" {
			return false
		}
		if strings.HasPrefix(a, "urn:authova:management:") {
			return false
		}
	}
	for _, sc := range c.Scopes {
		if sc == "" || strings.ContainsAny(sc, " \r\n\t") {
			return false
		}
	}
	return len(c.Grants) > 0
}
func (s *Service) DisableUser(t ports.AppTx, actor, app, id string, reasons ...string) error {
	if e := t.LockOwners(); e != nil {
		return e
	}
	u, e := t.User(id, true)
	if e != nil {
		return e
	}
	if u.Status == domain.Quarantined {
		return domain.ErrConflict
	}
	role, e := t.AdminRole(id)
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	if role == "owner" {
		n, e := t.CountOwners()
		if e != nil {
			return e
		}
		if n <= 1 {
			return domain.ErrConflict
		}
	}
	u.Status = "disabled"
	u.Epoch++
	if e = t.PutUser(u); e != nil {
		return e
	}
	if e = t.RevokeSessions(id); e != nil {
		return e
	}
	return t.Audit(audit(app, actor, "user.disable", id, reasons...))
}
func (s *Service) RotateKey(t ports.AppTx, actor, app string, k domain.SigningKey, reasons ...string) error {
	if e := t.LockOwners(); e != nil {
		return e
	}
	a, e := t.App()
	if e != nil {
		return e
	}
	if a.Status == domain.Quarantined {
		return domain.ErrConflict
	}
	keys, e := t.Keys()
	if e != nil {
		return e
	}
	for _, old := range keys {
		if old.Current {
			old.Current = false
			old.Retire = s.Now().Add(24 * time.Hour).Unix()
			if e = t.PutKey(old); e != nil {
				return e
			}
		}
	}
	if e = t.PutKey(k); e != nil {
		return e
	}
	a.KeyEpoch++
	a.Version++
	if e = t.UpdateAppConfig(a); e != nil {
		return e
	}
	return t.Audit(audit(app, actor, "key.rotate", k.ID, reasons...))
}

func (s *Service) ValidateClient(c domain.Client) bool { return validClient(c, s.Insecure) }
