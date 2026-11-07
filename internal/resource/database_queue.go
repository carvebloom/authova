package resource

import (
	"container/list"
	"context"
	"time"
)

const databaseQueueTimeout = time.Second

type databaseWaiter struct {
	ctx      context.Context
	app      string
	capacity int
	deadline time.Time
	ready    chan struct{}
	element  *list.Element
	granted  bool
	err      error
}

// Called with m.mu held; always unlocks before returning. A waiter owns no DB
// permit or connection. One App can fill at most half of the global queue.
func (m *Manager) waitDatabaseLocked(ctx context.Context, app string, capacity int, reason string) (func(), error) {
	// Allow a bounded burst to wait through short driver/runtime stalls. The
	// waiting budget is separate from the active connection budget. These are
	// waiting contexts only: DB connection/concurrency caps and the management reserve
	// remain unchanged, and one App still owns at most half the waiting slots.
	globalBound := max(8, 8*m.global[Database])
	appBound := min(4*capacity, globalBound/2)
	if m.dbQueue.Len() >= globalBound || m.dbWaiting[app] >= appBound {
		m.mu.Unlock()
		m.observeDatabaseWait("queue_full", 0)
		return nil, m.reject(Database, reason)
	}
	started := time.Now()
	w := &databaseWaiter{ctx: ctx, app: app, capacity: capacity, deadline: started.Add(databaseQueueTimeout), ready: make(chan struct{})}
	w.element = m.dbQueue.PushBack(w)
	m.dbWaiting[app]++
	m.mu.Unlock()
	timer := time.NewTimer(databaseQueueTimeout)
	defer timer.Stop()
	select {
	case <-w.ready:
	case <-ctx.Done():
	case <-timer.C:
	}
	m.mu.Lock()
	if w.element != nil {
		m.removeDatabaseWaiterLocked(w)
	}
	granted, err := w.granted, w.err
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	m.mu.Unlock()
	if err != nil {
		if granted {
			m.release(app, Database)()
		}
		outcome := "cancelled"
		if ctx.Err() == nil {
			outcome = "policy_changed"
			_ = m.reject(Database, "app")
		}
		m.observeDatabaseWait(outcome, time.Since(started))
		return nil, err
	}
	if !granted {
		m.observeDatabaseWait("timeout", time.Since(started))
		return nil, m.reject(Database, reason)
	}
	m.observeDatabaseWait("admitted", time.Since(started))
	return m.release(app, Database), nil
}

func (m *Manager) observeDatabaseWait(outcome string, duration time.Duration) {
	if m.ObserveWait != nil {
		m.ObserveWait(Database, outcome, duration)
	}
}

func (m *Manager) removeDatabaseWaiterLocked(w *databaseWaiter) {
	m.dbQueue.Remove(w.element)
	w.element = nil
	m.dbWaiting[w.app]--
	if m.dbWaiting[w.app] == 0 {
		delete(m.dbWaiting, w.app)
	}
}

// FIFO among eligible waiters. A full App cannot block another App; management
// never joins this queue. Reservation is atomic with releasing the old permit,
// so a fresh stream of introspection requests cannot starve an older refresh.
func (m *Manager) dispatchDatabaseLocked() {
	for element := m.dbQueue.Front(); element != nil; {
		w := element.Value.(*databaseWaiter)
		element = element.Next()
		if err := w.ctx.Err(); err != nil {
			w.err = err
			m.removeDatabaseWaiterLocked(w)
			close(w.ready)
			continue
		}
		if !time.Now().Before(w.deadline) {
			m.removeDatabaseWaiterLocked(w)
			close(w.ready)
			continue
		}
		if entry := m.cache[w.app]; entry != nil {
			select {
			case <-entry.ready:
				if entry.err == nil {
					w.capacity = min(w.capacity, entry.limits.DBConcurrency)
				}
			default:
			}
		}
		if m.rejectionLocked(w.app, Database, w.capacity) == "" {
			m.acquireLocked(w.app, Database)
			w.granted = true
			m.removeDatabaseWaiterLocked(w)
			close(w.ready)
		}
	}
}
