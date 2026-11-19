"""Runs only in a new CI-owned no-network material helper with empty own volumes."""
import base64
import json
import os
from pathlib import Path
import subprocess
import sys


def install(root, value, command=subprocess.run):
    if set(value) != {'scope','master','session','valkey_password','recipient','control','scripts'}:
        raise ValueError('MATERIAL_KEYS')
    if not all(type(v) is str for k,v in value.items() if k!='scripts'):raise ValueError('MATERIAL_TYPES')
    paths = {k:root/k for k in ('core','console','valkey','proxy')}
    for p in paths.values():
        if any(p.iterdir()):raise ValueError('MATERIAL_NOT_EMPTY')
    def once(path, data):
        fd = os.open(path, os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
    def openssl(*args):
        p = command(['openssl', *args], stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                    timeout=15, cwd=root)
        if p.returncode:raise ValueError('TEST_PKI_GENERATION')
    # Fresh keys only, generated inside this helper; no system trust changes.
    openssl('req','-x509','-newkey','rsa:2048','-nodes','-keyout','proxy/ca.key',
            '-out','proxy/ca.pem','-days','2','-subj','/CN=Authova isolated CI '+value['scope'])
    openssl('req','-newkey','rsa:2048','-nodes','-keyout','proxy/tls.key',
            '-out','proxy/tls.csr','-subj','/CN=host.docker.internal')
    once(paths['proxy']/'tls.ext', b'basicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth,clientAuth\nsubjectAltName=DNS:host.docker.internal,DNS:proxy,DNS:valkey,DNS:localhost,IP:127.0.0.1\n')
    openssl('x509','-req','-in','proxy/tls.csr','-CA','proxy/ca.pem','-CAkey','proxy/ca.key',
            '-CAcreateserial','-out','proxy/tls.crt','-days','2','-extfile','proxy/tls.ext')
    ca = (paths['proxy']/'ca.pem').read_bytes()
    cert = (paths['proxy']/'tls.crt').read_bytes()
    key = (paths['proxy']/'tls.key').read_bytes()
    for role in ('core','console','valkey'):
        once(paths[role]/'ca.pem', ca)
        once(paths[role]/'tls.crt', cert)
        once(paths[role]/'tls.key', key)
    for field, role, name in (('master','core','master.key'),('session','console','session.key')):
        raw = base64.b64decode(value[field], validate=True)
        if len(raw) != 32:raise ValueError('MATERIAL_KEY_SIZE')
        once(paths[role]/name, raw)
    password = value['valkey_password']
    if len(password) != 64 or any(c not in '0123456789abcdef' for c in password):raise ValueError('VALKEY_PASSWORD')
    for role in ('core','console','valkey'):once(paths[role]/'valkey.password', password.encode())
    # Test-only named ACL, normal TLS/mTLS; not a production Valkey grade.
    once(paths['valkey']/'valkey.conf', b'port 0\ntls-port 6379\ntls-cert-file /run/ci/tls.crt\ntls-key-file /run/ci/tls.key\ntls-ca-cert-file /run/ci/ca.pem\ntls-auth-clients yes\naclfile /run/ci/users.acl\nsave ""\nappendonly no\nmaxmemory 64mb\nmaxmemory-policy noeviction\n')
    once(paths['valkey']/'users.acl', ('user default reset off\nuser ci reset on >'+password+' ~authova:* +@all\n').encode())
    once(paths['proxy']/'control.json', json.dumps({'recipient':value['recipient'],'control':value['control']}).encode())
    code=paths['proxy']/'code';code.mkdir(mode=0o700)
    expected={'release_contract.py','host_admission.py','private_native.py','deployment_http.py','test_http.py','test_proxy.py'}
    if type(value['scripts']) is not dict or set(value['scripts'])!=expected:raise ValueError('MATERIAL_SOURCE_SET')
    for name,body in value['scripts'].items():
        if type(body) is not str or len(body.encode())>65536:raise ValueError('MATERIAL_SOURCE_SIZE')
        once(code/name,body.encode());os.chown(code/name,10001,10001)
    os.chown(code,10001,10001)
    # All helper-selected output paths only. Private leafs become uid10001:10001.
    for folder in paths.values():
        for child in folder.iterdir():
            if child==code:continue
            if child.is_symlink() or not child.is_file():raise ValueError('MATERIAL_TYPE')
            child.chmod(0o600);os.chown(child,10001,10001)
        fd=os.open(folder,os.O_RDONLY|os.O_DIRECTORY)
        folder.chmod(0o700);os.chown(folder,10001,10001)
        try:os.fsync(fd)
        finally:os.close(fd)
    return {'schema':'authova-ci-materials/v1','result':'INSTALLED','ca_pem':ca.decode('ascii')}


def main():
    raw=sys.stdin.buffer.read(262145)
    if len(raw)>262144:raise ValueError('MATERIAL_INPUT_SIZE')
    print(json.dumps(install(Path('/fixture'),json.loads(raw)),sort_keys=True))


if __name__ == '__main__':
    try:main()
    except BaseException:raise SystemExit('CI_MATERIAL_INSTALL_FAILED') from None
