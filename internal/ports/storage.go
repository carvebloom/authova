// Package ports defines the application-owned persistence boundary.
package ports

import (
	"context"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

// RateLimiter is the shared deployment counter used by all challenge entry points.
// Dependency failure must be returned, never treated as an allow decision.
type RateLimiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}

type Store interface {
	Health(context.Context) error
	Stats() PoolStats
	Close() error
	RouteApp(context.Context, string) (domain.App, error)
	App(context.Context, domain.Scope, bool, func(AppTx) error) error
	Platform(context.Context, bool, func(PlatformTx) error) error
}

// IntrospectionReader is an optional primary-snapshot capability. The result
// includes current resource credentials, token liveness and required session
// MFA. Implementations must enforce the bound App and never cache an allow
// decision. Stores without it use the ordinary scoped transaction path.
type IntrospectionReader interface {
	ReadIntrospection(context.Context, domain.Scope, string, string) (domain.Client, domain.TokenState, bool, error)
}

// PoolStats exposes bounded operational facts without leaking driver types.
type PoolStats struct {
	Open, InUse, Idle, MaxOpen int
	WaitCount                  int64
	WaitSeconds                float64
}

// All AppTx methods use the bound App; identifiers cannot select another App.
// Transactions and repositories must not escape the callback.
type AppTx interface {
	RecoveryKeys(string, int) ([]domain.SigningKey, error)
	RecoveryBatch(domain.RecoveryFact, string, int) (domain.RecoveryBatch, error)
	AgentTx
	PasskeyTx
	ManagementTx
	ClientTx
	UserTx
	SessionTx
	FlowTx
	ProtocolTx
	FederationTx
	SigningTx
	AuditTx
	IdempotencyTx
}

type PasskeyTx interface {
	Passkeys(string) ([]domain.Passkey, error)
	Passkey(string, bool) (domain.Passkey, error)
	PasskeyByCredential(string, bool) (domain.Passkey, error)
	PutPasskey(domain.Passkey) error
	DeletePasskey(string, string, int64) error
	EraseUserPasskeys(string) error
	PasskeyCeremony(string, bool) (domain.PasskeyCeremony, error)
	PutPasskeyCeremony(domain.PasskeyCeremony) error
}

// ManagementTx is a capability of the same App-bound transaction.
type ManagementTx interface {
	ManagementOperator(string, bool) (domain.Operator, error)
	App() (domain.App, error)
	// UpdateAppConfig requires Version=current+1 and rejects lost updates.
	UpdateAppConfig(domain.App) error
	AdminRole(string) (string, error)
	SetAdminRole(string, string) error
	CountOwners() (int, error)
	LockOwners() error
}

// ClientTx is a capability of the same App-bound transaction.
type ClientTx interface {
	Client(string) (domain.Client, error)
	LockClient(string) (domain.Client, error)
	Clients(string, int) ([]domain.Client, error)
	PutClient(domain.Client) error
}

// UserTx is a capability of the same App-bound transaction.
type UserTx interface {
	User(string, bool) (domain.User, error)
	FindUser(string, string) (domain.User, error)
	Users(string, int) ([]domain.User, error)
	CreateUser(domain.User, string, string) error
	ReplaceIdentifier(string, string, string, string) error
	EraseIdentifiers(string) error
	PutUser(domain.User) error
}

// SessionTx is a capability of the same App-bound transaction.
type SessionTx interface {
	Session(string, bool) (domain.Session, error)
	SessionByHandle(string) (domain.Session, error)
	Sessions(string, string, int) ([]domain.Session, error)
	PutSession(domain.Session) error
	RevokeSessions(string) error
}

// FlowTx is a capability of the same App-bound transaction.
type FlowTx interface {
	Flow(string, bool) (domain.Flow, error)
	PutFlow(domain.Flow) error
	Challenge(string, bool) (domain.Challenge, error)
	PutChallenge(domain.Challenge) error
	Consent(string, string, []string) error
}

// ProtocolTx is a capability of the same App-bound transaction.
type ProtocolTx interface {
	Code(string, bool) (domain.Code, error)
	PutCode(domain.Code) error
	Token(string) (domain.Token, error)
	PutToken(domain.Token) error
	ActiveToken(string) (domain.Token, error)
	// The protocol transaction fallback separately verifies required session MFA.
	IntrospectionState(string, string) (domain.Client, domain.TokenState, bool, error)
	Refresh(string, bool) (domain.Refresh, error)
	PutRefresh(domain.Refresh) error
	CreateFamily(string, string, string, int64) error
	LockFamily(string) (bool, error)
	RevokeFamily(string) error
}

// FederationTx is a capability of the same App-bound transaction.
type FederationTx interface {
	Connections() ([]domain.Connection, error)
	ConnectionPage(string, int) ([]domain.Connection, error)
	Connection(string) (domain.Connection, error)
	LockConnection(string) (domain.Connection, error)
	ShareConnection(string) (domain.Connection, error)
	PutConnection(domain.Connection) error
	ExternalUser(string, string) (string, error)
	BindExternal(string, string, string) error
	ExternalBindings(string) ([]domain.ExternalBinding, error)
	UnbindExternal(string, string) error
}

// SigningTx is a capability of the same App-bound transaction.
type SigningTx interface {
	Keys() ([]domain.SigningKey, error)
	VerificationKeys() ([]domain.SigningKey, error)
	PutKey(domain.SigningKey) error
}

// AuditTx is a capability of the same App-bound transaction.
type AuditTx interface {
	Audit(domain.Audit) error
	Audits(string, int) ([]domain.Audit, error)
	Enqueue(domain.Message) error
}

// IdempotencyTx is a capability of the same App-bound transaction.
type IdempotencyTx interface {
	Idempotency(string, string) ([]byte, error)
	PutIdempotency(string, string, []byte, int64) error
}

type PlatformTx interface {
	RecoveryManagementSessions(string, int) (domain.RecoveryBatch, error)
	Idempotency(string, string, string) ([]byte, error)
	PutIdempotency(string, string, string, []byte, int64) error
	ProvisionApp(domain.App, domain.SigningKey, domain.Client, domain.Audit) error
	Apps(string, int) ([]domain.App, error)
	App(string, bool) (domain.App, error)
	PutApp(domain.App) error
	Operator(string, bool) (domain.Operator, error)
	OperatorByEmail(string) (domain.Operator, error)
	Operators() ([]domain.Operator, error)
	OperatorPage(string, int) ([]domain.Operator, error)
	CountOperatorOwners() (int, error)
	PutOperator(domain.Operator) error
	RevokeOperatorSessions(string) error
	CountOperators() (int, error)
	ManagementSession(string) (domain.ManagementSession, error)
	PutManagementSession(domain.ManagementSession) error
	Audit(domain.Audit) error
}

// DeliveryWorker leases encrypted messages; provider I/O runs outside database transactions.
type DeliveryQueue interface {
	// The worker supplies its current in-flight snapshot. Adapters must respect
	// both the remaining total slots and the per-App budget, and return no
	// usable messages if lease publication fails.
	LeaseMessages(context.Context, DeliveryLeaseRequest) ([]domain.Message, error)
	FinishMessage(context.Context, string, int, int, int64) error
}

// DeliveryLeaseRequest bounds one worker's lease. AppLimit applies per worker;
// shared fair scheduling orders Apps across all workers without imposing a
// fixed global App cap that would prevent horizontal scaling.
type DeliveryLeaseRequest struct {
	Limit    int
	AppLimit int
	InFlight map[string]int
}

// DeliveryFailure is a safe, bounded persisted attempt diagnostic.
type DeliveryFailure struct {
	DeliveryState  string
	Phase, Code    string
	OutcomeUnknown bool
}
type DiagnosticDeliveryQueue interface {
	FinishMessageWithFailure(context.Context, string, int, int, int64, DeliveryFailure) error
}
