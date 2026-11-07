package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Normal protocol tokens retain the real five-minute TTL. Fixture setup is
// synthetic; all measured issuance/refresh/introspection goes through HTTP.
func TestProtocolSoak(t *testing.T) {
	duration, err := time.ParseDuration(os.Getenv("AUTHOVA_PROTOCOL_SOAK_DURATION"))
	if err != nil || duration < 10*time.Minute {
		t.Skip("opt in with AUTHOVA_PROTOCOL_SOAK_DURATION>=10m")
	}
	if duration > 24*time.Hour {
		t.Fatal("duration exceeds local gate limit")
	}
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			t.Fatal("both real database DSNs required")
		}
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			f := setup(t, driver, dsn)
			code, verifier := f.code()
			status, current := f.exchange(code, verifier)
			if status != 200 {
				t.Fatal("normal issuance failed")
			}
			deadline := time.Now().Add(duration)
			nextRefresh := time.Now().Add(4 * time.Minute)
			checks, rotations := 0, 0
			for time.Now().Before(deadline) {
				if !f.active(current.Access) {
					t.Fatal("live protocol token became inactive")
				}
				checks++
				if time.Now().After(nextRefresh) {
					status, body := f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "refresh_token", "refresh_token", current.Refresh), true)
					if status != 200 || json.Unmarshal(body, &current) != nil || current.Refresh == "" {
						t.Fatal("normal rotation failed", status)
					}
					rotations++
					nextRefresh = time.Now().Add(4 * time.Minute)
				}
				time.Sleep(100 * time.Millisecond)
			}
			if rotations < 2 {
				t.Fatal("actual five-minute token lifetime not crossed")
			}
			t.Logf("normal HTTP issuance/rotation: duration=%s checks=%d rotations=%d", duration, checks, rotations)
		})
	}
}
