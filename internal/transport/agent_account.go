package transport

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

var agentAccountTemplate = template.Must(template.ParseFS(loginAssets, "assets/agents.html"))

type accountAgentGrant struct {
	Grant     domain.AgentGrant
	Name      string
	Live      bool
	ExpiresAt string
}
type accountAgentPage struct {
	App                            domain.App
	Items                          []accountAgentGrant
	Next, Base, CSRF, AssetVersion string
}

func (s *Server) accountAgents(w http.ResponseWriter, r *http.Request, a domain.App, scope domain.Scope, handle, csrf string) bool {
	base := "/a/" + a.Key + "/ui/account/agents"
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		return false
	}
	if r.Method != "GET" && r.Method != "POST" {
		w.WriteHeader(405)
		return true
	}
	if r.Method == "POST" && (!s.sameOrigin(r) || r.ParseForm() != nil || !secure.Equal(csrf, r.Form.Get("_csrf"))) {
		problem(w, r, domain.ErrForbidden)
		return true
	}
	after, limit := paging(r)
	page := accountAgentPage{App: a, Base: base, CSRF: csrf, AssetVersion: loginAssetVersion, Items: []accountAgentGrant{}}
	err := s.Service.Store.App(r.Context(), scope, r.Method == "POST", func(t ports.AppTx) error {
		_, u, e := s.Service.AuthenticatedSession(t, scope, handle, false)
		if e != nil {
			return e
		}
		if r.Method == "POST" {
			if r.Form.Get("confirm") != "revoke" {
				return domain.ErrInvalid
			}
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, base+"/"), "/")
			if len(parts) != 2 || parts[1] != "revoke" {
				return domain.ErrNotFound
			}
			version, e := strconv.ParseInt(r.Form.Get("config_version"), 10, 64)
			if e != nil {
				return domain.ErrInvalid
			}
			_, e = s.Service.RevokeAgentGrant(t, u.ID, parts[0], application.AgentVersionedReason{Version: version, Reason: "user_revoked_from_account"}, u.ID)
			return e
		}
		grants, e := t.AgentGrants(domain.AgentFilter{After: after, Limit: limit + 1, UserID: u.ID})
		if e != nil {
			return e
		}
		if len(grants) > limit {
			page.Next = grants[limit-1].ID
			grants = grants[:limit]
		}
		names := map[string]string{}
		for _, g := range grants {
			if names[g.AgentID] == "" {
				agent, e := t.Agent(g.AgentID, false)
				if e != nil {
					return e
				}
				names[g.AgentID] = agent.Name
			}
			page.Items = append(page.Items, accountAgentGrant{Grant: g, Name: names[g.AgentID], Live: g.Status == "active" && g.Expires > s.Service.Now().Unix(), ExpiresAt: time.Unix(g.Expires, 0).UTC().Format(time.RFC3339)})
		}
		return nil
	})
	if err != nil {
		problem(w, r, err)
		return true
	}
	if r.Method == "POST" {
		http.Redirect(w, r, base, http.StatusSeeOther)
		return true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	agentAccountTemplate.Execute(w, page)
	return true
}
