package transport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/valkeyconfig"
)

func TestValkeyTLSOutageRecovery(t *testing.T) {
	control := os.Getenv("AUTHOVA_TEST_VALKEY_CONTROL")
	if control == "" {
		t.Skip("isolated lifecycle runner required")
	}
	data, err := os.ReadFile(os.Getenv("AUTHOVA_TEST_VALKEY_TLS_CONFIG"))
	if err != nil {
		t.Fatal("cannot read TLS fixture")
	}
	var configs map[string]valkeyconfig.Config
	if json.Unmarshal(data, &configs) != nil {
		t.Fatal("invalid TLS fixture")
	}
	limiter, err := NewValkeyLimiter(configs["core"])
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()
	probe := func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return limiter.Allow(ctx, "outage-recovery", 100, time.Minute)
	}
	if allowed, err := probe(); err != nil || !allowed {
		t.Fatal("initial limiter failed")
	}
	step := func(request, ready string) {
		t.Helper()
		if os.WriteFile(filepath.Join(control, request), nil, 0600) != nil {
			t.Fatal("cannot signal lifecycle")
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(control, ready)); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("lifecycle timeout", ready)
	}
	step("stop", "stopped")
	if allowed, err := probe(); err == nil || allowed {
		t.Fatal("dependency outage did not fail closed")
	}
	step("restart", "restarted")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if allowed, err := probe(); err == nil && allowed {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("existing TLS client did not recover")
}
