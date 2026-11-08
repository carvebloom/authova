// Command authova-query provides the same read-only operations as one-shot JSON
// queries or MCP stdio. Secrets stay in a private config file and process memory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/carvebloom/authova/contracts/mcpquery"
	"github.com/carvebloom/authova/contracts/sdk/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("authova-query", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := flags.String("config", "", "path to private 0600 query-identity JSON")
	stdio := flags.Bool("mcp", false, "serve the read-only tools over MCP stdio")
	q := query.Request{}
	flags.StringVar(&q.Operation, "operation", "context", "query operation (context, configuration, users, user, sessions, agents, agent, installations, installation, grants, grant, audit)")
	flags.StringVar(&q.ID, "id", "", "object ID")
	flags.StringVar(&q.UserID, "user-id", "", "user ID or grant/installation filter")
	flags.StringVar(&q.AgentID, "agent-id", "", "Agent filter")
	flags.StringVar(&q.InstallationID, "installation-id", "", "installation filter")
	flags.StringVar(&q.TenantID, "tenant-id", "", "optional tenant filter; never an authorization override")
	flags.StringVar(&q.Cursor, "cursor", "", "opaque pagination cursor")
	flags.IntVar(&q.Limit, "limit", 0, "page size, 1-100 (default 20 for lists)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stderr)
			flags.PrintDefaults()
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	c, err := query.LoadConfig(*config)
	if err != nil {
		return err
	}
	client, err := query.New(c, nil)
	if err != nil {
		return err
	}
	if *stdio {
		return mcpquery.New(client).Run(ctx, &mcp.StdioTransport{})
	}
	response, err := client.Query(ctx, q)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(response)
}

func main() {
	defer func() {
		if v := recover(); v != nil {
			_ = mcpquery.RecoveredPanic(context.Background(), v)
			fmt.Fprintln(os.Stderr, "Authova query failed")
			os.Exit(1)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		mcpquery.LogFailure(ctx, err)
		fmt.Fprintln(os.Stderr, query.PublicError(err))
		os.Exit(1)
	}
}
