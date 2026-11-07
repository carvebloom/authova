# Contributing to Authova

Authova uses Apache-2.0. Contributions must be licensed under Apache-2.0; do not copy code with incompatible obligations. Keep changes focused, explain behavior and migration/API compatibility, and add tests for security or state transitions.

Run the repository's check targets before submitting. A passing unit test does not replace real database, protocol, browser or performance evidence. Include the commands and environment for claims you make.

Use idiomatic Go and strict TypeScript. Keep SQL and driver errors out of domain code; bind every App-owned operation to a verified scope. Public DTOs, secret serialization and transaction ownership must be reviewed. Never log passwords, OTPs, tokens, private keys or provider secrets. Unknown dependencies and unknown commit outcomes must remain explicit failures.

Report vulnerabilities privately to the deployment owner or project maintainer. Before public release the maintainer must enable private vulnerability reporting on the repository. Do not put exploit credentials or private data in public issues.

Public API changes originate in authova-contracts; implementation and Console changes include compatibility checks. Use forward-compatible migrations with an explicit maintenance/rollback plan. Benchmark results include hardware, dataset, distribution, request type, duration, errors and percentiles.
