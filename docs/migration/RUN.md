> This describes the preserved historical PostgreSQL migration instance. For new server deployments use [MySQL-first local setup](../local-test.md). Do not run these historical commands against old data as an engine-switch procedure.

# Isolated local run

Required: Go 1.27.4, Docker, Docker Compose, Node/npm. The optional `scripts/migration/go.sh` wrapper uses this machine's cached matching Go toolchain; portable environments can use normal Go 1.27.4 directly.

From the repository root:

```sh
docker-compose -p authova-migration -f deploy/migration/compose.yaml --profile mysql up --pull never -d --wait
make build-all
npm --prefix console/web ci
npm --prefix console/web run build
python3 scripts/migration/quality.py --evidence .migration/acceptance
```

Use `docker compose` instead of `docker-compose` on systems with the CLI plugin. The Compose file uses host-only ports: PG 35432, MySQL 33306, Valkey 36379, Mailpit 38025/31025. Volumes are under the `authova-migration` project. Passwords in this fixture are public test credentials. The cached MySQL image is digest-pinned; network access may be needed on another machine.

The quality runner creates randomly named databases, grants DML-only runtime roles, generates a private disposable master key, migrates with a separate owner, runs tests, and removes only databases it created. It never imports old data or keys. For a focused run append `--test-run '^TestEntBoundTransactions$'`; for real CLI/MCP and SDK composition append `--contract-only`.

Core: `bin/authova serve`; Worker: `bin/authova worker` (same build, independently deployed process); Console: `bin/authova-console`; stdio MCP: `bin/authova-query -mcp -config /private/test-query.json`. These require explicit environment/configuration from the existing configuration documentation. Do not source an old `.dev/env`; create new test keys and credentials. Console assets default to `console/web/dist` from repository root. Console has no Core database or master key dependency.

Ent: `make ent` / `make ent-check` (Ent v0.14.6 and compatible x/tools pinned in go.mod). Migrations: reviewed SQL in `internal/migrations/sql/{postgres,mysql}`, `make migration-hash` updates Atlas directory integrity. `authova migrate` validates Atlas sums and uses the OSS Atlas executor on the same deployment-lock connection. It enforces v1 baseline SHA/dirty history; old version records are not rewritten. A dirty or contradictory history requires reconciliation; no automatic replay.

Reviewed per-dialect versioned SQL owns deployed DDL. `make schema-plan` builds the complete Ent-to-Atlas review planner, including compound keys and protected-object checks; see SCHEMA-PLANNING.md. The OSS Atlas inspector mapping guard verifies both physical schemas. See MIGRATIONS.md. All 63 management/query routes use generated bindings. Proto-authoritative API generation: `python3 scripts/generate-api.py --install-tools` (protoc 36.2 required), then `make api-check`. See PROTO.md and VALIDATION.md.

No production rollout, remote creation, old data migration, issuer/key replacement, or old service replacement is included in these commands.

## Instance created during this migration

The new isolated PostgreSQL-backed instance is initialized with independently
generated master/Console keys and a test-only owner. Core is on
`http://127.0.0.1:38080`, Console on `http://127.0.0.1:38090`; the Worker is a separate
process. Private local test login material is in `.migration/instance-v1/bootstrap.json`
(mode 0600), never in Git. This instance contains the browser verification App.

To restart each process in its own terminal from the root:

```sh
python3 scripts/migration/instance.py serve
python3 scripts/migration/instance.py console
python3 scripts/migration/instance.py worker
```

For a fresh v1 instance only, `python3 scripts/migration/instance.py init` creates new
keys and initializes the new dedicated Compose `authova_v1` database. It refuses to replace
an existing instance directory. Do not delete/reinitialize that directory while
retaining its encrypted database. The instance is for local migration validation,
not publication or production deployment.

`python3 scripts/migration/instance.py migrate` verifies the current v1 instance. It cannot adopt or renumber the prior v8 instance; see VERSIONING.md.


## Independent browser examples

`python3 scripts/agent-demo.py start` creates the dedicated
`authova_migration_agent_v1` database in the new Compose project, new private
keys and separate Core/Worker/Console/RP/resource processes. Agent RP is
http://localhost:38288, Core 38280, Worker 38281, resource 38289, Console 38290.
Use `python3 scripts/agent-demo.py stop` / `status` to manage only its recorded
processes. It does not delete its database on stop.

`python3 scripts/passkey-demo.py start` provides the independent password/Passkey
example on RP http://localhost:38188, Core 38180 and Console 38190, with a separate
`authova_migration_passkey_v1` database and keys. Mailpit is 38025 for both
examples. Test-only bootstrap files are private under `.migration/demos/v1/<profile>`.
Do not delete key directories while retaining their encrypted databases.

TLS/ACL and outage recovery: `python3 scripts/valkey-tls-gates.py --help`, then use
a new `.migration` evidence directory as required by that runner. It creates and
removes its own container/network; it does not attach to an existing network.

A bounded old/new introspection comparison can be repeated with
`python3 scripts/migration/performance.py --help`; see performance-summary.json.
It creates/removes only its own random databases and uses fresh test keys.
No result is a capacity promise.


Container verification: `docker build -t authova-migration-core:local .` and
`docker build -f console/Dockerfile -t authova-migration-console:local .`.
Then `python3 scripts/migration/container-smoke.py --valkey-fixture
.migration/valkey-tls-final --evidence .migration/oci-new` (use a fresh TLS fixture
when certificates expire). This smoke owns its disposable DB/network/containers
and removes them. Production-mode, nonroot/readonly, Valkey mTLS/ACL, readiness,
static assets and BFF origin/credential separation are checked. It does not test
ingress TLS, database TLS, a real deployment or image vulnerability scanning.


The optional source/new benchmark requires a separately built historical binary.
The runner now initializes separate databases with each binary, clones only its
fresh synthetic product facts by column name and retains each genuine migration
history (source v8 versus current v1). It never edits version metadata to make
binaries accept a database. See VERSIONING.md and performance-summary.json.
