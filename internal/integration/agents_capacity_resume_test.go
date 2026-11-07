package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
)

// Resume only the authorization fixture after completed user construction. It
// never re-seeds or deletes users, and refuses an ambiguous population match.
func TestAgentCapacityResume(t *testing.T) {
	directory := os.Getenv("AUTHOVA_AGENT_ACCEPTANCE_DIR")
	count, err := strconv.ParseInt(os.Getenv("AUTHOVA_AGENT_CAPACITY_USERS"), 10, 64)
	if directory == "" || err != nil || os.Getenv("AUTHOVA_AGENT_RESUME_FIXTURE") != "1" {
		t.Skip("requires explicit resume in an owned private capacity run")
	}
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		t.Run(driver, func(t *testing.T) {
			path := filepath.Join(directory, driver+"-fixture.json")
			if _, err := os.Stat(path); err == nil {
				t.Log("completed fixture retained; no repeated credential mutation")
				return
			}
			key, err := os.ReadFile(os.Getenv("AUTHOVA_MASTER_KEY_FILE"))
			if err != nil {
				t.Fatal(err)
			}
			vault, err := secure.NewVault(key)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			store, err := sqlstore.Open(ctx, driver, dsn, "http://127.0.0.1:18080", vault, 4)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			var apps []domain.App
			if err = store.Platform(ctx, false, func(tx ports.PlatformTx) error {
				var err error
				apps, err = tx.Apps("", 100)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var saved agentRecoveryFixture
			matches := 0
			for _, app := range apps {
				scope, _ := domain.AppScope(app.ID)
				n, err := store.UserCount(ctx, scope)
				if err != nil {
					t.Fatal(err)
				}
				if n == count && app.Name == "Test App" {
					saved.App, saved.Actual = app, n
					matches++
				}
			}
			if matches != 1 {
				t.Fatal("resume requires exactly one complete synthetic population", matches)
			}
			base := strings.TrimSuffix(saved.App.Issuer, "/a/"+saved.App.Key)
			for _, app := range apps {
				if app.ID != saved.App.ID && app.Issuer == base+"/a/"+app.Key {
					saved.OtherApp = app
				}
			}
			if saved.OtherApp.ID == "" {
				t.Fatal("original paired App is unavailable")
			}
			scope, _ := domain.AppScope(saved.App.ID)
			if err := store.App(ctx, scope, false, func(tx ports.AppTx) error {
				var err error
				saved.User, err = tx.FindUser("email", vault.Digest(saved.App.ID+":identifier:email", "same@example.com"))
				if err != nil {
					return err
				}
				users, err := tx.Users("", 1000)
				for _, user := range users {
					saved.Users = append(saved.Users, user.ID)
				}
				if err == nil && len(saved.Users) == 0 {
					return errors.New("missing decrypted samples")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			f := restoredAgentFixture(t, driver, dsn, saved)
			authenticateAcceptanceOwner(t, f)
			f.client, f.secret, err = f.service.CreateClient(ctx, f.operatorToken, f.scope1, domain.Client{Name: "Resumed capacity resource", Kind: "confidential", RedirectURIs: []string{f.server.URL + "/test-callback"},
				Scopes: []string{"openid", "offline_access", "api.read"}, Audiences: []string{"urn:authova:integration"}, IntrospectionAudiences: []string{"urn:authova:integration"}, Grants: []string{"authorization_code", "refresh_token", "client_credentials"}}, "acceptance-"+application.ID())
			if err != nil {
				t.Fatal(err)
			}
			finishAgentCapacityFixture(t, f, saved.Users, saved.Actual, directory)
			data, _ := json.Marshal(map[string]any{"driver": driver, "actual_users": saved.Actual, "resumed_without_reseeding": true})
			if err := os.WriteFile(filepath.Join(directory, driver+"-resumed.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("resumed authorization fixture on retained actual users=%d", saved.Actual)
		})
	}
}
