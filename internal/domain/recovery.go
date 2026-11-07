package domain

// Quarantined is a recovery fence, not an ordinary reversible suspension.
// Only the local recovery workflow can release it after credential review.
const Quarantined = "quarantined"

type RecoveryFact string

const (
	RecoverySessions RecoveryFact = "sessions"
	RecoveryFamilies RecoveryFact = "refresh_families"
	RecoveryFlows    RecoveryFact = "flows"
)

type RecoveryBatch struct {
	After            string
	Scanned, Changed int
}
