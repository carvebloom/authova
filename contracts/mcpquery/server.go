// Package mcpquery adapts the fixed read-only query API to MCP tools. The App,
// issuer, credential and capability ceiling belong to trusted configuration,
// never to model-generated tool arguments.
package mcpquery

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/carvebloom/authova/contracts/sdk/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type emptyArgs struct{}
type pageArgs struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque next_cursor returned by the previous page"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; default 20"`
}
type objectArgs struct {
	ID string `json:"id" jsonschema:"Object ID in the configured App"`
}
type sessionArgs struct {
	UserID string `json:"user_id" jsonschema:"User ID in the configured App"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type filterArgs struct {
	Cursor         string `json:"cursor,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	InstallationID string `json:"installation_id,omitempty"`
	UserID         string `json:"user_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty" jsonschema:"Optional filter; cannot override the credential's tenant boundary"`
}

type Result struct {
	Data      map[string]any `json:"data"`
	RequestID string         `json:"request_id,omitempty"`
}

// New exposes only the operations requested in the local configuration.
// This catalog is a usability filter, not authorization: Core checks the live
// Agent/installation/grant and exact scope for every tool request.
func New(client *query.Client) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "authova-query", Version: "0.1.0"}, nil)
	for _, op := range query.Operations() {
		if !slices.Contains(client.Scopes(), op.Scope) {
			continue
		}
		switch op.Name {
		case "context":
			add(server, client, op, func(emptyArgs) query.Request { return query.Request{Operation: op.Name} })
		case "user", "agent", "installation", "grant":
			add(server, client, op, func(a objectArgs) query.Request { return query.Request{Operation: op.Name, ID: a.ID} })
		case "sessions":
			add(server, client, op, func(a sessionArgs) query.Request {
				return query.Request{Operation: op.Name, UserID: a.UserID, Cursor: a.Cursor, Limit: a.Limit}
			})
		case "installations", "grants":
			add(server, client, op, func(a filterArgs) query.Request {
				return query.Request{Operation: op.Name, Cursor: a.Cursor, Limit: a.Limit, AgentID: a.AgentID, InstallationID: a.InstallationID, UserID: a.UserID, TenantID: a.TenantID}
			})
		default:
			add(server, client, op, func(a pageArgs) query.Request {
				return query.Request{Operation: op.Name, Cursor: a.Cursor, Limit: a.Limit}
			})
		}
	}
	return server
}

func ToolName(operation string) string {
	switch operation {
	case "context":
		return "authova_get_context"
	case "configuration":
		return "authova_get_configuration"
	case "user":
		return "authova_get_user"
	case "agent":
		return "authova_get_agent"
	case "installation":
		return "authova_get_installation"
	case "grant":
		return "authova_get_grant"
	case "sessions":
		return "authova_list_user_sessions"
	default:
		return "authova_list_" + operation
	}
}

func add[T any](server *mcp.Server, client *query.Client, op query.Operation, request func(T) query.Request) {
	no := false
	mcp.AddTool(server, &mcp.Tool{Name: ToolName(op.Name), Description: op.Description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &no, OpenWorldHint: &no}}, func(ctx context.Context, _ *mcp.CallToolRequest, args T) (toolResult *mcp.CallToolResult, output Result, failure error) {
		defer func() {
			if v := recover(); v != nil {
				failure = RecoveredPanic(ctx, v)
				toolResult = nil
				output = Result{}
			}
		}()
		response, err := client.Query(ctx, request(args))
		if err != nil {
			LogFailure(ctx, err)
			return nil, Result{}, err
		}
		out := Result{RequestID: response.RequestID}
		if err = json.Unmarshal(response.Data, &out.Data); err != nil {
			err = &responseFailure{cause: err}
			LogFailure(ctx, err)
			return nil, Result{}, err
		}
		return nil, out, nil
	})
}
