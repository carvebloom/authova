package observability

import (
	"context"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/prometheus/client_golang/prometheus"
)

type OutboxFacts = domain.OutboxStats

// PollOutbox exports explicit freshness: failed reads retain the last snapshot
// with valid=0. Monitoring must not mistake a stale zero for an empty queue.
func (m *Metrics) PollOutbox(parent context.Context, read func(context.Context) (OutboxFacts, error)) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	pending := prometheus.NewGauge(prometheus.GaugeOpts{Name: "authova_outbox_pending", Help: "Shared pending outbox rows capped at 10000; inspect snapshot_capped."})
	failed := prometheus.NewGauge(prometheus.GaugeOpts{Name: "authova_outbox_failed", Help: "Shared failed outbox rows capped at 10000; inspect snapshot_capped."})
	age := prometheus.NewGauge(prometheus.GaugeOpts{Name: "authova_outbox_oldest_due_seconds", Help: "Delay of the oldest currently due pending message, not its original creation age."})
	valid := prometheus.NewGauge(prometheus.GaugeOpts{Name: "authova_outbox_snapshot_valid", Help: "Whether the latest outbox poll succeeded."})
	last := prometheus.NewGauge(prometheus.GaugeOpts{Name: "authova_outbox_last_success_timestamp_seconds", Help: "Last successful shared outbox poll."})
	capped := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "authova_outbox_snapshot_capped", Help: "Count is a lower bound when 1; exact when 0."}, []string{"state"})
	m.Register(pending, failed, age, valid, last, capped)
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			deadline, cancel := context.WithTimeout(ctx, time.Second)
			facts, err := read(deadline)
			cancel()
			if err != nil {
				valid.Set(0)
			} else {
				pending.Set(float64(facts.Pending))
				failed.Set(float64(facts.Failed))
				age.Set(facts.OldestDueSeconds)
				capped.WithLabelValues("pending").Set(0)
				if facts.PendingCapped {
					capped.WithLabelValues("pending").Set(1)
				}
				capped.WithLabelValues("failed").Set(0)
				if facts.FailedCapped {
					capped.WithLabelValues("failed").Set(1)
				}
				valid.Set(1)
				last.SetToCurrentTime()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
