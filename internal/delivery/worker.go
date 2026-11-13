// Package delivery implements the real email/SMS outbox boundary.
package delivery

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/diagnostic"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/observability"
	"github.com/carvebloom/authova/internal/ports"
	"go.opentelemetry.io/otel/attribute"
)

type Sender interface {
	Send(context.Context, domain.Message) error
}
type SMTP struct {
	Address, Username, Password, From string
	Development                       bool
}

func (s SMTP) Send(ctx context.Context, m domain.Message) (err error) {
	state := "not_sent"
	defer func() {
		if err != nil {
			if ctx.Err() != nil && !diagnostic.Has(err, ctx.Err()) {
				err = errors.Join(err, ctx.Err())
			}
			err = &sendError{state: state, code: "transport", cause: diagnostic.Wrap(err, "worker", "send")}
		}
	}()
	if m.Kind != "email" {
		return errors.New("unsupported channel")
	}
	if strings.ContainsAny(s.From+m.Destination, "\r\n") {
		return errors.New("invalid address")
	}
	host, _, e := net.SplitHostPort(s.Address)
	if e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	conn, e := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", s.Address)
	if e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if e = conn.SetDeadline(deadline); e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	client, e := smtp.NewClient(conn, host)
	if e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	defer client.Close()
	if !s.Development {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP requires STARTTLS")
		}
		if e = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); e != nil {
			return diagnostic.Wrap(e, "worker", "send")
		}
	}
	if s.Username != "" {
		if e = client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); e != nil {
			return diagnostic.Wrap(e, "worker", "send")
		}
	}
	if e = client.Mail(s.From); e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	if e = client.Rcpt(m.Destination); e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	writer, e := client.Data()
	if e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: Authova verification and security\r\nMessage-ID: <%s@authova.local>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", s.From, m.Destination, m.ID, m.Body)
	state = "unknown"
	if _, e = writer.Write([]byte(body)); e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	if e = writer.Close(); e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	state = "accepted"
	return client.Quit()
}

type SMS struct {
	URL, Token string
	Client     *http.Client
}

func (s SMS) Send(ctx context.Context, m domain.Message) error {
	if m.Kind != "phone" {
		return errors.New("unsupported channel")
	}
	body, _ := json.Marshal(map[string]string{"message_id": m.ID, "app_id": m.AppID, "destination": m.Destination, "body": m.Body})
	r, e := http.NewRequestWithContext(ctx, "POST", s.URL, bytes.NewReader(body))
	if e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+s.Token)
	r.Header.Set("Idempotency-Key", m.ID)
	resp, e := s.Client.Do(r)
	if e != nil {
		return diagnostic.Wrap(e, "worker", "send")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &sendError{state: "unknown", code: "provider_rejected", cause: diagnostic.Wrap(errors.New("SMS adapter rejected message"), "worker", "send")}
	}
	return nil
}

type Worker struct {
	Observe      func(string, string, time.Duration)
	Queue        ports.DeliveryQueue
	Email, Phone Sender
}

// Budgets are per worker: adding replicas increases throughput while reserving
// half of each worker's provider slots for another App.
const workerConcurrency = 4
const workerAppConcurrency = 2

func (w Worker) Run(ctx context.Context) (runErr error) {
	ctx, cancelWorker := context.WithCancel(ctx)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	completed := make(chan string, workerConcurrency)
	inFlight := map[string]int{}
	running := 0
	var group sync.WaitGroup
	failures := make(chan error, workerConcurrency)
	defer func() {
		if v := recover(); v != nil {
			diagnostic.Panic(ctx, "worker", v)
			runErr = diagnostic.Wrap(errors.New("worker panic"), "worker", "panic")
		}
		cancelWorker()
		group.Wait()
		select {
		case e := <-failures:
			if runErr == nil {
				runErr = e
			}
		default:
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if running < workerConcurrency {
			budget := ports.DeliveryLeaseRequest{Limit: workerConcurrency - running, AppLimit: workerAppConcurrency, InFlight: map[string]int{}}
			for app, count := range inFlight {
				budget.InFlight[app] = count
			}
			lease, cancel := context.WithTimeout(ctx, 5*time.Second)
			items, err := w.Queue.LeaseMessages(lease, budget)
			cancel()
			if err != nil {
				diagnostic.Log(ctx, "worker", "lease", err)
			} else {
				if !validLeasedMessages(items, budget) {
					return errors.New("outbox adapter violated delivery budget or message identity")
				}
				for _, message := range items {
					inFlight[message.AppID]++
					running++
					group.Add(1)
					go func() {
						defer group.Done()
						defer func() {
							if v := recover(); v != nil {
								diagnostic.Panic(diagnostic.WithRequest(ctx, message.ID), "worker", v)
								failures <- diagnostic.Wrap(errors.New("worker panic"), "worker", "panic")
							}
						}()
						w.processMessage(ctx, message)
						completed <- message.AppID // Bounded buffer also drains safely on shutdown.
					}()
				}
			}
		}
		select {
		case err := <-failures:
			return err
		case <-ctx.Done():
			return nil
		case app := <-completed:
			running--
			inFlight[app]--
			if inFlight[app] == 0 {
				delete(inFlight, app)
			}
		case <-ticker.C:
		}
	}
}

func validLeasedMessages(items []domain.Message, budget ports.DeliveryLeaseRequest) bool {
	if len(items) > budget.Limit {
		return false
	}
	counts := map[string]int{}
	ids := map[string]bool{}
	for _, message := range items {
		if message.ID == "" || message.AppID == "" || diagnostic.ID(message.ID) != message.ID || diagnostic.ID(message.AppID) != message.AppID || message.Attempts < 1 || ids[message.ID] {
			return false
		}
		ids[message.ID] = true
		counts[message.AppID]++
		if counts[message.AppID]+budget.InFlight[message.AppID] > budget.AppLimit {
			return false
		}
	}
	return true
}

func (w Worker) processMessage(ctx context.Context, m domain.Message) {
	started := time.Now()
	result := "cancelled"
	var spanErr error
	ctx, span := observability.Start(ctx, "outbox.process", attribute.String("authova.app_id", m.AppID), attribute.String("messaging.message.id", m.ID))
	defer func() {
		panicValue := recover()
		if panicValue != nil {
			spanErr = errors.New("delivery panic")
			result = "panic"
		}
		observability.End(span, spanErr)
		if panicValue != nil {
			panic(panicValue)
		}
		if w.Observe != nil {
			w.Observe(m.Kind, result, time.Since(started))
		}
	}()
	if ctx.Err() != nil {
		return // Leave the lease for recovery; never start provider I/O after shutdown.
	}
	expires := m.Expires
	if expires == 0 {
		expires = m.Created + 15*60
	}
	outcome := 0
	var err error
	if m.Attempts > 8 || time.Now().Unix() >= expires {
		outcome = 2
	} else {
		sender := w.Email
		if m.Kind == "phone" {
			sender = w.Phone
		}
		if sender == nil {
			err = &sendError{state: "not_sent", code: "provider_missing", cause: errors.New("delivery provider missing")}
		} else {
			deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = sender.Send(deadline, m)
			cancel()
		}
		if err == nil {
			outcome = 1
		}
	}
	delay := time.Duration(1<<min(m.Attempts, 8)) * time.Second
	next := time.Now().Add(delay).Unix()
	completion, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result = "retry"
	if outcome == 1 {
		result = "accepted"
	}
	if outcome == 2 {
		result = "expired"
	}
	f := ports.DeliveryFailure{Phase: "send", Code: diagnostic.Inspect(err).Kind, OutcomeUnknown: err != nil, DeliveryState: "unknown"}
	if err == nil {
		f.Code = "accepted"
		f.DeliveryState = "accepted"
	}
	if outcome == 2 {
		f.Code = "expired"
		f.DeliveryState = "not_sent"
	}
	var sent *sendError
	if errors.As(err, &sent) {
		f.DeliveryState = sent.state
		if sent.code != "" && f.Code != "cancelled" && f.Code != "deadline" && f.Code != "outcome_unknown" {
			f.Code = sent.code
		}
	}
	if f.DeliveryState != "unknown" {
		f.OutcomeUnknown = false
	}
	var update error
	if q, ok := w.Queue.(ports.DiagnosticDeliveryQueue); ok {
		update = q.FinishMessageWithFailure(completion, m.ID, m.Attempts, outcome, next, f)
	} else {
		update = w.Queue.FinishMessage(completion, m.ID, m.Attempts, outcome, next)
	}
	spanErr = err
	if outcome == 2 {
		spanErr = domain.ErrExpired
	}
	if update != nil {
		spanErr = diagnostic.Combine(err, update, false)
		result = "ack_failed"
		diagnostic.Log(diagnostic.WithDelivery(diagnostic.WithAttempt(diagnostic.WithRequest(completion, m.ID), m.Attempts), f.DeliveryState, true, f.Code), "worker", "ack", diagnostic.Combine(err, update, false))
	}
	if outcome == 2 {
		slog.Warn("delivery expired", "message_id", diagnostic.ID(m.ID))
	} else if err != nil && update == nil {
		diagnostic.Log(diagnostic.WithDelivery(diagnostic.WithAttempt(diagnostic.WithRequest(ctx, m.ID), m.Attempts), f.DeliveryState, false, f.Code), "worker", "send", err)
	}
}

type sendError struct {
	state string
	code  string
	cause error
}

func (e *sendError) Error() string { return "delivery failed" }
func (e *sendError) Unwrap() error { return e.cause }
