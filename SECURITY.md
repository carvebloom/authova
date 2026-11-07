# Security policy

This checkout is an unreleased production candidate. No independently audited production release exists yet. Supported versions and security response ownership will be published with the first release.

Do not disclose live passwords, tokens, OTPs, private keys, database contents or tenant data in public issues. Send a minimal reproducer through the deployment's private incident channel or the maintainer's private vulnerability reporting channel when configured.

The release gate includes dependency scanning, a locked SBOM, license/notice review, tenant isolation and authentication attack tests, database restore, operational fault tests and independent security review. Library certification does not certify Authova.

If a signing key or deployment master key is exposed, restrict traffic, preserve redacted evidence and follow the documented recovery procedure. Offline JWT consumers retain a validity window until expiration; use online introspection where immediate invalidation is required.
