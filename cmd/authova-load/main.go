// authova-load creates an explicitly synthetic data set in development mode only.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/config"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
)

func main() {
	count := flag.Int("users", 12000000, "synthetic users (development only)")
	input := flag.String("operator-file", ".dev/bootstrap.json", "private operator credential file")
	resume := flag.String("resume-app", "", "resume a synthetic App id after a failed fixture generation")
	output := flag.String("output", ".dev/load.json", "private fixture output")
	flag.Parse()
	ctx := context.Background()
	c, e := config.Load()
	if e != nil || !c.Development {
		slog.Error("load seeding requires validated development configuration")
		os.Exit(1)
	}
	vault, e := secure.NewVault(c.MasterKey)
	if e != nil {
		panic(e)
	}
	store, e := sqlstore.Open(ctx, c.Dialect, c.DSN, c.BaseURL, vault, 8)
	if e != nil {
		panic(e)
	}
	defer store.Close()
	service, e := application.New(store, vault, c.BaseURL, true, 2)
	if e != nil {
		panic(e)
	}
	data, e := os.ReadFile(*input)
	if e != nil {
		panic(e)
	}
	var operator struct {
		Email, Password string
		Secret          string `json:"totp_secret"`
	}
	if json.Unmarshal(data, &operator) != nil {
		panic("invalid operator input")
	}
	token, _, _, e := service.LoginOperator(ctx, operator.Email, operator.Password, secure.TOTP(operator.Secret, time.Now().Unix()/30))
	if e != nil {
		panic("operator authentication failed")
	}
	var app domain.App
	if *resume != "" {
		e = store.Platform(ctx, false, func(t ports.PlatformTx) error { var err error; app, err = t.App(*resume, false); return err })
		if e == nil && app.Name != "Synthetic capacity dataset" {
			panic("only synthetic App can be resumed")
		}
	} else {
		app, e = service.CreateApp(ctx, token, "Synthetic capacity dataset", "load-"+application.ID()[:8])
	}
	if e != nil {
		panic(e)
	}
	scope, _ := domain.AppScope(app.ID)
	client, secret, e := service.CreateClient(ctx, token, scope, domain.Client{Name: "Capacity resource", Kind: "confidential", Scopes: []string{"api.read"}, Audiences: []string{"urn:authova:capacity"}, IntrospectionAudiences: []string{"urn:authova:capacity"}, Grants: []string{"client_credentials"}}, "capacity-"+application.ID())
	if e != nil {
		panic(e)
	}
	hash, e := service.HashPassword(ctx, secure.Random(32))
	if e != nil {
		panic(e)
	}
	start := time.Now()
	slog.Info("synthetic load started", "users", *count, "database", c.Dialect)
	samples, e := store.LoadSyntheticUsers(ctx, scope, *count, hash, func(done int) {
		slog.Info("synthetic load progress", "users", done, "elapsed", time.Since(start).Round(time.Second))
	})
	if e != nil {
		panic(e)
	}
	actual, e := store.UserCount(ctx, scope)
	if e != nil {
		panic(e)
	}
	result := map[string]any{"app": app, "client_id": client.ClientID, "client_secret": secret, "sample_user_ids": samples, "actual_users": actual, "seed_seconds": time.Since(start).Seconds(), "database": c.Dialect}
	data, _ = json.MarshalIndent(result, "", "  ")
	if e = os.WriteFile(*output, data, 0600); e != nil {
		panic(e)
	}
	slog.Info("synthetic load complete", "actual_users", actual, "elapsed", time.Since(start).Round(time.Second), "private_output", *output)
}
