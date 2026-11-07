// Command agent-resource exposes a small tenant-scoped REST API and official
// Go SDK MCP server, protected by live Authova Agent introspection.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	agentsdk "github.com/carvebloom/authova/contracts/sdk/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type config struct {
	Listen               string `json:"listen"`
	PublicURL            string `json:"public_url"`
	Issuer               string `json:"issuer"`
	AppID                string `json:"app_id"`
	Resource             string `json:"resource"`
	ResourceClientID     string `json:"resource_client_id"`
	ResourceClientSecret string `json:"resource_client_secret"`
	TenantID             string `json:"tenant_id"`
	AgentID              string `json:"agent_id"`
	UserID               string `json:"user_id"`
}
type ticket struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Tenant string `json:"tenant_id"`
}
type ticketResult struct {
	Tickets []ticket `json:"tickets"`
	AgentID string   `json:"agent_id"`
	Subject string   `json:"subject"`
	GrantID string   `json:"grant_id"`
	Tenant  string   `json:"tenant_id"`
}

func businessPolicy(c config, p agentsdk.Principal) error {
	if p.AgentID != c.AgentID || p.TenantID != c.TenantID {
		return errors.New("agent or tenant is not approved")
	}
	if p.Mode == "delegated" && (c.UserID != "" && p.Subject != c.UserID) {
		return errors.New("user has no permission to these tickets")
	}
	return nil
}
func result(c config, p agentsdk.Principal) ticketResult {
	// Explicit synthetic data. A real application queries its own database with
	// BOTH verified tenant and current object/user authorization predicates.
	return ticketResult{Tickets: []ticket{{"demo-ticket-1", "示例工单：更新登录说明", c.TenantID}}, AgentID: p.AgentID, Subject: p.Subject, GrantID: p.GrantID, Tenant: p.TenantID}
}
func handler(c config) (http.Handler, error) {
	verifier, e := agentsdk.NewVerifier(agentsdk.Config{Issuer: c.Issuer, AppID: c.AppID, Resource: c.Resource, ClientID: c.ResourceClientID, ClientSecret: c.ResourceClientSecret, AllowLoopbackHTTP: true})
	if e != nil {
		return nil, e
	}
	mux := http.NewServeMux()
	mux.Handle("GET /.well-known/oauth-protected-resource", verifier.ProtectedResourceMetadata([]string{"tickets:read"}))
	tenant := func(*http.Request) (string, error) { return c.TenantID, nil }
	authorize := func(_ context.Context, p agentsdk.Principal, _ *http.Request) error { return businessPolicy(c, p) }
	api := verifier.Middleware([]string{"tickets:read"}, tenant, authorize, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := agentsdk.FromContext(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result(c, p))
	}))
	mux.Handle("GET /api/tickets", api)
	stream := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		p, ok := agentsdk.FromContext(r.Context())
		if !ok {
			return nil
		}
		server := mcp.NewServer(&mcp.Implementation{Name: "authova-agent-resource", Version: "0.1.0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "list_tickets", Description: "Read synthetic tickets in the authorized tenant"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ticketResult, error) {
			if e := businessPolicy(c, p); e != nil {
				return nil, ticketResult{}, e
			}
			return nil, result(c, p), nil
		})
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mcpHandler := verifier.Middleware([]string{"tickets:read"}, tenant, authorize, stream)
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+c.PublicURL+`/.well-known/oauth-protected-resource"`)
		mcpHandler.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"demo": "Authova Agent 身份与委托", "data": "synthetic", "rest": "/api/tickets", "mcp": "/mcp", "tenant": c.TenantID, "issuer": c.Issuer, "instructions": "Use the paired agent-client CLI; credentials stay in private configuration files."})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		mux.ServeHTTP(w, r)
	}), nil
}
func readConfig(path string) (config, error) {
	var c config
	info, e := os.Stat(path)
	if e != nil {
		return c, e
	}
	if info.Mode().Perm()&0077 != 0 {
		return c, errors.New("configuration must have mode 0600")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if json.Unmarshal(raw, &c) != nil {
		return c, errors.New("invalid configuration")
	}
	host, _, e := net.SplitHostPort(c.Listen)
	if e != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return c, errors.New("example must listen on loopback")
	}
	u, e := url.Parse(c.PublicURL)
	if e != nil || u.Host == "" || !strings.HasPrefix(c.Resource, c.PublicURL+"/") || c.AgentID == "" {
		return c, errors.New("invalid example resource configuration")
	}
	return c, nil
}
func main() {
	path := flag.String("config", "", "private 0600 configuration JSON")
	flag.Parse()
	c, e := readConfig(*path)
	if e != nil {
		log.Fatal("configuration rejected")
	}
	h, e := handler(c)
	if e != nil {
		log.Fatal("verifier configuration rejected")
	}
	server := &http.Server{Addr: c.Listen, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	fmt.Println("Authova Agent resource ready at " + c.PublicURL)
	if e = server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		log.Fatal("resource server stopped unexpectedly")
	}
}
