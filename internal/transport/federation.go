package transport

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/federation"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	"golang.org/x/oauth2"
)

type federationState struct {
	Connection, StateDigest, Nonce, Verifier, Issuer string
	Used                                             bool
	Version                                          int64
}

func (s *Server) federationStart(w http.ResponseWriter, r *http.Request, app domain.App, scope domain.Scope, flow, connection string) {
	if meta := securityMeta(r); meta != nil {
		meta.Action = "authentication.federation_start"
		meta.Success = true
	}
	var c domain.Connection
	e := s.Service.Store.App(r.Context(), scope, false, func(t ports.AppTx) error {
		f, err := t.Flow(flow, false)
		if err != nil {
			return err
		}
		if err = application.LiveFlow(f); err != nil {
			return err
		}
		if err = application.CheckAuthenticationPolicy(t, f, "federated", "login"); err != nil {
			return err
		}
		c, err = t.Connection(connection)
		return err
	})
	if e != nil {
		problem(w, r, e)
		return
	}
	if c.Status != "active" {
		problem(w, r, domain.ErrNotFound)
		return
	}
	upstream, e := federation.New(c.ProviderType, s.Service.UpstreamClient)
	if e != nil {
		problem(w, r, e)
		return
	}
	state := secure.Random(32)
	nonce := secure.Random(32)
	verifier := oauth2.GenerateVerifier()
	destination, e := upstream.Authorization(r.Context(), c, app.Issuer+"/ui/federation/callback", flow+"."+state, nonce, verifier)
	if e != nil {
		problem(w, r, e)
		return
	}
	data, _ := json.Marshal(federationState{Connection: c.ID, StateDigest: s.Service.Digest(app.ID, "federation-state", state), Nonce: nonce, Verifier: verifier, Issuer: c.Issuer, Version: c.Version})
	e = s.Service.Store.App(r.Context(), scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = application.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "choose_method" {
			return domain.ErrConflict
		}
		if e = application.CheckAuthenticationPolicy(t, f, "federated", "login"); e != nil {
			return e
		}
		f.Step = domain.StepFederation
		f.Federation = data
		return t.PutFlow(f)
	})
	if e != nil {
		problem(w, r, e)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	federationNavigation.Execute(w, federationPresentation{App: app, URL: destination, AssetVersion: loginAssetVersion})

}
func (s *Server) federationCallback(w http.ResponseWriter, r *http.Request, app domain.App, scope domain.Scope) {
	if meta := securityMeta(r); meta != nil {
		meta.Action = "authentication.federation"
		meta.Success = true
	}
	parts := strings.SplitN(r.URL.Query().Get("state"), ".", 2)
	if len(parts) != 2 || len(parts[0]) != 43 {
		problem(w, r, domain.ErrForbidden)
		return
	}
	flow := parts[0]
	cookie, e := r.Cookie("av_flow_" + flow)
	if e != nil {
		problem(w, r, domain.ErrForbidden)
		return
	}
	var fs federationState
	var c domain.Connection
	e = s.Service.Store.App(r.Context(), scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = application.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "federation" || !secure.Equal(f.CookieDigest, s.Service.Digest(app.ID, "flow-cookie", cookie.Value)) {
			return domain.ErrForbidden
		}
		if json.Unmarshal(f.Federation, &fs) != nil || fs.Used || !secure.Equal(fs.StateDigest, s.Service.Digest(app.ID, "federation-state", parts[1])) {
			return domain.ErrForbidden
		}
		c, e = t.Connection(fs.Connection)
		if e != nil {
			return e
		}
		if c.Status != "active" || c.Issuer != fs.Issuer || c.Version != fs.Version {
			return domain.ErrForbidden
		}
		if e = application.CheckAuthenticationPolicy(t, f, "federated", "login"); e != nil {
			return e
		}
		fs.Used = true
		f.Federation, _ = json.Marshal(fs)
		return t.PutFlow(f)
	})
	if e != nil {
		problem(w, r, e)
		return
	}
	if r.URL.Query().Get("error") != "" {
		problem(w, r, domain.ErrUnauthenticated)
		return
	}
	upstream, e := federation.New(c.ProviderType, s.Service.UpstreamClient)
	if e != nil {
		problem(w, r, e)
		return
	}
	code := r.URL.Query().Get("code")
	if c.ProviderType == "alipay" {
		query := r.URL.Query()
		if len(query["auth_code"]) != 1 || len(query["app_id"]) != 1 || query.Get("app_id") != c.ClientID || query.Has("code") {
			problem(w, r, domain.ErrUnauthenticated)
			return
		}
		code = query.Get("auth_code")
	}
	identity, e := upstream.Authenticate(r.Context(), c, app.Issuer+"/ui/federation/callback", code, fs.Nonce, fs.Verifier)
	if e != nil {
		problem(w, r, e)
		return
	}

	e = s.Service.Store.App(r.Context(), scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = application.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "federation" {
			return domain.ErrConflict
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		if a.Status != "active" {
			return domain.ErrForbidden
		}
		// Lock user facts before provider config, matching App management writes.
		// The shared config lock protects credential writes without serializing other users.
		checkConfig := func() error {
			current, e := t.ShareConnection(c.ID)
			if e != nil {
				return e
			}
			if current.Status != "active" || current.Version != c.Version {
				return domain.ErrForbidden
			}
			return nil
		}

		if e := application.CheckAuthenticationPolicy(t, f, "federated", "login"); e != nil {
			return e
		}
		if f.Purpose == "link" {
			session, e := t.Session(f.SessionID, false)
			if e != nil {
				return e
			}
			u, e := t.User(f.UserID, true)
			if e != nil {
				return e
			}
			if u.Status != "active" || session.ExpiresAt.Unix() <= time.Now().Unix() || session.Status != "active" || session.Epoch != u.Epoch || f.AuthEpoch != u.Epoch || session.UserID != u.ID || session.AuthTime < time.Now().Add(-10*time.Minute).Unix() || f.AuthTime < time.Now().Add(-10*time.Minute).Unix() {
				return domain.ErrUnauthenticated
			}
			if e = checkConfig(); e != nil {
				return e
			}
			digest := s.Service.Digest(app.ID, "external:"+c.ID, c.Issuer+"\x00"+identity.Subject)
			existing, err := t.ExternalUser(c.ID, digest)
			if err == nil && existing != u.ID {
				return domain.ErrConflict
			}
			if errors.Is(err, domain.ErrNotFound) {
				if e = t.BindExternal(c.ID, digest, u.ID); e != nil {
					return e
				}
			} else if err != nil {
				return err
			}
			f.Completed = true
			f.Step = domain.StepAccountComplete
			if e = t.PutFlow(f); e != nil {
				return e
			}
			return t.Audit(event(app.ID, u.ID, "user.external_link", c.ID))
		}
		digest := s.Service.Digest(app.ID, "external:"+c.ID, c.Issuer+"\x00"+identity.Subject)
		user, e := t.ExternalUser(c.ID, digest)
		if errors.Is(e, domain.ErrNotFound) {
			u := domain.User{ID: application.ID(), AppID: app.ID, DisplayName: identity.Name, Status: "active", Epoch: 1, CreatedAt: time.Now().UTC()}
			if identity.EmailVerified {
				if v, e := secure.Normalize("email", identity.Email); e == nil {
					u.Email = v
				}
			}
			f.Registered = true
			if e = application.CheckAuthenticationPolicy(t, f, "federated", "register"); e != nil {
				return e
			}
			if e = t.CreateUser(u, "", ""); e != nil {
				return e
			}
			if e = checkConfig(); e != nil {
				return e
			}
			if e = t.BindExternal(c.ID, digest, u.ID); e != nil {
				return e
			}
			user = u.ID
		} else if e != nil {
			return e
		}
		u, e := t.User(user, true)
		if e != nil {
			return e
		}
		if e = checkConfig(); e != nil {
			return e
		}
		if u.Status != "active" {
			return domain.ErrForbidden
		}
		f.UserID = u.ID
		f.AuthEpoch = u.Epoch
		f.AuthTime = time.Now().Unix()
		f.AMR = []string{"federated"}
		f.PrimaryMethod = "federated"
		f.Step = domain.StepConsent
		client, e := t.Client(f.ClientID)
		if e != nil {
			return e
		}
		if a.RequireMFA || u.TOTPSecret != "" || client.System {
			f.Step = domain.StepMFA
		}
		f.CSRF = secure.Random(32)
		if e = t.PutFlow(f); e != nil {
			return e
		}
		if meta := securityMeta(r); meta != nil {
			meta.SubjectRef = u.ID
			meta.ClientID = f.ClientID
		}
		return t.Audit(event(app.ID, u.ID, "user.federation", c.ID))
	})
	if e != nil {
		problem(w, r, e)
		return
	}
	http.Redirect(w, r, app.Issuer+"/ui/flows/"+url.PathEscape(flow), http.StatusSeeOther)
}

var federationNavigation = template.Must(template.ParseFS(loginAssets, "assets/federation.html"))
