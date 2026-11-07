package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// The scheduler is open loop and bounded. Histogram latency starts at the
// planned arrival, including generator queue delay; drops are never successes.
// Normal 120-second Agent tokens are renewed by real HTTP refresh, not stretched.
func TestAgentCapacityHTTP(t *testing.T) {
	directory := os.Getenv("AUTHOVA_AGENT_ACCEPTANCE_DIR")
	duration, err := time.ParseDuration(os.Getenv("AUTHOVA_AGENT_CAPACITY_DURATION"))
	qps, parseErr := strconv.Atoi(os.Getenv("AUTHOVA_AGENT_CAPACITY_QPS"))
	driver := os.Getenv("AUTHOVA_AGENT_CAPACITY_DRIVER")
	if directory == "" || err != nil || parseErr != nil || (driver != "postgres" && driver != "mysql") {
		t.Skip("requires private capacity fixture, duration, QPS and one driver")
	}
	if duration < time.Second || duration > 24*time.Hour || qps < 1 || qps > 20000 {
		t.Fatal("invalid local capacity profile")
	}
	queueCapacity := 256 * 8
	if value := os.Getenv("AUTHOVA_AGENT_CAPACITY_QUEUE"); value != "" {
		queueCapacity, err = strconv.Atoi(value)
		if err != nil || queueCapacity < 4 || queueCapacity > 65536 {
			t.Fatal("invalid bounded generator queue capacity")
		}
	}
	earlyFailAfter := time.Duration(0)
	if value := os.Getenv("AUTHOVA_AGENT_CAPACITY_FAIL_FAST"); value != "" {
		earlyFailAfter, err = time.ParseDuration(value)
		if err != nil || earlyFailAfter < 2*time.Minute {
			t.Fatal("capacity early-failure boundary must be at least two minutes")
		}
	}
	var saved agentRecoveryFixture
	raw, err := os.ReadFile(filepath.Join(directory, driver+"-fixture.json"))
	if err != nil || json.Unmarshal(raw, &saved) != nil {
		t.Fatal("private capacity fixture unavailable")
	}
	dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
	f := restoredAgentFixture(t, driver, dsn, saved)
	servers := startAgentCapacityRuntime(t, f, driver, dsn, directory)
	ctx := context.Background()
	actual, err := f.store.UserCount(ctx, f.scope1)
	if err != nil || actual != saved.Actual {
		t.Fatal("capacity population changed", actual, err)
	}
	// Preparation creates a fresh test owner. It does not modify the saved
	// recovery grants, refresh families or their expected backup-state evidence.
	var fixtureUser domain.User
	if err = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		var e error
		fixtureUser, e = tx.User(saved.User.ID, false)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	secret := secure.NewTOTP()
	owner := domain.Operator{ID: application.ID(), Email: "capacity-" + application.ID() + "@example.test", Role: "owner", Status: "active", Password: fixtureUser.PasswordHash, TOTP: secret}
	if err = f.store.Platform(ctx, true, func(tx ports.PlatformTx) error { return tx.PutOperator(owner) }); err != nil {
		t.Fatal(err)
	}
	f.operatorToken, _, _, err = f.service.LoginOperator(ctx, owner.Email, password, secure.TOTP(secret, time.Now().Unix()/30))
	if err != nil {
		t.Fatal(err)
	}
	appDB := 32
	if raw := os.Getenv("AUTHOVA_AGENT_CAPACITY_DB_CONCURRENCY"); raw != "" {
		appDB, err = strconv.Atoi(raw)
		if err != nil || appDB < 1 || appDB > 256 {
			t.Fatal("invalid explicit App database budget")
		}
	}
	if err = f.service.AppManage(ctx, f.operatorToken, f.scope1, true, true, func(tx ports.AppTx, owner domain.Operator) error {
		if err := tx.LockOwners(); err != nil {
			return err
		}
		app, err := tx.App()
		if err != nil {
			return err
		}
		limits := app.ResourceLimits()
		limits.DBConcurrency = appDB
		app.Resources, app.Version = &limits, app.Version+1
		return tx.UpdateAppConfig(app)
	}); err != nil {
		t.Fatal(err)
	}
	a, controlInstallation := acceptanceAgent(t, f)
	const samples, workers = 24, 256
	type sample struct {
		user    domain.User
		install domain.AgentInstallation
		current recoveryCredential
		blocked bool // An unknown refresh outcome is never blindly replayed.
	}
	active := make([]sample, samples)
	for index := range active {
		ordinal := int64(index+1) * (actual - 1) / (samples + 1)
		if ordinal < 1 {
			ordinal = 1
		}
		var user domain.User
		err = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			var e error
			user, e = tx.FindUser("email", f.service.Digest(f.app1.ID, "identifier:email", fmt.Sprintf("load-%d@example.test", ordinal)))
			return e
		})
		if err != nil {
			t.Fatal("spread-out indexed capacity user unavailable", err)
		}
		i, err := f.service.CreateAgentInstallation(ctx, f.operatorToken, f.scope1, application.AgentInstallationCreate{
			AgentID: a.Agent.ID, TenantID: fmt.Sprintf("capacity-tenant-%d", index), UserID: user.ID,
			ResponsibleParty: "trusted capacity provisioner", Resource: "urn:authova:integration", Scopes: []string{"api.read"}, Modes: []string{domain.AgentDelegated},
			Expires: time.Now().Add(48 * time.Hour).Unix(), Reason: "spread-out synthetic user approval",
		}, "capacity-"+application.ID())
		if err != nil {
			t.Fatal(err)
		}
		copy := *f
		copy.user1 = user
		cred := recoveryCredentialFrom(t, f, issueDelegated(t, &copy, a, i))
		active[index] = sample{user: user, install: i, current: cred}
	}
	canary := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, controlInstallation))
	loadTransport := &http.Transport{MaxIdleConns: workers, MaxIdleConnsPerHost: workers, MaxConnsPerHost: workers}
	defer loadTransport.CloseIdleConnections()
	loadClient := &http.Client{Transport: loadTransport, Timeout: 10 * time.Second}
	var mu sync.Mutex
	success, failed, rotations, rotationErrors := int64(0), int64(0), 0, 0
	statuses := map[int]int64{}
	failureClasses := map[string]int64{}
	refreshFailures := []map[string]any{}
	histogram := make([]uint64, 120001)
	var overflow uint64
	var maximumLatency int64
	var mismatch int64
	windowQPS := []float64{}
	lastWindowElapsed, lastWindowSuccess := 0.0, int64(0)
	jobs := make(chan int, queueCapacity)
	var workload sync.WaitGroup
	started := time.Now()
	for n := 0; n < workers; n++ {
		workload.Add(1)
		go func() {
			defer workload.Done()
			for index := range jobs {
				mu.Lock()
				s := active[index%samples]
				mu.Unlock()
				status, out, err := agentHTTP(loadClient, f.app1.Issuer+"/oauth/introspect", f.client.ClientID, f.secret, url.Values{"token": {s.current.Access}})
				correct := err == nil && status == 200 && out["active"] == true
				bindingErr := error(nil)
				if correct {
					bindingErr = requireAgentBinding(out, f, a, s.install, domain.AgentDelegated, s.user.ID, s.current.GrantID)
					correct = bindingErr == nil
				}
				arrival := started.Add(time.Duration(float64(index) / float64(qps) * float64(time.Second)))
				latency := max(0, time.Since(arrival).Milliseconds())
				mu.Lock()
				maximumLatency = max(maximumLatency, latency)
				if correct {
					success++
				} else {
					failed++
				}
				if !correct {
					class := "inactive"
					if err != nil {
						class = "transport_or_decode"
					} else if status != 200 {
						class = "http_status"
					} else if bindingErr != nil {
						class = "principal_binding"
					}
					failureClasses[class]++
				}
				if bindingErr != nil {
					mismatch++
				}
				statuses[status]++
				if latency < int64(len(histogram)) {
					histogram[latency]++
				} else {
					overflow++
				}
				mu.Unlock()
			}
		}()
	}
	stop := make(chan struct{})
	rotationDone := make(chan struct{})
	canaryDone := make(chan struct{})
	var canaryError error
	var canaryVerified bool
	go func() {
		defer close(canaryDone)
		select {
		case <-stop:
			return
		case <-time.After(time.Minute):
			err := capacityReplicaRevocation(f, servers, a, controlInstallation, canary)
			mu.Lock()
			canaryError = err
			canaryVerified = canaryError == nil
			mu.Unlock()
		}
	}()
	go func() {
		defer close(rotationDone)
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				for index := range active {
					mu.Lock()
					s := active[index]
					mu.Unlock()
					if s.blocked {
						continue
					}
					status, out, err := agentRefreshHTTP(f, a, s.current.Refresh)
					mu.Lock()
					if err != nil || status != 200 {
						rotationErrors++
						refreshFailures = append(refreshFailures, map[string]any{"elapsed_seconds": time.Since(started).Seconds(), "sample": index, "status": status, "transport_or_decode_error": err != nil, "error": capacityErrorCode(out)})
						active[index].blocked = true
					} else {
						access, _ := out["access_token"].(string)
						refresh, _ := out["refresh_token"].(string)
						if access == "" || refresh == "" {
							rotationErrors++
							refreshFailures = append(refreshFailures, map[string]any{"elapsed_seconds": time.Since(started).Seconds(), "sample": index, "status": status, "error": "missing_token"})
							active[index].blocked = true
						} else {
							active[index].current.Access, active[index].current.Refresh = access, refresh
							rotations++
						}
					}
					mu.Unlock()
				}
			}
		}
	}()
	progress, err := os.OpenFile(filepath.Join(directory, "agent-capacity-"+driver+"-progress.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer progress.Close()
	var dropped atomic.Int64
	earlyStop := make(chan struct{})
	samplerDone := make(chan struct{})
	var samplerError error
	go func() {
		defer close(samplerDone)
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			mu.Lock()
			windowElapsed := time.Since(started).Seconds()
			if windowElapsed >= duration.Seconds() {
				mu.Unlock()
				return
			}
			currentQPS := float64(success-lastWindowSuccess) / (windowElapsed - lastWindowElapsed)
			windowQPS = append(windowQPS, currentQPS)
			row := map[string]any{"elapsed_seconds": windowElapsed, "success": success, "failed": failed, "queue_dropped": dropped.Load(), "refresh_rotations": rotations, "refresh_errors": rotationErrors, "fixture_pool": f.store.Stats(), "window_successful_qps": currentQPS, "window_seconds": windowElapsed - lastWindowElapsed}
			lastWindowElapsed, lastWindowSuccess = windowElapsed, success
			alreadyFailed := failed > 0 || dropped.Load() > 0 || rotationErrors > 0 || mismatch > 0 || canaryError != nil
			mu.Unlock()
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			row["generator_heap_bytes"], row["generator_goroutines"] = memory.HeapAlloc, runtime.NumGoroutine()
			row["core_nodes"] = servers.observe()
			if err := json.NewEncoder(progress).Encode(row); err != nil {
				samplerError = fmt.Errorf("cannot persist capacity observations")
				close(earlyStop)
				return
			}
			t.Logf("capacity elapsed=%s success=%v dropped=%d window_qps=%.2f", time.Duration(windowElapsed*float64(time.Second)).Round(time.Second), row["success"], dropped.Load(), currentQPS)
			if earlyFailAfter > 0 && time.Since(started) >= earlyFailAfter && alreadyFailed {
				close(earlyStop)
				return
			}
		}
	}()
	scheduled := int(duration.Seconds() * float64(qps))
	scheduledActual := 0
	stoppedEarly := false
	var maximumArrivalLag int64
	var maximumQueued int
schedule:
	for index := 0; index < scheduled; index++ {
		select {
		case <-earlyStop:
			stoppedEarly = true
			break schedule
		default:
		}
		arrival := started.Add(time.Duration(float64(index) / float64(qps) * float64(time.Second)))
		if pause := time.Until(arrival); pause > 0 {
			time.Sleep(pause)
		}
		maximumArrivalLag = max(maximumArrivalLag, time.Since(arrival).Milliseconds())
		select {
		case jobs <- index:
			maximumQueued = max(maximumQueued, len(jobs))
		default:
			dropped.Add(1)
		}
		scheduledActual++
	}
	if stoppedEarly {
		t.Log("stopped at a declared failure boundary or failed observation write; unexecuted requests are not completed")
	}
	close(jobs)
	workload.Wait()
	elapsed := time.Since(started)
	close(stop)
	<-rotationDone
	<-canaryDone
	<-samplerDone
	windowQPS = append(windowQPS, float64(success-lastWindowSuccess)/(elapsed.Seconds()-lastWindowElapsed))
	minWindowQPS := windowQPS[0]
	for _, rate := range windowQPS {
		minWindowQPS = min(minWindowQPS, rate)
	}
	quantile := func(p float64) int {
		threshold := uint64(float64(success+failed)*p + 0.999)
		var sum uint64
		for ms, count := range histogram {
			sum += count
			if sum >= threshold {
				return ms
			}
		}
		return 120001
	}
	profilePassed := !stoppedEarly && failed == 0 && dropped.Load() == 0 && samplerError == nil && rotationErrors == 0 && float64(success)/elapsed.Seconds() > 5000 && minWindowQPS > 5000 && mismatch == 0 && canaryError == nil && (duration < 2*time.Minute || canaryVerified)
	report := map[string]any{
		"driver": driver, "actual_app_users": actual,
		"active_spread_out_users_and_tenants": samples,
		"target_qps":                          qps, "measurement_seconds": duration.Seconds(),
		"elapsed_seconds_including_drain": elapsed.Seconds(),
		"scheduled":                       scheduled, "success": success, "failed": failed,
		"queue_dropped": dropped.Load(), "statuses": statuses,
		"successful_qps":               float64(success) / elapsed.Seconds(),
		"window_successful_qps":        windowQPS,
		"min_window_successful_qps":    minWindowQPS,
		"window_basis":                 "per-minute counter deltas / actual elapsed time; final interval includes drain",
		"principal_binding_mismatches": mismatch,
		"normal_agent_ttl_seconds":     120, "real_http_refresh_rotations": rotations,
		"refresh_errors":          rotationErrors,
		"refresh_failure_details": refreshFailures,
		"failure_classes":         failureClasses,
		"latency_ms":              map[string]int{"p50": quantile(.5), "p95": quantile(.95), "p99": quantile(.99), "max": int(maximumLatency)},
		"maximum_arrival_lag_ms":  maximumArrivalLag,
		"maximum_queued_jobs":     maximumQueued,
		"latency_overflow":        overflow,
		"latency_basis":           "planned arrival through response; includes queue delay",
		"histogram_resolution_ms": 1,
		"workers":                 workers, "queue_capacity": cap(jobs), "fixture_pool": f.store.Stats(),
		"app_db_concurrency":        appDB,
		"profile_passed":            profilePassed,
		"sustained_5000_qps_passed": profilePassed && duration >= time.Hour,
		"profile":                   "real cmd/authova replicas behind a local round-robin proxy; independent processes/caches, shared primary SQL and Valkey; loopback HTTP on one physical host",
		"acceptance_criteria":       "actual whole-run and each-minute success >5000 QPS; zero unexpected HTTP/refresh failures, drops or principal mismatches; sustained gate additionally requires >=1h",
		"limitations":               []string{"local shared host and Docker VM", "24 active users across a real reported population; not all users active", "not token issuance/password throughput", "not independent load host or external deployment certification"},
	}
	report["sampling"] = "independent observer; metrics and file IO never block the arrival scheduler"
	if samplerError != nil {
		report["sampling_error"] = samplerError.Error()
	}
	report["core_replicas"] = len(servers.Nodes)
	report["runtime"] = servers.Runtime
	report["generator_os"] = runtime.GOOS
	report["requested_measurement_seconds"] = duration.Seconds()
	report["measurement_seconds"] = float64(scheduledActual) / float64(qps)
	report["scheduled_requested"] = scheduled
	report["scheduled"] = scheduledActual
	report["stopped_early"] = stoppedEarly
	report["early_failure_boundary_seconds"] = earlyFailAfter.Seconds()
	report["per_replica_db_pool"] = servers.Pool
	report["server_binary_sha256"] = servers.SHA256
	report["real_core_processes"] = true
	report["cross_replica_revocation_verified"] = canaryVerified
	if canaryError != nil {
		report["cross_replica_revocation_error"] = canaryError.Error()
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(directory, "agent-capacity-"+driver+".json"), data, 0600) != nil {
		t.Fatal("cannot save capacity report")
	}
	t.Logf("capacity result: successful_qps=%.2f success=%d failure=%d drops=%d refresh=%d refresh_errors=%d binding_mismatch=%d", float64(success)/elapsed.Seconds(), success, failed, dropped.Load(), rotations, rotationErrors, mismatch)
	if !profilePassed {
		t.Fatal("capacity profile did not pass; complete measurements retained")
	}
}

// Only a fixed error vocabulary enters public capacity evidence. Never record
// arbitrary response text, tokens, client secrets or request bodies.
func capacityErrorCode(out map[string]any) string {
	value, _ := out["error"].(string)
	switch value {
	case "rate_limited", "server_error", "invalid_client", "invalid_grant", "invalid_request", "temporarily_unavailable", "unauthorized_client", "invalid_scope":
		return value
	default:
		return "unclassified"
	}
}
