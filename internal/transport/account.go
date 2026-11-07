package transport

import (
	"html/template"
	"net/http"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

type accountConnection struct {
	connectionPresentation
	Bound int
}

var accountTemplate = template.Must(template.ParseFS(loginAssets, "assets/account.html"))

func (s *Server) account(w http.ResponseWriter, r *http.Request, a domain.App, scope domain.Scope) {
	cookie, e := r.Cookie("av_session_" + a.Key)
	if e != nil {
		problem(w, r, domain.ErrUnauthenticated)
		return
	}
	var session domain.Session
	var user domain.User
	var connections []accountConnection
	e = s.Service.Store.App(r.Context(), scope, false, func(t ports.AppTx) error {
		var err error
		session, user, err = s.Service.AuthenticatedSession(t, scope, cookie.Value, false)
		if err != nil {
			return err
		}
		configured, err := t.Connections()
		if err != nil {
			return err
		}
		bindings, err := t.ExternalBindings(user.ID)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for _, b := range bindings {
			counts[b.ConnectionID] = b.Count
		}
		for _, c := range configured {
			connections = append(connections, accountConnection{presentConnection(c), counts[c.ID]})
		}
		return nil
	})
	if e != nil {
		problem(w, r, e)
		return
	}
	base := "/a/" + a.Key + "/ui/account"
	csrf := s.Service.Digest(a.ID, "account-csrf", cookie.Value)
	if s.accountAgents(w, r, a, scope, cookie.Value, csrf) {
		return
	}
	if s.accountPasskeys(w, r, a, scope, cookie.Value, csrf, user) {
		return
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		accountTemplate.Execute(w, accountPresentation{PasskeyEnabled: a.AuthenticationPolicy().PasskeyEnabled, PasskeyAvailable: s.Service.PasskeyAvailable(a), App: a, User: presentUser(user), Connections: connections, Base: base, CSRF: csrf, AssetVersion: loginAssetVersion})
		return
	}
	if r.Method != "POST" || !s.sameOrigin(r) || r.ParseForm() != nil || !secure.Equal(csrf, r.Form.Get("_csrf")) {
		problem(w, r, domain.ErrForbidden)
		return
	}
	if len(r.URL.Path) <= len(base) {
		w.WriteHeader(405)
		return
	}
	action := r.URL.Path[len(base)+1:]
	if meta := securityMeta(r); meta != nil {
		meta.Action = "account.security_change"
		meta.ClientID = session.ClientID
		meta.SubjectRef = user.ID
		meta.Success = true
	}
	switch action {
	case "mfa/replace":
		if !s.limit(w, r, "mfa-user:"+a.ID+":"+user.ID, 5, time.Minute) {
			return
		}
		handle := secure.Random(32)
		flow, err := s.Service.BeginMFAReplacement(r.Context(), scope, cookie.Value, r.Form.Get("code"), handle)
		if err != nil {
			problem(w, r, err)
			return
		}
		s.cookie(w, "av_flow_"+flow.ID, handle, "/a/"+a.Key, 600)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		loginTemplate.Execute(w, loginPage{App: a, Flow: flowPresentation{Step: flow.Step}, Secret: flow.PendingTOTP, CSRF: flow.CSRF, Base: "/a/" + a.Key + "/ui/flows/" + flow.ID})
		return
	case "mfa/recovery-codes":
		if !s.limit(w, r, "mfa-user:"+a.ID+":"+user.ID, 5, time.Minute) {
			return
		}
		codes, err := s.Service.RegenerateRecoveryCodes(r.Context(), scope, cookie.Value, r.Form.Get("code"))
		if err != nil {
			problem(w, r, err)
			return
		}
		s.cookie(w, "av_session_"+a.Key, "", "/a/"+a.Key, -1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		loginTemplate.Execute(w, loginPage{App: a, RecoveryCodes: codes, Flow: flowPresentation{Step: domain.StepMFAMaintenanceComplete}})
		return
	case "password":
		if !s.limit(w, r, "password-user:"+a.ID+":"+user.ID, 5, time.Minute) ||
			!s.limit(w, r, "password-source:"+a.ID+":"+remoteIP(r), 20, time.Minute) {
			return
		}
		e = s.Service.SelfPassword(r.Context(), scope, cookie.Value, r.Form.Get("old_password"), r.Form.Get("new_password"))
	case "erase":
		if r.Form.Get("confirm") != "DELETE" {
			e = domain.ErrInvalid
		} else {
			e = s.Service.EraseSelf(r.Context(), scope, cookie.Value)
		}
	case "identifier", "link":
		if session.AuthTime < time.Now().Add(-10*time.Minute).Unix() || session.ClientID == "" {
			problem(w, r, domain.ErrUnauthenticated)
			return
		}
		flow := domain.Flow{ID: secure.Random(32), ClientID: session.ClientID, SessionID: session.ID, UserID: user.ID, AuthTime: session.AuthTime, AuthEpoch: user.Epoch, MFATime: session.MFATime, AMR: session.AMR, Step: domain.StepChoose, Purpose: "change_identifier", Expires: time.Now().Add(10 * time.Minute).Unix(), CSRF: secure.Random(32)}
		handle := secure.Random(32)
		flow.CookieDigest = s.Service.Digest(a.ID, "flow-cookie", handle)
		if action == "link" {
			flow.Purpose = "link"
		}
		e = s.Service.Store.App(r.Context(), scope, true, func(t ports.AppTx) error { return t.PutFlow(flow) })
		if e != nil {
			break
		}
		s.cookie(w, "av_flow_"+flow.ID, handle, "/a/"+a.Key, 600)
		if action == "link" {
			s.federationStart(w, r, a, scope, flow.ID, r.Form.Get("connection_id"))
			return
		}
		if !s.limit(w, r, "account-identifier:"+a.ID+":"+user.ID, 5, time.Hour) {
			return
		}
		kind := r.Form.Get("kind")
		configured := s.EmailConfigured
		if kind == "phone" {
			configured = s.PhoneConfigured
		}
		_, e = s.Service.RequestChallenge(r.Context(), scope, flow.ID, "change_identifier", kind, r.Form.Get("destination"), configured)
		if e == nil {
			http.Redirect(w, r, a.Issuer+"/ui/flows/"+flow.ID, http.StatusSeeOther)
			return
		}
	case "unlink":
		if session.AuthTime < time.Now().Add(-10*time.Minute).Unix() {
			e = domain.ErrUnauthenticated
			break
		}
		e = s.Service.UnlinkExternal(r.Context(), scope, cookie.Value, r.Form.Get("connection_id"), s.EmailConfigured, s.PhoneConfigured)

	default:
		e = domain.ErrNotFound
	}
	if e != nil {
		problem(w, r, e)
		return
	}
	s.cookie(w, "av_session_"+a.Key, "", "/a/"+a.Key, -1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	loginTemplate.Execute(w, loginPage{App: a, Flow: flowPresentation{Step: "signed_out"}})
}
