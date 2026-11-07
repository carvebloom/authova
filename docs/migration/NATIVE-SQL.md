# Deliberate native SQL boundaries

Ordinary repository operations use generated Ent entities/queries for 28 tables.
The former generic string-built upsert and Agent CAS implementations were removed.
Encrypted payload versions, gob fallback, AAD and existing physical tables remain.
Every repository Ent operation uses the caller's exact sql.Tx. No global Ent
client/pool is available to the adapter. Platform operations remain separate from
App-bound ports. Read transaction mutations are rejected by Ent hooks as well as
repository/database guards.

| Location | Native SQL purpose and regression coverage |
| --- | --- |
| `store.go` | Begin/commit/rollback, PG transaction-local app scope, startup role/schema/RLS checks, route resource admission; commit-unknown and restricted-role tests |
| `app.go` LockOwners | Lock the App row to serialize owner removal; last-owner tests |
| `app.go` ActiveToken | One-snapshot App/client/user/session/family/Agent authority join; refresh/reuse/revoke/query/Agent concurrency tests |
| `platform.go` ProvisionApp / CountOperators | Same-transaction PG scope switching and singleton bootstrap lock; platform/bootstrap/owner tests |
| `ent_upsert.go` | Parameterized Ent conflict expressions preserve MySQL secondary-unique identity guard; dual-db collision/unchanged-payload/same-identity-update regression |
| `introspection*.go` | Established single-snapshot purpose/authority hot path and restricted reader; protocol dependency/revocation tests |
| `outbox.go`, `outbox_stats.go` | DB-clock scheduling, ordered per-App opportunity locks, SKIP LOCKED, lease/attempt fencing, fairness and bounded stats; producer message INSERT is now Ent; worker/outbox/upgrade tests |
| `cleanup.go` | Ordered bounded multi-table retention with FK-safe deletes; retention and cleanup tests |
| `recovery.go`, `rewrap.go` | Explicitly enumerated encrypted-payload maintenance/recovery tables and bounded locking; recovery/rewrap acceptance fixtures |
| `migrate.go`, `internal/migrations` | Deployment advisory lock, dirty/SHA history, Atlas execution/revisions and original v1 payload backfill; fresh/upgrade/tamper/rerun tests |
| `benchmark_seed.go` | Opt-in test capacity seeding, not request-path repository access |

Values are bound parameters. Schema identifiers originate from fixed reviewed
constants, never API input. Outbox and token liveness are not split into unrelated
Ent reads, which would weaken their snapshot/locking behavior. PostgreSQL FORCE
RLS and compound FK constraints remain enforced by migrations and runtime role
checks. MySQL does not have RLS; its boundary is explicit App predicates,
transaction-bound ports, compound constraints and the restricted runtime account.

The completed Ent-to-Atlas planning graph composes generated field/index metadata
with explicit compound PK/FK/check/special-index definitions. Use the review-only
`schema-plan` command, not the unextended Ent table slice or Schema.Create.
RLS/functions/policies remain explicit versioned SQL objects protected from the
OSS table differ. See SCHEMA-PLANNING.md for generation, review and verification.
