package transport

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

type passwordAdmissionStore struct {
	ports.Store
	tx *passwordAdmissionTx
}
type passwordAdmissionTx struct {
	ports.PlatformTx
	t *testing.T
}

func (s passwordAdmissionStore) Platform(_ context.Context, _ bool, fn func(ports.PlatformTx) error) error {
	return fn(s.tx)
}
func (p *passwordAdmissionTx) ManagementSession(string) (domain.ManagementSession, error) {
	return domain.ManagementSession{OperatorID: "owner", Expires: time.Now().Add(time.Hour).Unix(), MFATime: time.Now().Unix()}, nil
}
func (p *passwordAdmissionTx) Operator(string, bool) (domain.Operator, error) {
	return domain.Operator{ID: "owner", Role: "owner", Status: "active"}, nil
}
func (p *passwordAdmissionTx) CountOperators() (int, error) {
	p.t.Fatal("malformed request reached mutation")
	return 0, nil
}

func TestOperatorPasswordPatchRejectsMixedOperations(t *testing.T) {
	vault, err := secure.NewVault(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := &application.Service{Store: passwordAdmissionStore{tx: &passwordAdmissionTx{t: t}}, Vault: vault}
	handler := (&Server{Service: service}).protoQueryRouter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("generated operator route bypassed") }))
	for _, body := range []string{
		`{"password":"SECRET-CANARY-LONG","reason":"rotation"}`,
		`{"current_password":"SECRET-CANARY-LONG","reason":"rotation"}`,
		`{"password":"SECRET-CANARY-LONG","current_password":"old","role":"owner","reason":"rotation"}`,
		`{"password":"SECRET-CANARY-LONG","current_password":"old","status":"active","reason":"rotation"}`,
		`{"role":"admin","reason":"rotation"}`,
		`{"status":"disabled","reason":"rotation"}`,
		`{"password":"SECRET-CANARY-LONG","current_password":"old","reason":" "}`,
	} {
		r := httptest.NewRequest("PATCH", "/api/v1/platform/operators/019acc73-b8b8-710e-a933-b6fd041fc075", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer session")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "SECRET-CANARY") {
			t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
		}
	}
}
