package mcpquery

import (
	"context"
	"encoding/json"
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
