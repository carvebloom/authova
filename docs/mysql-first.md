# MySQL-first: deployment and compatibility

Current organization decision: **mysql-first-v1**, 2026-11-08, source
`Workbench/docs/database-policy/mysql-first-v1.md`, SHA256
`264cc0ea20661b7c8c56aca4ad6c2ec2b6d3f784b72968f6ae6efb9f545be9a6`.
The organization owns that source. This page maps it to Authova; older PostgreSQL
acceptance remains historical. Go error standard v1.0.1 stays unchanged.

## Configuration and boundaries

- New Core/Worker deployments default to **MySQL 8.4/InnoDB**. Set the explicit
  driver and DSN in `deploy/production.env.example`; verified DB TLS is required
  by the recommended production DSN. No fallback follows connection failure.
- Existing PostgreSQL is supported by explicit `AUTHOVA_DATABASE_DRIVER=postgres`.
  A PostgreSQL URL with the MySQL default is rejected with a fixed safe error;
  DSNs, issuer, identities, keys and existing environment files are not rewritten.
- `compose.test.yaml` runs Core, independent Worker, Console, MySQL, Valkey
  mTLS/ACL and Mailpit, including one-shot role setup/migrate/bootstrap. Console
  only accesses Core over HTTP and never receives Core DB/master credentials.
  New `.local-test/.env` records `LOCAL_TEST_DATABASE_DRIVER=mysql`.
- Existing private state without an engine marker requires explicit
  `AUTHOVA_LOCAL_TEST_DATABASE_DRIVER=postgres` to select
  `compose.test.postgres.yaml`; it never selects MySQL implicitly. A conflicting
  marker is rejected. For independent new fixtures set
  `AUTHOVA_LOCAL_TEST_STATE_DIR` and a unique `COMPOSE_PROJECT_NAME`; never reuse
  a PG volume for MySQL. Initialization preserves existing secrets/passwords.
- `make dev-env` initializes the new independent entry; `make integration`/`make quality-local` use the owned MySQL runner, and `make migration-test` uses its focused gates. `make infrastructure`
  invokes its full `up`. The old `scripts/migration/instance.py` and historical
  migration Compose remain explicit legacy tools, not new-deployment defaults.
- `deploy/compose.dev.yaml` defaults to MySQL; PostgreSQL needs the explicit
  `postgres-compat` profile. Production Compose consumes the operator's private
  explicit env files, so an existing PostgreSQL env continues to win.

## Storage review

Ent and narrow parameterized SQL retain one physical `sql.Tx` and commit owner.
MySQL writes use READ COMMITTED and reads REPEATABLE READ; runtime only has DML
on its own schema, without DDL, global or grant privileges. Initial tables/DSNs
retain `utf8mb4_bin` for protocol identifiers and digests. No migration SQL,
checksum, encrypted payload encoding or crypto implementation changes here.

MySQL has **no RLS**. App-scoped repositories enforce App predicates; compound
unique keys/foreign keys and real cross-App tests supplement that boundary.
PostgreSQL compatibility retains transaction-local App scope and FORCE RLS.
MySQL outbox uses the existing dispatch-row-before-message lock order, SKIP
LOCKED, attempt fencing and App budgets. At-least-once delivery, unknown send/ack
facts, and prohibition on replaying uncertain business commits remain unchanged.
DDL implicit commit is not treated as rollback-capable DML: the reviewed migrator
and dirty/checksum ledgers remain the only schema owner.

## CI and reproducible acceptance

`mysql-primary` has only MySQL/Valkey and real restricted-runtime integration,
storage, delivery and compiled contract tests. `postgres-compatibility` follows
it with the explicit dual-dialect compatibility fixture, retaining the original Atlas CLI gate that requires both engines. Local resource-constrained acceptance remains serial and lists that CLI gate as skipped. Go build/test concurrency is bounded
by GOMAXPROCS=2 and -p=2. GitHub CI status must be reported separately from local
results; configuration changes alone do not mean CI passed.

`python3 scripts/tests/go-errors.py --focused` now defaults to a fresh random
MySQL project. `--dialect postgres` explicitly runs the same necessary PG
compatibility checks; `--dialect both` is opt-in. Each creates only owned
fixtures, random credentials/ports and named project volumes, then cleans up
verified owned resources. It does not attach to Lab or legacy containers.

Existing Lab's old 001 checksum remains incompatible with the current baseline.
This task never repairs it by rewriting history, resetting identities or
rebuilding that instance. Cross-engine data migration needs a separate reviewed
logical mapping, invariant checks, maintenance/rollback plan and authorization.

Actual candidate evidence, resource use, skips and Git/CI status are recorded in
[mysql-first-validation.json](mysql-first-validation.json). Prior Go error, PG,
UI or TLS reports are not relabeled as current MySQL acceptance. This task does
not introduce SQLite or remove target-database/driver compatibility.
