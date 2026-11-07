# Validation report — 2026-11-06

Current database schema is the unpublished **v1** baseline. The latest fresh-v1
full race run passed 693 test nodes across 29 test-bearing packages (integration
542.278 seconds), followed by compiled SDK/CLI/MCP combination tests. Genuine
v1→v2 upgrade and production-mode OCI gates also passed. Current evidence is in
[v1-validation.json](v1-validation.json); earlier results below retain their actual
pre-consolidation context. See [VERSIONING.md](VERSIONING.md).


The local implementation includes Kratos Core/Console lifecycles, all 63 generated
management/query routes, 28 Ent table mappings for ordinary repository operations
and the OSS Atlas versioned-migration executor. This is a fresh local Git
repository with no remote or publication. It has not replaced an existing service
or imported old business identities, issuers, data or keys. Release limitations
below remain explicit.

## Verified implementation

- All 1,163 original file hashes remain unchanged. Import includes 837 product
  files, including locally uncommitted query API/SDK/CLI/MCP/Skill; 326 excluded
  files were runtime/dependencies/secrets/old output. Licenses and notices remain.
- Core, Worker, Console and query CLI/MCP build from the single module, without
  sibling repositories. Domain/use-case/ports have no Ent dependencies; Console
  has no Core storage/master-key dependency; public SDK has no internal imports.
- Ordinary reads/writes use generated Ent entities, predicates and builders on
  the exact scoped SQL transaction. App/Platform ports, PG transaction-local
  scope/FORCE RLS/compound constraints/restricted roles are retained. MySQL is
  tested as a distinct dialect and has no RLS claim. Deliberate native SQL is
  enumerated in NATIVE-SQL.md; no parallel legacy CRUD adapter remains.
- Both real databases pass fresh initialization, versioned upgrade rehearsal,
  dirty/checksum/conflicting-history rejection, Ent transaction/rollback/read-only
  boundaries and physical mapping checks. Atlas executes on the deployment-lock
  connection without Cloud/login/commercial features. See MIGRATIONS.md for the
  reviewed DDL execution strategy and the completed Ent-to-Atlas review planner.
- The full dual-database race suite and compiled contract/SDK/CLI/MCP combination
  passed in `.migration/complete-proto`. It covers management/version/last-owner,
  OTP/code one-use, refresh concurrency/reuse revocation, committed failure
  counts, credential/session revocation, Agent tenant/grant/actor and read-query
  audience/scope/App boundaries, PII masking, resource budgets, outbox
  lease/attempt fencing/fairness and cancellation/queue behavior. The final duplicate-null patch-inclusive full race run also passed
  (integration package: 557.477 seconds); its completed contract-combination gate
  is recorded in the machine summary.
- Real HTTP commit-barrier tests hold a real App transaction before completion:
  no response escapes early; rollback and commit-unknown yield 503, with one
  provisioning attempt and no automatic replay. Driver fault injection verifies
  commit outcome/cause behavior. This is not a claim of physical DB failover or
  every possible network-disconnect outcome.
- All 51 management and 12 query operations use generated typed Kratos services.
  Generation checks reproduce 207 Ent files plus Proto/HTTP/OpenAPI/Go/TS/Console
  outputs. JSON tests cover numeric int64, absence/false/empty/null, unknown fields,
  duplicate order, pointer null clearing, nested merge, one-time output schemas,
  authorization/decode precedence, trailing slashes and empty 204 responses.
- Frontend typecheck, lint, formatting, 245 tests across 33 files and production
  build passed. The undici override is pinned to 8.12.2; full npm audit reports
  zero vulnerabilities. Go vet and staticcheck v0.8.2 pass. Govulncheck v1.1.4
  reports zero vulnerabilities in called code or imported packages after Go
  1.27.3 fixed the reachable x509 advisory GO-2026-6809. Verbose inventory lists
  three advisories only in unused packages from required modules (x/net HTML and
  x/crypto OpenPGP); these are not represented as reachable findings.
- The merged CycloneDX Go source-module inventory covers 254 modules and 267
  upstream license/notice files. The frontend build emits its runtime web notices.
  These inventories are not OS image vulnerability scans or legal opinions.

## Browser, Valkey and containers

Actual browser checks on the isolated instance passed owner and auditor
password/TOTP login, App creation/read, missing CSRF 403, auditor write 403 with a
valid CSRF token, hidden write controls, HttpOnly visibility, and logout 204 then
unauthenticated 401. On the independent Agent instance, actual Mailpit delivery
from the separate Worker, registration/password setup, password login, TOTP
first enrollment/confirmation, PKCE delegation consent, resource access and user
revocation were exercised. Online resource access changed from success to 401
following grant revocation. Details: `browser-proto.json`; the owned browser tab
was closed.

`TestPasskeyLifecycle` uses independently constructed COSE/CBOR and real ECDSA
signatures with the production WebAuthn verifier on both databases. It covers
origin/RP/challenge/signature/user-handle/UV/counter/replay/App boundaries and
credential lifecycle. The additional HTTP ceremony gate uses the same virtual
authenticator through actual Kratos-hosted registration/login endpoints and CSRF.
This does not claim browser credential-API or hardware authenticator coverage.

Four independent Valkey TLS/ACL gates passed: Core and Console ACL/TLS behavior,
and each process's outage/fail-closed/recovery behavior. The runner created and
removed its own container/network. Plain local Valkey tests are separate evidence.

Both Dockerfiles built successfully with digest-pinned Go 1.27.3. The isolated
production-mode OCI smoke passed Core/Console nonroot UID 10001, read-only root
filesystem, dropped capabilities, Valkey verified mTLS/separate ACL users,
readiness, static assets, unauthenticated admission and wrong-origin BFF rejection.
Console's container had no Core database/master-key environment. The smoke removed
its disposable DB/network/containers. It did not deploy or test ingress/database
TLS. Docker context excludes local key/evidence/dependency directories.

## Environment and performance

Apple M4 Pro, 14 logical CPUs, 48 GiB, macOS 26.5.2 arm64, Go 1.27.3; isolated
Compose project `authova-migration`, PostgreSQL 17, MySQL 8.4.11 by digest, Valkey
9.1 and Mailpit. Host-only infrastructure ports are 35432/33306/36379/38025/31025.
Tests use random databases, new private keys, DML-only runtime users and separate
migration users. No old service/database/container/key was reused.

`performance-summary.json` records a repeatable ABBA old/new introspection
comparison with each binary's genuine schema history (source 8 / current 1),
independent owned databases and identical freshly seeded synthetic facts. Both
binaries use Go 1.27.3, 1,000 users / 100 sampled live sessions, 100 scheduled QPS,
eight workers, GOMAXPROCS=4, pool=16 and five seconds per run. All eight runs
completed 500 requests each with zero failures/drops. Separate caches and other
host workloads limit interpretation. This is not a capacity, endurance, HA or
production SLO claim. The earlier measurement is retained in
`performance-pre-v1.json` rather than relabeled as a v1 result.


## Remaining limits and release status

- Exact opt-in test skips and reasons are in `skipped-tests.json`. Formal
  hour-scale soaks, large capacity/resume/12M restore campaigns, restored backup
  verification and full recovery rehearsals were not run. They require separately
  provisioned fixtures and are explicit follow-up work; no historical result is
  counted as new acceptance.
- Browser WebAuthn credential API/hardware, live external vendor authentication,
  real email/SMS vendors, production ingress/SQL TLS, actual HA/failover and image
  vulnerability scanning remain unverified. External provider unit tests use
  simulated providers; local Mailpit proves SMTP delivery, not a vendor service.
- The official Ent-to-Atlas planner composes the complete physical extension and
  produces review-only candidates. Direct unextended Ent/Schema.Create DDL remains
  unsupported; reviewed versioned SQL is the production path. Full payload recoding and old-data migration are excluded.
- Container smoke is local verification, not publication/deployment. GitHub CI
  configuration is updated but has not run remotely. No remote/visibility choice,
  push, release, production substitution or old-data deletion is authorized here.

Earlier failing broad commands are not relabeled green: discovered Ent aggregate
alias, timeout/queue race, pagination assumption and Kratos empty-204 bugs were
fixed and rerun. Evidence is under ignored `.migration`; shareable summaries in
this directory contain no test secrets. See `validation-summary.json` for current
completed gate results and `RUN.md` for starting the isolated deliverable.


## Schema engineering completion

The former Ent-to-Atlas planning gap is implemented. The composed model contains
30 product tables and 39 FKs per dialect, including composite identities, shared
Ent indexes, dialect-specific helper/partial/generated indexes and four checks.
`cmd/schema-plan` generates and verifies review-only candidates from a read-only
transaction, preserves external tables, fingerprints non-table objects and rejects
broken RLS or insufficient MySQL metadata visibility. The compiled migration
catalog now discovers continuous future revisions without a hardcoded dispatcher.

`TestAtlasPlanning`, `TestAtlasProposedUpgrade`, `TestAtlasPlanningCLI`, safety unit
tests and existing physical/maintenance gates passed on real PostgreSQL/MySQL.
They cover zero drift, additive Ent field/index generation and Atlas execution,
convergence, protected-object definition changes, stale/tampered reports, output
overwrite, RLS failures and new-table policy review. The current unpublished baseline is now v1; see VERSIONING.md and v1-validation.json for fresh-baseline evidence.
See SCHEMA-PLANNING.md and engineering-validation.json for the exact evidence.

The engineering full race run passed 691 test nodes across 28 test-bearing
packages (integration 560.341 seconds), followed by the compiled contract
combination gate. Later planner-only security/qualification refinements and their
additional tests passed the final dedicated engineering gate and unit/static
checks. Exact scope and log digest are recorded in engineering-validation.json.


## Unpublished baseline normalization

The source system's schema-v8 history was consolidated into a complete v1 initial
migration, per the user's request before publication. Prior reports above remain
historical results. `v1-validation.json` records the fresh v1 dual-database gates;
no prior result is renamed to claim v1 acceptance. Existing old test databases
and keys remain preserved; new isolated instances have independent v1 databases
and keys. API v1 and encrypted payload format versions are unchanged.
