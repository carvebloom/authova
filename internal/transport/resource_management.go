package transport

import (
	"github.com/carvebloom/authova/internal/domain"
)

func (s *Server) resourceConfig(a domain.App) map[string]any {
	limits := a.ResourceLimits()
	effective := limits
	if s.Resources != nil {
		effective = s.Resources.Effective(limits)
	}
	return map[string]any{"config_version": a.Version, "limits": limits, "effective_limits": effective, "concurrency_scope": "per_process", "counter_scope": "shared_limiter", "propagation_seconds": 5}
}
