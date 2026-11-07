package domain

import (
	"encoding/json"
)

// UnmarshalJSON requires the complete policy. Missing/null booleans must not
// silently disable a method during a configuration update or corrupt DB read.
func (p *AuthenticationPolicy) UnmarshalJSON(data []byte) error {
	var values map[string]json.RawMessage
	if json.Unmarshal(data, &values) != nil || len(values) != 7 {
		return ErrInvalid
	}
	fields := map[string]*bool{"registration_enabled": &p.RegistrationEnabled, "password_login_enabled": &p.PasswordLoginEnabled, "email_otp_enabled": &p.EmailOTPEnabled, "phone_otp_enabled": &p.PhoneOTPEnabled, "federation_enabled": &p.FederationEnabled, "external_registration_enabled": &p.ExternalRegistrationEnabled}
	fields["passkey_enabled"] = &p.PasskeyEnabled
	for name, target := range fields {
		value, ok := values[name]
		if !ok || (string(value) != "true" && string(value) != "false") || json.Unmarshal(value, target) != nil {
			return ErrInvalid
		}
	}
	return nil
}
