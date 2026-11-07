package sqlstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/delivery"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	mysql "github.com/go-sql-driver/mysql"
)

// Every destructive query is confined to a new, explicitly test-owned database.
func outboxFixture(t *testing.T, dialect string) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("AUTHOVA_TEST_MIGRATOR_" + strings.ToUpper(dialect) + "_DSN")
	if dsn == "" {
		t.Skip("explicit test migrator DSN required")
	}
	vault, _ := secure.NewVault(bytes.Repeat([]byte{9}, 32))
	admin, err := Open(ctx, dialect, dsn, "https://identity.example", vault, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	suffix := make([]byte, 6)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "authova_outbox_" + hex.EncodeToString(suffix)
	if _, err = admin.db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		q := "DROP DATABASE " + name
		if dialect == "postgres" {
			q += " WITH (FORCE)"
		}
		if _, err := admin.db.ExecContext(ctx, q); err != nil {
			t.Error(err)
		}
	})
	target := dsn
	if dialect == "postgres" {
		u, _ := url.Parse(dsn)
		u.Path = "/" + name
		target = u.String()
	} else {
		c, _ := mysql.ParseDSN(dsn)
		c.DBName = name
		target = c.FormatDSN()
	}
	s, err := Open(ctx, dialect, target, "https://identity.example", vault, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s, target
}

func TestOutboxAppFairness(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			s, dsn := outboxFixture(t, dialect)
			ctx := context.Background()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := s.db.ExecContext(ctx, s.bind(q), args...); err != nil {
					t.Fatal(err)
				}
			}
			for _, app := range []string{"a", "b", "c", "poison", "peer", "atomic", "future", "locked"} {
				exec("INSERT INTO apps(id,app_key,slug,name,status,config_version,created_at) VALUES(?,?,?,?,'active',1,0)", app, app, app, app)
			}
			now := time.Now().Unix()
			enqueue := func(app, id string, available int64) {
				t.Helper()
				if err := s.transaction(ctx, app, true, func(tx *tx) error {
					return tx.Enqueue(domain.Message{ID: id, AppID: app, Kind: "email", Created: now, Expires: now + 600, Available: available})
				}); err != nil {
					t.Fatal(err)
				}
			}
			lease := func(n, appLimit int, flight map[string]int) []domain.Message {
				t.Helper()
				items, err := s.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: n, AppLimit: appLimit, InFlight: flight})
				if err != nil {
					t.Fatal(err)
				}
				return items
			}
			for i := 0; i < 16; i++ {
				enqueue("a", fmt.Sprint("a", i), now-10)
			}
			enqueue("b", "b", now-1)
			enqueue("c", "c", now-1)
			first := lease(2, 2, nil)
			if len(first) != 2 || first[0].AppID != "a" || first[1].AppID != "a" {
				t.Fatal("old backlog should receive first bounded opportunity", first)
			}
			// A new process retains the rotation; its budget is independent of this worker.
			peer, err := Open(ctx, dialect, dsn, s.baseURL, s.vault, 8)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			second, err := peer.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: 2, AppLimit: 1})
			if err != nil || len(second) != 2 || second[0].AppID == "a" || second[1].AppID == "a" {
				t.Fatal("persisted fairness lost across workers", second, err)
			}
			if got := lease(4, 2, map[string]int{"a": 2}); len(got) != 0 {
				t.Fatal("ignored local App budget", got)
			}
			// Multiple workers never lease the same message. Per-worker App capacity scales.
			var wg sync.WaitGroup
			results := make(chan []domain.Message, 2)
			failures := make(chan error, 2)
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					items, err := s.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: 2, AppLimit: 2})
					results <- items
					failures <- err
				}()
			}
			wg.Wait()
			close(results)
			close(failures)
			seen := map[string]bool{}
			for err := range failures {
				if err != nil {
					t.Fatal(err)
				}
			}
			for items := range results {
				for _, m := range items {
					if seen[m.ID] {
						t.Fatal("duplicate concurrent lease")
					}
					seen[m.ID] = true
				}
			}
			// A worker may skip a locked App and deliver a quiet peer immediately.
			exec("DELETE FROM outbox")
			exec("DELETE FROM outbox_dispatch")
			enqueue("locked", "locked", now)
			enqueue("peer", "peer", now)
			lock, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback()
			var app string
			if err = lock.QueryRowContext(ctx, "SELECT app_id FROM outbox_dispatch WHERE app_id='locked' FOR UPDATE").Scan(&app); err != nil {
				t.Fatal(err)
			}
			got := lease(2, 2, nil)
			if len(got) != 1 || got[0].AppID != "peer" {
				t.Fatal("locked App blocked peer", got)
			}
			lock.Rollback()
			// App-bound enqueue validation and rollback must include the scheduler wakeup.
			err = s.transaction(ctx, "atomic", true, func(tx *tx) error {
				if e := tx.Enqueue(domain.Message{ID: "wrong", AppID: "b"}); !errors.Is(e, domain.ErrForbidden) {
					return errors.New("cross-App enqueue accepted")
				}
				if e := tx.Enqueue(domain.Message{ID: "rollback", AppID: "atomic", Available: now}); e != nil {
					return e
				}
				return domain.ErrInvalid
			})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatal(err)
			}
			var count int
			if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM outbox_dispatch WHERE app_id='atomic'").Scan(&count); err != nil || count != 0 {
				t.Fatal("scheduler escaped transaction", count, err)
			}
			// Malformed ciphertext cannot starve or disclose a different App's message.
			exec("DELETE FROM outbox")
			exec("DELETE FROM outbox_dispatch")
			enqueue("poison", "poison", now-10)
			enqueue("peer", "healthy", now)
			exec("UPDATE outbox SET payload=? WHERE id='poison'", []byte("invalid"))
			got = lease(4, 2, nil)
			if len(got) != 1 || got[0].ID != "healthy" || got[0].AppID != "peer" {
				t.Fatal("poisoned App affected peer", got)
			}
			var attempts int
			if err = s.db.QueryRowContext(ctx, "SELECT attempts FROM outbox WHERE id='poison'").Scan(&attempts); err != nil || attempts != 0 {
				t.Fatal("poison consumed attempts", attempts, err)
			}
			if err = s.FinishMessage(ctx, "healthy", 1, 1, now); err != nil {
				t.Fatal(err)
			}
			for index, envelope := range []domain.Message{
				{ID: "different-id", AppID: "poison"}, {ID: "poison", AppID: "peer"},
			} {
				var ciphertext []byte
				if err = s.transaction(ctx, "poison", true, func(tx *tx) error { var e error; ciphertext, e = tx.seal("message", envelope); return e }); err != nil {
					t.Fatal(err)
				}
				exec("UPDATE outbox SET payload=? WHERE id='poison'", ciphertext)
				exec("UPDATE outbox_dispatch SET next_dispatch_at=? WHERE app_id='poison'", (now-10)*1000000)
				id := fmt.Sprint("healthy-", index)
				enqueue("peer", id, now)
				got = lease(4, 2, nil)
				if len(got) != 1 || got[0].ID != id || got[0].AppID != "peer" {
					t.Fatal("payload identity bypass", got)
				}
				if err = s.FinishMessage(ctx, id, 1, 1, now); err != nil {
					t.Fatal(err)
				}
			}
			// A SQL error after an earlier App was processed rolls back every lease.
			exec("DELETE FROM outbox")
			exec("DELETE FROM outbox_dispatch")
			enqueue("a", "rollback-a", now-10)
			enqueue("b", "rollback-b", now)
			if dialect == "postgres" {
				exec("CREATE FUNCTION outbox_failure() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN IF NEW.app_id = ''b'' THEN RAISE EXCEPTION ''test failure''; END IF; RETURN NEW; END'")
				exec("CREATE TRIGGER outbox_failure BEFORE UPDATE ON outbox FOR EACH ROW EXECUTE FUNCTION outbox_failure()")
			} else {
				exec("CREATE TRIGGER outbox_failure BEFORE UPDATE ON outbox FOR EACH ROW BEGIN IF NEW.app_id = 'b' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'; END IF; END")
			}
			got, err = s.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: 4, AppLimit: 2})
			if err == nil || len(got) != 0 {
				t.Fatal("failed transaction published usable leases", got)
			}
			if err = s.db.QueryRowContext(ctx, "SELECT SUM(attempts) FROM outbox").Scan(&attempts); err != nil || attempts != 0 {
				t.Fatal("partial lease escaped rollback", attempts, err)
			}
			if dialect == "postgres" {
				exec("DROP TRIGGER outbox_failure ON outbox")
				exec("DROP FUNCTION outbox_failure()")
			} else {
				exec("DROP TRIGGER outbox_failure")
			}

			// Backoff, idle wakeup, expired lease and fenced acknowledgements.
			exec("DELETE FROM outbox")
			exec("DELETE FROM outbox_dispatch")
			enqueue("future", "future", now+3600)
			if got = lease(4, 2, nil); len(got) != 0 {
				t.Fatal("future message delivered")
			}
			enqueue("future", "due", now)
			got = lease(4, 2, nil)
			if len(got) != 1 || got[0].ID != "due" {
				t.Fatal("idle/future wakeup failed", got)
			}
			old := got[0].Attempts
			exec("UPDATE outbox SET available_at=? WHERE id='due'", now-1)
			exec("UPDATE outbox_dispatch SET next_dispatch_at=? WHERE app_id='future'", (now-1)*1000000)
			got = lease(4, 2, nil)
			if len(got) != 1 || got[0].Attempts != old+1 {
				t.Fatal("expired lease not recovered", got)
			}
			if err = s.FinishMessage(ctx, "due", old, 1, now); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRowContext(ctx, "SELECT completed FROM outbox WHERE id='due'").Scan(&count); err != nil || count != 0 {
				t.Fatal("stale attempt completed newer lease")
			}
			if err = s.FinishMessage(ctx, "due", old+1, 0, now+60); err != nil {
				t.Fatal(err)
			}
			if got = lease(4, 2, nil); len(got) != 0 {
				t.Fatal("retry backoff ignored")
			}
			if err = s.FinishMessage(ctx, "due", old+1, 1, now); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishMessage(ctx, "due", old+1, 0, now); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRowContext(ctx, "SELECT completed FROM outbox WHERE id='due'").Scan(&count); err != nil || count != 1 {
				t.Fatal("duplicate ACK resurrected terminal message")
			}
			// Repeated v1 migrations preserve queue payloads, attempts and scheduling facts.
			snapshot := func() [][]any {
				t.Helper()
				rows, err := s.db.QueryContext(ctx, "SELECT id,app_id,available_at,attempts,completed,payload FROM outbox ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var values [][]any
				for rows.Next() {
					var id, app string
					var available int64
					var attempts, completed int
					var payload []byte
					if err := rows.Scan(&id, &app, &available, &attempts, &completed, &payload); err != nil {
						t.Fatal(err)
					}
					values = append(values, []any{id, app, available, attempts, completed, payload})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return values
			}
			before := snapshot()
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("v1 rerun changed payload, attempts or message facts")
			}
			var next int64
			if err = s.db.QueryRowContext(ctx, "SELECT next_dispatch_at FROM outbox_dispatch WHERE app_id='future'").Scan(&next); err != nil || next != (now+3600)*1000000 {
				t.Fatal("v1 pending scheduling preservation", next, err)
			}
		})
	}
}

type outboxTestSender func(context.Context, domain.Message) error

func (f outboxTestSender) Send(ctx context.Context, m domain.Message) error { return f(ctx, m) }

func TestOutboxWorkerAppIsolation(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			s, _ := outboxFixture(t, dialect)
			for _, app := range []string{"slow", "quiet"} {
				if _, err := s.db.ExecContext(context.Background(), s.bind("INSERT INTO apps(id,app_key,slug,name,status,config_version,created_at) VALUES(?,?,?,?,'active',1,0)"), app, app, app, app); err != nil {
					t.Fatal(err)
				}
			}
			enqueue := func(app, id string) {
				t.Helper()
				if err := s.transaction(context.Background(), app, true, func(tx *tx) error {
					now := time.Now().Unix()
					return tx.Enqueue(domain.Message{ID: id, AppID: app, Kind: "email", Created: now, Expires: now + 60, Available: now})
				}); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 12; i++ {
				enqueue("slow", fmt.Sprint(i))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan string, 16)
			worker := delivery.Worker{Queue: s, Email: outboxTestSender(func(ctx context.Context, m domain.Message) error {
				started <- m.AppID
				if m.AppID == "slow" {
					<-ctx.Done()
					return ctx.Err()
				}
				return nil
			})}
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("worker did not drain")
				}
			})
			for range 2 {
				select {
				case app := <-started:
					if app != "slow" {
						t.Fatal(app)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("slow App failed to start")
				}
			}
			enqueue("quiet", "quiet")
			select {
			case app := <-started:
				if app != "quiet" {
					t.Fatal("slow App exceeded budget", app)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("quiet App blocked by slow sender")
			}
			deadline := time.Now().Add(time.Second)
			var completed int
			for time.Now().Before(deadline) {
				if err := s.db.QueryRow("SELECT completed FROM outbox WHERE id='quiet'").Scan(&completed); err != nil {
					t.Fatal(err)
				}
				if completed == 1 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if completed != 1 {
				t.Fatal("quiet delivery not acknowledged")
			}
			var attempts int
			if err := s.db.QueryRow("SELECT SUM(attempts) FROM outbox WHERE app_id='slow'").Scan(&attempts); err != nil || attempts != 2 {
				t.Fatal("slow App consumed more than two slots", attempts, err)
			}
		})
	}
}
