package domain

// QueryAudience separates read-only control-plane access from business and
// Console credentials. Approval uses the existing Agent/installation lifecycle.
func QueryAudience(app string) string { return "urn:authova:query:" + app }

const QueryScopePrefix = "authova.query."

// QueryCapability is a closed operation catalog; no caller-supplied path or
// method can select a management mutation. Tenant credentials are deliberately
// limited to facts that carry an authoritative tenant key.
func QueryCapability(kind string) (scope string, tenantAllowed bool) {
	switch kind {
	case "context":
		return QueryScopePrefix + "context", true
	case "configuration":
		return QueryScopePrefix + "configuration", false
	case "users", "user":
		return QueryScopePrefix + "users", false
	case "sessions":
		return QueryScopePrefix + "sessions", false
	case "agents", "agent":
		return QueryScopePrefix + "agents", false
	case "installations", "installation":
		return QueryScopePrefix + "installations", true
	case "grants", "grant":
		return QueryScopePrefix + "grants", true
	case "audit":
		return QueryScopePrefix + "audit", false
	}
	return "", false
}

type QueryPrincipal struct {
	AppID          string   `json:"app_id"`
	ClientID       string   `json:"client_id"`
	AgentID        string   `json:"agent_id"`
	InstallationID string   `json:"installation_id"`
	GrantID        string   `json:"grant_id"`
	TenantID       string   `json:"tenant_id"`
	Scopes         []string `json:"scopes"`
	Expires        int64    `json:"expires_at"`
}
