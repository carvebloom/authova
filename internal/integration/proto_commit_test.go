package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

type commitProbeStore struct {
	ports.Store
	entered    chan domain.App
	release    chan struct{}
	rollback   bool
	unknown    bool
	provisions atomic.Int32
}
type commitProbePlatform struct {
	ports.PlatformTx
	probe   *commitProbeStore
	created *domain.App
}

func (tx *commitProbePlatform) ProvisionApp(app domain.App, key domain.SigningKey, client domain.Client, audit domain.Audit) error {
	if err := tx.PlatformTx.ProvisionApp(app, key, client, audit); err != nil {
		return err
	}
	tx.probe.provisions.Add(1)
	*tx.created = app
	return nil
}
func (s *commitProbeStore) Platform(ctx context.Context, write bool, callback func(ports.PlatformTx) error) error {
	var created domain.App
	err := s.Store.Platform(ctx, write, func(tx ports.PlatformTx) error {
		if err := callback(&commitProbePlatform{tx, s, &created}); err != nil {
			return err
		}
		if created.ID != "" {
			s.entered <- created
			select {
			case <-s.release:
			case <-ctx.Done():
				return ctx.Err()
			}
			if s.rollback {
				return domain.ErrUnavailable
			}
		}
		return nil
	})
	// Simulate a lost confirmation after the real DB commit. Do not replay the
	// non-idempotent callback; the caller must receive an uncertain result.
	if err == nil && created.ID != "" && s.unknown {
		return domain.ErrCommitUnknown
	}
	return err
}

func TestProtoHTTPWaitsForCommit(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		for _, mode := range []string{"success", "rollback", "unknown"} {
			t.Run(mode, func(t *testing.T) {
				original := f.service.Store
				probe := &commitProbeStore{Store: original, entered: make(chan domain.App, 1), release: make(chan struct{}), rollback: mode == "rollback", unknown: mode == "unknown"}
				f.service.Store = probe
				defer func() { f.service.Store = original }()
				body, _ := json.Marshal(map[string]string{"name": "Commit boundary", "slug": "commit-" + application.ID()})
				request, err := http.NewRequest("POST", f.server.URL+"/api/v1/apps", bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer "+f.operatorToken)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", "commit-"+application.ID())
				type result struct {
					response *http.Response
					err      error
				}
				resultCh := make(chan result, 1)
				go func() { response, err := f.http.Do(request); resultCh <- result{response, err} }()
				var created domain.App
				select {
				case created = <-probe.entered:
				case out := <-resultCh:
					if out.response != nil {
						out.response.Body.Close()
					}
					t.Fatal("request returned before provision", out.err)
				case <-time.After(10 * time.Second):
					t.Fatal("provision did not reach commit barrier")
				}
				select {
				case out := <-resultCh:
					if out.response != nil {
						out.response.Body.Close()
					}
					close(probe.release)
					t.Fatal("HTTP headers escaped before commit confirmation", out.err)
				case <-time.After(100 * time.Millisecond):
				}
				close(probe.release)
				var out result
				select {
				case out = <-resultCh:
				case <-time.After(10 * time.Second):
					t.Fatal("HTTP request did not drain")
				}
				if out.err != nil {
					t.Fatal(out.err)
				}
				defer out.response.Body.Close()
				_, _ = io.Copy(io.Discard, out.response.Body)
				want := http.StatusCreated
				if mode != "success" {
					want = http.StatusServiceUnavailable
				}
				if out.response.StatusCode != want {
					t.Fatal("commit result status", out.response.StatusCode, want)
				}
				if probe.provisions.Load() != 1 {
					t.Fatal("non-idempotent callback replayed", probe.provisions.Load())
				}
				err = original.Platform(context.Background(), false, func(tx ports.PlatformTx) error { _, err := tx.App(created.ID, false); return err })
				if mode == "rollback" {
					if !errors.Is(err, domain.ErrNotFound) {
						t.Fatal("rolled back App persisted", err)
					}
				} else if err != nil {
					t.Fatal("committed App missing", err)
				}
			})
		}
	})
}
