package transport

import (
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

// Template models deliberately exclude provider credentials and user proofs.
// JSON omission tags on domain objects do not protect html/template fields.
type connectionPresentation struct {
	ID, Name, Issuer, ClientID, Status, ProviderType string
	Version                                          int64
	CreatedAt                                        time.Time
}

func presentConnection(c domain.Connection) connectionPresentation {
	return connectionPresentation{
		ID: c.ID, Name: c.Name, Issuer: c.Issuer, ClientID: c.ClientID,
		Status: c.Status, ProviderType: c.ProviderType, Version: c.Version, CreatedAt: c.CreatedAt,
	}
}

func presentConnections(connections []domain.Connection) []connectionPresentation {
	result := make([]connectionPresentation, 0, len(connections))
	for _, c := range connections {
		result = append(result, presentConnection(c))
	}
	return result
}

type userPresentation struct {
	ID, DisplayName, Email, Phone, Status string
	MFAConfigured                         bool
}

func presentUser(u domain.User) userPresentation {
	return userPresentation{
		ID: u.ID, DisplayName: u.DisplayName, Email: u.Email, Phone: u.Phone,
		Status: u.Status, MFAConfigured: u.TOTPSecret != "",
	}
}

type flowPresentation struct{ Step domain.FlowStep }

type accountPresentation struct {
	PasskeyEnabled, PasskeyAvailable bool
	App                              domain.App
	User                             userPresentation
	Connections                      []accountConnection
	Base, CSRF, AssetVersion         string
}

type federationPresentation struct {
	App               domain.App
	URL, AssetVersion string
}
