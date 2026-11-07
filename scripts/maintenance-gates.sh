#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# Explicit local development migrators. Every destructive gate creates and drops
# its own random database; no tests alter the Demo or capacity schema.
export AUTHOVA_TEST_MIGRATOR_POSTGRES_DSN='postgres://authova_migrator:authova-local-database-only@127.0.0.1:55432/authova?sslmode=disable'
export AUTHOVA_TEST_MIGRATOR_MYSQL_DSN='root:authova-local-database-only@tcp(127.0.0.1:53306)/authova?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'
go test -race -count=1 -v ./internal/storage/sqlstore ./internal/delivery ./internal/runtimehttp
