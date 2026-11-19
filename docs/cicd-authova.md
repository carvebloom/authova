# Authova CI/CD product interface

Status: source candidate; not a running pipeline or production release capability.
Owner: Authova existing writer. Platform/poller/upload-only/packager/review producer: CI/CD owner.

## Trusted source admission

The poller supplies **both** `CI_COMMIT_SHA` and `CI_EXPECTED_SHA`. Nonempty exact lower
40-hex values must match each other and Git HEAD/commit; the checkout must be clean.
`main` authority is checked by the trusted poller/API, never inferred from a caller
branch string. An untrusted PR has no release/promotion credentials or host socket.

## Frozen command interface (four serial workflows)

All outputs must be in the workflow's private/shared workspace sibling of the
checkout, e.g. `/woodpecker/authova-ci-out`, not inside the Git repository. No
workspace is assumed shared **between workflows**. `PATH` includes `$PWD/.tools/bin`.
Each trusted-SHA workflow first runs the explicit network preparation:

```
python3 scripts/ci/prepare_tools.py --out /woodpecker/authova-ci-out/prepare
```

Hard dependency chain and exact product argv:

1. `authova-checks`:
   `python3 scripts/ci/driver.py checks --out /woodpecker/authova-ci-out/checks`
2. `authova-mysql`:
   `python3 scripts/ci/fixture.py --dialect mysql --out /woodpecker/authova-ci-out/mysql-fixture`
   then `python3 scripts/ci/driver.py mysql-primary --fixture-context /woodpecker/authova-ci-out/mysql-fixture/context.json --out /woodpecker/authova-ci-out/mysql`
3. `authova-postgres`:
   `python3 scripts/ci/fixture.py --dialect both --out /woodpecker/authova-ci-out/both-fixture`
   then `python3 scripts/ci/driver.py postgres-both --fixture-context /woodpecker/authova-ci-out/both-fixture/context.json --out /woodpecker/authova-ci-out/postgres`
4. `authova-build`:
   `python3 scripts/ci/driver.py console-generation --out /woodpecker/authova-ci-out/console`
   then `python3 scripts/ci/driver.py bundle --receipts /woodpecker/authova-ci-out/console --out /woodpecker/authova-ci-out/compile`

The final upload-only artifact root is `/woodpecker/authova-ci-out/compile/bundle`.
Only its local console-generation native receipt is in the compile manifest.
The trusted host verifies **all four actual workflows in the same pipeline/source**
via Woodpecker API; commands do not fabricate copies of another workflow's receipts.
The final step builds Linux core/console binaries once and consumes the exact
web/legal output hashes from console-generation, without a second UI build.

Gates preserve existing Actions: boundaries/gofmt/vet/full Go race/Python/discovery/
scanners/build; Proto/Ent/Console generation plus full UI checks/audit/tests/build;
strict MySQL integration/contract; then both-DB compatibility. Each command records
native exit, known-return flag, original UTC I and elapsed; first failure stops.
Raw logs and fixture context stay private0600 and are never upload artifacts.
Preparation is explicitly networked. This is not an offline guarantee.

Manifest `authova-compile-bundle/v1` exact fields:
`schema,repo,source,main,platform,checks,native_build,tools,schemaDigest,files,console_assets,configuration_included,required_workflows,cross_workflow_gate_authority`.
`files` contains only relative paths, bytes, SHA256 under `core`, `console`, `web/`,
`legal/`; manifest itself is excluded from its member list. No env/config/key/DB.
`native_build` records each original binary compile exit/time; buildinfo must prove
Go1.27.4/Linux/arm64/CGO0. The trusted packager augments (not forges/rewrites) the
compile manifest provenance with API-verified four-workflow gates and image/archive
bindings. Test and production subsequently use those identical image IDs.

## Optional failed database-profile observation

`test_profiles.py` observes the same Go JSON iterator already consumed by its
verifier, after the attempted native groups have returned and closed their files.
It adds no Go invocation, evidence-path traversal, retry or test selection. The
original private result and gate decision remain unchanged. One bounded stdout
frame has prefix `AUTHOVA_TEST_PROFILE_SAFE_V1 `; the failed first database command
in `driver.py` may consume that frame into optional `test_profile_summary` on its
existing safe gate receipt. This is diagnostic evidence, not a replacement gate.

The summary is a closed nine-field object:

| Field | Contract |
| --- | --- |
| `schema` | `authova-test-profile-summary/v1` |
| `profile` | `database` for this driver consumer |
| `dialect` | `mysql` for `mysql-primary`, `both` for `postgres-both` |
| `native_exits` | Original attempted Go-group exit codes, 1–2 integers in -255..255; booleans rejected |
| `verification` | Original `PASS` or `FAIL`, independent of outer make status |
| `verification_failure` | `NONE`, `INVALID_GO_JSON`, `INVALID_EVENT_SHAPE`, `UNEXPECTED_SKIP`, `NATIVE_OR_TEST_FAILURE`, `PACKAGE_INCOMPLETE`, `REQUIRED_TEST_INCOMPLETE`, or `UNKNOWN` |
| `evidence_state` | `COMPLETE`, `PARTIAL`, or `UNAVAILABLE` |
| `counts` | Closed counters below, or null when unavailable |
| `tests` | Closed source-registered identifier arrays below, or null when unavailable |

`counts` contains only `registered_top_level`, `package_passed`, `fail_events`,
`started_top_level`, `passed_top_level`, `failed_top_level`, `skipped_top_level`.
Counters are nonboolean integers in 0..100000. Top-level counts distinguish the
registered package/test pairs internally; `tests` deliberately exports no package
paths and contains only sorted unique `started`, `passed`, `failed`, `skipped`
arrays. Each identifier must exactly match an ASCII `Test[A-Za-z0-9_]+` name
(at most 128 characters) in the selected public source inventory. Dynamic subtests,
unknown names, messages, output, DSNs, paths and content hashes are never exported.
Same-named tests in different packages may make a counter exceed its array length.

`COMPLETE` means the verifier reached EOF on the attempted groups' Go JSON, not
that all required groups/tests passed. `PARTIAL` marks a prefix when verification
stopped early; missing events cannot become failed tests. Shape/observation-limit
failure gives `UNAVAILABLE`. A native nonzero alone never adds a failed Test ID.
The observer stops collecting after 100000 events or 1024 registered identifiers;
these limits do not weaken or stop the existing verifier.

The frame is at most 64KiB. Driver reads only its own closed command log (at most
1MiB), with nofollow, regular0600/owner/singlelink and stable FD/endpath checks.
Missing, duplicate, malformed or unregistered frames omit the optional field;
they cannot change native status, failure, ordinal or timing. Platform projection
must omit an invalid optional summary and its receiver must reject malformed
present summaries. Existing seven-value `diagnostic` semantics are unchanged.

## Tool image interface

Go **1.27.4**, Node **26.13.0** and its npm, Python >=3.9 (platform image 3.11.2), make/git/OpenSSL,
CC+race runtime, protoc **36.2** with includes, mysql/psql clients.
Preinstall scanner `govulncheck` **1.9.0**, `staticcheck` **0.8.2**;
generator pins: protobuf **1.36.11**, Kratos HTTP plugin
**v3.0.0-20261026152050-dd0fb7df1e32**, oapi-codegen **2.8.0**,
openapi-typescript **7.10.1**, Redocly CLI **2.54.3**.
Repository go.sum/npm lockfiles remain authoritative. Plugins must be in the
`.tools/bin` location expected by `scripts/generate-api.py`. The image needs the
complete build/generation/audit graph; version strings alone are not qualification.


### Prepared generator consumption

`prepare_tools.py` is the explicit networked producer: its seven commands and
version pins are unchanged. It installs five Go tools in `.tools/bin` and warms
the pinned `openapi-typescript@7.10.1` npm package. Generation consumes the three
prepared generator binaries there, verifies their Go buildinfo pins, and passes
the checked Proto plugin paths explicitly to protoc. Scanners stay on the same
`.tools/bin` PATH. A partial or wrong-version prepared set fails rather than
mixing it with legacy binaries.

Standalone `generate-api.py --install-tools` still explicitly installs in `.tools`.
Existing two-plugin standalone layouts remain supported with the pinned oapi Go
source invocation; check mode requires its modules already cached. Ordinary
API/Ent checks use `GOPROXY=off`, `GOTOOLCHAIN=local`, `GOWORK=off`, and API/Console
TypeScript checks use pinned `npm exec --offline`. Missing prepared/cache inputs
fail without an implicit install/download. The Go wrapper respects an explicitly
set `GOPROXY`, including `off`; its historical host-cache fallback applies only
when unset. This narrows generation checks, not all CI/network behavior.

The `scripts/tests/test_generator_tools.py` component actor runs the actual
prepare → contract entry → nested generation/verification Python chain with
synthetic owned tools/files, plus standalone, wrong/partial layout and wrapper
controls. It proves path/env consumption and refusal behavior, not real Go/npm,
DB or generated-output compatibility. Those gates still run in fresh CI.

## Dedicated test fixture

Use `authova-ci-test-<sha12>-<runID>`, fresh outside-checkout state, random
`authova_quality_<12lowerhex>` schema, new keys and accounts, no production mounts,
no existing authova-test/Lab/production resources. MySQL 8.4.11 digest from current
Actions; Valkey 9.1; PG17 only for explicit both-DB compatibility. Runtime users are
DML-only, migrator is fixture-only; PG NOSUPERUSER/NOBYPASSRLS + FORCE RLS.
Pass the exact `AUTHOVA_TEST_*` DSNs/marker/Valkey/key contract from TESTING.md
privately to the DB stages. Existing `scripts/tests/go-errors.py` demonstrates
fixture lifecycle but is not the new CI fixture installer: its CLI secret transport
and fixed evidence paths are not suitable as a public pipeline adapter.

`scripts/ci/fixture.py` supplies bootstrap/migrate for these **new** CI services:
- aliases `authova-ci-mysql`, `authova-ci-postgres`, `authova-ci-valkey`, never loopback;
- fixture-only environment inputs `AUTHOVA_CI_MYSQL_ROOT_PASSWORD` and, for both,
  `AUTHOVA_CI_POSTGRES_PASSWORD`; no production secret refs;
- mysql CLI defaults-extra-file and PGPASSFILE0600; SQL/password only private stdin;
- random new schema/runtime role, separate DML-only runtime identity, fresh master key;
- migrate then private `context.json` passed directly to the DB gate, never uploaded.

MariaDB10.11.19 mysql client / psql15.20 image interfaces are source-compatible with
these argv (`--defaults-extra-file`, `--batch`, psql `ON_ERROR_STOP`), without
`--ssl-mode` or a claimed MySQL8/PG17 *client* grade; real server compatibility is
still a required native run. Platform must ensure bounded readiness before init;
mutations are not retried. Workflow teardown owns only its service containers and
fresh anonymous volumes; record teardown failures, no prune/production cleanup.

Services are workflow-local, only mysql+Valkey for mysql and mysql+PG+Valkey for
both. Memory quotas come from the selected Authova agent configuration, not
unsupported per-service backend_options. The next Authova-only candidate targets
1536MiB per step, CPU1.5 and one workflow inside the existing 2CPU/3GiB VM; other
agents and application-daemon budgets are separate. Constrain server internals:
MySQL bufferpool192MiB,
performance_schema OFF/max_connections32; PG shared_buffers64MiB/max_connections16/
work_mem4MiB; Valkey maxmemory64MiB/no persistence. Command Go heap256MiB/Node512MiB,
GOMAXPROCS1/-p1 under the fixed `serial768` CI profile. These are candidate limits,
**not** 3GiB headroom or cold-fit evidence;
platform must qualify the actual 3GiB VM serially before activation.

CI preparation, gate execution, fresh-fixture migration and both bundle compilers
share `driver.ci_environment`: `GOMAXPROCS=1`, `GOFLAGS=-p=1`,
`GOMEMLIMIT=256MiB`, `AUTHOVA_CI_RESOURCE_PROFILE=serial768`. The profile is a fixed
resource selector, not an arbitrary Go flags interface. `test_profiles.py` retains
it across environment sanitization, fixes `-parallel=1` and replaces caller
`GOFLAGS`; unsupported profile values refuse before Go. Ordinary local/Actions
calls without this profile keep their existing defaults. Package/case selection,
required events, allowed skips, race/count/timeout and phase bounds are unchanged.
These process limits do not establish a hard RSS ceiling or successful cold fit.
`serial768` is the preset's fixed name; it does not set a Docker memory quota.

Platform-reported qualification on 2026-11-19: the isolated 768MiB serial race run
failed during Ent compilation with OOM. A separate same-Go-source run using its
own cache at 1.5CPU/1536MiB completed `go test -race -count=1 ./...` with exit0,
32 package `ok`/zero package failures and cgroup peak1396891648B; its OOM counters
and exact-container OOM events were zero. Test-level Skip is UNKNOWN from the
nonverbose output. This is a limited independent Go diagnostic, not a DB gate,
full workflow, cold-cache/whole-VM fit or test-deployment qualification. The failed
768MiB run and successful vet diagnostic remain separate historical facts; source
resource-control OWN tests provide no native capacity grade.

Same-image test deployment/acceptance and host execution now have source implementations
in the owner host package. Their actual installation/execution and full CI resource fit
remain pending; gate/fixture OWN checks do not certify them.

## Trusted packaging / promotion

Upload-only plugin sends only the closed compile bundle to private host content
storage. Host validates membership, lengths/hashes, tool/source/gate binding before
executing a **frozen** COPY-only recipe to seal core+console images once. It must
include existing runtime CA/dependencies and original notices, set nonroot identity,
check Linux/arm64 image/buildinfo, and produce one docker-save archive. Test and
production consume the same immutable IDs/archive, never rebuild.

Host candidate exact keys:
`schema,repo,source,coreID,consoleID,manifest,archive,schemaDigest`;
schema=`authova-image-candidate/v1`, repo=`carvebloom/authova`.
Manifest/archive/schemaDigest are SHA256; source is exact main40; image IDs use sha256:.

`scripts/ci/host_release.py` remains a plan-only inspector. Actual host admission
and durable execution boundaries are in `host_admission.py` / `host_adapter.py`,
imported only by a pinned installed host worker. They independently verify the fixed
platform HMAC v1, private nonce-keyed immutable ledger and whole test receipt,
same-inode lock/current CAS, then EXCL/fsync nonce+intent before mutation. They do
not authorize themselves from a job's review fields. The exact private binding,
nine required test checks, native-evidence receipt and implemented installed
operations are in [the host contract](cicd-host-adapter.md). The owner host package contains concrete Core/Console operations and an isolated
TLS/SMTP/HTTP/OIDC test runner. The lane remains held until normal Git integration,
worker hookup and actual gates/packaging/deployment qualification; a model or plan
helper cannot close that runtime gap.

The host-controlled current configuration must supply `schemaDigest` from the
adopted source and preserve **all** current active layers referenced by
Workbench `docs/project-progress/authova-active-control.json`. Do not follow private
locators to enroll/sign off this source. The host later generates exactly a new
two-service image override (Console asset path `/opt/authova-console/web`) and uses
`up --detach --no-deps --no-build --pull never --wait --wait-timeout 90 core console`.
Keep Worker/DB/issuer/keys/TOTP/trust and mounts/config semantics unchanged except
these image/assets fields. Schema difference refuses; no automatic migration.
Unknown execution stops; known poststate rollback restores code/images, never data.

## Git intake status (2026-11-19)

Fresh main is `d04e1322dfc3525c130a8f6ffde2811ba4452598`, confirmed through
GitHub API and HTTPS Git with process-only proxy. Its tree equals the previously
used feature HEAD tree; no legacy commit replay or force update is needed.
Integration branch: `feat/authova-product-cicd-20261119` based directly on that main.
Product commit `568c201` includes deployed password/enum/navigation and the two
already-deployed SBOM-output/footer-selector source fixes. Unrelated local agents,
real-login tools and ignored credentials are excluded. This CI interface is a
separate commit. The pipeline stays held until exactmain normal PR integration,
fixed pipeline/tool-image native qualification and remaining trusted host/test
adapters are complete. This document does not claim a merge or CI success.
