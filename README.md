# Authova

Apache-2.0 multi-App identity service: OAuth2/OIDC, password/OTP/MFA/Passkey,
external identities, Agent identities/delegation, administration and read-only queries.
No enterprise organization model or general-purpose permission engine is included.

This is a **fresh Authova monorepo**. It preserves the three
source workspaces including their uncommitted query API, SDK, CLI, MCP and Skill.
Core and Console run on Kratos; all 63 management/query operations use generated
Proto bindings, ordinary repositories use 28 Ent mappings, and reviewed dual-dialect
migrations execute through OSS Atlas. See the validation report for verified gates
and remaining acceptance limits.

| Directory | Responsibility |
| --- | --- |
| `cmd/authova`, `internal` | Core and separately invoked Worker |
| `console` | independently built Console BFF and frontend |
| `api/authova` | authoritative Proto contracts, compatibility options and generated HTTP bindings |
| `contracts` | derived OpenAPI, Go/TS clients, query CLI/MCP and Skill |
| `internal/ent` | pinned generated typed repository entities |
| `internal/migrations/sql` | reviewed PostgreSQL/MySQL migrations, RLS and Atlas sums |
| `docs/migration` | provenance, decisions, compatibility and acceptance status |

Requires Go 1.27.3; `make build-all` builds all three Go executables from this
checkout alone. `npm --prefix console/web ci && npm --prefix console/web run build`
builds the frontend. Core: `bin/authova serve`; Worker: `bin/authova worker`;
Console: `bin/authova-console`. Each needs its own explicit runtime configuration.
Console communicates only through Core HTTP API and has no Core database/master key.

Start with [isolated run instructions](docs/migration/RUN.md),
[implementation decisions and capability matrix](docs/migration/IMPLEMENTATION.md),
and [current validation report](docs/migration/VALIDATION.md).
The [previous Core README](docs/migration/LEGACY-CORE-README.md) is retained as
historical documentation; its old deployment commands and performance claims are
not acceptance evidence for this repository.

Production deployment and business-data migration remain separate work. Existing
issuers, identities and keys are outside this repository migration.

Schema engineering: [Ent → Atlas review planning](docs/migration/SCHEMA-PLANNING.md), including compound constraints, protected-object fingerprints and dual-dialect upgrade rehearsals.

Database schema starts at the unpublished **v1** baseline (`001_initial.sql`); the next migration is 002. [Versioning and prior test instances](docs/migration/VERSIONING.md).
