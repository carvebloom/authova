package mcpquery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/contracts/sdk/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOfficialMCPToolsDiscoveryAndReadBoundary(t *testing.T) {
	var reads atomic.Int64
	var revoked atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-token", "token_type": "Bearer", "expires_in": 120})
			return
		}
		reads.Add(1)
		if revoked.Load() {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "authentication_required"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"app_id": "app", "items": []any{}})
	}))
	defer backend.Close()
	q, err := query.New(query.Config{BaseURL: backend.URL, Issuer: backend.URL + "/a/" + strings.Repeat("a", 32), AppID: "app", ClientID: "agent", ClientSecret: "private-secret", InstallationID: "install", Scopes: []string{"authova.query.context", "authova.query.users"}, AllowLoopbackHTTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(q).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "authova-query-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 3 {
		t.Fatal("unexpected exposed query tools", err)
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("query tool is not annotated read-only")
		}
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "authova_get_context", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatal("real MCP call failed", err)
	}
	before := reads.Load()
	for name, args := range map[string]map[string]any{"authova_get_context": {"url": "https://unapproved.example"}, "authova_get_user": {"id": "../../platform/operators"}, "authova_list_users": {"limit": 101}, "authova_delete_user": {"id": "user"}} {
		res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil && !res.IsError {
			t.Fatal("unexpected tool/path/argument accepted", name)
		}
	}
	if reads.Load() != before {
		t.Fatal("invalid MCP arguments reached the API")
	}
	revoked.Store(true)
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "authova_get_context", Arguments: map[string]any{}})
	if err == nil && !res.IsError {
		t.Fatal("MCP session cached authorization")
	}
}

type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("CANARY-TRANSPORT-PASSWORD")
}
func TestToolPanicIsSafeAndDoesNotCrashStdio(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	q, e := query.New(query.Config{BaseURL: "https://core.example", Issuer: "https://core.example/a/" + strings.Repeat("a", 32), AppID: "app", ClientID: "client", ClientSecret: "CANARY", InstallationID: "install", Scopes: []string{"authova.query.context"}}, &http.Client{Transport: panicTransport{}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, e := New(q).Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, e := client.Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	result, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "authova_get_context", Arguments: map[string]any{}})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(result)
	if !result.IsError || strings.Contains(string(raw), "CANARY") || strings.Contains(logs.String(), "CANARY") || !strings.Contains(logs.String(), "panic_unwind") {
		t.Fatal("unsafe or missing MCP panic boundary")
	}
}

func TestQueryFailureDiagnosticRetainsContactNotText(t *testing.T) {
	var out bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	defer slog.SetDefault(old)
	LogFailure(context.Background(), fmt.Errorf("CANARY-TOKEN: %w", context.Canceled))
	if strings.Contains(out.String(), "CANARY") || !strings.Contains(out.String(), "cancelled") || !strings.Contains(out.String(), "diagnostic_id") || !strings.Contains(out.String(), "recorded_at") {
		t.Fatal("unsafe or empty CLI diagnostic")
	}
}
