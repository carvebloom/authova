# Database versions: current v2, retained v1 baseline

Current schema is **v2**: the new PG/MySQL `002_delivery_diagnostics.sql` adds
bounded outbox failure classification. The original 001 files/checksums and
payload v1 stay unchanged. See [error-handling implementation](../go-error-handling.md)
and [current validation](../go-errors-validation.md). The next migration is 003.
Existing Lab instances were not upgraded by this work. The original consolidation
record below describes creation of the retained v1 baseline.

The former **v8** meant the source database's eighth schema migration. It was
not an Authova product release number. Because this repository is unpublished,
its complete current structure is consolidated into one reviewed initial
migration per dialect: `internal/migrations/sql/{postgres,mysql}/001_initial.sql`.
At consolidation the compiled database schema version was **1**. The public API was already
`/api/v1` and stays there. Product/package build labels and encrypted payload
format versions are separate version domains and are not renumbered here.

The baseline directly creates all 30 product tables, final column/default
values, 39 foreign keys, compound identities, checks, special indexes and the 24
PostgreSQL FORCE RLS policies. It does not run the source project's eight-step
history or its data backfills. Encryption, AAD, legacy gob readers and persisted
payload formats remain unchanged. The Ent-to-Atlas model has zero diff on fresh
PostgreSQL and MySQL v1 databases.

The first subsequent reviewed change is 002 delivery diagnostics. `schema-plan`
now emits `003_<name>.sql`. Later migrations continue 004, 005, etc.; runtime and migrator
both read the same checksum-validated contiguous catalog. There is no hardcoded
version-eight startup list or special version-two legacy backfill branch.

## Existing data is not renumbered

Never update `authova_schema.version` to pretend an old database is v1. The new
binary rejects a newer database version, a different baseline checksum, dirty
history and an unversioned nonempty application schema. Applying v1 is an empty
schema initialization, not an implicit source-data migration. Any business-data,
identity, issuer or key migration remains a separate explicitly planned task.

The previous isolated test databases (`authova` and
`authova_migration_agent_demo` in the new migration Compose project) and their
private keys remain preserved. The original three source repositories and their
services/databases/keys are untouched. New local instances use:

| Purpose | Database | Private local state |
| --- | --- | --- |
| Core / Worker / Console | `authova_v1` | `.migration/instance-v1` |
| Agent example | `authova_migration_agent_v1` | `.migration/demos/v1/agent` |
| Passkey example (on demand) | `authova_migration_passkey_v1` | `.migration/demos/v1/passkey` |

Core and Console retain the localhost ports 38080/38090. The Agent example
retains 38288 (Core 38280 and Console 38290). New databases have independent new
keys/operators/Apps; prior demonstration users are not silently imported.

## Evidence and historical references

`v1-validation.json` records acceptance of the consolidated baseline. Earlier
validation/performance reports may mention schema 8; those are preserved as
historical evidence and are not relabeled as v1 test results. Source manifests
and historical design documents likewise retain their actual source versions.
Current instructions and generation candidates use v1/002.

The bounded old/new benchmark uses two fresh databases, each initialized by its
own binary. Only newly seeded synthetic product facts are copied by column name;
version histories are never copied or edited. This permits a source-schema-8
binary and a new-baseline-1 binary to be compared without faking compatibility.


A genuine next-version rehearsal is reproducible with
`python3 scripts/migration/upgrade-gates.py --evidence .migration/v1-upgrade-new`.
It builds a proposed v2 binary in a private source copy, initializes disposable
v1 databases with the current binary, applies and reruns v2 through the real
locked migrator, checks both revision ledgers, starts the upgraded HTTP server
under DML-only credentials and confirms the v1 binary refuses the v2 DB. It
removes only its created databases and leaves the product catalog at v1.
