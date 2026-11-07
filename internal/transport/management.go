package transport

import (
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func page[T any](items []T, limit int, id func(T) string) domain.Page[T] {
	if items == nil {
		items = []T{}
	}
	p := domain.Page[T]{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		v := id(p.Items[len(p.Items)-1])
		p.NextCursor = &v
	}
	return p
}
func paging(r *http.Request) (string, int) {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 100 {
		n = 50
	}
	after := r.URL.Query().Get("cursor")
	if len(after) > 64 {
		after = "invalid"
	}
	return after, n
}

// Canonical management operations are registered exclusively by Proto bindings.
// This fallback retains admission/error behavior for unsupported paths and verbs.
func (s *Server) management(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/"), "/")
	if meta := securityMeta(r); meta != nil {
		meta.Action = "management.request"
		meta.Success = r.Method != "GET"
	}
	if len(parts) >= 2 && parts[0] == "platform" && parts[1] == "operators" {
		_, _, actor, err := (&inventoryService{server: s}).authorize(r.Context(), r.Method != "GET")
		if err == nil && actor.AppID != "" {
			err = domain.ErrForbidden
		}
		if err == nil {
			err = domain.ErrNotFound
		}
		problem(w, r, err)
		return
	}
	if len(parts) == 0 || parts[0] != "apps" {
		http.NotFound(w, r)
		return
	}
	if len(parts) >= 3 && parts[2] == "query" {
		s.query(w, r, parts)
		return
	}
	if len(parts) == 1 {
		if _, _, _, err := (&inventoryService{server: s}).authorize(r.Context(), r.Method != "GET"); err != nil {
			problem(w, r, err)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	call, err := (&managementService{server: s}).app(r.Context(), parts[1], r.Method != "GET")
	if err != nil {
		problem(w, r, err)
		return
	}
	if len(parts) == 2 {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	kind := parts[2]
	private := kind == "users" || kind == "audit" || kind == "agent-installations" || kind == "agent-grants" || kind == "agent-operations"
	err = s.Service.AppManage(call.ctx, bearer(r), call.scope, r.Method != "GET", private, func(_ ports.AppTx, actor domain.Operator) error {
		if strings.HasPrefix(kind, "agent") && r.Method != "GET" && actor.Role != "owner" {
			return domain.ErrForbidden
		}
		return domain.ErrNotFound
	})
	problem(w, r, err)
}

// The old API accepted a trailing slash without redirecting a write or losing
// its body. Keep that spelling while letting only generated methods execute it.
func managementSlashCompatibility(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") && strings.HasSuffix(r.URL.Path, "/") && !strings.HasSuffix(r.URL.Path, "//") {
			r = r.Clone(r.Context())
			r.URL.Path = strings.TrimSuffix(r.URL.Path, "/")
			r.URL.RawPath = ""
		}
		next.ServeHTTP(w, r)
	})
}

func event(app, actor, action, subject string, reasons ...string) domain.Audit {
	a := domain.Audit{ID: application.ID(), AppID: app, ActorID: actor, Action: action, SubjectID: subject, CreatedAt: time.Now().UTC()}
	if len(reasons) > 0 {
		a.Reason = reasons[0]
	}
	return a
}
