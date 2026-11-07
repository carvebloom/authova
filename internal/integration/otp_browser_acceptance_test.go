package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func TestOTPBrowserFixture(t *testing.T) {
	dir := os.Getenv("AUTHOVA_BROWSER_ACCEPTANCE_DIR")
	if dir == "" {
		t.Skip("explicit private browser acceptance directory required")
	}
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			t.Fatal("real disposable DB required")
		}
		t.Run(driver, func(t *testing.T) {
			f := setupChannels(t, driver, dsn, false, true, func(core http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/test-callback" {
						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						w.Write([]byte("<!doctype html><html lang=zh-CN><meta charset=utf-8><title>Authova 验收</title><main><h1>注册与授权已完成</h1><p>返回的 Code 将由独立测试进程执行 PKCE 交换与在线内省。</p></main></html>"))
						return
					}
					core.ServeHTTP(w, r)
				})
			})
			kind, value := "email", "browser-otp@example.test"
			if driver == "mysql" {
				kind, value = "phone", "+8613800138099"
			}
			if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
				a, e := tx.App()
				if e != nil {
					return e
				}
				p := domain.AuthenticationPolicy{RegistrationEnabled: true}
				if kind == "email" {
					p.EmailOTPEnabled = true
				} else {
					p.PhoneOTPEnabled = true
				}
				a.Policy = &p
				a.Version++
				return tx.UpdateAppConfig(a)
			}); e != nil {
				t.Fatal(e)
			}
			otpFile := filepath.Join(dir, driver+"-otp.private")
			f.service.Store = browserCaptureStore{Store: f.store, file: otpFile}
			verifier := secure.Random(32)
			hash := sha256.Sum256([]byte(verifier))
			state := secure.Random(16)
			query := url.Values{"client_id": {f.client.ClientID}, "redirect_uri": {f.server.URL + "/test-callback"}, "response_type": {"code"}, "scope": {"openid email profile offline_access"}, "state": {state}, "nonce": {secure.Random(16)}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
			done := filepath.Join(dir, driver+"-done.private")
			info := map[string]string{"url": f.app1.Issuer + "/authorize?" + query.Encode(), "kind": kind, "destination": value, "otp_file": otpFile, "done_file": done}
			data, _ := json.Marshal(info)
			if e := os.WriteFile(filepath.Join(dir, driver+"-browser.private.json"), data, 0600); e != nil {
				t.Fatal(e)
			}
			timeout := time.NewTimer(5 * time.Minute)
			defer timeout.Stop()
			poll := time.NewTicker(100 * time.Millisecond)
			defer poll.Stop()
			for {
				select {
				case <-timeout.C:
					t.Fatal("browser acceptance did not finish")
				case <-poll.C:
					data, e := os.ReadFile(done)
					if os.IsNotExist(e) {
						continue
					}
					if e != nil {
						t.Fatal(e)
					}
					callback, e := url.Parse(string(data))
					if e != nil || callback.Query().Get("state") != state || callback.Query().Get("code") == "" {
						t.Fatal("invalid browser callback")
					}
					status, tok := f.exchange(callback.Query().Get("code"), verifier)
					if status != 200 || !f.active(tok.Access) {
						t.Fatal("browser registration did not produce valid Code/PKCE/token")
					}
					if e = f.store.App(context.Background(), f.scope1, false, func(tx ports.AppTx) error {
						u, e := tx.FindUser(kind, f.service.Digest(f.app1.ID, "identifier:"+kind, value))
						if e == nil && (u.PasswordHash != "" || u.Status != "active") {
							t.Fatal("wrong OTP-only user")
						}
						return e
					}); e != nil {
						t.Fatal(e)
					}
					t.Log("real browser registration, Code/PKCE and SQL-backed introspection passed")
					return
				}
			}
		})
	}
}

type browserCaptureStore struct {
	ports.Store
	file string
}

func (s browserCaptureStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	var message domain.Message
	err := s.Store.App(ctx, scope, write, func(tx ports.AppTx) error { return fn(challengeCaptureTx{AppTx: tx, message: &message}) })
	if err == nil && message.Body != "" {
		code := strings.Split(strings.TrimPrefix(message.Body, "Authova verification code: "), ".")[0]
		err = os.WriteFile(s.file, []byte(code), 0600)
	}
	return err
}
