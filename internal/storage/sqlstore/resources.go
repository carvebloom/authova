package sqlstore

import (
	"context"
	"encoding/json"
	appentity "github.com/carvebloom/authova/internal/ent/app"

	"github.com/carvebloom/authova/internal/domain"
)

func decodeAppConfiguration(a *domain.App, policy, resources string) error {
	var p domain.AuthenticationPolicy
	var limits domain.ResourceLimits
	if json.Unmarshal([]byte(policy), &p) != nil || json.Unmarshal([]byte(resources), &limits) != nil || !limits.Valid() {
		return domain.ErrUnavailable
	}
	a.Policy, a.Resources = &p, &limits
	return nil
}

// LoadResourceLimits reads only routing metadata. Its own bounded DB admission
// does not recursively load App policy, preserving reserved management capacity.
func (s *Store) LoadResourceLimits(ctx context.Context, app string) (domain.ResourceLimits, error) {
	if s.Admission != nil {
		release, err := s.Admission.AcquireMetadata(ctx)
		if err != nil {
			return domain.ResourceLimits{}, err
		}
		defer release()
	}
	var raw string
	err := s.transaction(ctx, "", false, func(t *tx) error {
		row, e := t.entities().App.Query().Where(appentity.IDEQ(app)).Only(ctx)
		if e != nil {
			return classify(e)
		}
		raw = row.ResourceLimits
		return nil
	})
	if err != nil {
		return domain.ResourceLimits{}, err
	}
	var limits domain.ResourceLimits
	if json.Unmarshal([]byte(raw), &limits) != nil || !limits.Valid() {
		return limits, domain.ErrUnavailable
	}
	return limits, nil
}
