package transport

import (
	"context"
	"encoding/json"
	"github.com/carvebloom/authova/internal/valkeyconfig"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/secure"
)

func TestSharedAppRateBudgetAcrossReplicas(t *testing.T) {
	address := os.Getenv("AUTHOVA_TEST_VALKEY_ADDRESS")
	if address == "" {
		t.Skip("real Valkey required")
	}
	config := valkeyconfig.Config{Address: address, Password: os.Getenv("AUTHOVA_TEST_VALKEY_PASSWORD")}
	if file := os.Getenv("AUTHOVA_TEST_VALKEY_TLS_CONFIG"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal("cannot read TLS fixture")
		}
		var configs map[string]valkeyconfig.Config
		if json.Unmarshal(data, &configs) != nil {
			t.Fatal("invalid TLS fixture")
		}
		config = configs["core"]
	}
	first, e := NewValkeyLimiter(config)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	second, e := NewValkeyLimiter(config)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	key := "app-requests:test-" + secure.Random(16)
	var admitted atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			client := first
			if i%2 == 1 {
				client = second
			}
			allowed, e := client.Allow(context.Background(), key, 20, time.Minute)
			if e != nil {
				failed.Add(1)
			}
			if allowed {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if failed.Load() != 0 || admitted.Load() != 20 {
		t.Fatalf("shared budget admitted %d, failures %d", admitted.Load(), failed.Load())
	}
	allowed, e := second.Allow(context.Background(), "app-requests:independent-"+secure.Random(16), 20, time.Minute)
	if e != nil || !allowed {
		t.Fatal("another App budget affected", e)
	}
}
