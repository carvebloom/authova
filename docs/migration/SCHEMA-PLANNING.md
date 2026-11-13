# Ent → Atlas OSS migration planning

The planning gap is implemented by `cmd/schema-plan` and `internal/schemaplan`.
The official path combines generated Ent field/index metadata with an explicit
physical extension. Direct `ent.Client.Schema.Create` or an unextended
`internal/ent/migrate.Tables` diff remains unsupported and is never used by
production. The extension deliberately keeps ORM entity identity separate from
physical App-bound identity, without changing runtime repository transactions.

## Model ownership

- `internal/ent/schema/*.go` owns every ordinary column's type, size, nullability,
  dialect-specific SQL default and shared index. Defaults are SQL annotations,
  not newly assigned application default values. The pinned Ent generator emits
  `internal/ent/migrate/schema.go`. Its local Apache-licensed template override
  fixes quoting of JSON/string expression defaults; generation is reproducible.
- `internal/schemaplan/model/{postgres,mysql}.json` owns composite primary/foreign
  keys, delete/update actions, physical index-name aliases, checks, PostgreSQL's
  partial signing-key index and MySQL's generated current-App column/index.
  Ordinary Ent columns are prohibited in the extension. SQL-only `owner_locks`
  and `outbox_dispatch` are explicitly modeled. MySQL helper indexes needed by
  foreign keys remain dialect-specific. Index aliases reference Ent index names;
  stale aliases and App-unbound FKs fail, rather than silently hiding a change.
- The composed Atlas schema models all 30 product tables and 39 foreign keys in
  each dialect, with 32 PostgreSQL / 45 MySQL indexes and four checks. The three migration
  bookkeeping tables and any additional externally owned tables are preserved by
  identity. Database-level charset/collation belongs to deployment; MySQL product
  tables explicitly remain InnoDB/utf8mb4/utf8mb4_bin. No foreign key gets inferred
  solely from an ORM edge, and no App-bound FK may omit its App component.

Both real PostgreSQL and MySQL must have zero diff against the reviewed v1
physical schema. New fields and shared indexes flow from Ent into the target
model; changes to physical identities/security objects require extension or SQL
review. Generated entities and the physical extension are one planning chain,
not independently maintained alternative ORM repositories.

## Generate and verify a review

Build with Go 1.27.4 and `make schema-plan`. Load the isolated test database DSN
into `AUTHOVA_PLAN_DATABASE_URL` using a private environment/secret mechanism.
The command takes an environment variable name, never a DSN command argument.
Use a migration owner or a dedicated schema-audit account. It always starts a
read-only repeatable-read transaction, even with an owner credential.

```sh
mkdir -p .migration
bin/schema-plan --dialect postgres --check
bin/schema-plan --dialect postgres --out .migration/review-postgres --name add_field
bin/schema-plan --dialect postgres --verify .migration/review-postgres/review.json
```

Repeat with `--dialect mysql` and its DSN. Each output directory must be new. A
no-op emits only `review.json`; it does not create an empty migration. A real diff
emits the next numbered SQL candidate (currently `002_<name>.sql`) and a report
with source/target fingerprints, statements, SQL checksum and protected-object
hashes. `--verify` recomputes the plan and checks the SQL artifact; stale source,
changed model, changed protected objects or edited SQL is rejected. PostgreSQL candidates explicitly qualify the `public` schema to avoid
`search_path` ambiguity. No command
applies DDL, advances database history, modifies an existing output directory or
writes directly into the production migration catalog.

By default, destructive changes, primary-key/column replacement, new tables and
non-null additions without a default are rejected. `--review-destructive` only
includes them in the candidate for explicit destructive/backfill/security review;
it does not authorize or execute them. Missing/new App tables are additionally
marked `new-app-tables-require-policy-review`; their versioned policy companion
must enable and force RLS before runtime traffic. Existing tables with broken RLS
are refused even when destructive candidates are requested. Unique indexes and foreign keys also need
data-validity review before deployment. Test every generated plan on a disposable
copy of the intended database version, including its realistic existing data.

For MySQL a complete protection report requires directly visible `SHOW_ROUTINE`
globally plus `SHOW VIEW` and `TRIGGER` in the target schema (or corresponding
owner privileges). `SHOW GRANTS` is checked; inherited roles are conservatively
refused until their grants are made explicit. This prevents hidden metadata from
being treated as absence. Restricted runtime users can still inspect and compare
the complete table model (`TestAtlasPlanning`); runtime credentials need no new
privileges. PostgreSQL validates the exact `app_scope` predicate, PUBLIC roles,
policy command, enabled RLS and FORCE RLS, and rejects extra permissive policies.

## Objects outside the OSS table differ

The planner invokes Atlas OSS's table-only inspector/differ. Policies, routines,
triggers and views never enter its desired-state deletion set. Reports separately
hash their visible definitions and ACL/security metadata; definition bodies are
not exported. Changing such an object is an explicit versioned SQL edit, with
its before/after security acceptance, not an Ent diff side effect. A new App-bound
table additionally needs reviewed ENABLE/FORCE RLS and an `app_scope` policy in
PostgreSQL. MySQL has no RLS claim. PostgreSQL role/schema ACLs remain deployment
responsibilities, checked by existing restricted-runtime startup gates.

## Promote reviewed versioned SQL

After reviewing both dialect candidates and any required policy/backfill SQL,
copy the identically named numbered files into
`internal/migrations/sql/{postgres,mysql}`. Regenerate with `make migration-hash`.
The compiled catalog discovers contiguous numbered revisions automatically and
requires PostgreSQL/MySQL filenames to agree. It retains the historical checksum,
dirty fence, deployment lock, OSS Atlas execution and baseline checksums.
Both runtime and migration execution discover the same continuous catalog. A failed/unknown migration
is not replayed automatically; reconcile authoritative state first.

Run generation consistency, `make schema-plan-test`, and full disposable
`quality.py` gates before building a deployment artifact. Never apply against the
old business instance or assume a generated review is permission to migrate it.
The unpublished complete structure is `001_initial.sql`; its schema version is
1 and the next change starts at 002. See VERSIONING.md.

## Acceptance evidence

- `TestAtlasPlanning`: complete physical target has zero diff on both databases
  under restricted runtime users.
- `TestAtlasProposedUpgrade`: add a proposed Ent column and index; generate and
  execute versioned SQL with the pinned OSS executor on an owned disposable DB;
  assert convergence and unchanged policies/functions/views/triggers. Reject a
  stale report, disabled RLS and an extra permissive policy. The no-op revision
  writer in this test is exclusively for its disposable DDL rehearsal, not
  production migration tracking.
- `TestAtlasPlanningCLI`: compiled command checks, writes/verifies a no-op report,
  rejects output overwrite and rejects an injected SQL artifact, on both dialects.
- `TestVersionCatalog` and existing maintenance gates: contiguous future revision
  discovery, fresh initialization, v1 initialization/rerun, old-history refusal and checksum/dirty checks.
- `TestEntPhysicalMapping`: existing independent physical inspection guard remains.

Latest exact gate results are recorded in `engineering-validation.json`; raw
logs and disposable fixtures are ignored under `.migration/`.


After unpublished-baseline consolidation, the compiled-catalog `v1 -> v2`
rehearsal is available through `scripts/migration/upgrade-gates.py`. It verifies
real Authova and Atlas revision ledgers and restricted HTTP startup, in addition
to the proposed-model DDL tests above. The v2 source/binary exist only in private
acceptance copies; production remains at `001_initial.sql`.
