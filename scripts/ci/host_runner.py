#!/usr/bin/env python3
"""Pinned installed owner entry, never an ordinary pipeline command."""
import time
ORIGINAL=(time.time(),time.monotonic())
import argparse
import ast
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import types

SOURCES=('host_runner.py','host_adapter.py','host_admission.py','release_contract.py',
         'private_native.py','compose_owner.py','deployment_http.py','test_owner.py',
         'test_materials.py','test_proxy.py','test_http.py')
PROFILE_KEYS={'schema','role','state_root','ledger_root','docker','compose','docker_host','docker_config','daemon_id',
              'active_control','archive','fixture_images','core_origin','console_origin','ca_pem','compile_manifest'}
BASE=Path('/Users/chenhuanxiang/Workbench/authova/.local-test/authova-cicd-host01')
CI_DOCKER='unix:///Users/chenhuanxiang/Workbench/cicd/.local/colima/lab-ci/docker.sock'
FIXTURE_IMAGES={
    'mysql':'docker.io/library/mysql@sha256:24989179f58856db763f418010c1169fecc944e3ec1ea7e921572d83eb070f70',
    'valkey':'docker.io/valkey/valkey@sha256:56729e68bdda139cbd3f6a2538683dc3523325fc5711ea1a379938f1b37893f0',
    'tools':'sha256:b8678913e1f8e9af59ee49092042147eb3beb3743f3381dafa19bbff0b5115cd'}
REPORT_SECONDS=30
STDOUT_ATTEMPTED=False
APPROVED_CODES=set()
ACTIVE_OWNER=None


def bootstrap(expected):
    """Read/hash held source bytes before importing any sibling code."""
    root=Path(__file__).parent
    fd=os.open('/',os.O_RDONLY|os.O_DIRECTORY)
    first=None
    try:
        for part in str(root).split('/')[1:]:
            child=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
            previous,fd=fd,child;os.close(previous)
        s=os.fstat(fd)
        if s.st_uid!=os.getuid() or stat.S_IMODE(s.st_mode)!=0o700:raise ValueError('CODE_DIRECTORY')
        def read(name):
            item=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
            first=None
            try:
                before=os.fstat(item)
                if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or stat.S_IMODE(before.st_mode)!=0o600 or before.st_size>131072:raise ValueError('CODE_METADATA')
                data=bytearray()
                while True:
                    part=os.read(item,65536)
                    if not part:break
                    data.extend(part)
                    if len(data)>131072:raise ValueError('CODE_SIZE')
                token=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_uid,v.st_gid,v.st_nlink,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
                if len(data)!=before.st_size or token(before)!=token(os.fstat(item)) or token(before)!=token(os.stat(name,dir_fd=fd,follow_symlinks=False)):raise ValueError('CODE_DRIFT')
                return bytes(data)
            except BaseException as exc:first=exc;raise
            finally:
                try:os.close(item)
                except BaseException:
                    if first is None:raise
                    first.close_secondary=getattr(first,'close_secondary',())+('BOOTSTRAP_CLOSE',)
        raw=read('code-pins.json')
        if hashlib.sha256(raw).hexdigest()!=expected:raise ValueError('CODE_MANIFEST_PIN')
        def pairs(items):
            value={}
            for k,v in items:
                if k in value:raise ValueError('CODE_MANIFEST_DUPLICATE')
                value[k]=v
            return value
        pins=json.loads(raw.decode('utf-8'),object_pairs_hook=pairs)
        if type(pins) is not dict or set(pins)!=set(SOURCES):raise ValueError('CODE_SET')
        held={}
        for name,sha in pins.items():
            body=read(name)
            if type(sha) is not str or not re.fullmatch('[0-9a-f]{64}',sha) or hashlib.sha256(body).hexdigest()!=sha:raise ValueError('CODE_SHA')
            held[name]=body
    except BaseException as exc:first=exc;raise
    finally:
        try:os.close(fd)
        except BaseException:
            if first is None:raise
            first.close_secondary=getattr(first,'close_secondary',())+('BOOTSTRAP_DIRECTORY_CLOSE',)
    modules={}
    for name in ('release_contract','host_admission','host_adapter','private_native','deployment_http','compose_owner','test_owner'):
        module=types.ModuleType(name);module.__file__=str(root/(name+'.py'))
        sys.modules[name]=module
        exec(compile(held[name+'.py'],module.__file__,'exec'),module.__dict__)
        modules[name]=module
    return modules,held


def report(value):
    global STDOUT_ATTEMPTED
    def timely():
        if not (ORIGINAL[0]<=time.time()<ORIGINAL[0]+REPORT_SECONDS and ORIGINAL[1]<=time.monotonic()<ORIGINAL[1]+REPORT_SECONDS):raise ValueError('REPORT_EXPIRED')
    timely();STDOUT_ATTEMPTED=True
    sys.stdout.buffer.write(json.dumps(value,sort_keys=True,separators=(',',':'),allow_nan=False).encode()+b'\n')
    sys.stdout.buffer.flush();timely()


def main():
    global REPORT_SECONDS,APPROVED_CODES,ACTIVE_OWNER
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--installation',required=True)
    parser.add_argument('--admission',required=True)
    parser.add_argument('--executor-sha256',required=True)
    args=parser.parse_args()
    if not re.fullmatch(r'[0-9a-f]{64}\.admission\.json',args.admission) or not re.fullmatch('[0-9a-f]{64}',args.executor_sha256):raise ValueError('ARGUMENTS')
    modules,held=bootstrap(args.executor_sha256)
    for body in held.values():
        for node in ast.walk(ast.parse(body)):
            if isinstance(node,ast.Call):
                for value in node.args[1:] if isinstance(node.func,ast.Name) and node.func.id=='need' else node.args[:1]:
                    if isinstance(value,ast.Constant) and type(value.value) is str and re.fullmatch('[A-Z][A-Z0-9_]{2,64}',value.value):APPROVED_CODES.add(value.value)
    a=modules['host_adapter'];h=modules['host_admission'];need=modules['release_contract'].need
    with a.directory(args.installation,os.getuid()) as root:
        profile=h.strict_json(a.read_private(root,'profile.json',os.getuid()))
        candidate=h.strict_json(a.read_private(root,'candidate.json',os.getuid()))
        key=a.read_private(root,'key',os.getuid(),limit=4096)
        receipt=None
        envelope=h.strict_json(a.read_private(root,args.admission,os.getuid()))
    need(type(profile) is dict and set(profile)==PROFILE_KEYS and profile['schema']=='authova-installed-host-profile/v2','HOST_PROFILE')
    nonce=args.admission[:-15]
    need(envelope['payload']['nonce']==nonce,'ADMISSION_FILENAME_BINDING')
    action='test' if profile['role']=='ci-test' else 'production'
    need(Path(args.installation)==BASE/'requests'/nonce and profile['state_root']==str(BASE/'state'/action)
         and profile['docker_config']==str(BASE/'docker-config'/action),'FIXED_OWNER_ROOTS')
    payload=h.verify(envelope,key,candidate,action,int(time.time()))
    with a.directory(args.installation,os.getuid()) as root:
        if action=='production':receipt=h.strict_json(a.read_private(root,'test-receipt.json',os.getuid(),limit=262144))
    with a.directory(profile['ledger_root'],os.getuid()) as ledger_root:a.ledger(ledger_root,payload,receipt,os.getuid())
    REPORT_SECONDS=3600 if action=='test' else 1800
    def guard():
        need(ORIGINAL[0]<=time.time()<ORIGINAL[0]+REPORT_SECONDS and ORIGINAL[1]<=time.monotonic()<ORIGINAL[1]+REPORT_SECONDS,'OPERATION_EXPIRED')
    def full(whole,suffix=0):
        guard();need(time.time()+whole+suffix<ORIGINAL[0]+REPORT_SECONDS and time.monotonic()+whole+suffix<ORIGINAL[1]+REPORT_SECONDS,'OPERATION_FULL_SUFFIX')
    guard.full=full
    with a.directory(profile['docker_config'],os.getuid()) as cfg:need(not os.listdir(cfg),'EMPTY_DOCKER_CONFIG_REQUIRED')
    project='authova-ci-test-'+candidate['source'][:12]+'-'+str(payload['build_pipeline']) if action=='test' else 'authova-local-production-20261114'
    binding={'schema':'authova-owner-binding/v1','action':action,'candidate':candidate,'project':project,'seconds':REPORT_SECONDS,'fixture':None,'current':{'schemaDigest':candidate['schemaDigest']}}
    if action=='test':
        need(profile['role']=='ci-test' and profile['docker_host']==CI_DOCKER
             and profile['daemon_id']=='06b356e5-903e-42a1-b1ec-1c5e29da05e5'
             and profile['docker']['path']=='/Applications/Docker.app/Contents/Resources/bin/docker'
             and profile['compose']['path']=='/Applications/Docker.app/Contents/Resources/cli-plugins/docker-compose'
             and profile['docker']['sha256']=='d79834f5443651e2a3a5a5baf54eed1f41955e90c0eb323b570f913bdf75062d'
             and profile['compose']['sha256']=='817dde1748900478a6db1e509c7cf8e8a0957ea6050c5705d60feb147ad0c5a6'
             and profile['active_control'] is None
             and profile['core_origin'] is None and profile['console_origin'] is None and profile['ca_pem'] is None
             and profile['fixture_images']==FIXTURE_IMAGES,'CI_HOST_BINDING')
        scope=nonce[:32];root=profile['state_root']+'/test/'+scope
        fixture={'schema':'authova-ci-test-binding/v2','scope':scope,'root':root,'network':project+'-network','mysql_volume':project+'-mysql',
                 'secret_volumes':{k:project+'-'+k+'-credentials' for k in ('core','console','valkey','proxy')},
                 'service_names':{k:project+'-'+k for k in ('core','console','mysql','valkey','proxy')}}
        binding.update(fixture=fixture,current={'schema':'authova-owner-current/v1','project':project,'schemaDigest':candidate['schemaDigest'],
                      'layers':[root+'/compose.json'],'services':{r:{'id':None,'image':None} for r in ('core','console')}})
        owner=modules['test_owner'].TestOwner(profile,profile['state_root'],nonce,source_bytes=held)
    else:
        need(action=='production' and profile['role']=='application-production'
             and profile['docker_host']!=CI_DOCKER and profile['fixture_images'] is None
             and profile['active_control']=='/Users/chenhuanxiang/Workbench/docs/project-progress/authova-active-control.json','PRODUCTION_HOST_BINDING')
        import urllib.parse
        for origin in (profile['core_origin'],profile['console_origin']):
            need(type(origin) is str,'PRODUCTION_ORIGIN')
            url=urllib.parse.urlsplit(origin)
            need(url.scheme=='https' and url.hostname and not url.path and not url.query and not url.fragment and not url.username and not url.password,'PRODUCTION_ORIGIN')
        need(profile['core_origin']!=profile['console_origin'],'PRODUCTION_ORIGINS_DISTINCT')
        need(profile['ca_pem'] is None or type(profile['ca_pem']) is str,'PRODUCTION_CA_INPUT')
        owner=modules['compose_owner'].CoreConsoleOwner(profile,profile['state_root'],nonce)
    ACTIVE_OWNER=owner
    with a.directory(args.installation,os.getuid()) as root:a.durable(root,'owner-started.json',{'schema':'authova-owner-attempt/v1','nonce':nonce,'action':action,'candidate_digest':payload['candidate_digest']})
    # Only the validated fixed owner action gets its full original report domain.
    need(binding['seconds']==(3600 if binding['action']=='test' else 1800),'OWNER_REPORT_BUDGET')
    REPORT_SECONDS=binding['seconds']
    first=None
    try:
        if action=='production':binding['current']=owner.current(binding,guard)
        with a.directory(args.installation,os.getuid()) as root:a.durable(root,'owner-binding.json',binding)
        result=a.execute(binding,envelope,key,profile['ledger_root'],profile['state_root'],owner,receipt=receipt,_origin=ORIGINAL)
    except BaseException as exc:first=exc;raise
    finally:
        try:
            guard()
            with a.directory(profile['state_root'],os.getuid()) as root:
                a.durable(root,nonce+'.native.json',{'schema':'authova-owner-native/v1','commands':owner.native.records+owner.compose_native.records,'tools':{'docker':owner.native.observed,'compose':owner.compose_native.observed},'all_OS_descriptors':'UNKNOWN'})
            guard()
        except BaseException:
            if first is None:raise
            first.close_secondary=getattr(first,'close_secondary',())+('NATIVE_REPORT',)
    report(result)
    return 0 if result['result'] in ('PASS_TEST','PASS_PRODUCTION') else 1


if __name__=='__main__':
    try:raise SystemExit(main())
    except SystemExit:raise
    except BaseException as exc:
        # No free exception strings or second/fallback frame after output began.
        # A failed/late operation keeps its intent and never automatically replays.
        if not STDOUT_ATTEMPTED:
            try:
                code=exc.args[0] if exc.args and type(exc.args[0]) is str and exc.args[0] in APPROVED_CODES else 'OWNER_UNCLASSIFIED'
                phase=getattr(ACTIVE_OWNER,'phase','BOOTSTRAP')
                if phase not in ('BOOTSTRAP','CURRENT','PREFLIGHT','APPLY','ACCEPT','ROLLBACK','PUBLISH','CLEANUP'):phase='BOOTSTRAP'
                gate=getattr(ACTIVE_OWNER,'gate',None)
                if gate not in ('artifact-identity','fresh-fixture-schema-and-privileges','runtime-configuration-and-hardening','normal-tls-health-and-static-assets','guest-and-authorization-boundaries','management-password-mfa-logout','ordinary-app-oidc-pkce-lifecycle','operator-password-and-session-revocation','same-storage-rollback-and-repromotion','owned-test-io-and-session-closure'):gate=None
                report({'schema':'authova-owner-execution-failure/v1','result':'UNKNOWN_OR_REFUSED','code':code,'checkpoint':phase,'gate':gate,'replay':False})
            except BaseException:pass
        raise SystemExit(2) from None
