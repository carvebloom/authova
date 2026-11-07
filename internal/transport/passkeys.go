package transport

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

type passkeyFinish struct {
	CeremonyID string          `json:"ceremony_id"`
	Credential json.RawMessage `json:"credential"`
}

func (s *Server) flowPasskey(w http.ResponseWriter, r *http.Request, a domain.App, scope domain.Scope, flow string, parts []string) {
	if len(parts) != 5 || (parts[3] != "login" && parts[3] != "register") || (parts[4] != "begin" && parts[4] != "finish") {
		problem(w, r, domain.ErrNotFound)
		return
	}
	if meta := securityMeta(r); meta != nil {
		meta.Action = "authentication.passkey_" + parts[3] + "_" + parts[4]
		meta.Success = true
	}
	if parts[4] == "begin" {
		var in struct {
			Name    string `json:"name"`
			Display string `json:"display_name"`
		}
		if err := decode(r, &in); err != nil {
			problem(w, r, err)
			return
		}
		var id string
		var options any
		var err error
		if parts[3] == "login" {
			id, options, err = s.Service.BeginPasskeyLogin(r.Context(), scope, flow)
		} else {
			id, options, err = s.Service.BeginPasskeyRegistration(r.Context(), scope, "", flow, in.Name, in.Display)
		}
		if err != nil {
			problem(w, r, application.PasskeyError(err))
			return
		}
		jsonReply(w, 200, map[string]any{"ceremony_id": id, "options": options})
		return
	}
	var in passkeyFinish
	if decode(r, &in) != nil || len(in.CeremonyID) != 43 {
		problem(w, r, domain.ErrInvalid)
		return
	}
	var err error
	if parts[3] == "login" {
		err = s.Service.FinishPasskeyLogin(r.Context(), scope, flow, in.CeremonyID, in.Credential)
	} else {
		err = s.Service.FinishPasskeyRegistration(r.Context(), scope, "", flow, in.CeremonyID, in.Credential)
	}
	if err != nil {
		problem(w, r, application.PasskeyError(err))
		return
	}
	jsonReply(w, 200, map[string]string{"redirect": a.Issuer + "/ui/flows/" + flow})
}

func (s *Server) accountPasskeys(w http.ResponseWriter, r *http.Request, a domain.App, scope domain.Scope, handle, csrf string, user domain.User) bool {
	base := "/a/" + a.Key + "/ui/account/passkeys"
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		return false
	}
	if r.Method == "GET" && r.URL.Path == base {
		items := []map[string]any{}
		enabled := false
		err := s.Service.Store.App(r.Context(), scope, false, func(t ports.AppTx) error {
			_, current, e := s.Service.AuthenticatedSession(t, scope, handle, false)
			if e != nil {
				return e
			}
			app, e := t.App()
			if e != nil {
				return e
			}
			enabled = app.AuthenticationPolicy().PasskeyEnabled
			keys, e := t.Passkeys(current.ID)
			if e != nil {
				return e
			}
			for _, key := range keys {
				var used any
				if key.Version > 1 {
					used = key.UpdatedAt
				}
				items = append(items, map[string]any{"id": key.ID, "name": key.Name, "config_version": key.Version, "created_at": key.CreatedAt, "last_used_at": used})
			}
			return nil
		})
		if err != nil {
			problem(w, r, err)
			return true
		}
		jsonReply(w, 200, map[string]any{"enabled": enabled, "available": s.Service.PasskeyAvailable(a), "items": items})
		return true
	}
	if !s.sameOrigin(r) || !secure.Equal(csrf, r.Header.Get("X-CSRF-Token")) {
		problem(w, r, domain.ErrForbidden)
		return true
	}
	if !s.limit(w, r, "passkey-account:"+a.ID+":"+user.ID, 10, time.Minute) {
		return true
	}
	if meta := securityMeta(r); meta != nil {
		meta.Action = "account.passkey_change"
		meta.SubjectRef = user.ID
		meta.Success = true
	}
	var err error
	switch {
	case r.Method == "POST" && r.URL.Path == base+"/register/begin":
		var in struct {
			Name string `json:"name"`
		}
		if err = decode(r, &in); err != nil {
			break
		}
		var id string
		var options any
		id, options, err = s.Service.BeginPasskeyRegistration(r.Context(), scope, handle, "", in.Name, "")
		if err == nil {
			jsonReply(w, 200, map[string]any{"ceremony_id": id, "options": options})
			return true
		}
	case r.Method == "POST" && r.URL.Path == base+"/register/finish":
		var in passkeyFinish
		if decode(r, &in) != nil || len(in.CeremonyID) != 43 {
			err = domain.ErrInvalid
			break
		}
		err = s.Service.FinishPasskeyRegistration(r.Context(), scope, handle, "", in.CeremonyID, in.Credential)
	case r.Method == "DELETE" && len(strings.Split(strings.TrimPrefix(r.URL.Path, base+"/"), "/")) == 1 && r.URL.Path != base:
		var in struct {
			Version int64 `json:"config_version"`
		}
		if decode(r, &in) != nil || in.Version < 1 {
			err = domain.ErrInvalid
			break
		}
		err = s.Service.DeletePasskey(r.Context(), scope, handle, strings.TrimPrefix(r.URL.Path, base+"/"), in.Version, s.EmailConfigured, s.PhoneConfigured)
	default:
		err = domain.ErrNotFound
	}
	if err != nil {
		problem(w, r, application.PasskeyError(err))
		return true
	}
	jsonReply(w, 200, map[string]string{"redirect": "/a/" + a.Key + "/ui/account"})
	return true
}
