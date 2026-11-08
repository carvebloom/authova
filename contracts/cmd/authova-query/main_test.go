package main

import (
	"bytes"
	"encoding/json"
	"github.com/carvebloom/authova/contracts/sdk/query"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompiledCLISafeStderrAndExitContract(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "query")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal("build failed", e, string(out))
	}
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "query-123")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"server_error","error_description":"CANARY-DSN-TOKEN-SQL"}`))
	}))
	defer core.Close()
	cfg := query.Config{BaseURL: core.URL, Issuer: core.URL + "/a/" + strings.Repeat("a", 32), AppID: "app", ClientID: "client", ClientSecret: "CANARY-PRIVATE", InstallationID: "install", Scopes: []string{"authova.query.context"}, AllowLoopbackHTTP: true}
	data, _ := json.Marshal(cfg)
	file := filepath.Join(t.TempDir(), "config.json")
	if e := os.WriteFile(file, data, 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--config", file}, {"--CANARY-TOKEN=secret"}} {
		cmd := exec.Command(bin, args...)
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		e := cmd.Run()
		exit, ok := e.(*exec.ExitError)
		if !ok || exit.ExitCode() != 1 || out.Len() != 0 {
			t.Fatal("CLI exit/stdout changed")
		}
		if strings.Contains(stderr.String(), "CANARY") || !strings.Contains(stderr.String(), "operation=query") || !strings.Contains(stderr.String(), "source=") {
			t.Fatal("stderr leaked or lost useful diagnostic", stderr.String())
		}
	}
}
