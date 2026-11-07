package main

import (
	"context"
	"net/http"

	"github.com/carvebloom/authova/internal/runtimehttp"
)

func serveHTTP(ctx context.Context, server *http.Server) error {
	return runtimehttp.Serve(ctx, server)
}
