package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
	"os"
)

// A single bounded page makes an operator-controlled checkpoint explicit and
// keeps an unknown commit from silently moving a long-running migration onward.
func rewrap(ctx context.Context, store *sqlstore.Store, args []string) error {
	fs := flag.NewFlagSet("rewrap", flag.ContinueOnError)
	app := fs.String("app", "", "App ID; empty selects platform")
	table := fs.String("table", "", "allowlisted payload table")
	after := fs.String("after", "[]", "JSON array cursor from prior known successful page")
	limit := fs.Int("limit", 100, "page size 1..500")
	verify := fs.Bool("verify", true, "authenticate and inventory without writes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected rewrap arguments")
	}
	var cursor []string
	if err := json.Unmarshal([]byte(*after), &cursor); err != nil {
		return fmt.Errorf("invalid cursor")
	}
	result, err := store.RewrapPage(ctx, *app, *table, cursor, *limit, *verify)
	if err != nil {
		return err
	} // never emit a resumable cursor after failed/unknown commit
	return json.NewEncoder(os.Stdout).Encode(result)
}
