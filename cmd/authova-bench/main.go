// authova-bench measures online introspection against a real synthetic user dataset.
package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/config"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
	jose "github.com/go-jose/go-jose/v4"
)

type fixture struct {
	App      domain.App `json:"app"`
	ClientID string     `json:"client_id"`
	Secret   string     `json:"client_secret"`
	Users    []string   `json:"sample_user_ids"`
	Actual   int64      `json:"actual_users"`
	Database string     `json:"database"`
}

func prepare(ctx context.Context, store *sqlstore.Store, f fixture, lifetime time.Duration) ([]string, error) {
	scope, _ := domain.AppScope(f.App.ID)
	out := []string{}
	err := store.App(ctx, scope, true, func(t ports.AppTx) error {
		keys, e := t.Keys()
		if e != nil {
			return e
		}
		var current domain.SigningKey
		for _, key := range keys {
			if key.Current {
				current = key
			}
		}
		private, e := x509.ParsePKCS1PrivateKey(current.Private)
		if e != nil {
			return e
		}
		signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: &jose.JSONWebKey{Key: private, KeyID: current.ID}}, (&jose.SignerOptions{}).WithType("at+jwt"))
		if e != nil {
			return e
		}
		client, e := t.Client(f.ClientID)
		if e != nil {
			return e
		}
		for _, id := range f.Users {
			user, e := t.User(id, false)
			if e != nil {
				return e
			}
			now := time.Now().UTC()
			session := domain.Session{ID: application.ID(), UserID: id, Status: "active", Epoch: user.Epoch, CreatedAt: now, ExpiresAt: now.Add(lifetime), HandleDigest: secure.Random(32), AMR: []string{"fixture"}, AuthTime: now.Unix()}
			if e = t.PutSession(session); e != nil {
				return e
			}
			token := domain.Token{ID: application.ID(), ClientID: f.ClientID, UserID: id, Subject: id, SessionID: session.ID, Created: now.Unix(), Expires: now.Add(lifetime).Unix(), Epoch: user.Epoch, Scopes: []string{"api.read"}, Audiences: client.Audiences}
			if e = t.PutToken(token); e != nil {
				return e
			}
			payload, _ := json.Marshal(map[string]any{"iss": f.App.Issuer, "sub": id, "client_id": f.ClientID, "aud": client.Audiences, "scope": "api.read", "principal_type": "user", "sid": session.ID, "jti": token.ID, "iat": token.Created, "exp": token.Expires})
			signed, e := signer.Sign(payload)
			if e != nil {
				return e
			}
			serialized, e := signed.CompactSerialize()
			if e != nil {
				return e
			}
			out = append(out, serialized)
		}
		return nil
	})
	return out, err
}
func main() {
	serverURL := flag.String("server-url", "", "test transport origin override; does not change issuer")
	samples := flag.Int("samples", 0, "spread-out synthetic users; 0 uses existing fixture sample")
	progress := flag.String("progress", "", "redacted one-minute JSONL output")
	file := flag.String("fixture", ".dev/load-postgres.json", "private synthetic fixture")
	qps := flag.Int("qps", 5000, "open-loop scheduled requests per second")
	duration := flag.Duration("duration", 60*time.Second, "measurement interval")
	workers := flag.Int("workers", 256, "bounded request workers")
	report := flag.String("report", "output/introspection-postgres.json", "redacted report")
	flag.Parse()
	if *qps < 1 || *qps > 50000 || *workers < 1 || *workers > 1024 || *duration <= 0 || *duration > 24*time.Hour || *samples < 0 || *samples > 1000000 {
		panic("invalid load parameters")
	}
	ctx := context.Background()
	c, e := config.Load()
	if e != nil || !c.Development {
		panic("benchmark requires development configuration")
	}
	raw, e := os.ReadFile(*file)
	if e != nil {
		panic(e)
	}
	var f fixture
	if json.Unmarshal(raw, &f) != nil || len(f.Users) == 0 {
		panic("invalid fixture")
	}
	vault, _ := secure.NewVault(c.MasterKey)
	store, e := sqlstore.Open(ctx, c.Dialect, c.DSN, c.BaseURL, vault, 8)
	if e != nil {
		panic(e)
	}
	defer store.Close()
	scope, _ := domain.AppScope(f.App.ID)
	actual, e := store.UserCount(ctx, scope)
	if e != nil {
		panic(e)
	}
	if *samples > 0 {
		f.Users, e = store.SyntheticSampleIDs(ctx, scope, actual, *samples)
		if e != nil {
			panic(e)
		}
	}
	tokens, e := prepare(ctx, store, f, *duration+10*time.Minute)
	if e != nil {
		panic(e)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConns: *workers, MaxIdleConnsPerHost: *workers, MaxConnsPerHost: *workers}}
	jobs := make(chan int, *workers*8)
	var started time.Time
	var success, failed, dropped atomic.Int64
	var mu sync.Mutex
	totalLatency := &histogram{}
	windowLatency := &histogram{}
	windowSuccess, windowFailed := int64(0), int64(0)
	statuses := map[int]int{}
	var wg sync.WaitGroup
	for worker := 0; worker < *workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				mixed := uint64(index) + 0x9e3779b97f4a7c15
				mixed = (mixed ^ (mixed >> 30)) * 0xbf58476d1ce4e5b9
				mixed = (mixed ^ (mixed >> 27)) * 0x94d049bb133111eb
				mixed ^= mixed >> 31
				form := url.Values{"token": {tokens[mixed%uint64(len(tokens))]}}
				request, _ := http.NewRequest("POST", func() string {
					if *serverURL != "" {
						return strings.TrimRight(*serverURL, "/") + "/a/" + f.App.Key + "/oauth/introspect"
					}
					return f.App.Issuer + "/oauth/introspect"
				}(), strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.SetBasicAuth(f.ClientID, f.Secret)
				response, e := httpClient.Do(request)
				status := 0
				active := false
				if e == nil {
					status = response.StatusCode
					var result struct{ Active bool }
					if status == 200 {
						e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
						active = result.Active
					}
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
				if e == nil && status == 200 && active {
					success.Add(1)
				} else {
					failed.Add(1)
				}
				mu.Lock()
				scheduledAt := started.Add(time.Duration(float64(index) / float64(*qps) * float64(time.Second)))
				latency := time.Since(scheduledAt).Microseconds()
				totalLatency.observe(latency)
				windowLatency.observe(latency)
				if e == nil && status == 200 && active {
					windowSuccess++
				} else {
					windowFailed++
				}
				statuses[status]++
				mu.Unlock()
			}
		}()
	}
	started = time.Now()
	var progressFile *os.File
	if *progress != "" {
		progressFile, e = os.Create(*progress)
		if e != nil {
			panic(e)
		}
		defer progressFile.Close()
	}
	stopProgress := make(chan struct{})
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		previous := started
		for {
			select {
			case <-stopProgress:
				return
			case now := <-ticker.C:
				mu.Lock()
				row := map[string]any{"elapsed_seconds": now.Sub(started).Seconds(), "window_seconds": now.Sub(previous).Seconds(), "success": success.Load(), "failed": failed.Load(), "queue_dropped": dropped.Load(), "window_success": windowSuccess, "window_failed": windowFailed, "window_success_qps": float64(windowSuccess) / now.Sub(previous).Seconds(), "window_latency_ms": windowLatency.summary()}
				*windowLatency = histogram{}
				windowSuccess, windowFailed = 0, 0
				mu.Unlock()
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				row["generator_heap_bytes"], row["generator_goroutines"] = memory.HeapAlloc, runtime.NumGoroutine()
				if progressFile != nil {
					json.NewEncoder(progressFile).Encode(row)
				}
				fmt.Printf("progress: elapsed=%.0fs success=%d failed=%d dropped=%d\n", now.Sub(started).Seconds(), success.Load(), failed.Load(), dropped.Load())
				previous = now
			}
		}
	}()
	scheduled := int(duration.Seconds() * float64(*qps))
	for index := 0; index < scheduled; index++ {
		target := started.Add(time.Duration(float64(index) / float64(*qps) * float64(time.Second)))
		if sleep := time.Until(target); sleep > 0 {
			time.Sleep(sleep)
		}
		select {
		case jobs <- index:
		default:
			dropped.Add(1)
		}
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(started)
	close(stopProgress)
	<-progressDone

	result := map[string]any{"date": time.Now().UTC().Format(time.RFC3339), "database": c.Dialect, "actual_users": actual, "active_user_samples": len(tokens), "fixture_token_ttl_seconds": (*duration + 10*time.Minute).Seconds(), "latency_histogram": "0.1ms bins through 1s, 1ms through 120s; explicit overflow", "fixture_kind": "synthetic valid user/session tokens with measurement-duration TTL; normal issuance/refresh tested separately", "go_version": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "target_qps": *qps, "scheduled": scheduled, "completed": success.Load() + failed.Load(), "success": success.Load(), "failed": failed.Load(), "queue_dropped": dropped.Load(), "measurement_seconds": duration.Seconds(), "elapsed_seconds_including_drain": elapsed.Seconds(), "successful_qps": float64(success.Load()) / elapsed.Seconds(), "latency_ms": totalLatency.summary(), "statuses": statuses, "workers": *workers, "queue_capacity": *workers * 8, "latency_basis": "scheduled arrival through response, includes generator queue delay", "limitations": []string{"single local host/container database", "load generator shares host with server", "no HA/failover claim", "deterministic pseudorandom requests across reported sample, not all active users"}}
	data, _ := json.MarshalIndent(result, "", "  ")
	os.MkdirAll("output", 0755)
	if e = os.WriteFile(*report, data, 0644); e != nil {
		panic(e)
	}
	fmt.Println(string(data))
}
