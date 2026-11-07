package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/carvebloom/authova/contracts/sdk/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}
func TestOfficialMCPClientAuthorizationAndRevocation(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	var issuer string
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agentsdk.Principal{Active: active.Load(), Issuer: issuer, AppID: "app", Subject: "agent-client", ClientID: "agent-client", AgentID: "agent", InstallationID: "installation", GrantID: "grant", TenantID: "tenant", Mode: "autonomous", PrincipalType: "client", Audience: []string{"http://localhost:18288/mcp"}, Scope: "tickets:read", Issued: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix(), TokenID: "token"})
	}))
	defer identity.Close()
	issuer = identity.URL
	h, e := handler(config{Issuer: issuer, AppID: "app", Resource: "http://localhost:18288/mcp", PublicURL: "http://localhost:18288", ResourceClientID: "resource", ResourceClientSecret: "secret", TenantID: "tenant", AgentID: "agent"})
	if e != nil {
		t.Fatal(e)
	}
	resource := httptest.NewServer(h)
	defer resource.Close()
	transport := &mcp.StreamableClientTransport{Endpoint: resource.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{http.DefaultTransport, "synthetic-token"}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "authova-test", Version: "0.1.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, e := client.Connect(ctx, transport, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, &mcp.ListToolsParams{})
	if e != nil || len(tools.Tools) != 1 {
		t.Fatalf("tool discovery: %v", e)
	}
	response, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_tickets", Arguments: map[string]any{}})
	if e != nil || response.IsError {
		t.Fatalf("tool call: %v", e)
	}
	active.Store(false)
	if _, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "list_tickets", Arguments: map[string]any{}}); e == nil {
		t.Fatal("MCP session retained authorization after revoke")
	}
}
