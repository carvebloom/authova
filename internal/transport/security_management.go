package transport

import (
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/federation"
)

func (s *Server) authenticationConfig(a domain.App) map[string]any {
	p := a.AuthenticationPolicy()
	return map[string]any{"passkey_available": s.Service.PasskeyAvailable(a), "password_login": true, "email_otp_configured": s.EmailConfigured, "phone_otp_configured": s.PhoneConfigured, "provider_types": federation.Types(), "federation_callback_url": a.Issuer + "/ui/federation/callback", "policy": p, "config_version": a.Version}
}
