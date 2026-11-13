package protocol

import (
	"bytes"
	"errors"
	"github.com/carvebloom/authova/internal/diagnostic"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAuthProjectionDropsArbitraryMessages(t *testing.T) {
	r := httptest.NewRecorder()
	r.Code = 400
	r.Body.WriteString(`{"error":"invalid_grant","error_description":"CANARY","parent":"CANARY","metadata":{"token":"CANARY"}}`)
	projectProtocolError(r)
	if strings.Contains(r.Body.String(), "CANARY") || !strings.Contains(r.Body.String(), "invalid_grant") {
		t.Fatal("unsafe or changed code")
	}
	r = httptest.NewRecorder()
	r.Code = 302
	r.Header().Set("Location", "https://registered.example/callback?state=expected&error=invalid_request&error_description=CANARY")
	projectProtocolError(r)
	location := r.Header().Get("Location")
	if strings.Contains(location, "CANARY") || !strings.Contains(location, "state=expected") || !strings.HasPrefix(location, "https://registered.example/callback?") {
		t.Fatal("redirect projection broke binding")
	}
}

func TestRealOIDCLibraryLoggingAndErrorProjection(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(diagnostic.Handler{Next: slog.NewJSONHandler(&logs, nil)}))
	defer slog.SetDefault(old)
	r := httptest.NewRequest("POST", "https://core.example/a/app/oauth/token?password=CANARY", strings.NewReader("CANARY"))
	r = r.WithContext(diagnostic.WithRequest(r.Context(), "protocol-123"))
	r.Header.Set("Authorization", "Bearer CANARY")
	rec := httptest.NewRecorder()
	op.WriteError(rec, r, oidc.ErrInvalidGrant().WithParent(errors.New("CANARY-DSN-SQL-TOKEN")), nil)
	projectProtocolError(rec)
	if strings.Contains(rec.Body.String()+logs.String(), "CANARY") || !strings.Contains(rec.Body.String(), "invalid_grant") || !strings.Contains(logs.String(), "protocol-123") || !strings.Contains(logs.String(), "operation") {
		t.Fatal("OIDC library boundary unsafe or empty")
	}
}
