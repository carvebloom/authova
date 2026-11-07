// Package resource provides bounded admission to shared process resources.
// It never decides whether an identity is authorized.
package resource

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

type Kind string

const (
	HTTP     Kind = "http"
	KDF      Kind = "kdf"
	Database Kind = "database"
)

type ScopeKey struct{}
type managementKey struct{}

func WithManagement(ctx context.Context) context.Context {
	return context.WithValue(ctx, managementKey{}, true)
}
func IsManagement(ctx context.Context) bool { v, _ := ctx.Value(managementKey{}).(bool); return v }
func WithApp(ctx context.Context, app string) context.Context {
	return context.WithValue(ctx, ScopeKey{}, app)
}
func App(ctx context.Context) string { v, _ := ctx.Value(ScopeKey{}).(string); return v }

type Loader func(context.Context, string) (domain.ResourceLimits, error)
type cacheEntry struct {
	ready   chan struct{}
	limits  domain.ResourceLimits
	err     error
	expires time.Time
}
type Manager struct {
	mu               sync.Mutex
	global, business map[Kind]int
	total, appTotal  map[Kind]int
	apps             map[string]map[Kind]int
	cache            map[string]*cacheEntry
	loader           Loader
	Observe          func(Kind, string)
	ObserveWait      func(Kind, string, time.Duration)
	dbQueue          list.List
	dbWaiting        map[string]int
}

func New(http, kdf, db int, load Loader) *Manager {
	global := map[Kind]int{HTTP: max(1, http), KDF: max(1, kdf), Database: max(1, db)}
	business := map[Kind]int{}
	for kind, n := range global {
		reserve := max(1, n/8)
		if kind == KDF {
			reserve = 1
		}
		business[kind] = max(1, n-reserve)
	}
	return &Manager{global: global, business: business, total: map[Kind]int{}, appTotal: map[Kind]int{}, apps: map[string]map[Kind]int{}, cache: map[string]*cacheEntry{}, loader: load, dbWaiting: map[string]int{}}
}

func (m *Manager) Limits(ctx context.Context, app string) (domain.ResourceLimits, error) {
	if app == "" || m.loader == nil {
		return domain.DefaultResourceLimits(), nil
	}
	m.mu.Lock()
	if e := m.cache[app]; e != nil {
		select {
		case <-e.ready:
			if time.Now().Before(e.expires) {
				m.mu.Unlock()
				return e.limits, e.err
			}
			delete(m.cache, app)
		default:
			m.mu.Unlock()
			select {
			case <-e.ready:
				return e.limits, e.err
			case <-ctx.Done():
				return domain.ResourceLimits{}, ctx.Err()
			}
		}
	}
	if len(m.cache) >= 4096 {
		for key, e := range m.cache {
			select {
			case <-e.ready:
				if time.Now().After(e.expires) {
					delete(m.cache, key)
				}
			default:
			}
		}
	}
	if len(m.cache) >= 4096 {
		m.mu.Unlock()
		return domain.ResourceLimits{}, domain.ErrUnavailable
	}
	e := &cacheEntry{ready: make(chan struct{})}
	m.cache[app] = e
	m.mu.Unlock()
	limits, err := m.loader(ctx, app)
	if err == nil && !limits.Valid() {
		err = domain.ErrUnavailable
	}
	m.mu.Lock()
	e.limits, e.err = limits, err
	e.expires = time.Now().Add(5 * time.Second)
	if err != nil {
		e.expires = time.Now().Add(time.Second)
	}
	close(e.ready)
	m.mu.Unlock()
	return limits, err
}

func (m *Manager) Invalidate(app string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, app)
	for element := m.dbQueue.Front(); element != nil; {
		w := element.Value.(*databaseWaiter)
		element = element.Next()
		if w.app == app {
			w.err = domain.ErrRateLimit
			m.removeDatabaseWaiterLocked(w)
			close(w.ready)
		}
	}
}
func (m *Manager) Effective(l domain.ResourceLimits) domain.ResourceLimits {
	l.HTTPConcurrency = min(l.HTTPConcurrency, m.business[HTTP])
	l.KDFConcurrency = min(l.KDFConcurrency, m.business[KDF])
	l.DBConcurrency = min(l.DBConcurrency, m.business[Database])
	return l
}

// Acquire never waits while owning a database permit. Database bursts use a
// bounded, deadline-limited queue with an independent per-App bound. HTTP and
// KDF admission remain immediate. Successful release is idempotent.
func (m *Manager) Acquire(ctx context.Context, app string, kind Kind) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if IsManagement(ctx) {
		app = ""
	}
	limits, err := m.Limits(ctx, app)
	if err != nil {
		return nil, err
	}
	limits = m.Effective(limits)
	cap := map[Kind]int{HTTP: limits.HTTPConcurrency, KDF: limits.KDFConcurrency, Database: limits.DBConcurrency}[kind]
	m.mu.Lock()
	reason := m.rejectionLocked(app, kind, cap)
	if reason != "" && kind == Database && app != "" {
		return m.waitDatabaseLocked(ctx, app, cap, reason)
	}
	if reason != "" {
		m.mu.Unlock()
		return nil, m.reject(kind, reason)
	}
	m.acquireLocked(app, kind)
	m.mu.Unlock()
	return m.release(app, kind), nil
}

func (m *Manager) rejectionLocked(app string, kind Kind, cap int) string {
	if app != "" && m.apps[app] != nil && m.apps[app][kind] >= cap {
		return "app"
	} else if m.total[kind] >= m.global[kind] || (app != "" && m.appTotal[kind] >= m.business[kind]) {
		return "shared"
	}
	return ""
}

func (m *Manager) reject(kind Kind, reason string) error {
	if m.Observe != nil {
		m.Observe(kind, reason)
	}
	if reason == "app" {
		return domain.ErrRateLimit
	}
	return domain.ErrUnavailable
}

func (m *Manager) acquireLocked(app string, kind Kind) {
	m.total[kind]++
	if app != "" {
		if m.apps[app] == nil {
			m.apps[app] = map[Kind]int{}
		}
		m.apps[app][kind]++
		m.appTotal[kind]++
	}
}

func (m *Manager) release(app string, kind Kind) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.total[kind]--
			if app != "" {
				m.apps[app][kind]--
				m.appTotal[kind]--
				empty := true
				for _, n := range m.apps[app] {
					empty = empty && n == 0
				}
				if empty {
					delete(m.apps, app)
				}
			}
			if kind == Database {
				m.dispatchDatabaseLocked()
			}
		})
	}
}
