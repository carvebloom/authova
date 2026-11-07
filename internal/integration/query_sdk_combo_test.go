//go:build contractcombo

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/contracts/sdk/query"
	"github.com/carvebloom/authova/internal/application"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPinnedContractCombinationQueryCLIAndMCP(t *testing.T) {
	// The actual compiled stdio server is the protocol boundary under test.
	// Credentials are private test-owned files, never argv or test output.
	binary := filepath.Join(t.TempDir(), "authova-query")
	build := exec.Command("go", "build", "-race", "-o", binary, "github.com/carvebloom/authova/contracts/cmd/authova-query")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("query binary build: %v %s", err, output)
	}
	agentDatabases(t, func(t *testing.T, f *fixture) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		a, i, _ := newQueryIdentity(t, f, "", queryScopes())
		config := query.Config{BaseURL: f.server.URL, Issuer: f.app1.Issuer, AppID: f.app1.ID, ClientID: a.Agent.ClientID, ClientSecret: a.Secret, InstallationID: i.ID, Scopes: queryScopes(), AllowLoopbackHTTP: true}
		path := filepath.Join(t.TempDir(), "query.json")
		raw, _ := json.Marshal(config)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		one := exec.CommandContext(ctx, binary, "-config", path, "-operation", "users", "-limit", "1")
		output, err := one.Output()
		if err != nil {
			t.Fatal("real query CLI failed", err)
		}
		var result query.Response
		if json.Unmarshal(output, &result) != nil || !strings.Contains(string(result.Data), f.user1.ID) || strings.Contains(string(output), a.Secret) || strings.Contains(string(output), f.user1.Email) {
			t.Fatal("CLI lost projection or disclosed contacts/credentials")
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "authova-core-query-combination", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.CommandContext(ctx, binary, "-mcp", "-config", path)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		tools, err := session.ListTools(ctx, nil)
		if err != nil || len(tools.Tools) != 12 {
			t.Fatal("stdio discovery did not expose the expected fixed read catalog", err)
		}
		for _, tool := range tools.Tools {
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatal("missing read-only tool annotation")
			}
		}
		for _, name := range []string{"authova_get_context", "authova_get_configuration", "authova_list_users", "authova_list_agents", "authova_list_installations", "authova_list_grants", "authova_list_audit"} {
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
			if err != nil || res.IsError {
				t.Fatal("real Core MCP query failed", name, err)
			}
			data, _ := json.Marshal(res)
			if strings.Contains(string(data), a.Secret) || strings.Contains(string(data), f.user1.PasswordHash) {
				t.Fatal("credential in MCP result")
			}
		}
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "authova_get_user", Arguments: map[string]any{"id": f.user2.ID}})
		if err == nil && !res.IsError {
			t.Fatal("MCP exposed a user from another App")
		}
		status, _ := agentJSON(t, f, "POST", "/agent-grants/"+i.AutonomousGrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "MCP query access revoked"})
		if status != 200 {
			t.Fatal(status)
		}
		res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "authova_get_context", Arguments: map[string]any{}})
		if err == nil && !res.IsError {
			t.Fatal("persistent MCP process retained access after committed revoke")
		}
	})
}
