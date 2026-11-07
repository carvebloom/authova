package transport

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
)

func TestAccountPresentationExcludesCredentials(t *testing.T) {
	user := domain.User{
		ID: "user-id", DisplayName: "Test User", Email: "user@example.test", Status: "active",
		PasswordHash: "PRIVATE-PASSWORD-HASH-MARKER", TOTPSecret: "PRIVATE-TOTP-MARKER",
	}
	connection := domain.Connection{
		ID: "connection-id", Name: "Alipay", ProviderType: "alipay", Status: "active",
		Secret: "PRIVATE-PROVIDER-RSA-KEY-MARKER",
	}
	view := accountPresentation{
		App: domain.App{Name: "Test App", Key: "app-key"}, User: presentUser(user),
		Connections: []accountConnection{{connectionPresentation: presentConnection(connection), Bound: 1}},
		Base:        "/a/app-key/ui/account", CSRF: "test-csrf", AssetVersion: loginAssetVersion,
	}
	if !view.User.MFAConfigured || view.User.Email != user.Email {
		t.Fatal("public account facts were lost")
	}
	var html bytes.Buffer
	if err := accountTemplate.Execute(&html, view); err != nil {
		t.Fatal("account template execution failed", err)
	}
	// Even accidentally formatting the entire view must not reveal credentials.
	for _, output := range []string{html.String(), fmt.Sprintf("%+v", view), fmt.Sprintf("%+v", presentConnections([]domain.Connection{connection}))} {
		for _, secret := range []string{user.PasswordHash, user.TOTPSecret, connection.Secret} {
			if strings.Contains(output, secret) {
				t.Fatal("presentation model exposed an internal credential")
			}
		}
	}
	for _, action := range []string{"password", "identifier", "link", "unlink", "erase", "mfa/replace", "mfa/recovery-codes"} {
		if !strings.Contains(html.String(), `action="`+view.Base+`/`+action+`"`) {
			t.Errorf("account %s action missing", action)
		}
	}
}

func TestFederationNavigationPresentation(t *testing.T) {
	var html bytes.Buffer
	view := federationPresentation{
		App: domain.App{Name: "Test App", Key: "app-key"}, AssetVersion: loginAssetVersion,
		URL: "https://openauth.alipay.com/oauth2/publicAppAuthorize.htm?app_id=2021000000000001&state=bound-state",
	}
	if err := federationNavigation.Execute(&html, view); err != nil {
		t.Fatal("provider navigation template execution failed", err)
	}
	if !strings.Contains(html.String(), `id="identity-provider-link"`) || !strings.Contains(html.String(), "https://openauth.alipay.com/") {
		t.Fatal("provider navigation no longer exposes the guarded fallback link")
	}
}
