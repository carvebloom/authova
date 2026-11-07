package transport

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/resource"
)

func (s *Server) query(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	out, err := s.queryResult(w, r, parts[1], func() (application.QueryRequest, error) { return queryRequest(r, parts[3:]) })
	if errors.Is(err, errResponseWritten) {
		return
	}
	if err != nil {
		problem(w, r, err)
		return
	}
	jsonReply(w, 200, out)
}

var errResponseWritten = errors.New("HTTP response already written")

// queryResult keeps admission, current authority checks and the application read
// in one shared path for generated canonical bindings and legacy URL fallbacks.
func (s *Server) queryResult(w http.ResponseWriter, r *http.Request, appID string, input func() (application.QueryRequest, error)) (any, error) {
	scope, err := domain.AppScope(appID)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	if meta := securityMeta(r); meta != nil {
		meta.AppID = scope.ID()
		meta.Action = "agent.query"
		meta.Success = true
	}
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return nil, domain.ErrUnauthenticated
	}
	q, err := input()
	if err != nil {
		return nil, err
	}
	if meta := securityMeta(r); meta != nil {
		meta.Action = "agent.query." + q.Kind
		meta.SubjectRef = s.Service.Digest(scope.ID(), "query-subject", q.Kind+":"+q.ID+":"+q.UserID)
	}
	if !s.limit(w, r, "query-ip:"+remoteIP(r), 300, time.Minute) {
		return nil, errResponseWritten
	}
	r = r.WithContext(resource.WithApp(r.Context(), scope.ID()))
	var out any
	err = s.Provider.ReadQuery(r.Context(), scope, bearer(r), q.Kind, func(t ports.AppTx, p domain.QueryPrincipal) error {
		if meta := securityMeta(r); meta != nil {
			meta.ClientID = p.ClientID
		}
		var e error
		out, e = s.Service.ReadQuery(t, p, q)
		return e
	})
	if meta := securityMeta(r); meta != nil {
		meta.Reason = securityFailure(err)
	}
	if err != nil {
		return nil, err
	}
	if q.Kind == "configuration" {
		config := out.(map[string]any)
		config["email_otp_configured"] = s.EmailConfigured
		config["phone_otp_configured"] = s.PhoneConfigured
	}
	return out, nil
}

func queryRequest(r *http.Request, parts []string) (application.QueryRequest, error) {
	q := application.QueryRequest{Limit: 20}
	if len(parts) == 0 {
		return q, domain.ErrNotFound
	}
	q.Kind = parts[0]
	if len(parts) == 2 {
		q.ID = parts[1]
		switch q.Kind {
		case "users":
			q.Kind = "user"
		case "agents":
			q.Kind = "agent"
		case "installations":
			q.Kind = "installation"
		case "grants":
			q.Kind = "grant"
		default:
			return q, domain.ErrNotFound
		}
	} else if len(parts) == 3 && parts[0] == "users" && parts[2] == "sessions" {
		q.Kind = "sessions"
		q.UserID = parts[1]
	} else if len(parts) != 1 {
		return q, domain.ErrNotFound
	}
	if capability, _ := domain.QueryCapability(q.Kind); capability == "" {
		return q, domain.ErrNotFound
	}
	values, err := r.URL.Query(), error(nil)
	if r.URL.RawQuery != "" {
		values, err = url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return q, domain.ErrInvalid
		}
	}
	for k, vs := range values {
		if len(vs) != 1 {
			return q, domain.ErrInvalid
		}
		switch k {
		case "cursor":
			q.Cursor = vs[0]
		case "limit":
			q.Limit, err = strconv.Atoi(vs[0])
			if err != nil || q.Limit < 1 || q.Limit > 100 {
				return q, domain.ErrInvalid
			}
		case "tenant_id":
			q.TenantID = vs[0]
		case "agent_id":
			q.AgentID = vs[0]
		case "installation_id":
			if q.Kind != "grants" {
				return q, domain.ErrInvalid
			}
			q.InstallationID = vs[0]
		case "user_id":
			if q.Kind == "sessions" {
				return q, domain.ErrInvalid
			}
			q.UserID = vs[0]
		default:
			return q, domain.ErrInvalid
		}
		if k != "cursor" && k != "limit" && q.Kind != "installations" && q.Kind != "grants" {
			return q, domain.ErrInvalid
		}
	}
	return q, nil
}
