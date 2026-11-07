package domain

// OutboxStats is a bounded operational snapshot, without message payloads.
type OutboxStats struct {
	PendingCapped, FailedCapped bool
	Pending, Failed             int64
	OldestDueSeconds            float64
}
