package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/transport"
	"github.com/carvebloom/authova/internal/valkeyconfig"
)

func TestChallengeQuotaAcrossReplicas(t *testing.T) {
	address := os.Getenv("AUTHOVA_TEST_VALKEY_ADDRESS")
	if address == "" {
		t.Skip("real Valkey required")
	}
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		t.Run(driver, func(t *testing.T) {
			f := setup(t, driver, dsn)
			ctx := context.Background()
			if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				a, e := tx.App()
				if e != nil {
					return e
				}
				limits := domain.DefaultResourceLimits()
				limits.EmailChallengesPerHour = 3
				a.Resources = &limits
				a.Version++
				return tx.UpdateAppConfig(a)
			}); e != nil {
				t.Fatal(e)
			}
			config := valkeyconfig.Config{Address: address, Password: os.Getenv("AUTHOVA_TEST_VALKEY_PASSWORD")}
			one, e := transport.NewValkeyLimiter(config)
			if e != nil {
				t.Fatal(e)
			}
			defer one.Close()
			two, e := transport.NewValkeyLimiter(config)
			if e != nil {
				t.Fatal(e)
			}
			defer two.Close()
			s1, s2 := *f.service, *f.service
			s1.ChallengeLimiter = one
			s2.ChallengeLimiter = two
			flows := []domain.Flow{}
			for i := 0; i < 12; i++ {
				flows = append(flows, f.securityFlow(t, "authorize"))
			}
			var succeeded, limited atomic.Int64
			var group sync.WaitGroup
			for i, flow := range flows {
				group.Add(1)
				go func() {
					defer group.Done()
					s := &s1
					if i%2 == 1 {
						s = &s2
					}
					_, err := s.RequestChallenge(ctx, f.scope1, flow.ID, "login", "email", fmt.Sprintf("quota-%d@example.test", i), true)
					if err == nil {
						succeeded.Add(1)
					} else if errors.Is(err, domain.ErrRateLimit) {
						limited.Add(1)
					} else {
						t.Error(err)
					}
				}()
			}
			group.Wait()
			if succeeded.Load() != 3 || limited.Load() != 9 {
				t.Fatal("shared App budget violated", succeeded.Load(), limited.Load())
			}
			// The same target cannot bypass its budget by changing purpose or user.
			f2 := setup(t, driver, dsn)
			s := *f2.service
			s.ChallengeLimiter = one
			for i := 0; i < 6; i++ {
				flow := f2.securityFlow(t, "authorize")
				purpose := []string{"login", "register", "reset_password"}[i%3]
				_, err := s.RequestChallenge(ctx, f2.scope1, flow.ID, purpose, "email", "shared-target@example.test", true)
				if i < 5 && err != nil {
					t.Fatal(err)
				}
				if i == 5 && !errors.Is(err, domain.ErrRateLimit) {
					t.Fatal("destination budget bypass", err)
				}
			}
			// A limiter outage cannot enqueue a challenge or fall back to a local allow.
			one.Close()
			flow := f2.securityFlow(t, "authorize")
			if _, err := s.RequestChallenge(ctx, f2.scope1, flow.ID, "login", "email", "outage@example.test", true); !errors.Is(err, domain.ErrUnavailable) {
				t.Fatal("limiter outage failed open", err)
			}
		})
	}
}
