package ports

import "github.com/carvebloom/authova/internal/domain"

// AgentTx is a capability of the same App-bound transaction. No method accepts
// an App identifier, and no repository can select another App.
type AgentTx interface {
	Agent(string, bool) (domain.Agent, error)
	SharedAgent(string) (domain.Agent, error)
	AgentByClient(string) (domain.Agent, error)
	Agents(string, int) ([]domain.Agent, error)
	PutAgent(domain.Agent) error
	AgentInstallation(string, bool) (domain.AgentInstallation, error)
	SharedAgentInstallation(string) (domain.AgentInstallation, error)
	AgentInstallations(domain.AgentFilter) ([]domain.AgentInstallation, error)
	PutAgentInstallation(domain.AgentInstallation) error
	AgentGrant(string, bool) (domain.AgentGrant, error)
	SharedAgentGrant(string) (domain.AgentGrant, error)
	AgentGrants(domain.AgentFilter) ([]domain.AgentGrant, error)
	PutAgentGrant(domain.AgentGrant) error
	AgentToken(string) (domain.AgentTokenBinding, error)
	PutAgentToken(domain.AgentTokenBinding) error
	EraseUserAgentGrants(string) error
	AgentOperation(string) ([]byte, error)
}
