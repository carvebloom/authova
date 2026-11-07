// Package runtimehttp hosts protocol handlers within Kratos without re-encoding their wire responses.
package runtimehttp

import (
	"context"
	"net/http"
	"time"

	"github.com/go-kratos/kratos/v3"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// NewServer constructs the exact transport used by production and HTTP integration tests.
func NewServer(server *http.Server) *khttp.Server {
	// Authova's request middleware owns the configured budget. Kratos defaults
	// to one second, which would race the one-second DB admission queue and
	// turn a deterministic quota rejection into a dependency failure.
	h := khttp.NewServer(khttp.Address(server.Addr), khttp.StrictSlash(false), khttp.Timeout(0))
	// Register the existing protocol router as one handler: OAuth, OIDC and WebAuthn
	// must retain their established methods, errors, headers and response encoding.
	h.HandlePrefix("/", server.Handler)
	h.Server.ReadHeaderTimeout = server.ReadHeaderTimeout
	h.Server.ReadTimeout = server.ReadTimeout
	h.Server.WriteTimeout = server.WriteTimeout
	h.Server.IdleTimeout = server.IdleTimeout
	h.Server.MaxHeaderBytes = server.MaxHeaderBytes
	return h
}

func Serve(ctx context.Context, server *http.Server) error {
	h := NewServer(server)
	return kratos.New(kratos.Name("authova"), kratos.Context(ctx),
		kratos.StopTimeout(10*time.Second), kratos.Server(h)).Run()
}
