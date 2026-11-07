#!/usr/bin/env python3
"""Own only this checkout's local-test resources. Never print authentication material."""
import base64
import hashlib
import hmac
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / '.local-test'
os.umask(0o077)


def write_once(path, data):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if not path.exists():
        with path.open('xb') as f:
            f.write(data.encode() if isinstance(data, str) else data)
    path.chmod(0o600)


def init():
    STATE.mkdir(mode=0o700, exist_ok=True)
    STATE.chmod(0o700)
    for d in ['pki', 'core', 'console', 'valkey', 'bootstrap']:
        (STATE / d).mkdir(mode=0o700, exist_ok=True)
    defaults = (ROOT / 'deploy/local-test/env.example').read_text()
    write_once(STATE / '.env', defaults + '\nLOCAL_UID=' + str(os.getuid()) + '\nLOCAL_GID=' + str(os.getgid()) +
               '\nDB_MIGRATOR_PASSWORD=' + secrets.token_hex(24) + '\nDB_RUNTIME_PASSWORD=' + secrets.token_hex(24) + '\n')
    settings = env_values()
    write_once(STATE / 'core/master.key', secrets.token_bytes(32))
    write_once(STATE / 'console/session.key', secrets.token_bytes(32))
    write_once(STATE / 'bootstrap/bootstrap.json', json.dumps(dict(Email='owner@local-test.example.invalid',
               Password=secrets.token_urlsafe(32), totp_secret=base64.b32encode(secrets.token_bytes(20)).decode().rstrip('='))))
    # No ALTER PASSWORD: repeated init/up must not replace existing database credentials.
    password = settings['DB_RUNTIME_PASSWORD']
    if not re.fullmatch('[a-f0-9]{48}', password):
        raise RuntimeError('Keep the generated hexadecimal runtime password')
    write_once(STATE / 'roles.sql', "DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='authova_runtime') THEN "
               "CREATE ROLE authova_runtime LOGIN PASSWORD '" + password + "' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT; END IF; END $$;\n"
               "GRANT CONNECT ON DATABASE authova_test TO authova_runtime; GRANT USAGE ON SCHEMA public TO authova_runtime;\n"
               "ALTER DEFAULT PRIVILEGES FOR ROLE authova_migrator IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;\n")
    pki = STATE / 'pki'

    def openssl(*args):
        with (STATE / 'pki.log').open('ab') as log:
            subprocess.run(['openssl', *args], cwd=pki, stdout=log, stderr=log, check=True)

    if (pki / 'ca.key').exists() and not (pki / 'ca.crt').exists():
        raise RuntimeError('Incomplete CA fixture; restore it without replacing the existing key')
    if not (pki / 'ca.crt').exists():
        openssl('req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'ca.key', '-out', 'ca.crt',
                '-days', '365', '-subj', '/CN=Authova local-test Valkey CA')
    acl = ['user default reset off']
    for role in ['server', 'core', 'console', 'health']:
        if (pki / (role + '.key')).exists() and not (pki / (role + '.crt')).exists():
            raise RuntimeError('Incomplete TLS fixture; restore certificate for existing ' + role + ' key')
        if not (pki / (role + '.crt')).exists():
            write_once(pki / (role + '.ext'), 'basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\n' +
                       ('extendedKeyUsage=serverAuth\nsubjectAltName=DNS:valkey,DNS:localhost,IP:127.0.0.1\n' if role == 'server'
                        else 'extendedKeyUsage=clientAuth\n'))
            openssl('req', '-new', '-newkey', 'rsa:2048', '-nodes', '-keyout', role + '.key', '-out', role + '.csr', '-subj', '/CN=Authova ' + role)
            openssl('x509', '-req', '-in', role + '.csr', '-CA', 'ca.crt', '-CAkey', 'ca.key', '-CAcreateserial',
                    '-out', role + '.crt', '-days', '365', '-extfile', role + '.ext')
        if role == 'server':
            continue
        write_once(pki / (role + '.password'), secrets.token_urlsafe(32) + '\n')
        password = (pki / (role + '.password')).read_text().strip()
        commands = {'core': '~authova:limit:* +eval +incr +pexpire',
                    'console': '~authova:console:* +get +getdel +set +del +eval', 'health': '+ping'}[role]
        acl.append('user authova_' + role + ' reset on #' + hashlib.sha256(password.encode()).hexdigest() +
                   ' +hello +ping +client|setinfo +cluster|shards +cluster|slots ' + commands)
        target = STATE / ('valkey' if role == 'health' else role)
        for name in ['ca.crt', role + '.crt', role + '.key', role + '.password']:
            write_once(target / name, (pki / name).read_bytes())
    for name in ['ca.crt', 'server.crt', 'server.key']:
        write_once(STATE / 'valkey' / name, (pki / name).read_bytes())
    write_once(STATE / 'valkey/users.acl', '\n'.join(acl) + '\n')
    write_once(STATE / 'valkey/valkey.conf', 'port 0\ntls-port 6379\ntls-cert-file /tls/server.crt\n'
               'tls-key-file /tls/server.key\ntls-ca-cert-file /tls/ca.crt\ntls-auth-clients yes\n'
               'tls-protocols "TLSv1.2 TLSv1.3"\naclfile /tls/users.acl\nsave ""\nappendonly no\ndir /data\n')
    for path in STATE.rglob('*'):
        path.chmod(0o700 if path.is_dir() else 0o600)
    print('Local-test configuration initialized; existing keys and credentials preserved.')


def env_values():
    return dict(line.split('=', 1) for line in (STATE / '.env').read_text().splitlines() if line and not line.startswith('#'))


def compose(*args, capture=False):
    cli = ['docker', 'compose'] if subprocess.run(['docker', 'compose', 'version'], capture_output=True).returncode == 0 else ['docker-compose']
    project = os.environ.get('COMPOSE_PROJECT_NAME', 'authova-test')
    if project in ['authova', 'authova-migration'] or not re.fullmatch('[a-z0-9][a-z0-9_-]+', project):
        raise RuntimeError('Choose an independent local-test project name')
    p = subprocess.run(cli + ['--project-name', project, '--env-file', str(STATE / '.env'), '-f', str(ROOT / 'compose.test.yaml'), *args],
                       cwd=ROOT, check=True, capture_output=capture, text=True)
    return p.stdout if capture else None


def wait_ready():
    env = env_values() | os.environ
    for name, port, path in [('core', env['CORE_PORT'], '/readyz'), ('console', env['CONSOLE_PORT'], '/healthz')]:
        deadline = time.monotonic() + 150
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen('http://127.0.0.1:' + port + path, timeout=3) as r:
                    if r.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(1)
        else:
            raise RuntimeError(name + ' did not become ready; inspect project logs privately')
    print('Core and Console ready.')


def smoke():
    (STATE / 'last-smoke.json').unlink(missing_ok=True)
    env = env_values() | os.environ
    base = 'http://127.0.0.1:' + env['CORE_PORT']
    console = 'http://127.0.0.1:' + env['CONSOLE_PORT']
    credentials = json.loads((STATE / 'bootstrap/bootstrap.json').read_text())

    def totp():
        raw = credentials['totp_secret']
        key = base64.b32decode(raw + '=' * (-len(raw) % 8))
        digest = hmac.new(key, struct.pack('>Q', int(time.time()) // 30), hashlib.sha1).digest()
        offset = digest[-1] & 15
        return str((struct.unpack('>I', digest[offset:offset+4])[0] & 0x7fffffff) % 1000000).zfill(6)

    def request(origin, path, method='GET', body=None, token=None, opener=None, headers=None, expected=200):
        hdr = headers or {}
        if token:
            hdr['Authorization'] = 'Bearer ' + token
        if body is not None:
            hdr['Content-Type'] = 'application/json'
        req = urllib.request.Request(origin + path, method=method, headers=hdr, data=json.dumps(body).encode() if body is not None else None)
        try:
            with (opener or urllib.request.build_opener()).open(req, timeout=15) as r:
                status, data = r.status, r.read()
        except urllib.error.HTTPError as e:
            status, data = e.code, e.read()
        if status != expected:
            raise RuntimeError('Smoke failed: ' + method + ' ' + path + ' returned ' + str(status) + ', expected ' + str(expected))
        return json.loads(data) if data and data[:1] in [b'{', b'['] else None

    print('Waiting for a fresh TOTP step (single-use protection remains enabled).', flush=True)
    time.sleep(31 - time.time() % 30)
    login = dict(email=credentials['Email'], password=credentials['Password'], totp=totp())
    token = request(base, '/api/v1/platform/sessions', 'POST', login, expected=201)['token']
    marker = STATE / 'smoke-record.json'
    if not marker.exists():
        app = request(base, '/api/v1/apps', 'POST', {'name': 'Local test synthetic persistence', 'slug': 'local-test-' + secrets.token_hex(5)}, token, expected=201)
        write_once(marker, json.dumps({'app_id': app['id']}))
    app_id = json.loads(marker.read_text())['app_id']
    request(base, '/api/v1/apps/' + app_id, token=token)
    compose('restart', 'postgres', 'valkey', 'core', 'worker', 'console', capture=True)
    wait_ready()
    request(base, '/api/v1/apps/' + app_id, token=token)
    request(console, '/', expected=200)
    browser = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    print('Waiting for the next TOTP step for Console login.', flush=True)
    time.sleep(31 - time.time() % 30)
    login['totp'] = totp()
    bff_login = request(console, '/bff/login', 'POST', login, opener=browser,
                        headers={'Origin': env['AUTHOVA_CONSOLE_URL']})
    request(console, '/bff/me', opener=browser)
    request(console, '/bff/logout', 'POST', {}, opener=browser,
            headers={'Origin': env['AUTHOVA_CONSOLE_URL']}, expected=403)
    request(console, '/bff/logout', 'POST', {}, opener=browser,
            headers={'Origin': env['AUTHOVA_CONSOLE_URL'], 'X-CSRF-Token': bff_login['csrf_token']}, expected=204)
    version = compose('exec', '-T', 'postgres', 'psql', '-U', 'authova_migrator', '-d', 'authova_test',
                      '-At', '-v', 'ON_ERROR_STOP=1', '-c', 'SELECT version FROM authova_schema WHERE dirty=0', capture=True).strip()
    assert version == '1', 'Unexpected migration baseline'
    request(console, '/bff/me', expected=401)
    request(console, '/bff/login', 'POST', {}, headers={'Origin': 'http://untrusted.invalid'}, expected=403)
    request('http://127.0.0.1:' + env['MAILPIT_PORT'], '/api/v1/messages')
    # Verify the security boundary on the actual running BFF, without printing env values.
    config = json.loads(compose('config', '--format', 'json', capture=True))
    assert not any(k.startswith(('AUTHOVA_DATABASE_', 'AUTHOVA_MASTER_KEY')) for k in config['services']['console']['environment'])
    write_once(STATE / 'smoke-initialized', 'ok\n')
    (STATE / 'last-smoke.json').write_text(json.dumps({'passed': True, 'restart_persistence': True, 'console_csrf_rejection': True, 'console_password_totp_session': True,
              'valkey_mtls_acl': True, 'schema_baseline': 1, 'mode': env['AUTHOVA_MODE'], 'unix_time': int(time.time())}, indent=2) + '\n')
    print('Smoke passed: operator password/TOTP, synthetic App, PostgreSQL/cache/application restart persistence, Console auth/CSRF, Mailpit.')


def main():
    cmd = sys.argv[1] if len(sys.argv) == 2 else ''
    if cmd not in ['init', 'up', 'down', 'status', 'smoke', 'restart']:
        raise RuntimeError('Usage: scripts/local-test.sh init|up|down|status|smoke|restart')
    if cmd == 'init':
        init()
        return
    if not (STATE / '.env').exists():
        raise RuntimeError('Run scripts/local-test.sh init first')
    if cmd == 'up':
        compose('config', '--quiet')
        compose('up', '-d', '--build')
        wait_ready()
    elif cmd == 'down':
        compose('down')  # Intentionally never --volumes.
    elif cmd == 'status':
        compose('ps', '-a')
    elif cmd == 'restart':
        compose('restart', 'postgres', 'valkey', 'mailpit', 'core', 'worker', 'console')
        wait_ready()
    else:
        smoke()


if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print('local-test: ' + str(e), file=sys.stderr)
        sys.exit(1)
