package protocol

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

// browserCORS is App-bound and derives origins only from active public clients.
// It never enables credentialed CORS or changes authorization/PKCE validation.
func (p *Provider) browserCORS(w http.ResponseWriter, r *http.Request, app domain.App, path string) bool {
	method := ""
	switch path {
	case "/.well-known/openid-configuration", "/keys":
		method = http.MethodGet
	case "/oauth/token", "/revoke":
		method = http.MethodPost
	case "/userinfo":
		method = r.Method
		if method == http.MethodOptions {
			method = r.Header.Get("Access-Control-Request-Method")
		}
		if method != "GET" && method != "POST" {
			method = ""
		}
	default:
		return false
	}
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	// Same-origin requests use normal browser security and existing endpoint checks.
	if origin == p.service.BaseURL {
		return false
	}
	allowed := false
	scope, err := domain.AppScope(app.ID)
	if err == nil {
		err = p.service.Store.App(r.Context(), scope, false, func(t ports.AppTx) error {
			after := ""
			for {
				clients, err := t.Clients(after, 100)
				if err != nil {
					return err
				}
				for _, c := range clients {
					if c.Kind != "public" || c.Status != "active" || c.System {
						continue
					}
					for _, redirect := range c.RedirectURIs {
						u, err := url.Parse(redirect)
						if err == nil && u.User == nil && u.Scheme+"://"+u.Host == origin {
							allowed = true
							return nil
						}
					}
				}
				if len(clients) < 100 {
					return nil
				}
				after = clients[len(clients)-1].ID
			}
		})
	}
	if err != nil {
		failure(w, 503, "server_error")
		return true
	}
	requested := r.Method
	if requested == http.MethodOptions {
		requested = r.Header.Get("Access-Control-Request-Method")
	}
	if !allowed || method == "" || requested != method {
		failure(w, 403, "invalid_request")
		return true
	}
	if r.Method == http.MethodOptions {
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
		for _, h := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			switch strings.ToLower(strings.TrimSpace(h)) {
			case "", "content-type", "authorization", "accept":
			default:
				failure(w, 403, "invalid_request")
				return true
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", method)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept")
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	return false
}
