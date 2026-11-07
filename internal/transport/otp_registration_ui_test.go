package transport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
)

func TestRegistrationOptionsEffectiveCapabilities(t *testing.T) {
	for _, password := range []bool{false, true} {
		for _, passkeyPolicy := range []bool{false, true} {
			for _, passkeyAvailable := range []bool{false, true} {
				for _, kind := range []string{"email", "phone"} {
					name := fmt.Sprintf("password=%t/passkey-policy=%t/deployment=%t/%s", password, passkeyPolicy, passkeyAvailable, kind)
					t.Run(name, func(t *testing.T) {
						page := templateLoginPage()
						page.Policy = domain.AuthenticationPolicy{RegistrationEnabled: true, PasswordLoginEnabled: password, PasskeyEnabled: passkeyPolicy, EmailOTPEnabled: kind == "email", PhoneOTPEnabled: kind == "phone"}
						page.PasskeyAvailable = passkeyAvailable
						flow := domain.Flow{VerifiedKind: kind}
						page.registrationOptions(flow, false)
						wantOnly := !password && !(passkeyPolicy && passkeyAvailable)
						if page.OTPOnlyRegistration != wantOnly || page.OTPRegistration != !password {
							t.Fatal("registration options do not reflect effective App capabilities")
						}
						page.Flow.Step = domain.StepSetPassword
						body := renderLogin(t, page)
						if !strings.Contains(body, `action="`+page.Base+`/register"`) {
							t.Fatal("verified registration lost its account-creation action")
						}
						if strings.Contains(body, "data-passkey-register") != (passkeyPolicy && passkeyAvailable) {
							t.Fatal("Passkey registration differs from effective App capabilities")
						}
						// The POST response must recompute options from current policy.
						page.Policy.RegistrationEnabled = false
						page.registrationOptions(flow, false)
						if page.OTPRegistration || (!password && strings.Contains(renderLogin(t, page), `action="`+page.Base+`/register"`)) {
							t.Fatal("registration kept stale options after policy changed")
						}
					})
				}
			}
		}
	}
}

func TestOTPOnlyRegistrationRendersNoPasswordForm(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy domain.AuthenticationPolicy
	}{
		{"email", domain.AuthenticationPolicy{RegistrationEnabled: true, EmailOTPEnabled: true}},
		{"phone", domain.AuthenticationPolicy{RegistrationEnabled: true, PhoneOTPEnabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := templateLoginPage()
			page.Policy = tc.policy
			page.PasskeyAvailable = true // Supported deployment; App explicitly disables Passkey.
			page.registrationOptions(domain.Flow{VerifiedKind: tc.name}, false)
			page.Flow.Step = domain.StepSetPassword
			body := renderLogin(t, page)
			if strings.Count(body, `action="`+page.Base+`/register"`) != 1 {
				t.Fatal("OTP registration form does not post exactly once to the register endpoint")
			}
			if !strings.Contains(body, `name="_csrf" value="TEST-ONLY-CSRF"`) {
				t.Fatal("OTP registration form lost the CSRF hidden field")
			}
			if !strings.Contains(body, `name="display_name"`) {
				t.Fatal("OTP registration form lost the optional display name field")
			}
			if strings.Contains(body, `name="password"`) {
				t.Fatal("password field leaked into OTP-only registration")
			}
			if strings.Contains(body, "data-passkey-register") {
				t.Fatal("Passkey form rendered without an available Passkey path")
			}
		})
	}
}

func TestOTPOnlyRegistrationOfferedOnChooseMethod(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		policy                    domain.AuthenticationPolicy
		enabledFlag, disabledFlag string
	}{
		{"email", domain.AuthenticationPolicy{RegistrationEnabled: true, EmailOTPEnabled: true}, `data-email-otp="true"`, `data-phone-otp="false"`},
		{"phone", domain.AuthenticationPolicy{RegistrationEnabled: true, PhoneOTPEnabled: true}, `data-phone-otp="true"`, `data-email-otp="false"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := templateLoginPage()
			page.Policy = tc.policy
			page.PasskeyAvailable = true
			page.registrationOptions(domain.Flow{}, false)
			body := renderLogin(t, page)
			if !strings.Contains(body, `value="register"`) {
				t.Fatal("registration not offered for an enabled OTP channel")
			}
			if !strings.Contains(body, `data-otp-only-registration="true"`) {
				t.Fatal("channel filtering flag missing for OTP-only registration")
			}
			if !strings.Contains(body, tc.enabledFlag) || !strings.Contains(body, tc.disabledFlag) {
				t.Fatal("channel select cannot exclude the disabled OTP channel")
			}
			if strings.Contains(body, `value="reset_password"`) {
				t.Fatal("password reset offered without password login")
			}
			if strings.Contains(body, `name="password"`) {
				t.Fatal("password form leaked into OTP-only policy")
			}
		})
	}
}

func TestRegistrationDisabledOffersNoRegistration(t *testing.T) {
	page := templateLoginPage()
	page.Policy = domain.AuthenticationPolicy{EmailOTPEnabled: true, PhoneOTPEnabled: true}
	page.registrationOptions(domain.Flow{}, false)
	if body := renderLogin(t, page); strings.Contains(body, `value="register"`) {
		t.Fatal("registration offered while disabled by policy")
	}
	page.Flow.Step = domain.StepSetPassword
	if body := renderLogin(t, page); strings.Contains(body, `/register"`) {
		t.Fatal("register action rendered while registration is disabled")
	}
}

func TestPasswordAndPasskeyRegistrationPreserved(t *testing.T) {
	page := templateLoginPage()
	page.Flow.Step = domain.StepSetPassword
	body := renderLogin(t, page)
	if !strings.Contains(body, `name="password"`) {
		t.Fatal("password registration form lost its password field")
	}
	if !strings.Contains(body, `action="`+page.Base+`/register"`) {
		t.Fatal("password registration form lost the register action")
	}
	page = templateLoginPage()
	page.Policy = domain.AuthenticationPolicy{RegistrationEnabled: true, PasskeyEnabled: true}
	page.PasskeyAvailable = true
	page.registrationOptions(domain.Flow{}, false)
	if body := renderLogin(t, page); !strings.Contains(body, `value="register"`) {
		t.Fatal("Passkey registration path no longer offered")
	}
	page.Flow.Step = domain.StepSetPassword
	if body := renderLogin(t, page); !strings.Contains(body, "data-passkey-register") {
		t.Fatal("Passkey registration form missing on set_password")
	}
}
