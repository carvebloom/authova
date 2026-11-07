package transport

import (
	"bytes"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
)

func TestHostedLoginRendersFlowSteps(t *testing.T) {
	for _, step := range []domain.FlowStep{
		domain.StepChoose, domain.StepVerifyIdentifier, domain.StepSetPassword,
		domain.StepResetPassword, domain.StepBindIdentifier, domain.StepResetComplete,
		domain.StepAccountComplete, domain.StepMFA, domain.StepMFAReplaceConfirm,
		domain.StepMFAMaintenanceComplete, domain.StepConsent, domain.StepComplete,
		domain.StepFederation, domain.StepCancelled, "logout", "signed_out",
	} {
		t.Run(string(step), func(t *testing.T) {
			page := templateLoginPage()
			page.Flow.Step = step
			renderLogin(t, page)
		})
	}
	for name, page := range map[string]loginPage{
		"mfa-enrollment": {Flow: flowPresentation{Step: domain.StepMFA}, Secret: "TEST-ONLY-SECRET"},
		"mfa-required":   {Flow: flowPresentation{Step: domain.StepMFA}, NeedsMFASetup: true},
		"recovery-codes": {RecoveryCodes: []string{"TEST-ONLY-RECOVERY"}},
		"error":          {Error: "Unable to verify identity"},
	} {
		t.Run(name, func(t *testing.T) { renderLogin(t, page) })
	}
}

func TestHostedLoginOnlyOffersPermittedProofForms(t *testing.T) {
	for _, tc := range []struct {
		name       string
		policy     domain.AuthenticationPolicy
		connection bool
		password   bool
		otp        bool
		federation bool
	}{
		{"all-methods", domain.DefaultAuthenticationPolicy(), true, true, true, true},
		{"third-party-only", domain.AuthenticationPolicy{FederationEnabled: true}, true, false, false, true},
		{"third-party-without-connection", domain.AuthenticationPolicy{FederationEnabled: true}, false, false, false, false},
		{"maintenance", domain.AuthenticationPolicy{}, true, false, false, false},
		{"email-otp-only", domain.AuthenticationPolicy{EmailOTPEnabled: true}, true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := templateLoginPage()
			page.Policy = tc.policy
			if !tc.connection {
				page.Connections = nil
			}
			html := renderLogin(t, page)
			for action, enabled := range map[string]bool{"password": tc.password, "challenges": tc.otp, "federation": tc.federation} {
				if strings.Contains(html, `action="`+page.Base+`/`+action+`"`) != enabled {
					t.Errorf("%s form presence differs from the current policy", action)
				}
			}
		})
	}
}

func templateLoginPage() loginPage {
	return loginPage{
		App: domain.App{Name: "Test App", Key: "app-key"}, Flow: flowPresentation{Step: domain.StepChoose},
		Policy: domain.DefaultAuthenticationPolicy(), Base: "/a/app-key/ui/flows/test-flow",
		CSRF: "TEST-ONLY-CSRF", ChallengeID: "test-challenge", Scopes: []string{"openid", "email"},
		Connections: []connectionPresentation{{ID: "test-connection", Name: "Test WeChat", ProviderType: "wechat", Status: "active"}},
	}
}

func renderLogin(t *testing.T, page loginPage) string {
	t.Helper()
	var body bytes.Buffer
	if err := loginTemplate.Execute(&body, page); err != nil {
		t.Fatalf("hosted login template execution failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(body.String()), "<!doctype html>") {
		t.Fatal("hosted login did not render a complete HTML document")
	}
	return body.String()
}

func TestHostedPasskeyPolicyAndReadiness(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		enabled, available, registration, offered bool
	}{
		{"passkey-only", true, true, false, true}, {"passwordless-registration", true, true, true, true}, {"unusable-domain", true, false, true, false}, {"disabled", false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := templateLoginPage()
			page.Policy = domain.AuthenticationPolicy{PasskeyEnabled: tc.enabled, RegistrationEnabled: tc.registration}
			page.PasskeyAvailable = tc.available
			body := renderLogin(t, page)
			if strings.Contains(body, "data-passkey-login") != tc.offered {
				t.Fatal("Passkey offering does not match policy and domain")
			}
			if strings.Contains(body, `value="register"`) != (tc.offered && tc.registration) {
				t.Fatal("verified-address registration missing or incorrectly offered")
			}
			if strings.Contains(body, `name="password"`) {
				t.Fatal("password form leaked into Passkey-only policy")
			}
			page.Flow.Step = domain.StepSetPassword
			body = renderLogin(t, page)
			if strings.Contains(body, "data-passkey-register") != tc.offered {
				t.Fatal("registration credential form readiness mismatch")
			}
		})
	}
}
