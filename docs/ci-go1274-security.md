# Go 1.27.4 security patch — PR #1

## Confirmed failure

Producer `f3560d5b3d90f14ed9ab3085f0b6ab427fdfd5a2`, PR run
43333949598, checks job 131585273754: setup, pinned dependency preparation,
`make check` and `make test-discover test-python` succeeded. The unchanged
`go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` completed its analysis
and reported one reachable standard-library vulnerability, **GO-2026-6859 /
CVE-2026-97054**, in `net/http@go1.27.3`, fixed in Go 1.27.4. Scanner exit was
3, propagated by `go run` as job step exit 1. Staticcheck/build downstream were
SKIPPED and are not recorded as passed. Three additional module-level findings
were not called by this producer; this is not a claim that all dependencies are
free of vulnerabilities.

Official sources:

- [Go vulnerability database](https://vuln.go.dev/ID/GO-2026-6859.json)
- [Official advisory source](https://github.com/golang/vulndb/blob/master/data/osv/GO-2026-6859.json)
- [Go security fix](https://go.dev/cl/862185)
- [Go 1.27.4 release](https://go.dev/doc/devel/release#go1.27.4)

The advisory concerns unsynchronized HTTP/1 response header mutation after a
MaxBytesReader limit is exceeded while the handler writes headers concurrently.
This is a real standard-library reachability finding, not the historical scanner
SSA panic and not a network failure. No remote exploit was attempted or proved.
The failed job's original log is retained privately under `.local-test/`;
SHA256 `946de575181a510f5e82214984334c21722b24253ae1e13a8125ffad4403e66f`,
62373 bytes. An initial log-download query failed; the official REST job-log
redirect subsequently yielded the original log. Log retrieval failure is not the
application failure cause, and raw job logs are not published.

## Narrow fix and validation boundaries

Raise the module/build minimum from 1.27.3 to 1.27.4. All CI setup-go jobs already
read `go.mod`; both Core and Console Docker build stages now use the official
`golang:1.27.4-alpine` manifest index pinned to
`sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf`.
The Docker Hub registry index response (9019 bytes) was SHA256-verified and lists
linux/amd64 and linux/arm64. Only registry metadata was fetched: no OCI layers,
image build, container or running deployment was operated. Local cached-toolchain
paths, target metadata and current build requirements follow the same patch.
Historical producer/toolchain/validation documents remain unchanged.

No third-party dependency, go.sum entry, protocol/API, authentication, transaction,
migration or test assertion changes. Keep govulncheck v1.9.0, its full `./...`
scope, native nonzero failure and every other CI gate. Rebuilding is required for
binaries to receive the patched standard library; existing services/images have
not been upgraded by this source change.

Local checks for this patch are static pin/manifest consistency, Python AST,
POSIX shell syntax and git whitespace only. Full vulnerability scan, Go
race/vet/build, Node/generation and database compatibility for Go 1.27.4 are
**NOT_RUN locally** under the shared resource restriction; the new push/PR CI is
the authorized execution path and its actual result must be reported separately.
Earlier producer successes and the failed f3560d5 job remain historical facts.
