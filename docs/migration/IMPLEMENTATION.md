# Authova migration implementation (2026-11-06)

Status: implementation and acceptance in progress; remaining verification is tracked in VALIDATION.md. Target was verified empty before import. No source repository was changed, no remote created, no old instance or database touched.

## Source snapshot

`source-manifest.json` records working-tree paths, byte lengths, SHA-256, destinations and exclusions before import or namespace changes. Status snapshots record modified/untracked paths. Import includes uncommitted Core query application/domain/protocol/transport/tests and Contracts query SDK, CLI, MCP and Skill. Old evidence, logs, binaries, dependencies and runtime files are excluded; old narrative reports remain historical only. Apache licenses, NOTICE, third-party notices and encrypted legacy test fixtures are retained. Sources are the three local workspaces, not HEAD exports.

## Architecture decisions

1. One Go module `github.com/carvebloom/authova`; Core remains root, Console BFF/frontend under `console`, public contracts/SDK/tools under `contracts`. No sibling checkout needed. Independent executables and separate credentials remain required. Console must not import Core storage, cryptography or application internals.
2. Map Ent to the complete v1 baseline, retaining the source system’s final physical structure. Preserve ciphertext AAD, payload versioning, gob fallback, indexes, compound identities and RLS. Typed searchable columns plus encrypted payloads deliberately balance query typing with compatibility. Replacing payloads with plaintext JSON would lose encryption semantics and is out of scope. No old data import is authorized.
3. Keep explicit App/Platform transaction ports; bind Ent to the exact `database/sql` transaction. Never use a global Ent client from inside a transaction. Read transactions use repeatable read; writes read committed. Unknown commit is terminal, never replayed. Retain raw SQL only for documented locks, Ent conflict expressions, security-sensitive atomic operations, outbox and database policies until individually migrated and tested.
4. Kratos v3.0.0 hosts existing standard OIDC/OAuth/WebAuthn handlers. Preserve middleware, raw responses and commit-before-response semantics. Worker keeps its independent command. Console remains an independent process.
5. Target authoritative management/query contracts: Proto + google.api.http. Proto now owns all 63 operations; OpenAPI/Go HTTP/Go clients/TS are derived outputs. All 63 canonical operations use generated Kratos bindings and typed service adapters; the superseded management dispatcher implementations were removed. See PROTO.md.
6. Reference toolchain: kitarbor Go 1.27.1, runtime Kratos v3.0.0, HTTP generator v3.0.0-20261026152050-dd0fb7df1e32, protobuf generator v1.36.11, OpenAPI generator v0.7.1, Ent v0.14.6, Atlas library v0.36.2-0.20250730182955-2c6300d0a3e1. Authova source required Go 1.27.2; target pins Go 1.27.3 for GO-2026-6809 and protobuf runtime v1.36.12; retain higher source runtime versions. kitarbor TS generation locks openapi-typescript 7.13.0. Binary metadata confirms the Go/HTTP/OpenAPI generator versions; local protoc is 36.2. Imported Console keeps 7.10.1 until the Proto compatibility migration. No latest installation.
7. Versioned SQL is reviewed per dialect. RLS/policies/functions are explicit migration assets, never generated away by an Ent-only diff. Production never runs Schema.Create. Atlas library is locally executable without login; commercial/cloud functionality is not required. The OSS Atlas executor now executes reviewed SQL on the deployment-lock connection while enforcing the unpublished v1 baseline/checksums and dirty-state fence; future revisions begin at 002. See VERSIONING.md.

## Compatibility and acceptance matrix

| Existing capability / entry | New home | Acceptance evidence required |
| --- | --- | --- |
| Apps, management `/api/v1`, operators, last owner | Core application + Ent adapter + Proto | management, authorization, version conflict and last-owner regression |
| Password, email/phone OTP, recovery, TOTP/codes | existing application/protocol with Ent adapter | failure persistence, one-time consumption, session revocation; real HTTP login/MFA |
| OAuth/OIDC discovery, JWKS, token, userinfo, introspection/revocation | mature protocol handlers hosted by Kratos | issuer/audience/App isolation, refresh concurrency/reuse family revocation |
| Passkey ceremonies and external identities | protocol + application + Ent | virtual authenticator; distinguish simulated providers |
| Agent identity, tenant, delegation, actor | application/protocol + Ent | cross-tenant/App rejection, grant/actor constraints, revoke |
| **Uncommitted** query `/api/v1/apps/{app}/query/*` | Core + public contracts/query | query audience/scope, PII masking, revoked grant, query SDK combination |
| **Uncommitted** SDK/CLI/MCP/Skill | contracts/sdk/query, cmd/authova-query, mcpquery, skills | compiled subprocess against both real databases |
| Audit, budgets, outbox, Valkey | adapter + independent worker | at least once, lease fencing/fairness, limits, fail-closed and recovery |
| Console BFF/frontend | console | encrypted server sessions, HttpOnly, CSRF, per-request authority/read-only roles |
| SDK/examples/contracts | contracts | standalone imports, generation drift and full combination tests |

Wire compatibility gates include optional vs absent vs false/empty, int64 JSON numbers versus Proto strings, enum representations, strict unknown fields, write status codes, and once-only secret output. Standard handlers remain intact until each generated binding passes these gates.

## Execution gates

A. Source inventory and reviewed baseline import. B. Runnable Kratos and exact-transaction Ent vertical slice; restricted PG/MySQL tests, failure persistence and commit ordering. C. Domain-by-domain typed repository migration and existing regressions, delete superseded implementations. D. Proto-authoritative generated bindings/SDK/TS, independent Console and subprocess tools. E. fresh/upgrade dual-dialect migrations, browser and protocol acceptance, reproducibility, bounded performance comparison.

All new infrastructure must use unique project, high host ports, generated test-only keys, isolated database volumes. Never run imported demo/production scripts against old fixed ports. Existing scripts retain historical operational assumptions and require audit before execution. No benchmark claims are inherited.

## Live implementation observations

- Go 1.27 export data requires x/tools v0.52.0 (reference version); using the earlier transitive x/tools produced an Ent schema importer export-data failure. The pinned matching toolchain and generator now run locally.
- Docker Hub retrieval of mysql:8.4.6 timed out. The isolated environment instead pins the locally cached mysql image by digest `sha256:24989179f58856db763f418010c1169fecc944e3ec1ea7e921572d83eb070f70`; `SELECT VERSION()` reports 8.4.11. This is a new isolated instance, not a reused business database.
- Ent now maps 28 tables and handles ordinary repository operations across App, platform, authentication, Agent, Passkey, keys, audit and producer outbox insertion. The existing sqlstore package path remains for import compatibility but contains this single Ent-backed adapter. Deliberate native SQL is enumerated in NATIVE-SQL.md. Reviewed versioned SQL owns physical DDL; the Atlas inspector mapping guard validates both physical schemas. The Ent-to-Atlas review planner now composes a complete physical graph; direct Schema.Create remains disabled. See SCHEMA-PLANNING.md.
