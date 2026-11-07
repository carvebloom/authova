package domain

import "time"

const (
	AgentDelegated  = "delegated"
	AgentAutonomous = "autonomous"
)

// Agent is a preregistered OAuth client with an independent permission ceiling.
// An Agent belongs to exactly one App; it is not a human User.
type Agent struct {
	ID          string    `json:"id"`
	AppID       string    `json:"app_id"`
	ClientID    string    `json:"client_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	Modes       []string  `json:"allowed_modes"`
	Scopes      []string  `json:"allowed_scopes"`
	Resources   []string  `json:"allowed_resources"`
	TokenTTL    int64     `json:"token_ttl_seconds"`
	GrantTTL    int64     `json:"grant_ttl_seconds"`
	Version     int64     `json:"config_version"`
	CreatedAt   time.Time `json:"created_at"`
}

// AgentInstallation binds a client to an administratively approved context.
// TenantID is an external business identifier, never an organization model.
// Tenant-scoped delegated installations require a pinned UserID: the trusted
// business backend provisions membership, rather than trusting client input.
type AgentInstallation struct {
	ID                string    `json:"id"`
	AppID             string    `json:"app_id"`
	AgentID           string    `json:"agent_id"`
	TenantID          string    `json:"tenant_id"`
	UserID            string    `json:"user_id"`
	ResponsibleParty  string    `json:"responsible_party"`
	Resource          string    `json:"resource"`
	Scopes            []string  `json:"allowed_scopes"`
	Modes             []string  `json:"allowed_modes"`
	Status            string    `json:"status"`
	Version           int64     `json:"config_version"`
	CreatedAt         time.Time `json:"created_at"`
	Expires           int64     `json:"expires_at"`
	AutonomousGrantID string    `json:"autonomous_grant_id"`
}

// AgentGrant records durable consent independently from the OAuth client.
// Delegated grants retain their own user session and refresh-token family.
type AgentGrant struct {
	ID             string    `json:"id"`
	AppID          string    `json:"app_id"`
	AgentID        string    `json:"agent_id"`
	InstallationID string    `json:"installation_id"`
	ClientID       string    `json:"client_id"`
	UserID         string    `json:"user_id"`
	TenantID       string    `json:"tenant_id"`
	Mode           string    `json:"mode"`
	Resource       string    `json:"resource"`
	Scopes         []string  `json:"scopes"`
	Status         string    `json:"status"`
	Version        int64     `json:"config_version"`
	CreatedAt      time.Time `json:"created_at"`
	Expires        int64     `json:"expires_at"`
	RevokedAt      int64     `json:"revoked_at"`
	SessionID      string    `json:"-"`
}

// AgentTokenBinding is an immutable issuance snapshot. Online liveness checks
// compare all three versions against current primary-database state.
type AgentTokenBinding struct {
	TokenID, AgentID, InstallationID, GrantID, TenantID, Mode, UserID string
	AgentVersion, InstallationVersion, GrantVersion                   int64
}

type AgentFilter struct {
	After                                     string
	Limit                                     int
	AgentID, InstallationID, UserID, TenantID string
}
