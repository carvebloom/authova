package integration

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	mysql "github.com/go-sql-driver/mysql"
)

// Own Linux Core containers share the project's development SQL/Valkey network.
// Only loopback ports are published. No existing service or VM setting changes.
func startDockerCapacityNode(t *testing.T, f *fixture, driver, dsn, directory string, pool, index int) (string, int) {
	t.Helper()
	image := os.Getenv("AUTHOVA_AGENT_CAPACITY_IMAGE")
	if image == "" {
		t.Fatal("Linux profile requires a locally built Core image")
	}
	if driver == "postgres" {
		u, err := url.Parse(dsn)
		if err != nil || u.Host != "127.0.0.1:55432" {
			t.Fatal("Docker profile accepts only the owned local PostgreSQL endpoint")
		}
		u.Host = "postgres:5432"
		dsn = u.String()
	} else {
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil || cfg.Addr != "127.0.0.1:53306" {
			t.Fatal("Docker profile accepts only the owned local MySQL endpoint")
		}
		cfg.Addr = "mysql:3306"
		dsn = cfg.FormatDSN()
	}
	key, err := os.ReadFile(os.Getenv("AUTHOVA_MASTER_KEY_FILE"))
	if err != nil || len(key) != 32 {
		t.Fatal("private Linux profile key unavailable")
	}
	name := "authova-capacity-" + strings.ReplaceAll(application.ID(), "-", "")
	env := []string{"AUTHOVA_MODE=development", "AUTHOVA_BASE_URL=" + f.server.URL, "AUTHOVA_CONSOLE_URL=http://127.0.0.1:18090", "AUTHOVA_LISTEN=0.0.0.0:8080",
		"AUTHOVA_DATABASE_DRIVER=" + driver, "AUTHOVA_DATABASE_URL=" + dsn, "AUTHOVA_DB_CONNECTIONS=" + strconv.Itoa(pool), "AUTHOVA_HTTP_CONCURRENCY=1024", "AUTHOVA_KDF_CONCURRENCY=4",
		"AUTHOVA_VALKEY_ADDRESS=valkey:6379", "AUTHOVA_MASTER_KEY=" + base64.StdEncoding.EncodeToString(key), "AUTHOVA_MASTER_KEY_FILE=", "AUTHOVA_ENCRYPTION_KEYRING_FILE=",
		"GOMAXPROCS=4", "GOGC=100", "GOMEMLIMIT=512MiB"}
	path := filepath.Join(directory, fmt.Sprintf("capacity-%s-node-%d.env", driver, index))
	if err := os.WriteFile(path, []byte(strings.Join(env, "\n")+"\n"), 0600); err != nil {
		t.Fatal("cannot write private Linux runtime environment")
	}
	log, err := os.OpenFile(filepath.Join(directory, fmt.Sprintf("capacity-%s-node-%d.log", driver, index)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	create := exec.Command("docker", "create", "--name", name, "--label", "authova.acceptance=agent-capacity", "--network", "authova-dev_default",
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=10001:10001", "--memory=1g", "--cpus=4",
		"--env-file", path, "--entrypoint=/authova", "-p", "127.0.0.1::8080", image, "serve")
	create.Stdout, create.Stderr = log, log
	if err := create.Run(); err != nil {
		log.Close()
		t.Fatal("cannot create owned Linux Core container; private log retained")
	}
	t.Cleanup(func() {
		stop := exec.Command("docker", "stop", "--time", "15", name)
		stop.Stdout, stop.Stderr = log, log
		_ = stop.Run()
		logs := exec.Command("docker", "logs", name)
		logs.Stdout, logs.Stderr = log, log
		_ = logs.Run()
		rm := exec.Command("docker", "rm", name)
		rm.Stdout, rm.Stderr = log, log
		_ = rm.Run()
		log.Close()
	})
	actualPath := filepath.Join(directory, fmt.Sprintf("capacity-%s-node-%d.binary", driver, index))
	copy := exec.Command("docker", "cp", name+":/authova", actualPath)
	copy.Stdout, copy.Stderr = log, log
	if err := copy.Run(); err != nil {
		t.Fatal("cannot verify Linux Core image executable")
	}
	actual, readErr := os.ReadFile(actualPath)
	expected, expectedErr := os.ReadFile(os.Getenv("AUTHOVA_TEST_CORE_BINARY"))
	if readErr != nil || expectedErr != nil || sha256.Sum256(actual) != sha256.Sum256(expected) {
		t.Fatal("Linux image executable does not match the measured Core artifact")
	}
	start := exec.Command("docker", "start", name)
	start.Stdout, start.Stderr = log, log
	if err := start.Run(); err != nil {
		t.Fatal("cannot start owned Linux Core container; private log retained")
	}
	port, err := exec.Command("docker", "port", name, "8080/tcp").Output()
	if err != nil {
		t.Fatal("cannot resolve owned Linux port")
	}
	node := "http://" + strings.TrimSpace(string(port))
	client := &http.Client{Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
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
		t.Fatal("owned Linux Core did not become ready; private log retained")
	}
	pid, _ := exec.Command("docker", "inspect", "--format", "{{.State.Pid}}", name).Output()
	n, _ := strconv.Atoi(strings.TrimSpace(string(pid)))
	return node, n
}
