package transport

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/securityevents"
)

type securityRequest struct {
	AppID, ClientID, SubjectRef, Action, Reason string
	Success                                     bool
}
type securityRequestKey struct{}

func securityMeta(r *http.Request) *securityRequest {
	value, _ := r.Context().Value(securityRequestKey{}).(*securityRequest)
	return value
}
func securityFailure(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, domain.ErrUnauthenticated):
		return "invalid_proof"
	case errors.Is(err, domain.ErrForbidden):
		return "forbidden"
	case errors.Is(err, domain.ErrRateLimit):
		return "rate_limited"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	case errors.Is(err, domain.ErrExpired):
		return "expired"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrCommitUnknown):
		return "outcome_unknown"
	case errors.Is(err, domain.ErrInvalid):
		return "invalid_request"
	default:
		return "dependency_unavailable"
	}
}
func (s *Server) emitSecurity(r *http.Request, id string, status int) {
	meta := securityMeta(r)
	if s.SecurityEvents == nil || meta == nil || meta.Action == "" || (status < 400 && !meta.Success) {
		return
	}
	outcome, reason := "success", meta.Reason
	if status >= 400 {
		outcome = "rejected"
		if status >= 500 {
			outcome = "unavailable"
		}
		if reason == "" {
			reason = "request_rejected"
		}
	} else if reason == "" {
		reason = "accepted"
	}
	s.SecurityEvents.Record(securityevents.Event{RequestID: id, AppID: meta.AppID, ClientID: meta.ClientID, SubjectRef: meta.SubjectRef, SourceRef: s.Service.Digest(meta.AppID, "security-source", remoteIP(r)), Action: meta.Action, Outcome: outcome, Reason: reason, CreatedAt: time.Now().UTC()})
}
func withSecurityRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, securityRequestKey{}, &securityRequest{})
}
