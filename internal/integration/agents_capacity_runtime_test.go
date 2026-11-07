package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
)

type agentCapacityRuntime struct {
	Nodes   []string
	PIDs    []int
	SHA256  string
	Pool    int
	Runtime string
}

// The load generator is separate from real cmd/authova processes. A local
// reverse proxy preserves the issuer/Host and round-robins independent caches,
// shared primary SQL state and shared Valkey admission counters.
func startAgentCapacityRuntime(t *testing.T, f *fixture, driver, dsn, directory string) agentCapacityRuntime {
	t.Helper()
	binary := os.Getenv("AUTHOVA_TEST_CORE_BINARY")
	if binary == "" {
		t.Fatal("capacity requires the real compiled Core binary")
	}
	replicas, err := strconv.Atoi(os.Getenv("AUTHOVA_AGENT_CAPACITY_REPLICAS"))
	if err != nil || replicas < 1 || replicas > 2 {
		t.Fatal("local profile requires one or two real Core replicas")
	}
	pool := 64
	if value := os.Getenv("AUTHOVA_AGENT_CAPACITY_POOL"); value != "" {
		pool, err = strconv.Atoi(value)
		if err != nil || pool < 4 || pool > 64 {
			t.Fatal("per-replica pool must be <=64 to preserve server connection headroom")
		}
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	out := agentCapacityRuntime{SHA256: hex.EncodeToString(digest[:]), Pool: pool}
	out.Runtime = "native"
	valkey := os.Getenv("AUTHOVA_AGENT_CAPACITY_VALKEY_ADDRESS")
	if valkey == "" {
		valkey = "127.0.0.1:56379"
	}
	for index := 0; index < replicas; index++ {
		if os.Getenv("AUTHOVA_AGENT_CAPACITY_RUNTIME") == "docker" {
			node, pid := startDockerCapacityNode(t, f, driver, dsn, directory, pool, index)
			out.Runtime = "docker-linux"
			out.Nodes, out.PIDs = append(out.Nodes, node), append(out.PIDs, pid)
			continue
		}
		reservation, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := reservation.Addr().String()
		reservation.Close()
		log, err := os.OpenFile(filepath.Join(directory, fmt.Sprintf("capacity-%s-node-%d.log", driver, index)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "serve")
		cmd.Env = append(os.Environ(), "AUTHOVA_MODE=development", "AUTHOVA_BASE_URL="+f.server.URL, "AUTHOVA_LISTEN="+address,
			"AUTHOVA_DATABASE_DRIVER="+driver, "AUTHOVA_DATABASE_URL="+dsn, "AUTHOVA_DB_CONNECTIONS="+strconv.Itoa(pool), "AUTHOVA_HTTP_CONCURRENCY=1024",
			"AUTHOVA_VALKEY_ADDRESS="+valkey, "AUTHOVA_VALKEY_PASSWORD=", "AUTHOVA_TRACE_OTLP_ENDPOINT=", "AUTHOVA_WORKER_METRICS_LISTEN=",
			"AUTHOVA_SMTP_ADDRESS=", "AUTHOVA_SMS_URL=", "AUTHOVA_SMS_TOKEN=", "GOMAXPROCS=4", "GOGC=100", "GOMEMLIMIT=512MiB")
		cmd.Stdout, cmd.Stderr = log, log
		if err = cmd.Start(); err != nil {
			log.Close()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() {
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				cmd.Process.Kill()
				<-done
			}
			log.Close()
		})
		node := "http://" + address
		ready := false
		client := &http.Client{Timeout: time.Second}
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			resp, err := client.Get(node + "/readyz")
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				ready = resp.StatusCode == 200
			}
			if ready {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			t.Fatal("real Core replica did not become ready; private log retained")
		}
		out.Nodes = append(out.Nodes, node)
		out.PIDs = append(out.PIDs, cmd.Process.Pid)
	}
	canonical := f.server.URL
	f.server.Close()
	u, _ := url.Parse(canonical)
	listener, err := net.Listen("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	proxies := make([]*httputil.ReverseProxy, len(out.Nodes))
	for index, endpoint := range out.Nodes {
		target, _ := url.Parse(endpoint)
		proxies[index] = httputil.NewSingleHostReverseProxy(target)
		transport := &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 256, MaxConnsPerHost: 256}
		proxies[index].Transport = transport
		t.Cleanup(transport.CloseIdleConnections)
	}
	var next atomic.Uint64
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxies[next.Add(1)%uint64(len(proxies))].ServeHTTP(w, r)
	}))
	proxy.Listener.Close()
	proxy.Listener = listener
	proxy.Start()
	t.Cleanup(proxy.Close)
	f.server = proxy
	return out
}

// Pin a request to a replica while preserving the canonical issuer Host.
func capacityNodePost(f *fixture, node, path, id, secret string, form url.Values) (int, map[string]any, error) {
	r, err := http.NewRequest("POST", node+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	u, _ := url.Parse(f.app1.Issuer)
	r.Host = u.Host
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(id, secret)
	resp, err := f.http.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	return resp.StatusCode, out, err
}

func capacityReplicaRevocation(f *fixture, runtime agentCapacityRuntime, a application.CreatedAgent, installation domain.AgentInstallation, cred recoveryCredential) error {
	u, _ := url.Parse(f.app1.Issuer)
	expires := int64(0)
	for _, node := range runtime.Nodes {
		status, out, err := capacityNodePost(f, node, u.Path+"/oauth/introspect", f.client.ClientID, f.secret, url.Values{"token": {cred.Access}})
		if err != nil || status != 200 || out["active"] != true {
			return fmt.Errorf("replica did not warm the live revocation canary: status=%d err=%v", status, err)
		}
		if err := requireAgentBinding(out, f, a, installation, domain.AgentDelegated, f.user1.ID, cred.GrantID); err != nil {
			return err
		}
		expires = int64(out["exp"].(float64))
	}
	// This is a real management HTTP call concurrent with the load. The API
	// acknowledgement follows the primary commit, then every cache is checked.
	body := strings.NewReader(`{"config_version":1,"reason":"capacity cross-replica revocation canary"}`)
	r, _ := http.NewRequest("POST", f.server.URL+"/api/v1/apps/"+f.app1.ID+"/agent-grants/"+cred.GrantID+"/revoke", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.operatorToken)
	resp, err := f.http.Do(r.WithContext(context.Background()))
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("replica revocation management status %d", resp.StatusCode)
	}
	for _, node := range runtime.Nodes {
		if time.Now().Unix() >= expires {
			return fmt.Errorf("canary expired before replica denial; evidence would be ambiguous")
		}
		status, out, err := capacityNodePost(f, node, u.Path+"/oauth/introspect", f.client.ClientID, f.secret, url.Values{"token": {cred.Access}})
		if err != nil || status != 200 || out["active"] != false {
			return fmt.Errorf("replica accepted revoked canary: status=%d err=%v", status, err)
		}
		status, _, err = capacityNodePost(f, node, u.Path+"/oauth/token", a.Agent.ClientID, a.Secret, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.Refresh}})
		if err != nil || status != 400 {
			return fmt.Errorf("replica refreshed revoked canary: status=%d err=%v", status, err)
		}
	}
	return nil
}

func (r agentCapacityRuntime) observe() []map[string]any {
	rows := make([]map[string]any, 0, len(r.Nodes))
	client := &http.Client{Timeout: 2 * time.Second}
	for index, node := range r.Nodes {
		row := map[string]any{"pid": r.PIDs[index]}
		resp, err := client.Get(node + "/metrics")
		if err == nil {
			raw, e := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
			resp.Body.Close()
			if e == nil && resp.StatusCode == 200 {
				metrics := map[string]float64{}
				for _, line := range strings.Split(string(raw), "\n") {
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					fields := strings.Fields(line)
					if len(fields) == 2 {
						if value, e := strconv.ParseFloat(fields[1], 64); e == nil {
							metrics[fields[0]] = value
						}
					}
				}
				row["metrics"] = metrics
			} else {
				row["metrics_status"] = resp.StatusCode
			}
		} else {
			row["metrics_error"] = "unavailable"
		}
		rows = append(rows, row)
	}
	return rows
}
