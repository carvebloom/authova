// Authova runs the core, deployment migrations, outbox worker and operator bootstrap.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/config"
	"github.com/carvebloom/authova/internal/delivery"
	"github.com/carvebloom/authova/internal/observability"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/carvebloom/authova/internal/runtimehttp"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/securityevents"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
	"github.com/carvebloom/authova/internal/transport"
)

var version = "dev"

func main() {
	if e := run(); e != nil {
		slog.Error("Authova stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return nil
	}
	c, e := config.Load()
	if e != nil {
		return e
	}
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	dsn := c.DSN
	if command == "migrate" && c.MigrationDSN != "" {
		dsn = c.MigrationDSN
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	vault, e := c.Vault()
	if e != nil {
		return e
	}
	store, e := sqlstore.Open(ctx, c.Dialect, dsn, c.BaseURL, vault, c.Connections)
	if e != nil {
		return errors.New("database unavailable")
	}
	defer store.Close()
	if command == "migrate" {
		return store.Migrate(ctx)
	}
	startup, startupDone := context.WithTimeout(ctx, 10*time.Second)
	e = store.CheckRuntime(startup)
	startupDone()
	if e != nil {
		return e
	}
	if command == "rewrap" {
		return rewrap(ctx, store, os.Args[2:])
	}
	service, e := application.New(store, vault, c.BaseURL, c.Development, c.KDFConcurrency)
	if e != nil {
		return e
	}
	service.ConsoleOrigin = c.ConsoleOrigin
	defer service.UpstreamClient.CloseIdleConnections()
	if command == "quarantine-identities" || command == "recover-user" || command == "release-recovered-app" {
		return recoverIdentities(ctx, service, command)
	}
	if command == "quarantine-agents" {
		var input struct {
			Reason  string `json:"reason"`
			Confirm string `json:"confirm"`
		}
		if e = privateInput(&input); e != nil || input.Confirm != "disable_all_agents" {
			return errors.New("quarantine-agents requires JSON reason and confirm=disable_all_agents; stop all writers and isolate ingress first")
		}
		report, err := service.QuarantineRecoveredAgents(ctx, input.Reason)
		if err != nil {
			slog.Error("Agent quarantine incomplete; keep ingress isolated and rerun after reconciliation", "completed", report)
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	if command == "bootstrap" || command == "recover-operator" {
		var input struct {
			Email, Password string
			TOTPSecret      string `json:"totp_secret"`
			Role            string `json:"role"`
			Reason          string `json:"reason"`
			Confirm         string `json:"confirm"`
		}
		if e = privateInput(&input); e != nil {
			return errors.New("bootstrap requires JSON from stdin")
		}
		if command == "bootstrap" {
			e = service.Bootstrap(ctx, input.Email, input.Password, input.TOTPSecret)
		} else if input.Role != "" || input.Reason != "" || input.Confirm != "" {
			if input.Confirm != "operator_identity_and_role_verified" {
				return errors.New("operator recovery requires explicit identity and role approval")
			}
			e = service.RecoverApprovedOperator(ctx, input.Email, input.Password, input.TOTPSecret, application.OperatorRecoveryApproval{Role: input.Role, Reason: input.Reason})
		} else {
			e = service.RecoverOperator(ctx, input.Email, input.Password, input.TOTPSecret)
		}
		if e != nil {
			return e
		}
		slog.Info("operator bootstrap complete")
		return nil
	}
	metrics := observability.NewMetrics()
	shutdownTrace, err := observability.ConfigureTracing(ctx, c.TraceEndpoint, "authova-"+command, c.TraceSampleRatio, c.Development)
	if err != nil {
		return err
	}
	defer func() {
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = shutdownTrace(finish)
	}()
	store.ObserveTransaction = metrics.ObserveDB
	service.ObserveKDF = metrics.ObserveKDF
	var email, phone delivery.Sender
	if c.SMTPAddress != "" {
		email = delivery.SMTP{Address: c.SMTPAddress, Username: c.SMTPUsername, Password: c.SMTPPassword, From: c.SMTPFrom, Development: c.Development}
	}
	if c.SMSURL != "" {
		phone = delivery.SMS{URL: c.SMSURL, Token: c.SMSToken, Client: secure.PublicHTTPClient(c.Development)}
	}
	worker := delivery.Worker{Queue: store, Email: email, Phone: phone, Observe: metrics.ObserveDelivery}
	service.UpstreamClient.Transport = observability.HTTPTransport(service.UpstreamClient.Transport)
	if command == "cleanup" {
		removed, e := store.Cleanup(ctx, 500, 90)
		slog.Info("transient cleanup", "removed", removed)
		return e
	}
	if command == "worker" {
		stopPoll := metrics.PollOutbox(ctx, store.OutboxStats)
		defer stopPoll()
		if c.WorkerMetricsListen != "" {
			workCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			results := make(chan error, 2)
			metricsServer := &http.Server{Addr: c.WorkerMetricsListen, Handler: metrics.Handler(), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second}
			go func() { results <- runtimehttp.Serve(workCtx, metricsServer) }()
			go func() { results <- worker.Run(workCtx) }()
			err := <-results
			cancel()
			return errors.Join(err, <-results)
		}
		return worker.Run(ctx)
	}
	if command != "serve" && command != "all" {
		return errors.New("command must be serve, all, worker, migrate, bootstrap, recover-operator, quarantine-agents, quarantine-identities, recover-user, release-recovered-app, cleanup, rewrap, or version")
	}
	var limiter transport.Limiter
	if c.Valkey.Address != "" {
		v, e := transport.NewValkeyLimiter(c.Valkey)
		if e != nil {
			return e
		}
		defer v.Close()
		limiter = v
	} else if c.Development {
		limiter = transport.NewLocalLimiter()
	} else {
		return errors.New("production requires distributed Valkey rate limiting")
	}
	resources := resource.New(c.HTTPConcurrency, c.KDFConcurrency, c.Connections, store.LoadResourceLimits)
	resources.Observe = func(kind resource.Kind, reason string) { metrics.ObserveRejection(string(kind), reason) }
	resources.ObserveWait = func(kind resource.Kind, outcome string, duration time.Duration) {
		metrics.ObserveResourceWait(string(kind), outcome, duration)
	}
	store.Admission = resources
	service.Resources = resources
	service.ChallengeLimiter = limiter
	service.ObserveChallengeLimit = func(reason string) { metrics.ObserveRejection("challenge_quota", reason) }
	stopPoll := metrics.PollOutbox(ctx, store.OutboxStats)
	defer stopPoll()
	provider, e := protocol.New(service)
	if e != nil {
		return e
	}
	events := securityevents.New(slog.New(slog.NewJSONHandler(os.Stdout, nil)), 1024)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = events.Shutdown(shutdown)
	}()
	api := &transport.Server{Metrics: metrics, Resources: resources, SecurityEvents: events, Service: service, Provider: provider, Limiter: limiter, EmailConfigured: email != nil, PhoneConfigured: phone != nil, MaxInflight: int64(c.HTTPConcurrency)}
	server := &http.Server{Addr: c.Listen, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	if command == "all" {
		go worker.Run(ctx)
	}
	slog.Info("Authova listening", "address", c.Listen, "mode", c.Mode, "version", version)
	return runtimehttp.Serve(ctx, server)
}
