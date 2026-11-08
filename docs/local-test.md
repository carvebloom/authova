# Independent local test environment

Run from any working directory (Docker, Python 3 and OpenSSL required):

```sh
/Users/chenhuanxiang/Workbench/authova/scripts/local-test.sh init
/Users/chenhuanxiang/Workbench/authova/scripts/local-test.sh up
/Users/chenhuanxiang/Workbench/authova/scripts/local-test.sh smoke
/Users/chenhuanxiang/Workbench/authova/scripts/local-test.sh status
```

In another checkout use that checkout's absolute script path. The script selects
`docker compose`, falling back to standalone `docker-compose` (verified v5.1.0).
`up` builds both images from this checkout and leaves the environment running.
`restart` restarts only this project's running services and waits for readiness.
`down` removes this project's containers/network, **preserving all named volumes**.
It never uses `down -v`. No existing migration/demo/personal resources are reused.
Default Compose project: `authova-test`; `COMPOSE_PROJECT_NAME` overrides it.
Do not use the names `authova` or `authova-migration` for this environment.
A project override creates separate volumes; it shares this checkout's private
fixture directory. Use a new ignored absolute `AUTHOVA_LOCAL_TEST_STATE_DIR` (or a separate checkout) for independent keys/accounts too.

## Ports and services

| Service | Internal port | Default host binding | Readiness |
| --- | --- | --- | --- |
| core | 8080 | 127.0.0.1:42100 | /readyz, /healthz |
| console | 8090 | 127.0.0.1:42101 | /healthz |
| mailpit | SMTP 1025, UI 8025 | UI 127.0.0.1:42102 | /api/v1/messages |
| mysql | 3306 | none | mysqladmin ping |
| valkey | TLS 6379 | none | authenticated mTLS PING |
| worker | none | none | independent worker process |

`roles`, `migrate`, `bootstrap-check`, `bootstrap` are one-shot setup services;
exit 0 is expected. `bootstrap-check` reads the migrated database before deciding
whether bootstrap is needed, rather than trusting a stale local marker.
Migration initializes the current unpublished **v1** baseline, `001_initial.sql`,
including delivery diagnostics; no historical v8
metadata is adopted or renumbered. Core/Worker use a restricted MySQL DML-only role (no global/DDL/grant privileges). PostgreSQL compatibility retains NOSUPERUSER/NOBYPASSRLS and FORCE RLS. Only migration/setup receive elevated database credentials.

Existing pre-consolidation Lab v1 databases have a different 001 checksum. New binaries
will reject them; this work does not rebuild, renumber or migrate those instances.
Use a separately reviewed instance/data plan before switching existing services.

Private files are in `.local-test/`, ignored by Git AND Docker build context.
Directories use 0700, files 0600. `init` preserves existing keys/config/passwords;
partial TLS issuance fails rather than replacing an existing key. Containers
using bind-mounted secrets run with the generating user's UID/GID, without root,
with a read-only root filesystem. MySQL/Mailpit retain their own volume
ownership. TLS fixtures expire after 365 days; renewal requires deliberate local
maintenance, not automatic key replacement. Never share this disposable PKI.

The fresh synthetic owner/password/TOTP seed is in
`.local-test/bootstrap/bootstrap.json` (inspect locally; do not post its contents).
Console supports this operator login. Smoke generates the current TOTP in memory,
creates/reuses a synthetic App, restarts MySQL/Valkey/Core/Worker/Console,
then verifies the same App and management session remain usable. It also tests
Console password/TOTP login, encrypted Cookie session, unauthorized requests,
CSRF rejection and successful CSRF logout, Mailpit, and v1 version metadata.
No credentials/tokens are printed. Evidence: `.local-test/last-smoke.json`.
Valkey intentionally has no disk persistence: restarting it invalidates Console
sessions and rate counters; smoke logs in again after restart. Product records
and Core management sessions persist in MySQL. Mailpit messages persist in
its own volume. Never delete keys independently of their encrypted database.

## Environment and external HTTPS integration

Edit `.local-test/.env`, initially populated from
[env.example](../deploy/local-test/env.example); shell variables override it:

- `CORE_PORT`, `CONSOLE_PORT`, `MAILPIT_PORT`: loopback host ports.
- `AUTHOVA_MODE`: defaults to `development` for independent local HTTP.
- `AUTHOVA_BASE_URL`: **browser-visible Core origin**, used to construct App
  issuers and protocol URLs. Configure before creating Apps; changing it is not
  an issuer migration for existing identities/clients.
- `AUTHOVA_CONSOLE_URL`: exact browser Console origin; Core trusted-origin checks
  and BFF Origin/CSRF validation use this value. No wildcard or disabled checks.
- `AUTHOVA_CONSOLE_CORE_URL`: BFF-to-Core origin (default `http://core:8080`),
  injected as the BFF's `AUTHOVA_BASE_URL` separately from Core's public origin.
- `SSL_CERT_FILE`: PEM trust-bundle path **inside Core and Console**. Default
  system CA bundle. An integration overlay must mount a combined system + local
  HTTPS CA bundle read-only into both services (and Worker if needed), and set
  this variable. This is independent of Valkey's generated mTLS CA.

No extra private mount or external CA is needed for the default HTTP setup.
For HTTPS, the external integration owns certificates, private mounts, DNS and
network aliases. The browser-visible issuer hostname must resolve and connect
from both browsers and containers. An overlay can replace the container-side
Core URL with the HTTPS URL; production BFF **requires HTTPS**, even internally.
Set `AUTHOVA_MODE=production` only with canonical HTTPS Core/Console origins and
that HTTPS BFF upstream, correct trusted CA and routing. Secure cookies follow
production mode; no verification is weakened for proxy use. Valkey already uses
verified mTLS, default user disabled, separate minimal Core/Console/health ACLs.
The Console never receives Core DB credentials, master key, owner credentials,
or the Core's Valkey credential. No shared-network/domain/proxy dependency is
present in the base Compose file.

The stock smoke uses loopback HTTP and is intended for the base development
configuration; HTTPS integration needs its own external URL/browser smoke.
Default services publish no database/cache ports. If debugging requires a host
DB port, use a separate project-owned optional overlay/profile with a loopback
binding; no such port is enabled here. `docker-compose --project-name authova-test
--env-file .local-test/.env -f compose.test.yaml config --quiet` validates config;
avoid printing full config because it includes generated database passwords.

MySQL-first policy and explicit PostgreSQL compatibility: [mapping](mysql-first.md).
Existing `.env` without `LOCAL_TEST_DATABASE_DRIVER` is never rewritten: explicitly
set `AUTHOVA_LOCAL_TEST_DATABASE_DRIVER=postgres` to select the preserved
`compose.test.postgres.yaml`. New MySQL fixtures must use a separate state directory
(`AUTHOVA_LOCAL_TEST_STATE_DIR`) and unique `COMPOSE_PROJECT_NAME`. Do not switch
an existing engine marker. Both Compose files preserve Console database/key isolation.
