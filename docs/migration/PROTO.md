# Proto contracts and HTTP compatibility

`api/authova/v1/{management,query}.proto` with `google.api.http` is the single
public management/query contract authority. All 51 management and 12 read-only
query operations execute through generated Kratos HTTP bindings and typed service
adapters. The old regular management/Agent/operator dispatchers were removed.
Fallbacks only preserve historical URL spellings and unsupported-verb admission;
they share the same application/authorization path. Standard OAuth/OIDC,
WebAuthn ceremonies and browser pages keep their mature protocol handlers. MCP
stdio remains an independent entry point.

Run `python3 scripts/generate-api.py --install-tools` once with protoc 36.2, then
`make api` / `make api-check`. Pinned generators: protoc-gen-go 1.36.11,
protoc-gen-go-http v3.0.0-20261026152050-dd0fb7df1e32, oapi-codegen 2.8.0,
openapi-typescript 7.10.1. Reference gnostic 0.7.1 was verified; its default
projection loses existing OpenAPI 3.1.2 constraints, so `cmd/proto-openapi`
projects descriptors and Authova options instead. Old Python OpenAPI authoring
scripts were removed. Committed Go HTTP/messages, OpenAPI, Go clients, TS types
and Console inputs are generated; builds need no generator installation.
Historical draft OpenAPI files are not another authority.

Options carry response/security/constraint metadata; structural types and paths
come from descriptors. Enum values remain strings, with constraints enforced by
existing application validation. Five historical writes already returned JSON
objects but their old OpenAPI omitted the schemas. Their actual responses are
now modeled additively: user disable (`disabled`), system management client
provision (`client_id`), signing-key rotation (`key_id`), administrator assignment
(`user_id`, `role`) and operator update (`updated`). `proto-baseline.json` checks
that the query projection is unchanged and management changes consist of these
five documented outputs. HTTP payloads/statuses are preserved. Undocumented
legacy decoder fields are explicitly marked and excluded from public OpenAPI.

`internal/protojsoncompat` preserves numeric int64 above 2^53, explicit false,
absent/empty/null lists and nullable cursors. Pointer-specific `null_clears`
metadata and ordered JSON parsing reproduce legacy duplicate-field/null/object
merge behavior. List wrapper messages keep PATCH array presence. Unknown body
fields, quoted integers, overflow, invalid types and trailing JSON are rejected.
Management query defaults/unknown-parameter handling and strict read-query
unknown/duplicate rejection keep their previous behavior. Decode errors are
deferred until the original authorization stage, preserving status precedence.
Default protobuf JSON coercion is not used on these routes.

HTTP output is produced only after transaction completion. Tests hold a real
App write before commit and assert no response escapes; rollback/unknown commit
return 503 and perform no automatic replay. Authentication failure counters and
refresh-reuse revocations retain their explicit commit-on-rejection semantics.
The 204 encoder explicitly flushes Kratos' underlying response writer: its
wrapper otherwise buffers WriteHeader until a body is written, incorrectly
sending 200 for an empty response. Logout and identity-revoke regressions cover
this behavior on both databases.

Trailing-slash aliases are resolved without redirects or POST body loss. Both
Kratos transport layers disable the default one-second timeout; Authova's own
request budget controls admission, avoiding a race that changed DB queue 429 to
503. Focused real dual-database admission and HTTP compatibility tests cover
these cases.

Public Go clients and TS fetch clients import no server internals and perform no
automatic write retries. One-time secrets are modeled only in their historical
write responses, including existing creator-bound idempotency behavior. Console
retains its independent BFF, encrypted server sessions, CSRF and current per-request
authority checks. Compiled query CLI/MCP and SDK composition are tested against
real Core instances on both databases. See VALIDATION.md for current results and
remaining acceptance limits.
