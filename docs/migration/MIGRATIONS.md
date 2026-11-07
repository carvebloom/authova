# Ent data mapping and Atlas migrations

The chosen database model maps 28 Ent entities onto the complete v1 baseline
physical tables, preserving the source system's final physical layout. Ordinary application reads, typed predicates and writes use Ent;
application/domain/ports expose no generated Ent types. An App-bound repository
only receives its App scope. The adapter's driver executes Ent and approved native
SQL on the exact same `database/sql.Tx` and connection, with no pool fallback.
Platform operations use distinct ports. PostgreSQL sets transaction-local App
scope and retains FORCE RLS and restricted runtime users. MySQL has no RLS claim.

Physical changes are planned from generated Ent fields/shared indexes plus the
complete dialect extension in `internal/schemaplan/model`. The review-only
`schema-plan` command composes compound identities, foreign keys, checks and
special indexes, then uses Atlas OSS to produce numbered candidates and verified
review reports. See SCHEMA-PLANNING.md. Reviewed versioned SQL remains the only
production deployment input; no direct Schema.Create is called. The pinned OSS
Atlas executor requires no Cloud, login, registry or commercial feature.

`TestEntPhysicalMapping` uses the OSS Atlas inspector under the DML-only runtime
user to validate every mapped column's presence/nullability/declared length,
compound primary keys, App-bound foreign keys and critical compound unique
indexes. It also checks PostgreSQL's current-key partial index and MySQL's
`current_app_id` generated unique column. The existing isolation tests verify
RLS/policies, role restrictions and cross-App attempts. This independently guards mapping drift; the official planner additionally
checks complete structural equality after composing the physical extension.

For each future schema change, update the Ent mapping, write and review SQL for
both dialects, preserve explicitly managed RLS/functions/indexes, regenerate Ent,
run `make migration-hash`, inspect the diff and run fresh/upgrade/physical/isolation
gates against real PostgreSQL and MySQL. The review planner preserves external tables and fingerprints protected
objects; it cannot delete them as an omitted Ent entity. Native SQL purposes are listed in `NATIVE-SQL.md`.

`authova migrate` holds the deployment lock on the connection used by the
Atlas executor. `authova_atlas_revisions` records completed revisions, numeric
revision type and Atlas hashes/partial hashes. The schema version, checksum and
dirty fence remain checked. This unpublished repository starts at a single
`001_initial.sql`; future changes begin at 002. Both runtime startup and migration
execution read the same compiled catalog. There are no legacy eight-step data
backfills. Existing old histories are rejected, never renumbered or silently
adopted. Fresh initialization, repeat execution and tampered/conflicting history
have dedicated dual-dialect tests in disposable databases. See VERSIONING.md.

Typed searchable columns coexist with versioned encrypted payloads. The existing
AAD, encryption/keyring behavior and legacy gob reader are retained. Replacing
all encodings in this framework migration would add compatibility and
cryptographic risk; it is deliberately excluded. No old business database, user,
issuer or key has been imported or modified. This repo's database history and its
fresh Git history are separate concerns.
