package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func TestIdentityReviewRegression(t *testing.T) {
	configured := false
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(dialect, func(t *testing.T) {
			for _, kind := range []string{"email", "phone"} {
				for _, scenario := range []string{"normal", "reallocated", "identifier-ABA", "epoch", "unbound", "late-registration", "legacy-verified"} {
					t.Run("login-proof/"+kind+"/"+scenario, func(t *testing.T) {
						f := setup(t, dialect, dsn)
						ctx := context.Background()
						value := f.user1.Email
						if kind == "phone" {
							value = "+8613800138000"
							if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
								u, e := tx.User(f.user1.ID, true)
								if e != nil {
									return e
								}
								u.Phone = value
								if e = tx.ReplaceIdentifier(u.ID, kind, f.service.Digest(f.app1.ID, "identifier:"+kind, ""), f.service.Digest(f.app1.ID, "identifier:"+kind, value)); e != nil {
									return e
								}
								return tx.PutUser(u)
							}); e != nil {
								t.Fatal(e)
							}
						}
						if scenario == "late-registration" {
							if kind == "email" {
								value = "later@example.test"
							} else {
								value = "+8613800138009"
							}
						}
						flow := f.securityFlow(t, "authorize")
						var message domain.Message
						s := *f.service
						s.Store = challengeCaptureStore{Store: f.store, message: &message}
						id, e := s.RequestChallenge(ctx, f.scope1, flow.ID, "login", kind, value, true)
						if e != nil {
							t.Fatal(e)
						}
						code := strings.Split(strings.TrimPrefix(message.Body, "Authova verification code: "), ".")[0]
						if scenario != "normal" && scenario != "legacy-verified" {
							e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
								if scenario == "unbound" {
									flow, e := tx.Flow(flow.ID, true)
									if e != nil {
										return e
									}
									flow.UserID = ""
									flow.AuthEpoch = 0
									return tx.PutFlow(flow)
								}
								if scenario == "late-registration" {
									u := domain.User{ID: application.ID(), AppID: f.app1.ID, Status: "active", Epoch: 1, CreatedAt: time.Now()}
									if kind == "email" {
										u.Email = value
									} else {
										u.Phone = value
									}
									return tx.CreateUser(u, kind, s.Digest(f.app1.ID, "identifier:"+kind, value))
								}
								u, e := tx.User(f.user1.ID, true)
								if e != nil {
									return e
								}
								u.Epoch++
								if scenario != "epoch" {
									next := "replacement@example.test"
									if kind == "phone" {
										next = "+8613800138001"
										u.Phone = next
									} else {
										u.Email = next
									}
									if e = tx.ReplaceIdentifier(u.ID, kind, s.Digest(f.app1.ID, "identifier:"+kind, value), s.Digest(f.app1.ID, "identifier:"+kind, next)); e != nil {
										return e
									}
									if scenario == "identifier-ABA" {
										if e = tx.ReplaceIdentifier(u.ID, kind, s.Digest(f.app1.ID, "identifier:"+kind, next), s.Digest(f.app1.ID, "identifier:"+kind, value)); e != nil {
											return e
										}
										if kind == "email" {
											u.Email = value
										} else {
											u.Phone = value
										}
									}
								}
								if e = tx.PutUser(u); e != nil {
									return e
								}
								if e = tx.RevokeSessions(u.ID); e != nil {
									return e
								}
								if scenario == "reallocated" {
									other := domain.User{ID: application.ID(), AppID: f.app1.ID, Status: "active", Epoch: 1, CreatedAt: time.Now()}
									if kind == "email" {
										other.Email = value
									} else {
										other.Phone = value
									}
									return tx.CreateUser(other, kind, s.Digest(f.app1.ID, "identifier:"+kind, value))
								}
								return nil
							})
							if e != nil {
								t.Fatal(e)
							}
						}
						e = s.VerifyChallenge(ctx, f.scope1, flow.ID, id, code)
						if scenario != "normal" && scenario != "legacy-verified" {
							if !errors.Is(e, domain.ErrUnauthenticated) {
								t.Fatalf("stale proof: %v", e)
							}
							if _, e = s.Complete(ctx, f.scope1, flow.ID); e == nil {
								t.Fatal("stale proof minted session")
							}
							return
						}
						if e != nil {
							t.Fatal(e)
						}
						if scenario == "legacy-verified" {
							if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
								old, e := tx.Flow(flow.ID, true)
								if e != nil {
									return e
								}
								old.LoginProofVersion = 0
								return tx.PutFlow(old)
							}); e != nil {
								t.Fatal(e)
							}
							if _, e = s.Complete(ctx, f.scope1, flow.ID); !errors.Is(e, domain.ErrUnauthenticated) {
								t.Fatal("pre-upgrade verified OTP flow remained acceptable", e)
							}
							return
						}
						handle, e := s.Complete(ctx, f.scope1, flow.ID)
						if e != nil {
							t.Fatal(e)
						}
						if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
							_, u, e := s.AuthenticatedSession(tx, f.scope1, handle, false)
							if e == nil && u.ID != f.user1.ID {
								t.Fatal("wrong final session owner")
							}
							return e
						}); e != nil {
							t.Fatal(e)
						}
						if e = s.VerifyChallenge(ctx, f.scope1, flow.ID, id, code); e == nil {
							t.Fatal("OTP replay accepted")
						}
					})
				}
			}
			t.Run("userinfo-current-mfa", func(t *testing.T) {
				f := setup(t, dialect, dsn)
				code, v := f.code()
				status, tok := f.exchange(code, v)
				if status != 200 {
					t.Fatal(status)
				}
				userinfo := func() int {
					req, _ := http.NewRequest("GET", f.app1.Issuer+"/userinfo", nil)
					req.Header.Set("Authorization", "Bearer "+tok.Access)
					res, e := f.http.Do(req)
					if e != nil {
						t.Fatal(e)
					}
					defer res.Body.Close()
					b, _ := io.ReadAll(res.Body)
					if res.StatusCode == 200 {
						var info struct {
							Sub string `json:"sub"`
						}
						if e = json.Unmarshal(b, &info); e != nil || info.Sub != f.user1.ID {
							t.Fatal("wrong userinfo", e)
						}
					}
					return res.StatusCode
				}
				if !f.active(tok.Access) || userinfo() != 200 {
					t.Fatal("normal token rejected")
				}
				if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
					a, e := tx.App()
					if e != nil {
						return e
					}
					a.RequireMFA = true
					a.Version++
					return tx.UpdateAppConfig(a)
				}); e != nil {
					t.Fatal(e)
				}
				if f.active(tok.Access) || userinfo() == 200 {
					t.Fatal("old non-MFA token remained usable")
				}
				if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
					sessions, e := tx.Sessions(f.user1.ID, "", 100)
					if e != nil {
						return e
					}
					for _, s := range sessions {
						s.MFATime = time.Now().Unix()
						if e = tx.PutSession(s); e != nil {
							return e
						}
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
				if !f.active(tok.Access) || userinfo() != 200 {
					t.Fatal("valid MFA proof rejected")
				}
			})
			t.Run("last-passkey-profile-is-not-alias", func(t *testing.T) {
				f := passkeyFixture(t, dialect, dsn)
				handle := f.passkeySession(t)
				key := f.enroll(t, handle, newAuthenticator(t, f.service.BaseURL))
				if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
					u, e := tx.User(f.user1.ID, true)
					if e != nil {
						return e
					}
					u.PasswordHash = ""
					if e = tx.PutUser(u); e != nil {
						return e
					}
					return tx.EraseIdentifiers(u.ID)
				}); e != nil {
					t.Fatal(e)
				}
				if e := f.service.DeletePasskey(context.Background(), f.scope1, handle, key.ID, key.Version, true, true); !errors.Is(e, domain.ErrConflict) {
					t.Fatalf("last usable credential deleted: %v", e)
				}
				if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
					return tx.ReplaceIdentifier(f.user1.ID, "email", f.service.Digest(f.app1.ID, "identifier:email", ""), f.service.Digest(f.app1.ID, "identifier:email", f.user1.Email))
				}); e != nil {
					t.Fatal(e)
				}
				if e := f.service.DeletePasskey(context.Background(), f.scope1, handle, key.ID, key.Version, true, true); e != nil {
					t.Fatal("valid alternative rejected", e)
				}
			})
			t.Run("all-otp-entrypoints-share-app-budget", func(t *testing.T) {
				f := setup(t, dialect, dsn)
				ctx := context.Background()
				handle := f.passkeySession(t)
				base, _ := url.Parse(f.app1.Issuer)
				f.http.Jar.SetCookies(base, []*http.Cookie{{Name: "av_session_" + f.app1.Key, Value: handle, Path: "/a/" + f.app1.Key}})
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					a, e := tx.App()
					if e != nil {
						return e
					}
					l := domain.DefaultResourceLimits()
					l.EmailChallengesPerHour = 1
					a.Resources = &l
					a.Version++
					return tx.UpdateAppConfig(a)
				}); e != nil {
					t.Fatal(e)
				}
				var message domain.Message
				f.service.Store = challengeCaptureStore{Store: f.store, message: &message}
				flow := f.securityFlow(t, "authorize")
				if _, e := f.service.RequestChallenge(ctx, f.scope1, flow.ID, "login", "email", f.user1.Email, true); e != nil {
					t.Fatal(e)
				}
				first := message.ID
				status, _ := f.post(f.app1.Issuer+"/ui/account/identifier", url.Values{"_csrf": {f.service.Digest(f.app1.ID, "account-csrf", handle)}, "kind": {"email"}, "destination": {"new@example.test"}}, false)
				if status != 429 || message.ID != first {
					t.Fatalf("account bypassed budget status=%d newMessage=%v", status, message.ID != first)
				}
			})
		})
	}
	if !configured {
		t.Skip("real PG/MySQL required")
	}
}
