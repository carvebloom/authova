#!/usr/bin/env python3
"""Real TLS/ACL and outage gates in a new isolated Valkey; never alters the demo.

Secrets and ephemeral PKI remain under an ignored, 0700 evidence directory.
Use --keep-fixture only to reuse the owned TLS instance for subsequent gates;
remove exactly the container recorded in fixture-summary.json when finished.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--evidence', type=Path, required=True)
p.add_argument('--keep-fixture', action='store_true')
a = p.parse_args()
out = a.evidence.resolve()
if ROOT / '.migration' not in out.parents or out.exists():
    p.error('evidence must be a new directory below this repository .migration directory')
out.mkdir(mode=0o700, parents=True)
os.umask(0o077)
name = 'authova-tls-' + secrets.token_hex(6)
control = out / 'control'
control.mkdir()
results = []
started = False
network = name + '-network'
network_created = False
console = ROOT / 'console'
go = str(ROOT / 'scripts/migration/go.sh')


def run(command, **kwargs):
    return subprocess.check_output(command, stderr=subprocess.STDOUT, **kwargs).decode().strip()


def openssl(*args):
    with (out / 'pki.log').open('ab') as log:
        subprocess.run(['openssl', *args], cwd=out, stdout=log, stderr=log, check=True)


def ready(port):
    end = time.monotonic() + 10
    while time.monotonic() < end:
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=.2):
                return
        except OSError:
            time.sleep(.05)
    raise RuntimeError('owned TLS fixture did not open its port')


def gate(label, cwd, command, lifecycle=False):
    for path in control.iterdir():
        path.unlink()
    env = os.environ.copy()
    env.update(AUTHOVA_TEST_VALKEY_TLS_CONFIG=str(out / 'clients.json'),
               AUTHOVA_TEST_VALKEY_ADDRESS=f'127.0.0.1:{port}',
               AUTHOVA_TEST_VALKEY_ROLE='core' if cwd == ROOT else 'console',
               AUTHOVA_TEST_VALKEY_CONTROL=str(control) if lifecycle else '')
    with (out / (label + '.log')).open('wb') as log:
        child = subprocess.Popen(command, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + 180
        try:
            while child.poll() is None:
                if lifecycle and (control / 'stop').exists() and not (control / 'stopped').exists():
                    run(['docker', 'stop', '--time', '1', name])
                    (control / 'stopped').touch()
                if lifecycle and (control / 'restart').exists() and not (control / 'restarted').exists():
                    run(['docker', 'start', name])
                    ready(port)
                    (control / 'restarted').touch()
                if time.monotonic() > deadline:
                    raise RuntimeError(label + ' timed out')
                time.sleep(.05)
        finally:
            if child.poll() is None:
                child.terminate()
                child.wait(timeout=10)
    results.append({'gate': label, 'exit_code': child.returncode,
                    'log_sha256': hashlib.sha256((out / (label + '.log')).read_bytes()).hexdigest()})
    (out / 'gates.json').write_text(json.dumps(results, indent=2) + '\n')
    if child.returncode:
        raise RuntimeError(label + ' failed; inspect the private evidence log')


try:
    for ca in ['ca', 'untrusted-ca']:
        openssl('req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', ca+'.key',
                '-out', ca+'.crt', '-days', '2', '-subj', '/CN=Authova isolated test CA')
    for cert, ca, purpose in [('server', 'ca', 'serverAuth'), ('core', 'ca', 'clientAuth'),
                               ('console', 'ca', 'clientAuth'), ('untrusted', 'untrusted-ca', 'clientAuth')]:
        (out / (cert+'.ext')).write_text('basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage='+purpose+'\n'+
            ('subjectAltName=DNS:localhost,DNS:'+name+',IP:127.0.0.1\n' if cert=='server' else ''))
        openssl('req', '-new', '-newkey', 'rsa:2048', '-nodes', '-keyout', cert+'.key',
                '-out', cert+'.csr', '-subj', '/CN=Authova '+cert)
        openssl('x509', '-req', '-in', cert+'.csr', '-CA', ca+'.crt', '-CAkey', ca+'.key',
                '-CAcreateserial', '-out', cert+'.crt', '-days', '2', '-extfile', cert+'.ext')
    clients = {}
    acl = ['user default reset off']
    common = '+hello +ping +client|setinfo +cluster|shards +cluster|slots '
    for role, prefix, commands in [('core', 'limit', '+eval +incr +pexpire'),
                                    ('console', 'console', '+get +getdel +set +del +eval')]:
        password = secrets.token_urlsafe(32)
        (out / (role+'.password')).write_text(password+'\n')
        acl.append('user authova_'+role+' reset on #'+hashlib.sha256(password.encode()).hexdigest()+
                   ' ~authova:'+prefix+':* '+common+commands)
    (out / 'users.acl').write_text('\n'.join(acl)+'\n')
    (out / 'valkey.conf').write_text('port 0\ntls-port 6379\ntls-cert-file /tls/server.crt\n'
        'tls-key-file /tls/server.key\ntls-ca-cert-file /tls/ca.crt\ntls-auth-clients yes\n'
        'tls-protocols "TLSv1.2 TLSv1.3"\naclfile /tls/users.acl\nsave ""\nappendonly no\ndir /data\n')
    with socket.socket() as reservation:
        reservation.bind(('127.0.0.1',0))
        port=reservation.getsockname()[1]
    run(['docker', 'network', 'create', '--label', 'authova.acceptance=valkey-tls-gates', network])
    network_created = True
    run(['docker', 'create', '--name', name, '--label', 'authova.acceptance=valkey-tls-gates', '--network', network,
         '--network-alias', name, '--user', f'{os.getuid()}:{os.getgid()}', '--read-only',
         '--cap-drop=ALL', '--security-opt=no-new-privileges', '--pids-limit=128', '--memory=256m',
         '--tmpfs', '/data:rw,noexec,nosuid,size=32m,mode=1777',
         '-v', str(out)+':/tls:ro', '-p', f'127.0.0.1:{port}:6379',
         'valkey/valkey:9.1-alpine', 'valkey-server', '/tls/valkey.conf'])
    started = True # Ownership is known before starting; failed starts are cleaned too.
    run(['docker','start',name])
    port = int(run(['docker', 'port', name, '6379/tcp']).rsplit(':',1)[1])
    ready(port)
    for role in ['core', 'console']:
        clients[role] = dict(Address=f'127.0.0.1:{port}', Username='authova_'+role,
            PasswordFile=str(out/(role+'.password')), TLS=True, CAFile=str(out/'ca.crt'),
            CertFile=str(out/(role+'.crt')), KeyFile=str(out/(role+'.key')))
    clients['untrusted'] = dict(CAFile=str(out/'untrusted-ca.crt'),
                               CertFile=str(out/'untrusted.crt'), KeyFile=str(out/'untrusted.key'))
    (out / 'clients.json').write_text(json.dumps(clients, indent=2)+'\n')
    (out / 'fixture-summary.json').write_text(json.dumps(dict(container=name, alias=name, port=port,
        image=run(['docker','inspect',name,'--format','{{.Image}}']), mutual_tls=True,
        default_user_disabled=True, separate_acl_users=True, secret_material_included=False), indent=2)+'\n')
    gate('core-tls-acl', ROOT, [go,'test','-race','-count=1','-json','./internal/valkeyconfig',
                               './internal/transport','-run','TestValkeyTLSACL|TestSharedAppRateBudgetAcrossReplicas'])
    gate('console-tls-acl-sessions', console, [go,'test','-race','-count=1','-json','./...'])
    gate('core-tls-outage', ROOT, [go,'test','-race','-count=1','-json','./internal/transport',
                                 '-run','TestValkeyTLSOutageRecovery'], True)
    gate('console-tls-outage', console, [go,'test','-race','-count=1','-json','./cmd/authova-console',
                                                  '-run','TestConsoleValkeyTLSOutageRecovery'], True)
    print('Real Valkey TLS/ACL, Core/BFF sessions and outage recovery gates passed.')
finally:
    if started:
        with (out/'valkey.log').open('wb') as log:
            subprocess.run(['docker','logs',name],stdout=log,stderr=log)
    if started and not (a.keep_fixture and len(results)==4 and all(r['exit_code']==0 for r in results)):
        run(['docker','rm','-f',name])
    if network_created and not (a.keep_fixture and len(results)==4 and all(r['exit_code']==0 for r in results)):
        run(['docker','network','rm',network])
