//go:build contractcombo

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"testing"

	management "github.com/carvebloom/authova/contracts/generated/management"
	"github.com/carvebloom/authova/contracts/sdk"
)

// This explicitly opted-in candidate-combination test compiles against the
// selected sibling Contracts module. Ordinary Core builds stay independent.
// It checks selected real HTTP use cases, not full OpenAPI conformance.
func TestPinnedContractCombination(t *testing.T) {
	dialects := []string{"mysql", "postgres"}
	switch selected := os.Getenv("AUTHOVA_TEST_DATABASE_DIALECT"); selected {
	case "", "both":
	case "mysql", "postgres":
		dialects = []string{selected}
	default:
		t.Fatal("invalid explicitly selected contract database dialect")
	}
	for _, dialect := range dialects {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			t.Fatal("combination gate requires every explicitly selected real database")
		}
		t.Run(dialect, func(t *testing.T) {
			f := setup(t, dialect, dsn)
			client, e := sdk.New(f.server.URL)
			if e != nil {
				t.Fatal(e)
			}
			get := func(path string, target any) {
				t.Helper()
				r, e := client.Request(context.Background(), "GET", "/api/v1/apps/"+f.app1.ID+path, f.operatorToken, "", nil)
				if e != nil || r.Status != 200 {
					t.Fatal("contract HTTP read", r.Status, e)
				}
				d := json.NewDecoder(bytes.NewReader(r.Body))
				d.DisallowUnknownFields()
				if e = d.Decode(target); e != nil {
					t.Fatal("contract response shape", e)
				}
			}
			var app management.App
			get("", &app)
			if app.Id != f.app1.ID || app.ConfigVersion < 1 {
				t.Fatal("App identity/version absent")
			}
			var policy management.AuthenticationConfig
			get("/authentication-config", &policy)
			if policy.ConfigVersion < 1 || !policy.Policy.PasswordLoginEnabled {
				t.Fatal("policy facts absent")
			}
			var users management.UserPage
			get("/users", &users)
			if len(users.Items) != 1 || users.Items[0].Id != f.user1.ID {
				t.Fatal("user shape")
			}
			var clients management.ClientPage
			get("/clients", &clients)
			if len(clients.Items) == 0 {
				t.Fatal("client shape")
			}
			var keys management.SigningKeyPage
			get("/keys", &keys)
			if len(keys.Items) != 1 {
				t.Fatal("key shape")
			}
			body, e := json.Marshal(management.AuthenticationPolicyPatch{ConfigVersion: policy.ConfigVersion, Reason: "contract combination policy update", Policy: policy.Policy})
			if e != nil {
				t.Fatal(e)
			}
			path := "/api/v1/apps/" + f.app1.ID + "/authentication-config"
			r, e := client.Request(context.Background(), "PATCH", path, f.operatorToken, "", body)
			if e != nil || r.Status != 200 {
				t.Fatal("generated input rejected", r.Status, e)
			}
			var updated management.AuthenticationConfig
			if e = json.Unmarshal(r.Body, &updated); e != nil || updated.ConfigVersion != policy.ConfigVersion+1 {
				t.Fatal("version contract", e)
			}
			r, e = client.Request(context.Background(), "PATCH", path, f.operatorToken, "", body)
			// Request preserves raw HTTP rejection facts; Login additionally parses a typed problem.
			if r.Status != 409 || e != nil {
				t.Fatal("stale version contract", r.Status, e)
			}
			var problem management.Problem
			if err := json.Unmarshal(r.Body, &problem); err != nil || problem.Code != "conflict" || problem.Status != 409 {
				t.Fatal("problem contract", fmt.Sprint(err))
			}
			f.alipayContractCombination(t, client)
		})
	}
}

func (f *fixture) alipayContractCombination(t *testing.T, client *sdk.Client) {
	t.Helper()
	application, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	kind := management.ConnectionCreateProviderTypeAlipay
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(application)}))
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	command := management.ConnectionCreate{
		Name: "Generated Alipay contract", ClientId: "2021000000000001", ProviderType: &kind,
		AlipayCredentials: &management.AlipayCredentials{
			PrivateKey:      &privatePEM,
			AlipayPublicKey: &publicPEM,
		},
	}
	body, err := json.Marshal(command)
	if err != nil {
		t.Fatal("generated Alipay command marshal", err)
	}
	path := "/api/v1/apps/" + f.app1.ID + "/connections"
	r, err := client.Request(context.Background(), "POST", path, f.operatorToken, "", body)
	if err != nil || r.Status != 201 {
		t.Fatal("generated Alipay create rejected", r.Status, err)
	}
	var connection management.Connection
	decoder := json.NewDecoder(bytes.NewReader(r.Body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&connection); err != nil || connection.ProviderType != management.ConnectionProviderTypeAlipay {
		t.Fatal("generated Alipay response contract", err)
	}
	body, err = json.Marshal(management.ConnectionPatch{ConfigVersion: connection.ConfigVersion, Reason: "typed Alipay pair update", AlipayCredentials: command.AlipayCredentials})
	if err != nil {
		t.Fatal(err)
	}
	r, err = client.Request(context.Background(), "PATCH", path+"/"+connection.Id, f.operatorToken, "", body)
	if err != nil || r.Status != 200 || bytes.Contains(r.Body, []byte("PRIVATE KEY")) || bytes.Contains(r.Body, []byte("alipay_credentials")) {
		t.Fatal("generated Alipay update or secret boundary", r.Status, err)
	}
}
