package resource

import "context"

// The reserved internal name cannot collide with a validated App ID. Metadata
// reads have no App policy lookup of their own and consume business capacity.
const metadataScope = "\x00metadata"

// AcquireMetadata prevents routing/policy cache misses from borrowing the
// management reserve. Its own per-process bound also limits unknown-key storms.
func (m *Manager) AcquireMetadata(ctx context.Context) (func(), error) {
	if IsManagement(ctx) {
		return m.Acquire(ctx, "", Database)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	capacity := max(1, m.business[Database]/8)
	m.mu.Lock()
	if m.rejectionLocked(metadataScope, Database, capacity) != "" {
		return m.waitDatabaseLocked(ctx, metadataScope, capacity, "metadata")
	}
	m.acquireLocked(metadataScope, Database)
	m.mu.Unlock()
	return m.release(metadataScope, Database), nil
}
