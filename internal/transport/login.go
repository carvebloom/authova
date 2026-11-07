package transport

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"

	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

//go:embed assets/*.html assets/*.css assets/*.js
var loginAssets embed.FS
var loginTemplate = template.Must(template.ParseFS(loginAssets, "assets/login.html"))
var loginAssetVersion = func() string {
	hash := sha256.New()
	scripts, _ := fs.Glob(loginAssets, "assets/*.js")
	for _, path := range append([]string{"assets/login.css"}, scripts...) {
		data, err := loginAssets.ReadFile(path)
		if err != nil {
			panic(err) // Embedded asset inventory is verified when the binary is built.
		}
		hash.Write([]byte(path))
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}()

type loginPage struct {
	OTPRegistration                       bool
	OTPOnlyRegistration                   bool
	NeedsMFASetup                         bool
	PasskeyAvailable                      bool
	Policy                                domain.AuthenticationPolicy
	RecoveryCodes                         []string
	App                                   domain.App
	Flow                                  flowPresentation
	Connections                           []connectionPresentation
	ChallengeID, Error, Secret            string
	Scopes                                []string
	AgentName, AgentResource, AgentTenant string
	AgentGrantExpires                     string
	CSRF, Base                            string
}

func (loginPage) AssetVersion() string { return loginAssetVersion }

func normalizeQuery(kind, q string) (string, error) {
	v, e := secure.Normalize(kind, q)
	if e != nil {
		return "", domain.ErrInvalid
	}
	return v, nil
}
func maskUser(u *domain.User) {
	if u.Email != "" {
		u.Email = secure.Mask(u.Email)
	}
	if u.Phone != "" {
		u.Phone = secure.Mask(u.Phone)
	}
}
func (s *Server) cookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, Secure: !s.Service.Insecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (s *Server) sameOrigin(r *http.Request) bool { return r.Header.Get("Origin") == s.Service.BaseURL }
func (s *Server) loginUI(w http.ResponseWriter, r *http.Request, app domain.App) {
	scope, _ := domain.AppScope(app.ID)
	prefix := "/a/" + app.Key + "/ui/"
	relative := strings.TrimPrefix(r.URL.Path, prefix)
	if relative == "login-methods.js" && r.Method == "GET" {
		data, _ := loginAssets.ReadFile("assets/login-methods.js")
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(data)
		return
	}
	if relative == "passkeys.js" && r.Method == "GET" {
		data, err := loginAssets.ReadFile("assets/passkeys.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(data)
		return
	}
	if relative == "federation.js" && r.Method == "GET" {
		data, _ := loginAssets.ReadFile("assets/federation.js")
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(data)
		return
	}
	if relative == "login.css" && r.Method == "GET" {
		b, _ := loginAssets.ReadFile("assets/login.css")
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public,max-age=3600")
		w.Write(b)
		return
	}
	if strings.HasPrefix(relative, "account") {
		s.account(w, r, app, scope)
		return
	}
	if relative == "logout" {
		s.logout(w, r, app, scope)
		return
	}
	if relative == "federation/callback" {
		s.federationCallback(w, r, app, scope)
		return
	}
	parts := strings.Split(strings.Trim(relative, "/"), "/")
	if len(parts) < 2 || parts[0] != "flows" || len(parts[1]) != 43 {
		problem(w, r, domain.ErrNotFound)
		return
	}
	id := parts[1]
	var f domain.Flow
	var connections []domain.Connection
	var systemClient, needsMFASetup bool
	var agent domain.Agent
	var installation domain.AgentInstallation
	cookieValue := ""
	if c, e := r.Cookie("av_flow_" + id); e == nil {
		cookieValue = c.Value
	}
	newCookie := ""
	e := s.Service.Store.App(r.Context(), scope, r.Method == "GET", func(t ports.AppTx) error {
		var e error
		app, e = t.App()
		if e != nil {
			return e
		}
		f, e = t.Flow(id, r.Method == "GET")
		if e != nil {
			return e
		}
		if f.Expires <= time.Now().Unix() {
			return domain.ErrExpired
		}
		if app.Status != "active" {
			return domain.ErrForbidden
		}
		if f.CookieDigest == "" && r.Method == "GET" {
			newCookie = secure.Random(32)
			f.CookieDigest = s.Service.Digest(app.ID, "flow-cookie", newCookie)
			f.CSRF = secure.Random(32)
			if e = t.PutFlow(f); e != nil {
				return e
			}
			cookieValue = newCookie
		}
		if cookieValue == "" || !secure.Equal(f.CookieDigest, s.Service.Digest(app.ID, "flow-cookie", cookieValue)) {
			return domain.ErrForbidden
		}
		client, clientErr := t.Client(f.ClientID)
		if clientErr != nil {
			return clientErr
		}
		systemClient = client.System
		var ar struct {
			AgentInstallationID string
			Scopes              []string
		}
		if len(f.Data) > 0 && json.Unmarshal(f.Data, &ar) != nil {
			return domain.ErrUnavailable
		}
		var isAgent bool
		if f.Purpose == "authorize" || ar.AgentInstallationID != "" {
			agent, installation, isAgent, e = s.Service.AgentContext(t, f.ClientID, ar.AgentInstallationID, domain.AgentDelegated, f.UserID, ar.Scopes, false)
		}
		if e != nil {
			return e
		}
		if !isAgent {
			agent = domain.Agent{}
		}

		if f.Step == "mfa" && f.UserID != "" {
			u, err := t.User(f.UserID, false)
			if err != nil {
				return err
			}
			needsMFASetup = u.TOTPSecret == ""
		}
		connections, e = t.Connections()
		active := connections[:0]
		for _, c := range connections {
			if c.Status == "active" {
				active = append(active, c)
			}
		}
		connections = active
		return e
	})
	if e != nil {
		problem(w, r, e)
		return
	}
	if newCookie != "" {
		s.cookie(w, "av_flow_"+id, newCookie, "/a/"+app.Key, 600)
	}
	page := loginPage{NeedsMFASetup: needsMFASetup, App: app, Flow: flowPresentation{Step: f.Step}, Connections: presentConnections(connections), ChallengeID: f.ChallengeID, CSRF: f.CSRF, Base: "/a/" + app.Key + "/ui/flows/" + id}
	if agent.ID != "" {
		page.AgentName = agent.Name
		page.AgentResource = installation.Resource
		page.AgentTenant = installation.TenantID

	}
	page.Policy = app.AuthenticationPolicy()
	page.PasskeyAvailable = s.Service.PasskeyAvailable(app) && !systemClient
	if f.MFAOperation != "" {
		page.Secret = f.PendingTOTP
	}
	if systemClient {
		page.Policy.PasswordLoginEnabled = true
	}
	page.registrationOptions(f, systemClient)
	var request struct {
		AgentGrantExpires int64
		Scopes            []string
		RedirectURI       string
	}
	if json.Unmarshal(f.Data, &request) == nil {
		page.Scopes = request.Scopes
		if agent.ID != "" {
			page.AgentGrantExpires = time.Unix(request.AgentGrantExpires, 0).UTC().Format(time.RFC3339)
		}
		redirect, err := url.Parse(request.RedirectURI)
		if err == nil && redirect.Host != "" && (redirect.Scheme == "https" || redirect.Scheme == "http") {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; form-action 'self' "+redirect.Scheme+"://"+redirect.Host+"; frame-ancestors 'none'; base-uri 'none'")
		}
	}

	if r.Method == "GET" && len(parts) == 2 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		loginTemplate.Execute(w, page)
		return
	}
	if r.Method != "POST" || len(parts) < 3 {
		problem(w, r, domain.ErrNotFound)
		return
	}
	if !s.sameOrigin(r) {
		problem(w, r, domain.ErrForbidden)
		return
	}
	if e = r.ParseForm(); e != nil {
		problem(w, r, domain.ErrInvalid)
		return
	}
	csrf := r.Header.Get("X-CSRF-Token")
	if csrf == "" {
		csrf = r.Form.Get("_csrf")
	}
	if !secure.Equal(csrf, f.CSRF) || f.Completed {
		problem(w, r, domain.ErrForbidden)
		return
	}
	action := parts[2]
	if meta := securityMeta(r); meta != nil {
		meta.ClientID = f.ClientID
		meta.SubjectRef = f.UserID
		meta.Action = "authentication.request"
		meta.Success = true
		switch action {
		case "password", "challenges", "verify", "mfa", "recovery", "mfa-replace-confirm", "register", "reset", "complete", "enroll", "confirm":
			meta.Action = "authentication." + action
		}
	}
	if !s.limit(w, r, "flow-action:"+app.ID+":"+id+":"+remoteIP(r), 20, time.Minute) {
		return
	}
	if action == "passkey" {
		s.flowPasskey(w, r, app, scope, id, parts)
		return
	}
	switch action {
	case "password":
		kind := r.Form.Get("kind")
		if kind == "" {
			kind = "email"
		}
		value, normalErr := secure.Normalize(kind, r.Form.Get("identifier"))
		if normalErr != nil {
			problem(w, r, domain.ErrUnauthenticated)
			return
		}
		if !s.limit(w, r, "password-identifier:"+s.Service.Digest(app.ID, "attempt", value), 10, time.Minute) {
			return
		}
		if meta := securityMeta(r); meta != nil {
			meta.SubjectRef = s.Service.Digest(app.ID, "security-subject", value)
		}
		e = s.Service.PasswordLogin(r.Context(), scope, id, kind, value, r.Form.Get("password"))
	case "challenges":
		kind := r.Form.Get("channel")
		value, normalErr := secure.Normalize(kind, r.Form.Get("destination"))
		if normalErr != nil {
			problem(w, r, domain.ErrInvalid)
			return
		}
		configured := s.EmailConfigured
		if kind == "phone" {
			configured = s.PhoneConfigured
		}
		page.ChallengeID, e = s.Service.RequestChallenge(r.Context(), scope, id, r.Form.Get("purpose"), kind, value, configured)
	case "verify":
		e = s.Service.VerifyChallenge(r.Context(), scope, id, r.Form.Get("challenge_id"), r.Form.Get("code"))
	case "bind":
		e = s.Service.BindVerifiedIdentifier(r.Context(), scope, id)
	case "register":
		e = s.Service.Register(r.Context(), scope, id, r.Form.Get("password"), r.Form.Get("display_name"))
	case "reset":
		e = s.Service.ResetPassword(r.Context(), scope, id, r.Form.Get("password"))
	case "mfa":
		if !s.limit(w, r, "mfa-user:"+app.ID+":"+f.UserID, 5, time.Minute) {
			return
		}
		e = s.Service.VerifyMFA(r.Context(), scope, id, r.Form.Get("code"))
	case "enroll":
		page.Secret, e = s.Service.EnrollMFA(r.Context(), scope, id)
	case "confirm":
		page.RecoveryCodes, e = s.Service.ConfirmMFA(r.Context(), scope, id, r.Form.Get("code"))

	case "recovery":
		if !s.limit(w, r, "mfa-user:"+app.ID+":"+f.UserID, 5, time.Minute) {
			return
		}
		e = s.Service.RecoveryMFA(r.Context(), scope, id, r.Form.Get("code"))
	case "mfa-replace-confirm":
		if !s.limit(w, r, "mfa-user:"+app.ID+":"+f.UserID, 5, time.Minute) {
			return
		}
		page.RecoveryCodes, e = s.Service.ConfirmMFAReplacement(r.Context(), scope, id, r.Form.Get("code"))
	case "complete":
		var handle string
		handle, e = s.Service.Complete(r.Context(), scope, id)
		if e == nil {
			s.cookie(w, "av_session_"+app.Key, handle, "/a/"+app.Key, 30*24*3600)
			http.Redirect(w, r, "/a/"+app.Key+"/authorize/callback?id="+url.QueryEscape(id), http.StatusSeeOther)
			return
		}
	case "cancel":
		e = s.Service.Store.App(r.Context(), scope, true, func(t ports.AppTx) error {
			f, e := t.Flow(id, true)
			if e != nil {
				return e
			}
			f.Completed = true
			f.Step = domain.StepCancelled
			return t.PutFlow(f)
		})
	case "federation":
		s.federationStart(w, r, app, scope, id, r.Form.Get("connection_id"))
		return
	default:
		e = domain.ErrNotFound
	}
	if meta := securityMeta(r); meta != nil {
		meta.Reason = securityFailure(e)
	}
	if errors.Is(e, domain.ErrUnavailable) || errors.Is(e, domain.ErrCommitUnknown) || errors.Is(e, domain.ErrRateLimit) {
		problem(w, r, e)
		return
	}
	if e != nil {
		page.Error = "无法完成操作，请检查输入或稍后重试。"
		if errors.Is(e, domain.ErrConflict) {
			page.Error = "当前操作无法继续，请重新开始登录。"
		}
		if errors.Is(e, domain.ErrUnavailable) {
			page.Error = "认证服务暂时不可用，请稍后重试。"
		}
	}
	loadErr := s.Service.Store.App(r.Context(), scope, false, func(t ports.AppTx) error {
		var err error
		current, err := t.Flow(id, false)
		page.Flow = flowPresentation{Step: current.Step}
		page.CSRF = current.CSRF
		if err == nil {
			a, loadErr := t.App()
			if loadErr != nil {
				return loadErr
			}
			page.Policy = a.AuthenticationPolicy()
			if systemClient {
				page.Policy.PasswordLoginEnabled = true
			}
			page.registrationOptions(current, systemClient)
		}
		if err == nil && current.Step == "mfa" && current.UserID != "" {
			u, e := t.User(current.UserID, false)
			if e != nil {
				return e
			}
			page.NeedsMFASetup = u.TOTPSecret == ""
		}
		if current.MFAOperation != "" {
			page.Secret = current.PendingTOTP
		}
		return err
	})
	if loadErr != nil {
		problem(w, r, loadErr)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e != nil {
		w.WriteHeader(400)
	}
	loginTemplate.Execute(w, page)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, app domain.App, scope domain.Scope) {
	c, e := r.Cookie("av_session_" + app.Key)
	if e != nil {
		problem(w, r, domain.ErrUnauthenticated)
		return
	}
	csrf := s.Service.Digest(app.ID, "logout-csrf", c.Value)
	if r.Method == "GET" {
		page := loginPage{App: app, Flow: flowPresentation{Step: "logout"}, Base: "/a/" + app.Key + "/ui/logout", CSRF: csrf}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		loginTemplate.Execute(w, page)
		return
	}
	if r.Method != "POST" || !s.sameOrigin(r) || r.ParseForm() != nil || !secure.Equal(csrf, r.Form.Get("_csrf")) {
		problem(w, r, domain.ErrForbidden)
		return
	}
	e = s.Service.Store.App(r.Context(), scope, true, func(t ports.AppTx) error {
		session, u, e := s.Service.LockedSession(t, scope, c.Value, false)
		if e != nil {
			return e
		}
		session.Status = "revoked"
		if e = t.PutSession(session); e != nil {
			return e
		}
		return t.Audit(event(app.ID, u.ID, "session.logout", session.ID))
	})
	if e != nil {
		problem(w, r, e)
		return
	}
	s.cookie(w, "av_session_"+app.Key, "", "/a/"+app.Key, -1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	loginTemplate.Execute(w, loginPage{App: app, Flow: flowPresentation{Step: "signed_out"}})
}

// The rendered form follows the newly persisted flow after each POST, rather
// than the pre-action proof fields. No proof/contact values enter the template.
func (p *loginPage) registrationOptions(f domain.Flow, system bool) {
	p.OTPRegistration = p.Policy.RegistrationEnabled && !p.Policy.PasswordLoginEnabled && !system && ((f.VerifiedKind == "email" && p.Policy.EmailOTPEnabled) || (f.VerifiedKind == "phone" && p.Policy.PhoneOTPEnabled))
	p.OTPOnlyRegistration = !p.Policy.PasswordLoginEnabled && !(p.Policy.PasskeyEnabled && p.PasskeyAvailable)
}
