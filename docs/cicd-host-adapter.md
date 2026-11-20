# Authova installed host executor

The source implements the five owner operations, isolated test fixture and actual
HTTP/OIDC acceptance actor. It is **not installed or runtime-qualified** by these
source tests. Keep agent3 held/polling disabled until normal Git integration and
controlled qualification. No production configuration or secret was read to
prepare this interface.

## Fixed worker entry and files

The trusted worker installs the eleven files in `scripts/ci/code-pins.json` at:

```
/Users/chenhuanxiang/Workbench/authova/.local-test/authova-cicd-host01/installed/<executor-manifest-sha>/
```

Directories are0700, files0600, expected host owner, regular/singlelink, no symlink
parents. `code-pins.json` maps the exact eleven names to SHA256. The worker pins
both launcher and exact manifest bytes from the approved owner source **before**
invocation. The launcher then verifies every held source byte before importing any
sibling module; it executes those held modules, not an arbitrary checkout import.
The tool interpreter is the existing physical Python3.9 (or an explicitly qualified
owner installation), not a `PATH` fallback:

```
/Applications/Xcode.app/Contents/Developer/Library/Frameworks/Python3.framework/Versions/3.9/bin/python3.9 -I -B <installed>/host_runner.py --installation <request-root> --admission <nonce>.admission.json --executor-sha256 <exact-code-pins-file-sha>
```

`<request-root>` is exactly:
`.../authova-cicd-host01/requests/<signed64hexnonce>`.
Input files, all private0600/singlelink:

- `profile.json`: closed installed profile below, never a job-authored capability;
- `candidate.json`: exact existing eight-field candidate, selected by the trusted
  worker from authenticated build/packager facts;
- `<nonce>.admission.json`: unchanged signed carrier from the producer;
- `key`: raw >=32bytes, RAM only, no content hash or log;
- `test-receipt.json`: production only, complete same-candidate owner test receipt.

There is **no caller-supplied current CID or owner-binding boolean**. After signature
and private-ledger verification, the owner writes EXCL/fsynced `owner-started.json`;
a repeat request refuses before any Docker query. It derives the test binding or
samples the fixed production project, then writes `owner-binding.json`. The actual
operation separately takes the same-inode lock, checks whole current CAS and
EXCL/fsyncs nonce+intent before first possible Docker mutation.

Fixed execution roots are `.../authova-cicd-host01/state/test` and `state/production`.
The request's fixed `ledger_root` is the private immutable producer root; the owner
reads only `<nonce>.json` there. It has the agreed exact nine keys:
`schema,nonce,action,candidate_digest,payload_sha256,build_pipeline,test_pipeline,production_pipeline,test_receipt_sha256`.
Schema is `authova-host-admission-ledger/v1`. Both SHA values are canonical compact
UTF8 JSON hashes without prefix/newline. Test receipt SHA is null for test action.
Production compares **the entire** receipt against the trusted immutable ledger;
caller receipt replacement, job PASS booleans or a plan helper cannot authorize it.
The platform envelope/payload12/HMAC/300s protocol is unchanged.

Original UTC/monotonic origins precede bootstrap and survive all phases. Test uses
3600s, production1800s; these are hard total ceilings, not cold-fit evidence. Admission
must still be valid before intent and the first mutation; no nonce renewal. Commands
fit their whole/close/suffix caps. Reporting is checked before/after stdout+flush;
there is no second fallback frame after output started or a late positive grade.
Failures retain the original exception and fixed close secondaries, never free
exception strings. Public failure diagnostics contain only closed code/phase/gate.

Worker consumes original native return separately from all files. Exit0 plus
`PASS_TEST`/`PASS_PRODUCTION` is necessary; files alone or partial stdout do not
qualify. Exit1 is a known refusal/rollback outcome, exit2 unknown/refused; neither
replays. An existing intent or started marker is never removed to retry.

Fixed safe outputs under state root:

- `<nonce>.intent.json`: private consumed intent and exact current binding;
- `<nonce>.result.json`: finite safe owner result;
- `<nonce>.native.json`: original CLI exit/known wait/original clocks/selected IO
  closure and first-current tool metadata; no argv/stdout/stderr/private hashes;
- `<nonce>.test-receipt.json`: only after all nine actual test gates pass.

Nine safe check records are `<state>/test/<scope>/<check-id>.json`.
Read only these fixed outputs. Do not follow their artifact/control descriptors or
publish private request/context/Compose files. All-OS descendants remain UNKNOWN.

## Installed profile

`authova-installed-host-profile/v1` exact fifteen keys:
`schema,role,state_root,ledger_root,docker,compose,docker_host,docker_config,daemon_id,active_control,archive,fixture_images,core_origin,console_origin,ca_pem`.

Each tool is exact `{path,identity,sha256}`. Identity is the ordered nine-field tuple
`dev,ino,mode,nlink,uid,gid,size,mtime_ns,ctime_ns`; null explicitly selects one new
first-current read against the **fixed expected SHA**, then retains that exact
identity. Regular executable/singlelink/root-or-owner/non-group-writable metadata,
complete sameFD SHA/endpath/close and later identity guards are mandatory. This is
stable selected bytes, not vendor or atomic exec-mapping qualification. The trusted
worker pins the physical Python separately. No SDK/cache scan or tool installation.

For test, Docker/Compose physical paths, SHA and daemon ID are the fixed public
`cicd/config/authova-ci-endpoint.json` values: only
`unix:///Users/chenhuanxiang/Workbench/cicd/.local/colima/lab-ci/docker.sock`,
daemon `06b356e5-903e-42a1-b1ec-1c5e29da05e5`, Linux/aarch64.
Compose executes its physical plugin directly, not empty-HOME plugin discovery.
`DOCKER_CONTEXT` is absent; no default/application endpoint fallback.
`docker_config` is exactly `.../docker-config/<test|production>`, owner0700 and
initially empty; neither host config nor registry credentials are inherited.
`fixture_images` is exactly the fixed MySQL/Valkey/tools digests in `host_runner.py`.

Test preflight consumes these through a separate `fixture_image(role)` method.
The two fixed repository@sha256 references require the resolved local Id to equal
the pinned digest, Linux/arm64, and a nonempty unique RepoDigests list containing
only the role's explicitly enumerated Docker Hub canonical/short aliases at that
same digest. No tag, foreign repository, config-ID substitution or caller ref is
accepted. The tools fixture is already a fixed sha256 content ID and uses the
original exact-ID consumer, as do all application/rollback images.

If full inspect supplies Descriptor, its digest must match that same fixed digest,
its image/index media type is closed, size is a nonboolean positive bounded integer,
and any platform must be exactly Linux/arm64. A supplied null/malformed descriptor
refuses. Classic inspect JSON omitting this field is admitted only with all the
mandatory exact Id/repository digest/platform checks above; a failed separate CLI
projection never establishes absence or relaxes any guard. Fixtures still use the
same single local inspect command and `--pull never` consumers; no resolution pull,
additional service action, profile field, timeout or candidate image change.
For test, active_control/core_origin/console_origin/ca_pem are null.

For production, role is `application-production`, fixture_images null, endpoint
must differ from CI and its separately qualified daemon ID must match. Active
control is the existing public `docs/project-progress/authova-active-control.json`.
Core/Console canonical HTTPS origins and selected public CA (or explicitly selected
system trust via null) come from the existing route evidence, not a guessed port.
The old route's normal TLS/readiness/assets are checked **before** switching.
No production private configuration is needed to sign off this source contract.

`archive` is exact `{path,identity}` for the worker-sealed docker-save file; identity
uses the tuple above and archive SHA comes from the signed candidate. The consumer
bounds512MiB/4096members, sameFD/hash/endpath/close, and the exact two candidate image graph roots before
loading. The selected Docker29 containerd save format has two candidate OCI index
IDs, each binding one Linux/arm64 manifest/config/layer graph and its
unknown/unknown attestation manifest. Every referenced blob is read with bounded
chunks and checked against descriptor SHA256/size; shared blobs still require
identical descriptor sizes. Index IDs must exactly equal the two signed candidate
IDs. Docker `manifest.json` Config/Layers must match those runtime branches.
Only canonical metadata/blob paths and the two fixed blob directories are allowed;
duplicates, links/devices, external descriptors, missing/unreachable members, wrong
platforms and attestation references refuse. Unsupported archive layouts refuse
rather than treating an index digest as a config ID. Layers remain opaque hashed
bytes (no extraction/decompression); attestations confer no provenance grade.
It does not execute a Dockerfile or rebuild. Images load only **after**
intent consumption. The archive and three fixture image existence are actual inputs,
not granted by a source hash.

## Five operations and production limits

`CoreConsoleOwner.current/preflight/apply/accept/rollback` are implemented operations,
not job callbacks. Current target is only project
`authova-local-production-20261114`, exact Core/Console singleton labels/IDs.
The owner reads the whole public active control and preserves **all** active layers.
New control schema `authova-two-service-active-control/v1` has explicit
`compose_layers`; readers must not reconstruct only three layers or drop prior ones.
Old schema is only its known three-layer input; new publications always use the full
array. Old images/overrides/failure records remain retained.

Private Compose/config/inspect output is bounded RAM only. Before Compose is invoked,
retained JSON layers reject includes/extends/label_file and env-file references outside exactly
`core/service.env`, `console/service.env`, `migrator/service.env` below the retained
root. Those bodies/identities are compared in memory before/after operations, never
hashed/copied/logged. The original environment is compared as a complete map.
Candidate config may change only two images and the Console assets directory.
Other labels, Env, mounts, network, command, UID, health/hardening/resources stay
strictly compared; unsupported or differing semantics refuse. No Worker/DB action.

Core's `schema-check` subcommand returns only after the existing read-only
`store.CheckRuntime` version/dirty/checksum/privilege checks, before application
construction. It never migrates/bootstrap/serves. The nonce-owned probe has the
fresh Core network namespace, RO root, capdropALL/NNP/user10001 and only needed
existing RO mounts derived from its fixed file-env keys. Env values come from the
resolved map via RAM `--env-file /dev/stdin`, **not** retained Compose-quoted files.
The original private inputs are not rewritten. Known CID ACK/wait/nonforce rm/absence
are recorded; unknown creation/start/transport is retained without adoption.

Only after this probe and repeated public/private CAS may the owner issue:
`up --detach --no-deps --no-build --pull never --wait --wait-timeout90 core console`.
Poststate uses actual IDs/images and complete private semantic checks plus normal
TLS, static assets, health and anonymous401. No production login, account/password/
session mutation, schema migration, issuer/key/TOTP/trust or shared-route change.
Known failed switching may rollback only with exact fresh poststate/config CAS,
old immutable images and all original layers. Unknown state/IO/close stops, no blind
rollback or replay. Code rollback never restores data or passwords.

## Fresh CI test and nine required checks

Owner derives project `authova-ci-test-<source12>-<build_pipeline>`, scope nonce32,
and `authova-ci-test-binding/v2`. Exactly core/console/mysql/valkey/proxy, a fresh
network, one MySQL data volume and four role-specific credential volumes are used.
All namespace/resources must be absent; no existing test/production data is adopted.
No Mac host paths bind into the CI VM. Public pinned scripts and fresh credentials
are staged by a fixed no-network helper into new named volumes; private bytes stay
in pipes. The material helper has only the additional CHOWN capability needed for
uid10001 files, not a Docker socket or host mount. Core/Console/probes remain
RO/capdropALL/NNP. MySQL official entrypoint initializes only the new data volume.

New CA/keys/accounts are test-only; normal TLS/hostname and Valkey TLS/mTLS are used
without global trust changes. The fixed tools-image proxy supplies TLS and a single
fresh-recipient STARTTLS SMTP sink in RAM. One official challenge/verify/Register
flow creates the App-user; no direct user SQL or production Mailpit. Core `all` runs
its own worker only in this new fixture. The repeated flow uses password, never
another registration. TLS clients inside the proxy namespace use its fixed loopback
ports with the exact external issuer hostname/SNI; this is isolated-fixture TLS,
not a claim of a public route or browser/global trust qualification.

Core's `ci-oidc-proof` subprocess uses the existing locked coreos/go-oidc+oauth2:
Basic/S256/RS256+kid/exact issuer/singleaud/nonce/exp/at_hash and private server-sub
equality. It is limited to this fixture hostname/callback, one discovery/token/keys
request each, normal exclusive CA and bounded IO. It has no DB/session creation.
The parent precreates it without starting before consent, then independently guards
local state300/code60 on both clocks around IPC; Go keeps its own original40s and
UTC ceilings. Insufficient full50 parent budget sends no token request. No monotonic
clock is passed between processes; no retry or renewal.

The trusted owner source fixes exactly these IDs (never candidate/job-selected):

1. `artifact-identity`: actual same Core/Console image IDs and manifest provenance.
2. `fresh-fixture-schema-and-privileges`: new DB/role/migration plus read-only runtime
   schema/privilege check using the actual candidate.
3. `runtime-configuration-and-hardening`: actual uid/gid10001, RO/capdrop/NNP, own
   network/credential mounts; real Docker env-file quote/space canary (no real secret).
4. `normal-tls-health-and-static-assets`: actual HTTPS health/UI/referenced assets.
5. `guest-and-authorization-boundaries`: anonymous401 and wrong-Origin403.
6. `management-password-mfa-logout`: new owner wrong-password negative, real password+
   fresh TOTP/BFF login, cookie/CSRF/logout/old-cookie401.
7. `ordinary-app-oidc-pkce-lifecycle`: official new email verification/Register, server
   readback, real library-verified code twice with stable private subject, replay
   rejection and revoked IdP-cookie401; App cookie is not a management session.
8. `operator-password-and-session-revocation`: only a new CI admin target; reject
   admin resetting owner/wrong actor password/stale revoked proof, change password,
   revoke both target sessions, keep owner session/role/status and original MFA.
9. `owned-test-io-and-session-closure`: actual owned session logout/401 and exact known
   fixture CID/nonforce stop/rm/volume/network removal. Not global OS absence.

A pass destroys the ephemeral test fixture; same immutable images/archive remain
for production review. Unknown failure retains uncertain resources/intent, no retry
or blind cleanup; safe originals identify the stage for owner resolution.
Resource caps: MySQL768MiB/buffer192MiB/32connections; Core384MiB, Console256MiB,
Valkey128MiB/max64MiB, proxy192MiB, one transient helper256MiB; CPU/PIDs bounded.
No concurrent Go build is assumed. These are caps, **not measured peak/headroom**;
actual combined3GiB fit remains a required qualification.

Full receipt exact nine fields:
`schema,candidate,result,project,scope,checks,check_evidence,started_at,finished_at`.
Schema `authova-test-deployment/v1`, result PASS. `checks` has exactly nine IDs above,
each string PASS. Each evidence object has exactly
`result,exit_code,known_return,timely,started_at,finished_at,evidence_sha256`:
actual CLI sequences/HTTP actor zero returns, known/timely/selected IO closure,
ordered finite original time, and SHA of its **safe actual check record**, not a
subject/credential/token/private-body hash. Missing/failed/skipped/unknown cannot pass.
Worker still independently checks original owner exit0, exact result and native
records, actual test pipeline/workflow success, then binds canonical whole receipt
in production's immutable ledger. A receipt validator or file is not an execution.

## Source validation / remaining activation

`python3 -B -m unittest discover -s scripts/tests -p 'test_ci*.py'` tests real OWN
files/flock/fsync/subprocess byte pipes and controlled actors (17 targeted tests). It does not run Docker,
TLS/SMTP/product HTTP/Go/DB or the nine real acceptance gates. `gofmt` and the existing
boundary check are separate source checks. The Go policy unit belongs to
`go test -run '^TestCIOIDCPolicyBeforeNetwork$' ./cmd/authova` with the required
Go1.27.4. Local PATH Go1.27.1 refuses before compilation; no tempmod/downgrade/download.
Use the fixed qualified CI tool image for actual compile and full gates.

Worker hookup/real API signer, complete checks/build/packaging, installed owner
execution, combined resource fit, same-image test and separately human-reviewed
production are still actual acceptance stages. No actual key, signer installation,
5ops, product request or service action was performed for this source handoff.


### Static entry consumption

CI's normal-TLS/static gate and production's read-only acceptance share the same
HTML attribute consumer. It accepts only `/assets/<filename>` or
`./assets/<filename>` at the Console root, deduplicates the canonical root paths,
and actually requests every selected asset. Absolute entries remain supported.
External script/stylesheet URLs, base-href overrides, ambiguous attributes,
traversal, encoded escapes, query/fragment and nested asset paths refuse. A page
without assets still refuses in both gates. Transport/CA/SNI/header/body/timing
checks and production's anonymous `/bff/me`401 check are unchanged. This is asset
output consumption, not tracked-source normalization or a skipped static gate.

### Official ordinary-user forms and failed HTTP observation

The actor keeps the canonical `/a/<app_key>/ui/flows/<id>` URL separately from
the last response URL. Official form POSTs render200 at their action URL; the
next action must still match exactly one form rooted at that same flow. Register,
verify, password and explicit consent use this rule, current CSRF and the same
browser cookies. No automatic consent, account shortcut or relaxed code proof.

On failure the private actor emits only exact five fields
`result,code,checkpoint,status,ordinal`. Result/code retain FAIL/CI_HTTP_ASSERTION
or UNKNOWN/CI_HTTP_OUTCOME_UNKNOWN. `checkpoint` is one of the source-fixed
`HTTP_CHECKPOINTS`; status is an HTTP100..599 integer or null, ordinal0..256 is
the actor-local request number. No exception text, URL, private context or body
is projected. The held owner validates this closed frame and, only within its
original guard, writes `http-failure.json` with schema
`authova-ci-http-failure/v1`, closed stage and observation. A reporting failure
remains secondary to the first actor failure. The original outer six-field
failure frame, native records and nine PASS requirements are unchanged.

Test17's recorded failure has no such inner observation and remains unknown at
the endpoint/status level. Its five services plus extra `oidc-a` proof ACK need
their own complete fresh ownership CAS before any known recovery; the earlier
test16 five-service cleanup is not sufficient. This host-only repair does not
rebuild or adopt images, replay17 or authorize production.

Ordinary-user readback sends the complete new recipient as the exact `q` to
the App-scoped user endpoint. The source performs an identifier-digest lookup
and then masks email; the actor requires the exact expected mask, not plaintext
or a wildcard search. It requires one result and a null cursor, binds its ID to
the same App, then reads only that ID's official detail to check the actual
`admin_role` (the list does not populate it). Both responses must have the exact
App, active status and masked address; non-string/missing identity fields,
privileged roles, ambiguity, a different detail ID or changed repeat subject
refuse. Repeat subject mismatch stops before the detail GET. This adds one
bounded read per readback within the original actor cap, no new command or
budget. Email identity is bound by the exact server lookup, not by treating a
non-unique mask as identity. Test18's historical inner assertion remains
unrecorded; this source repair does not claim registration or real OIDC PASS.

### Production Docker image-store representation

The signed candidate and test receipt retain the two OCI index root IDs. The
guarded archive consumer also returns each root's unique Linux/arm64 manifest,
config digest, hashed layer descriptors, rootfs diffIDs and four provenance
labels. This map is returned only after the complete archive/descriptor/member
checks and original FD/close checks succeed. No extraction or repackaging.

Only the production owner may derive runtime config IDs from that graph. It
binds the whole original candidate and role/provenance, revalidates the complete
same graph before the single image load, then uses the unchanged exact-ID
`image()` guard on the derived config ID plus exact runtime RootFS diffIDs. It
does not try tags, alternate IDs or root-inspect fallbacks. CI-test root IDs and
the generic exact-ID guard remain unchanged.

Production Compose overrides/hashes, schema probe, poststate/semantic checks
and published active `images` all use these derived runtime IDs. Public
`ci_candidate` keeps the signed roots; `ci_runtime_image_graph` records their
verified relationship to runtime IDs. Rollback still uses the exact original
container image IDs and layers. Original21's consumed intent/nonce/failure are
not reused. This host-only fix neither rebuilds nor substitutes either tested
image; actual promotion needs a new native record, admission and nonce.

Production retains each current container's non-Compose/non-provenance labels
in `candidate-private-override.json` (0600 in the0700 private operation root).
It never belongs to safe artifacts and must not be collected, printed or
content-hashed. Its whole identity/bytes are held in the existing RAM private
input CAS, including before/after Compose consumption. Explicit retained Compose
labels must agree with current labels. Only the four existing image-provenance
labels take their bound candidate values.

The complete prospective Compose configuration must equal the old configuration
with just the selected images, assets path and these exact retained labels.
The candidate's effective labels are still compared with the original labels;
an introduced image label that cannot be removed still refuses. Post-switch
whole runtime semantics remain unchanged. Env is not adjusted or relaxed.
This preserves runtime metadata across differing image defaults rather than
ignoring label differences. The specific origin/keys/values of22's observed
LABEL_SEMANTICS_CHANGED remain unrecorded; no historical cause is inferred.

### Resolved Compose service hashes

Both forward and rollback hashes consume the exact bounded RAM bytes returned
by the same pinned Compose `config --format json` invocation. The parsed whole
model is still compared; it is never serialized back for hashing. In particular,
literal `$$`, whitespace and key order in the tool output are not rewritten.
An unresolved `env_file`, include/extends/label_file or wrong project model refuses.
`label_file` is rejected in the retained layer before the first Compose call:
Compose can consume and erase that field while rendering, so an output-only
check cannot establish its source ownership. Each role is hashed by
that same physical Compose, with only these fixed arguments and stdin bytes:

```
--project-name <binding.project> --project-directory <first-layer.parent>
--env-file /dev/null -f - config --hash <core|console>
```

The producer output/stdin/hash stay private RAM, with original input identity
and bytes CAS before/after rendering and each hash. No private frame or service
hash is included in a safe record. Existing Native limits/close/first error and
numeric-only records apply. This removes direct layer `config --hash` calls;
it does not remove any field from prospective or post-runtime comparison.

Forward reuses the existing prospective render (no new CLI). Rollback uses all
original binding layers plus only its old-image override, never the candidate
override. That new override is also in private input CAS. Its resolved whole
model must equal the old model with only the two exact old image IDs substituted.
The extra rollback render costs one native whole30, not a free FS operation.
Rollback admits its complete native suffix before any read/write/CLI:
4 current inspect commands120 + base render30 + rollback render30 + two hashes60
+ selective rollback120 + 4 restored inspect commands120 = **480 seconds**,
plus20 for FS/report. The rollback up itself reserves140 for restored reads/report.
The original production1800 dual-clock total, command30/body25/close5 and up120
remain unchanged. The earlier forward up's600 suffix is unchanged; elapsed
acceptance can still leave insufficient rollback budget, which refuses rather
than renewing the operation. Neither cold fit nor recovery of23 is established.

The independently recorded producer/actual hash diagnosis is
`cicd/evidence/20261120-authova-current23-compose-hash-diagnosis.json`, SHA256
`1df81d0e047d4d8cde45702bb926a08c5d35a2483d224dccf2722a43f37467f4`.
Its resolved hashes match both current labels; original23's specific failing
field and historical before remain unrecorded. This source fix never dispatches
against those current containers or publishes their control.
