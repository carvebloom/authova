# Current23 RAM materializer support contract

This is a bounded incident-specific consumer, not a release platform or installed
owner replacement. `scripts/ci/current_reconciliation.py:evaluate(materials)` does
no IO/CLI/HTTP/SQL, has no entry point, and never publishes or authorizes anything.
Its independent single-file pin is `scripts/ci/current-reconciliation-pins.json`.
The existing11 installed files/pins and application product remain unchanged.

## Evidence and authority

The fixed target IDs/configs/provenance are from the public23 failure receipt and
the previously sealed build12 graph. Rootfs diffIDs are literal source data from
`cicd/evidence/20261120-authova-production21-sealed-runtime-graph.json`, SHA256
`a85fb3b36d67760ee46a43cc93a5f908b92fd3af2ef6303c6b099b3afb609817`.
The current containers are NOT used to construct their own expected values.

The host producer reference is Authova commit
`efa0db11ad16b68ff767af3f8f1f585f1f39b05b`: `env_map`, physical tool/daemon pins,
selected image provenance, fixed layer order, original Native fields and clocks,
private `_token`/read constraints. These establish the supported material rules;
they do not establish vendor defaults or authentic runtime observations by themselves.

Public missing-authority audit:
`cicd/evidence/20261120-authova-production23-current-reconciliation-authority-plan.json`
SHA256 `30132875fb092599b9afc6c1802d0e7e0eb73259996dd00c76e4fdbc6878afaf`.
The platform confirmed the following are **NOT_RECORDED**:

- Exact physical Compose5.1.0 SHA817dde… to source commit/patch and compose-go/
  Engine-SDK dependency lock. A version string is insufficient.
- Application daemon e5f1dd… Engine version, API and source commit. Client29.2.1
  and the CI daemon06b356… cannot substitute for them.
- Independently retained external network/volume identities before23. Current
  names/IDs from inspection cannot qualify their own historical continuity.

No input field accepts an authority flag, default table, expected projection or
replacement scope. Missing producer branches are source-fixed NOT_ESTABLISHED;
setting an unused declaration constant is not qualification. Closing them needs
source-bound producer evidence and a reviewed implementation, not a job boolean.

## Private function input (not a public JSON upload)

Exact top-level keys: `schema,pre,post,images,compose,compose_hashes,layers,transport`.
Schema is `authova-current23-materials/v1`. `pre/post/images/compose_hashes` have
exact roles `core,console`; objects come from the fixed10 read transport. No
caller may replace the original inspected objects with normalized projections.

- `pre/post`: RAM Docker inspect objects for the exact two new23 CIDs, not old
  before23. Compare current/current Config, HostConfig, Mounts and NetworkSettings
  whole, plus IDs/image and running/healthy state.
- `images`: RAM inspect of the two sealed runtime config IDs, never an arbitrary
  image/tag. Identity/provenance/RootFS check uses fixed public source constants.
- `compose`: full fixed physical Compose rendered JSON (`name/services` required)
  for precisely the retained3 layers plus23's private override.
- `compose_hashes`: two original command responses parsed to role hashes in RAM.
  They are never returned, persisted or hashed again.
- `layers`: exact ordered four absolute paths. The last is derived from the
  original23 nonce prefix under the fixed production operations root.

Exact transport keys:
`daemon,docker_sha256,compose_sha256,operation_nonce,clock,native,private_pre,private_post,control_pre,control_post`.

`clock` is exact `I,M,end_I,end_M`, original finite dual-clock samples. `native`
is10 ordered `{role,record}` rows with roles/tags exactly:
`DAEMON,CORE_PRE,CONSOLE_PRE,CORE_IMAGE,CONSOLE_IMAGE,COMPOSE,CORE_HASH,CONSOLE_HASH,CORE_POST,CONSOLE_POST`.
`record` preserves original Native keys:
`tag,exit_code,known_return,timely,all_selected_IO_closed,started_at,original_monotonic,elapsed_monotonic,whole_seconds,finished_at`.
All returns must be known0/timely/IOclosed, whole30, within original480 and work390
ceilings. Each command start must fit every remaining whole30 plus workFS60.
There is no clock reset, minclip, retry or observed elapsed replacing whole.

`private_pre/private_post` have exact roles
`compose-base,password-override,ui-override,release23-override,core-env,console-env,migrator-env`.
Each row is `{identity,body}`: the original9-integer `_token` list
`dev,ino,mode,nlink,uid,gid,size,mtime_ns,ctime_ns` and RAM bytes (<=1MiB).
Regular0600/owner501/singlelink/size must match; complete rows compare equal.
These do NOT replace the transport's actual held-FD/endpath/allclose checks.
`control_pre/post` are bytes of the **fixed public stale control only**, <=64KiB,
equal and matching its source-fixed ec2022 digest. This public-control check is
the only hash operation; no private body/value digest is computed or exported.

Only the authenticated private transport may call this function, after verifying
the real23 API review, original intent/nonce/candidate/test19 lineage, physical
tools, actual command argv and original structured returns. Pure material checks
cannot authenticate a kernel/API record's origin. A job-supplied fake native row,
even one with a valid shape, is not evidence or an admission. The platform retains
the actual-origin checks independently; nothing in this consumer lowers them.

No arbitrary object/callback is accepted. Data must be plain dict/list/scalars
and RAM bytes, depth<=32/nodes<=100000/collection<=4096/each string or bytes<=1MiB/
combined text+bytes<=16MiB; integers bounded to10^30 and floats finite. Extra
top-level/role/transport/private-row/native-row fields reject their component.

## Complete support boundaries

| Component | Checks implemented | Authority still missing |
| --- | --- | --- |
| IDENTITY_PROVENANCE | Exact new CIDs/config IDs/OSarm64/RootFS and four source-bound labels; role/project/oneoff/running/healthy | Actual origin of observations remains transport responsibility |
| COMPOSE_ORIGIN | Fixed four layers/23 nonce-derived path/project/two role hashes/config image labels | Does not prove generated labels/defaults or historical before |
| ENV | Complete explicit typed rendered env map plus image defaults; exact unique-key equality | Null/list/untyped/duplicate maps unsupported, no implicit lookup |
| LABELS | All declared image+Compose label values checked; no prefix ignored | Exhaustive generated-label producer/keyset/value derivation absent; component never MATCH |
| PROCESS | Nonempty explicit argument lists and nonempty explicit user/working_dir match exactly | Omitted/null/empty reset/string parsing and remaining Config defaults absent; component never MATCH |
| HARDENING_RESOURCES | Mandatory RO/not-privileged/no-added-cap boundary; explicit booleans and integer Memory/MemorySwap/PidsLimit/CPU share/quota/period comparisons | Units/decimal CPUs/defaults/cap handling/restart/health/full HostConfig mapping absent; unknown service keys explicitly reported; never MATCH |
| NETWORK_MOUNTS | Explicit resolved names; typed bind/volume sources/targets/RO/counts; typed fixed port bindings | Driver options, generated names/defaults, independent external IDs and historical continuity absent; never MATCH |
| INPUT_AND_CURRENT_CAS | Exact original10 native shape/timing/suffixes; fixed daemon/tool pins; RAM private-input equality; current/current whole relevant metadata and stale-control CAS | Cannot authenticate observations or recover historical before; platform must bind actual provenance |

Explicit process decision table:

| Compose value | Result |
| --- | --- |
| omitted | PROCESS_OMITTED_DEFAULT_PRODUCER_MISSING |
| null | PROCESS_NULL_DEFAULT_PRODUCER_MISSING |
| empty list | PROCESS_EMPTY_RESET_PRODUCER_MISSING |
| any string (including empty) | PROCESS_STRING_PARSER_UNSUPPORTED |
| list containing non-string | PROCESS_ARGUMENT_TYPE_UNSUPPORTED |
| nonempty string list | Compare exact current Cmd/Entrypoint; still no complete-process MATCH |

Every component returns exact `{result,mismatch,missing_authority}` with source
literal enums only, never free field names, paths, values, counts or exceptions.
Overall result is CURRENT_INTENT_MISMATCH or NOT_ESTABLISHED. **There is no all
green/CURRENT_RECOVERY path with present authorities.** Missing shape and unsafe
data return explicit missing codes; no observed-as-expected, generic normalizer,
caller-provided expected or suppressed extra-field default can fill a gap.

## Historical identity decision and actual-state safety

Full before23 is irrecoverable from existing safe artifacts. Even if declared
names/mounts match current configuration, original external NetworkID and volume
identity continuity remain NOT_RECORDED. If safety requires that continuity,
the exact decision point is either an independent pre23 binding tied to this
project/retained layers, or explicit acceptance of a new current baseline without
that historical guarantee. This consumer grants neither and does not waive it.

Current services stay online. Do not replay23, invoke post_semantics with a fake
before, up/rollback, sign another promotion or publish control from these partial
results. Preserve stale control and23FAIL. The separate independent HTTP PASS
does not close any of the missing authorities above.
