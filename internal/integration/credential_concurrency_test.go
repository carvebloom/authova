package integration

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/transport"
)

type reviewBarrier struct {
	reached, resume chan struct{}
	once            sync.Once
}

func (b *reviewBarrier) pause(ctx context.Context) error {
	b.once.Do(func() { close(b.reached) })
	select {
	case <-b.resume:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type reviewStore struct {
	ports.Store
	barrier  *reviewBarrier
	userLock bool
}

func (s reviewStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	return s.Store.App(ctx, scope, write, func(t ports.AppTx) error {
		if write {
			return fn(reviewTx{AppTx: t, ctx: ctx, barrier: s.barrier, userLock: s.userLock})
		}
		return fn(t)
	})
}

type reviewTx struct {
	ports.AppTx
	ctx      context.Context
	barrier  *reviewBarrier
	userLock bool
}

func (t reviewTx) LockOwners() error {
	if !t.userLock {
		if e := t.barrier.pause(t.ctx); e != nil {
			return e
		}
	}
	return t.AppTx.LockOwners()
}
func (t reviewTx) User(id string, lock bool) (domain.User, error) {
	if t.userLock && lock {
		if e := t.barrier.pause(t.ctx); e != nil {
			return domain.User{}, e
		}
	}
	return t.AppTx.User(id, lock)
}
func awaitReview(t *testing.T, b *reviewBarrier) {
	t.Helper()
	select {
	case <-b.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("probe barrier not reached")
	}
}
func TestCredentialWriteConcurrency(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			t.Log("real DB not configured: " + dialect)
			continue
		}
		t.Run(dialect, func(t *testing.T) {
			t.Run("negative-control-management-disable-rejected", func(t *testing.T) {
				f := setup(t, dialect, dsn)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				token := f.managementToken()
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					u := domain.User{ID: application.ID(), AppID: f.app1.ID, Email: "second-owner@example.test", Status: "active", Epoch: 1, CreatedAt: time.Now()}
					if e := tx.CreateUser(u, "email", f.service.Digest(f.app1.ID, "identifier:email", u.Email)); e != nil {
						return e
					}
					return tx.SetAdminRole(u.ID, "owner")
				}); e != nil {
					t.Fatal(e)
				}
				barrier := &reviewBarrier{reached: make(chan struct{}), resume: make(chan struct{})}
				copyService := *f.service
				copyService.Store = reviewStore{Store: f.store, barrier: barrier}
				done := make(chan error, 1)
				var actorStatus string
				go func() {
					done <- copyService.AppManage(ctx, token, f.scope1, true, false, func(tx ports.AppTx, o domain.Operator) error {
						actorStatus = o.Status
						return tx.Audit(domain.Audit{ID: application.ID(), AppID: f.app1.ID, ActorID: o.ID, Action: "review.probe", SubjectID: o.ID, CreatedAt: time.Now()})
					})
				}()
				awaitReview(t, barrier)
				status, _ := f.managementJSON("POST", "/users/"+f.user1.ID+"/disable", map[string]any{"reason": "review fixture disable before paused write resumes"})
				close(barrier.resume)
				err := <-done
				if status != 200 || err == nil || actorStatus != "" {
					t.Fatalf("protection failed: disable=%d write=%v actor=%s", status, err, actorStatus)
				}
				t.Log("PROTECTION_CONFIRMED: management write denied after disable; active-user role join closed the candidate race")
			})
			t.Run("reset-proof-after-contact-rebind", func(t *testing.T) {
				f := setup(t, dialect, dsn)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				handle := f.sessionFor(t, f.user1)
				var session domain.Session
				if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					var e error
					session, e = tx.SessionByHandle(f.service.Digest(f.app1.ID, "session-handle", handle))
					return e
				}); e != nil {
					t.Fatal(e)
				}
				now := time.Now().Unix()
				reset := domain.Flow{UserID: f.user1.ID, AuthEpoch: f.user1.Epoch, ID: secure.Random(32), ClientID: f.client.ClientID, Purpose: "reset_password", Step: "reset_password", VerifiedKind: "email", VerifiedValue: f.user1.Email, Expires: now + 600, AuthTime: now}
				rebound := "new-contact@example.test"
				bind := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, SessionID: session.ID, UserID: f.user1.ID, AuthEpoch: f.user1.Epoch, AuthTime: now, Purpose: "change_identifier", Step: "bind_identifier", VerifiedKind: "email", VerifiedValue: rebound, Expires: now + 600}
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					if e := tx.PutFlow(reset); e != nil {
						return e
					}
					return tx.PutFlow(bind)
				}); e != nil {
					t.Fatal(e)
				}
				barrier := &reviewBarrier{reached: make(chan struct{}), resume: make(chan struct{})}
				copyService := *f.service
				copyService.Store = reviewStore{Store: f.store, barrier: barrier}
				changed := "Review probe replacement password phrase"
				done := make(chan error, 1)
				go func() { done <- copyService.ResetPassword(ctx, f.scope1, reset.ID, changed) }()
				awaitReview(t, barrier)
				bindErr := f.service.BindVerifiedIdentifier(ctx, f.scope1, bind.ID)
				close(barrier.resume)
				resetErr := <-done
				var u domain.User
				if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { var e error; u, e = tx.User(f.user1.ID, false); return e }); e != nil {
					t.Fatal(e)
				}
				if bindErr != nil || resetErr == nil || u.Email != rebound || u.PasswordHash != f.user1.PasswordHash {
					t.Fatalf("regression failed: rebind=%v reset=%v", bindErr, resetErr)
				}
				t.Log("stale reset proof rejected after contact rebind")
			})
			t.Run("self-password-after-recovery-codes-revoke-session", func(t *testing.T) {
				f := setup(t, dialect, dsn)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				seed := secure.NewTOTP()
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					u, e := tx.User(f.user1.ID, true)
					if e != nil {
						return e
					}
					u.TOTPSecret = seed
					return tx.PutUser(u)
				}); e != nil {
					t.Fatal(e)
				}
				handle := f.sessionFor(t, f.user1)
				// Match a real session issued after successful password + TOTP login.
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					session, e := tx.SessionByHandle(f.service.Digest(f.app1.ID, "session-handle", handle))
					if e != nil {
						return e
					}
					session.MFATime = time.Now().Unix()
					session.AMR = []string{"pwd", "otp"}
					return tx.PutSession(session)
				}); e != nil {
					t.Fatal(e)
				}
				barrier := &reviewBarrier{reached: make(chan struct{}), resume: make(chan struct{})}
				copyService := *f.service
				copyService.Store = reviewStore{Store: f.store, barrier: barrier, userLock: true}
				changed := "Review pending self-password change phrase"
				done := make(chan error, 1)
				go func() { done <- copyService.SelfPassword(ctx, f.scope1, handle, password, changed) }()
				awaitReview(t, barrier)
				codes, regenErr := f.service.RegenerateRecoveryCodes(ctx, f.scope1, handle, secure.TOTP(seed, time.Now().Unix()/30))
				close(barrier.resume)
				changeErr := <-done
				var u domain.User
				if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { var e error; u, e = tx.User(f.user1.ID, false); return e }); e != nil {
					t.Fatal(e)
				}
				if regenErr != nil || len(codes) != 10 || changeErr == nil || u.PasswordHash != f.user1.PasswordHash {
					t.Fatalf("regression failed: regeneration=%v change=%v", regenErr, changeErr)
				}
				t.Log("revoked-session password change rejected")
			})
			t.Run("account-password-limiter-before-verification", func(t *testing.T) {
				f := setup(t, dialect, dsn)
				f.sessionFor(t, f.user1)
				provider, e := protocol.New(f.service)
				if e != nil {
					t.Fatal(e)
				}
				denied := &reviewDenyLimiter{}
				counter := &atomic.Int64{}
				service := *f.service
				service.Store = passwordReadStore{Store: f.store, reads: counter}
				api := &transport.Server{Service: &service, Provider: provider, Limiter: denied, EmailConfigured: true}
				f.server.Config.Handler = api.Handler()
				// Derive the normal session-bound CSRF via the real account page.
				response, e := f.http.Get(f.app1.Issuer + "/ui/account")
				if e != nil {
					t.Fatal(e)
				}
				body, e := io.ReadAll(response.Body)
				response.Body.Close()
				if e != nil {
					t.Fatal(e)
				}
				proof := csrfPattern.FindSubmatch(body)
				if len(proof) != 2 {
					t.Fatal("CSRF missing")
				}
				counter.Store(0)
				status, _ := f.post(f.app1.Issuer+"/ui/account/password", mapForm("_csrf", string(proof[1]), "old_password", "known-wrong", "new_password", "Review test new password phrase"), false)
				if status != 429 || denied.calls.Load() != 1 || counter.Load() != 1 {
					t.Fatalf("regression failed: status=%d limiter_calls=%d", status, denied.calls.Load())
				}
				denied.failure = domain.ErrUnavailable
				counter.Store(0)
				status, _ = f.post(f.app1.Issuer+"/ui/account/password", mapForm("_csrf", string(proof[1]), "old_password", "known-wrong", "new_password", "Review test new password phrase"), false)
				if status != 503 || counter.Load() != 1 {
					t.Fatal("limiter failure entered password verification", status, counter.Load())
				}
				t.Log("password attempt denied before verification")
			})
		})
	}
}

type reviewDenyLimiter struct {
	calls   atomic.Int64
	failure error
}

func (l *reviewDenyLimiter) Allow(context.Context, string, int, time.Duration) (bool, error) {
	l.calls.Add(1)
	return false, l.failure
}

// Capture delivery only for testing the actual issuance/verification use case;
// the encrypted outbox write still executes in the real transaction.
type challengeCaptureStore struct {
	ports.Store
	message *domain.Message
}

func (s challengeCaptureStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	return s.Store.App(ctx, scope, write, func(tx ports.AppTx) error { return fn(challengeCaptureTx{AppTx: tx, message: s.message}) })
}

type challengeCaptureTx struct {
	ports.AppTx
	message *domain.Message
}

func (t challengeCaptureTx) Enqueue(m domain.Message) error {
	*t.message = m
	return t.AppTx.Enqueue(m)
}
func TestResetProofLifecycle(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		for _, kind := range []string{"email", "phone"} {
			for _, scenario := range []string{"normal", "rebound-before-verification", "reallocated-before-verification", "epoch-before-reset", "legacy-unbound", "two-resets", "identifier-ABA"} {
				t.Run(dialect+"/"+kind+"/"+scenario, func(t *testing.T) {
					f := setup(t, dialect, dsn)
					ctx := context.Background()
					value := f.user1.Email
					if kind == "phone" {
						value = "+8613800138000"
						if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
							u, e := tx.User(f.user1.ID, true)
							if e != nil {
								return e
							}
							u.Phone = value
							if e = tx.ReplaceIdentifier(u.ID, "phone", f.service.Digest(f.app1.ID, "identifier:phone", ""), f.service.Digest(f.app1.ID, "identifier:phone", value)); e != nil {
								return e
							}
							return tx.PutUser(u)
						}); err != nil {
							t.Fatal(err)
						}
					}
					flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: "choose_method", Expires: time.Now().Unix() + 600}
					if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); e != nil {
						t.Fatal(e)
					}
					var message domain.Message
					s := *f.service
					s.Store = challengeCaptureStore{Store: f.store, message: &message}
					challenge, err := s.RequestChallenge(ctx, f.scope1, flow.ID, "reset_password", kind, value, true)
					if err != nil {
						t.Fatal(err)
					}
					code := strings.Split(strings.TrimPrefix(message.Body, "Authova verification code: "), ".")[0]
					if scenario == "rebound-before-verification" || scenario == "reallocated-before-verification" {
						// This mirrors the epoch + identifier changes atomically committed by binding.
						err = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
							u, e := tx.User(f.user1.ID, true)
							if e != nil {
								return e
							}
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
							u.Epoch++
							if e = tx.PutUser(u); e != nil {
								return e
							}
							if scenario == "reallocated-before-verification" {
								other := domain.User{ID: application.ID(), AppID: f.app1.ID, Status: "active", Epoch: 1, CreatedAt: time.Now()}
								if kind == "phone" {
									other.Phone = value
								} else {
									other.Email = value
								}
								return tx.CreateUser(other, kind, s.Digest(f.app1.ID, "identifier:"+kind, value))
							}
							return nil
						})
						if err != nil {
							t.Fatal(err)
						}
						if s.VerifyChallenge(ctx, f.scope1, flow.ID, challenge, code) == nil {
							t.Fatal("old OTP accepted after owner change")
						}
						return
					}
					if err = s.VerifyChallenge(ctx, f.scope1, flow.ID, challenge, code); err != nil {
						t.Fatal(err)
					}
					if scenario == "two-resets" {
						second := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: domain.StepChoose, Expires: time.Now().Unix() + 600}
						if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(second) }); e != nil {
							t.Fatal(e)
						}
						id, e := s.RequestChallenge(ctx, f.scope1, second.ID, "reset_password", kind, value, true)
						if e != nil {
							t.Fatal(e)
						}
						proof := strings.Split(strings.TrimPrefix(message.Body, "Authova verification code: "), ".")[0]
						if e = s.VerifyChallenge(ctx, f.scope1, second.ID, id, proof); e != nil {
							t.Fatal(e)
						}
						barrier := &reviewBarrier{reached: make(chan struct{}), resume: make(chan struct{})}
						pending := *f.service
						pending.Store = reviewStore{Store: f.store, barrier: barrier}
						bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
						defer cancel()
						done := make(chan error, 1)
						go func() { done <- pending.ResetPassword(bounded, f.scope1, flow.ID, "Reset losing password phrase") }()
						awaitReview(t, barrier)
						winner := s.ResetPassword(bounded, f.scope1, second.ID, "Reset winning password phrase")
						close(barrier.resume)
						loser := <-done
						if winner != nil || !errors.Is(loser, domain.ErrUnauthenticated) {
							t.Fatalf("concurrent reset winner=%v loser=%v", winner, loser)
						}
						return
					}
					if scenario == "identifier-ABA" {
						previous := value
						temporary := "temporary@example.test"
						if kind == "phone" {
							temporary = "+8613800138002"
						}
						for _, next := range []string{temporary, value} {
							if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
								u, e := tx.User(f.user1.ID, true)
								if e != nil {
									return e
								}
								if e = tx.ReplaceIdentifier(u.ID, kind, s.Digest(f.app1.ID, "identifier:"+kind, previous), s.Digest(f.app1.ID, "identifier:"+kind, next)); e != nil {
									return e
								}
								if kind == "phone" {
									u.Phone = next
								} else {
									u.Email = next
								}
								u.Epoch++
								return tx.PutUser(u)
							}); e != nil {
								t.Fatal(e)
							}
							previous = next
						}
						if s.ResetPassword(ctx, f.scope1, flow.ID, "ABA denied new password phrase") == nil {
							t.Fatal("proof survived contact ABA")
						}
						return
					}
					if scenario == "epoch-before-reset" || scenario == "legacy-unbound" {
						if err = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
							if scenario == "legacy-unbound" {
								v, e := tx.Flow(flow.ID, true)
								if e != nil {
									return e
								}
								v.UserID = ""
								v.AuthEpoch = 0
								return tx.PutFlow(v)
							}
							u, e := tx.User(f.user1.ID, true)
							if e != nil {
								return e
							}
							u.Epoch++
							return tx.PutUser(u)
						}); err != nil {
							t.Fatal(err)
						}
						if s.ResetPassword(ctx, f.scope1, flow.ID, "Reset denied new password phrase") == nil {
							t.Fatal("stale reset committed")
						}
					} else {
						if err = s.ResetPassword(ctx, f.scope1, flow.ID, "Reset accepted new password phrase"); err != nil {
							t.Fatal(err)
						}
						if s.ResetPassword(ctx, f.scope1, flow.ID, "Reset replay new password phrase") == nil {
							t.Fatal("completed flow replayed")
						}
					}
				})
			}
		}
	}
}

func TestSensitiveWritesRejectSingleSessionRevocation(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		for _, operation := range []string{"password", "mfa-replace", "recovery-codes", "erase", "unlink"} {
			t.Run(dialect+"/"+operation, func(t *testing.T) {
				f := setup(t, dialect, dsn)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				seed := secure.NewTOTP()
				if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					u, e := tx.User(f.user1.ID, true)
					if e != nil {
						return e
					}
					u.TOTPSecret = seed
					return tx.PutUser(u)
				}); err != nil {
					t.Fatal(err)
				}
				handle := f.sessionFor(t, f.user1)
				barrier := &reviewBarrier{reached: make(chan struct{}), resume: make(chan struct{})}
				service := *f.service
				service.Store = reviewStore{Store: f.store, barrier: barrier, userLock: true}
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "password":
						err = service.SelfPassword(ctx, f.scope1, handle, password, "Sensitive write new password phrase")
					case "mfa-replace":
						_, err = service.BeginMFAReplacement(ctx, f.scope1, handle, secure.TOTP(seed, time.Now().Unix()/30), secure.Random(32))
					case "recovery-codes":
						_, err = service.RegenerateRecoveryCodes(ctx, f.scope1, handle, secure.TOTP(seed, time.Now().Unix()/30))
					case "erase":
						err = service.EraseSelf(ctx, f.scope1, handle)
					case "unlink":
						err = service.UnlinkExternal(ctx, f.scope1, handle, "absent", true, true)
					}
					done <- err
				}()
				awaitReview(t, barrier)
				err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					session, e := tx.SessionByHandle(f.service.Digest(f.app1.ID, "session-handle", handle))
					if e != nil {
						return e
					}
					session, e = tx.Session(session.ID, true)
					if e != nil {
						return e
					}
					session.Status = "revoked"
					return tx.PutSession(session)
				})
				close(barrier.resume)
				writeErr := <-done
				if err != nil || !errors.Is(writeErr, domain.ErrUnauthenticated) {
					t.Fatalf("revocation=%v sensitive write=%v", err, writeErr)
				}
				if err = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					u, e := tx.User(f.user1.ID, false)
					if e != nil {
						return e
					}
					if u.Epoch != f.user1.Epoch || u.PasswordHash != f.user1.PasswordHash || u.Status != "active" {
						t.Fatal("rejected operation mutated user")
					}
					events, e := tx.Audits("", 100)
					if e != nil {
						return e
					}
					for _, event := range events {
						if event.Action == "user.password_change" || event.Action == "user.erase" || event.Action == "user.mfa_replace" {
							t.Fatal("rejected operation wrote success audit")
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

type passwordReadStore struct {
	ports.Store
	reads *atomic.Int64
}

func (s passwordReadStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	return s.Store.App(ctx, scope, write, func(t ports.AppTx) error { return fn(passwordReadTx{AppTx: t, reads: s.reads}) })
}

type passwordReadTx struct {
	ports.AppTx
	reads *atomic.Int64
}

func (t passwordReadTx) User(id string, lock bool) (domain.User, error) {
	t.reads.Add(1)
	return t.AppTx.User(id, lock)
}
