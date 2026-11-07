package main

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Route families are intentionally bounded. The Core remains responsible for
// each resource's method, authorization, App scope, and version constraints.
func managementRoute(path string) bool {
	for _, family := range []string{"/bff/apps", "/bff/platform/operators"} {
		if path == family || strings.HasPrefix(path, family+"/") {
			return true
		}
	}
	return false
}

func (b *bff) proxyManagement(w http.ResponseWriter, r *http.Request, s session, sessionID string) {
	if !managementRoute(r.URL.Path) {
		problem(w, 404, "not_found")
		return
	}
	switch r.Method {
	case "GET", "POST", "PATCH", "PUT", "DELETE":
	default:
		w.Header().Set("Allow", "GET, POST, PATCH, PUT, DELETE")
		problem(w, 405, "method_not_allowed")
		return
	}
	path := "/api/v1/" + strings.TrimPrefix(r.URL.Path, "/bff/")
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		problem(w, 400, "invalid_request")
		return
	}
	idempotency := r.Header.Get("Idempotency-Key")
	// Preserve an explicitly supplied key; never retry the upstream call here.
	if r.Method == "POST" && idempotency == "" {
		idempotency = random()
	}
	resp, err := b.api.Request(r.Context(), r.Method, path, s.Token, idempotency, body)
	if err != nil {
		b.proxyFailure(w, r)
		return
	}
	// A redirect, truncated body, or HTML error page is not a successful JSON
	// response. A mutation may already have committed, so its outcome is unknown.
	kind, _, mimeErr := mime.ParseMediaType(resp.ContentType)
	if resp.Status != http.StatusNoContent && (resp.Status < 200 || resp.Status >= 300 && resp.Status < 400 || mimeErr != nil || kind != "application/json" && kind != "application/problem+json" || !json.Valid(resp.Body)) {
		b.proxyFailure(w, r)
		return
	}
	if resp.Status == http.StatusUnauthorized {
		_ = b.invalidateRejectedSession(r.Context(), sessionID, s)
	}
	w.Header().Set("Content-Type", resp.ContentType)
	if resp.RequestID != "" {
		w.Header().Set("X-Request-ID", resp.RequestID)
	}
	w.WriteHeader(resp.Status)
	w.Write(resp.Body)
}

func (b *bff) proxyFailure(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		problem(w, 503, "dependency_unavailable")
	} else {
		problem(w, 503, "outcome_unknown")
	}
}
