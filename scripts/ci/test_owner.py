"""New CI-only deployment binding. Never reads the production control/env/volumes."""
import base64
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import time
import urllib.parse

from compose_owner import CoreConsoleOwner, ROLES, stable
from host_admission import canonical, strict_json
from private_native import Native, Unknown
from release_contract import REQUIRED_CHECKS, need

CODE=('release_contract.py','host_admission.py','private_native.py','deployment_http.py','test_http.py','test_proxy.py')

FIXTURE_PINS={
    'mysql':'docker.io/library/mysql@sha256:24989179f58856db763f418010c1169fecc944e3ec1ea7e921572d83eb070f70',
    'valkey':'docker.io/valkey/valkey@sha256:56729e68bdda139cbd3f6a2538683dc3523325fc5711ea1a379938f1b37893f0',
    'tools':'sha256:b8678913e1f8e9af59ee49092042147eb3beb3743f3381dafa19bbff0b5115cd'}
FIXTURE_REPOSITORIES={
    'mysql':('docker.io/library/mysql','library/mysql','mysql'),
    'valkey':('docker.io/valkey/valkey','valkey/valkey')}



class TestOwner(CoreConsoleOwner):
    def __init__(self,profile,state_root,nonce,*,source_bytes):
        super().__init__(profile,state_root,nonce);self.source_bytes=source_bytes
    def command(self,tag,args,guard,**kw):
        kw.setdefault('suffix',600)
        return super().command(tag,args,guard,**kw)
    def current(self,binding,guard):
        self.phase='CURRENT'
        ids=self.command('CI_NAMESPACE_ABSENCE',['ps','--all','--quiet','--no-trunc','--filter','label=com.docker.compose.project='+binding['project']],guard)
        need(not ids.strip(),'CI_NAMESPACE_EXISTS')
        return binding['current']
    def fixture_image(self,role,guard):
        # Digest references are not Docker content-ID strings. This separate
        # consumer admits only the fixed CI fixtures, never tags/caller refs.
        need(type(role) is str and role in FIXTURE_PINS
             and self.profile['fixture_images']==FIXTURE_PINS,'CI_FIXTURE_PIN')
        ref=FIXTURE_PINS[role]
        if role=='tools':return self.image(ref,guard)  # Already a fixed content ID.
        value=strict_json(self.command('IMAGE_LOCAL_ONLY',['image','inspect',ref],guard))
        need(type(value) is list and len(value)==1 and type(value[0]) is dict,'CI_FIXTURE_IMAGE')
        image=value[0];digest=ref.split('@',1)[1]
        need(image.get('Id')==digest and image.get('Os')=='linux'
             and image.get('Architecture')=='arm64','CI_FIXTURE_IMAGE')
        repos=image.get('RepoDigests');allowed={name+'@'+digest for name in FIXTURE_REPOSITORIES[role]}
        need(type(repos) is list and 0<len(repos)<=len(allowed)
             and all(type(v) is str and v in allowed for v in repos)
             and len(set(repos))==len(repos),'CI_FIXTURE_REPO_DIGEST')
        # Classic inspect APIs omit Descriptor. Exact Id==pinned digest and the
        # selected repository digest remain mandatory even in that shape. Never
        # infer absence from a separate failed projection or accept a config-ID
        # substitution. If supplied, every descriptor identity field must agree.
        if 'Descriptor' in image:
            descriptor=image['Descriptor']
            need(type(descriptor) is dict and set(descriptor)<={'mediaType','digest','size','platform','annotations'}
                 and {'mediaType','digest','size'}<=set(descriptor)
                 and descriptor['digest']==digest and descriptor['mediaType'] in
                     ('application/vnd.oci.image.index.v1+json','application/vnd.docker.distribution.manifest.list.v2+json',
                      'application/vnd.oci.image.manifest.v1+json','application/vnd.docker.distribution.manifest.v2+json')
                 and type(descriptor['size']) is int and 0<descriptor['size']<=1048576,'CI_FIXTURE_DESCRIPTOR')
            if 'platform' in descriptor:
                need(descriptor['platform']=={'os':'linux','architecture':'arm64'},'CI_FIXTURE_DESCRIPTOR_PLATFORM')
            if 'annotations' in descriptor:
                need(type(descriptor['annotations']) is dict
                     and all(type(k) is str and type(v) is str for k,v in descriptor['annotations'].items()),'CI_FIXTURE_DESCRIPTOR')
        return image
    def preflight(self,binding,guard):
        self.phase='PREFLIGHT'
        self.operation=Path(binding['fixture']['root'])
        parent=self.operation.parent
        try:parent.mkdir(mode=0o700)
        except FileExistsError:pass
        need(not parent.is_symlink() and (parent.stat().st_mode&0o777)==0o700 and parent.stat().st_uid==os.getuid(),'CI_ROOT_PARENT')
        self.operation.mkdir(mode=0o700)
        self.archive_guard(binding['candidate'],guard)
        for role in self.profile['fixture_images']:self.fixture_image(role,guard)
        for name in [binding['fixture']['network'],binding['fixture']['mysql_volume'],*binding['fixture']['secret_volumes'].values()]:
            kind='network' if name==binding['fixture']['network'] else 'volume'
            raw=self.command('CI_RESOURCE_ABSENCE',[kind,'ls','--quiet','--filter','name=^'+name+'$'],guard)
            need(not raw.strip(),'CI_RESOURCE_EXISTS')
        scope=binding['fixture']['scope']
        self.secret={
            'scope':scope,'master':base64.b64encode(secrets.token_bytes(32)).decode(),
            'session':base64.b64encode(secrets.token_bytes(32)).decode(),
            'valkey_password':secrets.token_hex(32),'recipient':'ci-'+scope+'@example.invalid',
            'control':secrets.token_urlsafe(32)}
        self.bootstrap={'Email':'owner-'+scope+'@example.invalid','Password':secrets.token_urlsafe(32),
                        'totp_secret':base64.b32encode(secrets.token_bytes(20)).decode().rstrip('=')}
        self.root_password=secrets.token_hex(24);self.runtime_password=secrets.token_hex(24)
        self.database='authova_quality_'+scope[:12]
        self.materials_script=self.source_bytes['test_materials.py'].decode('utf-8')
        self.secret['scripts']={n:self.source_bytes[n].decode('utf-8') for n in CODE}
    def role_id(self,binding,role,guard):
        raw=self.command('CI_ROLE_SELECT',['ps','--all','--quiet','--no-trunc','--filter',
                    'label=com.docker.compose.project='+binding['project'],'--filter',
                    'label=com.docker.compose.service='+role],guard).decode().split()
        need(len(raw)==1 and re.fullmatch('[0-9a-f]{64}',raw[0]),'CI_ROLE_SINGLETON')
        return raw[0]
    def volume_mount(self,binding,role):
        return {'Type':'volume','Name':binding['fixture']['secret_volumes'][role],
                'Destination':'/run/ci','RW':False}
    def core_env(self):
        return {'AUTHOVA_MODE':'production','AUTHOVA_BASE_URL':self.core_origin,'AUTHOVA_CONSOLE_URL':self.console_origin,
                'AUTHOVA_LISTEN':':8080','AUTHOVA_DATABASE_DRIVER':'mysql',
                'AUTHOVA_DATABASE_URL':'ci_runtime:'+self.runtime_password+'@tcp(mysql:3306)/'+self.database+'?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin',
                'AUTHOVA_MASTER_KEY_FILE':'/run/ci/master.key','AUTHOVA_VALKEY_ADDRESS':'valkey:6379','AUTHOVA_VALKEY_TLS':'true',
                'AUTHOVA_VALKEY_USERNAME':'ci','AUTHOVA_VALKEY_PASSWORD_FILE':'/run/ci/valkey.password','AUTHOVA_VALKEY_TLS_CA_FILE':'/run/ci/ca.pem',
                'AUTHOVA_VALKEY_TLS_CERT_FILE':'/run/ci/tls.crt','AUTHOVA_VALKEY_TLS_KEY_FILE':'/run/ci/tls.key',
                'AUTHOVA_SMTP_ADDRESS':'proxy:1025','AUTHOVA_SMTP_FROM':'ci@example.invalid','SSL_CERT_FILE':'/run/ci/ca.pem',
                'AUTHOVA_DB_CONNECTIONS':'4','AUTHOVA_KDF_CONCURRENCY':'2','GOMAXPROCS':'2','GOMEMLIMIT':'256MiB'}
    def material_install(self,binding,guard):
        f=binding['fixture'];image=self.profile['fixture_images']['tools']
        args=['create','--pull=never','--interactive','--name',binding['project']+'-materials','--network','none',
              '--user','0:0','--read-only','--cap-drop','ALL','--cap-add','CHOWN','--security-opt','no-new-privileges:true',
              '--memory','256m','--memory-swap','256m','--cpus','0.5','--pids-limit','64',
              '--label','io.carvebloom.ci.operation='+self.nonce,'--entrypoint','python3']
        for role,volume in f['secret_volumes'].items():args+=['--mount','type=volume,source='+volume+',target=/fixture/'+role+',volume-nocopy']
        cid=self.command('CI_MATERIAL_CREATE',args+[image,'-I','-B','-c',self.materials_script],guard).decode().strip()
        need(re.fullmatch('[0-9a-f]{64}',cid),'CI_MATERIAL_ACK_UNKNOWN')
        self.write('material-ack.json',{'id':cid,'operation':self.nonce})
        raw=self.command('CI_MATERIAL_START',['start','--attach','--interactive',cid],guard,
                         body=canonical(self.secret),whole=90,suffix=600)
        result=strict_json(raw);need(set(result)=={'schema','result','ca_pem'} and result['schema']=='authova-ci-materials/v1' and result['result']=='INSTALLED','CI_MATERIAL_RESULT')
        self.ca=result['ca_pem']
        state=strict_json(self.command('CI_MATERIAL_WAIT',['inspect','--format','{{json .State}}',cid],guard))
        need(state['Running'] is False and state['ExitCode']==0,'CI_MATERIAL_WAIT_UNKNOWN')
        self.command('CI_MATERIAL_REMOVE',['rm',cid],guard)
        need(not self.command('CI_MATERIAL_ABSENCE',['ps','--all','--quiet','--filter','id='+cid],guard).strip(),'CI_MATERIAL_ABSENCE_UNKNOWN')
    def dependencies(self,binding):
        f=binding['fixture'];images=self.profile['fixture_images'];project=binding['project']
        volume=lambda r:{'type':'volume','source':r+'-credentials','target':'/run/ci','read_only':True,'volume':{'nocopy':True}}
        shared={'restart':'no','cpus':0.5,'pids_limit':128,'cap_drop':['ALL'],'security_opt':['no-new-privileges:true'],'networks':['ci'],'labels':{'io.carvebloom.ci.operation':self.nonce}}
        services={
            'mysql':{'image':images['mysql'],'container_name':f['service_names']['mysql'],'mem_limit':'768m','memswap_limit':'768m',
                     'environment':{'MYSQL_ROOT_PASSWORD':self.root_password},'command':['mysqld','--innodb-buffer-pool-size=201326592','--performance-schema=OFF','--max-connections=32','--character-set-server=utf8mb4','--collation-server=utf8mb4_bin'],
                     'volumes':[{'type':'volume','source':'mysql-data','target':'/var/lib/mysql','volume':{'nocopy':True}}],
                     'healthcheck':{'test':['CMD-SHELL','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqladmin ping -h127.0.0.1 --silent'],'interval':'2s','timeout':'3s','retries':40}},
            'valkey':{'image':images['valkey'],'container_name':f['service_names']['valkey'],'user':'10001:10001','read_only':True,'mem_limit':'128m','memswap_limit':'128m',
                      'command':['valkey-server','/run/ci/valkey.conf'],'volumes':[volume('valkey')],'tmpfs':['/data:mode=1777'],
                      'healthcheck':{'test':['CMD-SHELL','VALKEYCLI_AUTH=$(cat /run/ci/valkey.password) valkey-cli --tls --cacert /run/ci/ca.pem --cert /run/ci/tls.crt --key /run/ci/tls.key --user ci -h127.0.0.1 ping | grep -q PONG'],'interval':'2s','timeout':'3s','retries':40}},
            'proxy':{'image':images['tools'],'container_name':f['service_names']['proxy'],'user':'10001:10001','read_only':True,'mem_limit':'192m','memswap_limit':'192m',
                     'entrypoint':['python3'],'command':['-I','-B','-c','import sys;sys.path.insert(0,"/run/ci/code");import test_proxy;test_proxy.main()'],
                     'environment':{'PYTHONDONTWRITEBYTECODE':'1'},'volumes':[volume('proxy')],
                     'ports':['127.0.0.1::8443','127.0.0.1::8444']}}
        for v in services.values():v.update(copy.deepcopy(shared))
        # MySQL image owns its fresh data volume; retain only capabilities its
        # official entrypoint needs, not a production root/persistence permission.
        services['mysql']['cap_drop']=[]
        return {'name':project,'services':services,'networks':{'ci':{'name':f['network'],'external':True}},
                'volumes':dict({'mysql-data':{'name':f['mysql_volume'],'external':True}},**{r+'-credentials':{'name':n,'external':True} for r,n in f['secret_volumes'].items()})}
    def apply(self,binding,guard):
        self.phase='APPLY'
        f=binding['fixture']
        # All following resource mutations are after owner intent/nonce fsync.
        self.command('CI_IMAGE_LOAD',['image','load','--input',self.profile['archive']['path']],guard,whole=120,suffix=600)
        for role in ROLES:
            image=self.image(binding['candidate'][role+'ID'],guard);labels=image['Config'].get('Labels') or {}
            need(image['Config']['User']=='10001:10001' and labels.get('org.opencontainers.image.revision')==binding['candidate']['source']
                 and labels.get('io.carvebloom.artifact-manifest')==binding['candidate']['manifest']
                 and labels.get('io.carvebloom.schema')==binding['candidate']['schemaDigest'] and labels.get('io.carvebloom.role')==role,'CI_IMAGE_BINDING')
        network=self.command('CI_NETWORK_CREATE',['network','create','--label','io.carvebloom.ci.operation='+self.nonce,f['network']],guard).decode().strip()
        need(re.fullmatch('[0-9a-f]{64}',network),'CI_NETWORK_ACK_UNKNOWN')
        self.write('network-ack.json',{'id':network,'name':f['network'],'operation':self.nonce})
        observed=strict_json(self.command('CI_NETWORK_OWNERSHIP',['network','inspect',network],guard))[0]
        need(observed['Name']==f['network'] and observed['Labels'].get('io.carvebloom.ci.operation')==self.nonce,'CI_NETWORK_OWNERSHIP')
        for name in [f['mysql_volume'],*f['secret_volumes'].values()]:
            created=self.command('CI_VOLUME_CREATE',['volume','create','--label','io.carvebloom.ci.operation='+self.nonce,name],guard).decode().strip()
            need(created==name,'CI_VOLUME_ACK_UNKNOWN')
            self.write(name+'-ack.json',{'name':name,'operation':self.nonce})
            observed=strict_json(self.command('CI_VOLUME_OWNERSHIP',['volume','inspect',name],guard))[0]
            need(observed['Name']==name and observed['Driver']=='local' and observed['Labels'].get('io.carvebloom.ci.operation')==self.nonce,'CI_VOLUME_OWNERSHIP')
        self.material_install(binding,guard)
        cfg=self.dependencies(binding)
        dependency_path=self.write('dependencies.json',cfg)
        self.command('CI_DEPENDENCIES_UP',self.compose(binding,[dependency_path])+['up','--detach','--no-deps','--no-build','--pull','never','--wait','--wait-timeout','90','mysql','valkey','proxy'],guard,whole=120,suffix=600)
        self.proxy=self.role_id(binding,'proxy',guard)
        ports=strict_json(self.command('CI_PROXY_PORTS',['inspect','--format','{{json .NetworkSettings.Ports}}',self.proxy],guard))
        selected=[]
        for key in ('8443/tcp','8444/tcp'):
            rows=ports.get(key);need(type(rows) is list and len(rows)==1 and rows[0]['HostIp']=='127.0.0.1' and rows[0]['HostPort'].isdecimal(),'CI_LOOPBACK_PORT')
            p=int(rows[0]['HostPort']);need(0<p<65536,'CI_LOOPBACK_PORT');selected.append(p)
        self.core_origin='https://host.docker.internal:'+str(selected[0]);self.console_origin='https://host.docker.internal:'+str(selected[1])
        mysql=self.role_id(binding,'mysql',guard)
        sql=('CREATE DATABASE `'+self.database+'` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;CREATE USER \'ci_runtime\'@\'%\' IDENTIFIED BY \''+self.runtime_password+'\';GRANT SELECT,INSERT,UPDATE,DELETE ON `'+self.database+'`.* TO \'ci_runtime\'@\'%\';').encode()
        self.command('CI_SQL_INITIALIZE',['exec','-i',mysql,'/bin/sh','-ec','export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql -uroot --batch --skip-column-names'],guard,body=sql,whole=60,suffix=600)
        env=self.core_env();migrate=dict(env,AUTHOVA_MIGRATION_DATABASE_URL='root:'+self.root_password+'@tcp(mysql:3306)/'+self.database+'?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin')
        for tag,args,values,body in [('CI_MIGRATE',['migrate'],migrate,None),('CI_BOOTSTRAP',['bootstrap'],env,canonical(self.bootstrap))]:
            rc,_=self.oneshot(binding,guard,binding['candidate']['coreID'],args,values,[self.volume_mount(binding,'core')],network=f['network'],body=body,tag=tag)
            need(rc==0,'CI_INITIALIZE_FAILED')
        shared={'user':'10001:10001','read_only':True,'cap_drop':['ALL'],'security_opt':['no-new-privileges:true'],'restart':'no','cpus':0.5,'pids_limit':128,'networks':['ci'],'labels':{'io.carvebloom.ci.operation':self.nonce}}
        cfg['services']['core']=dict(shared,image=binding['candidate']['coreID'],container_name=f['service_names']['core'],command=['all'],mem_limit='384m',memswap_limit='384m',environment=env,
                                    healthcheck={'test':['CMD','wget','-q','-O','/dev/null','http://127.0.0.1:8080/readyz'],'interval':'2s','timeout':'3s','retries':40},volumes=[{'type':'volume','source':'core-credentials','target':'/run/ci','read_only':True,'volume':{'nocopy':True}}])
        console_env={'AUTHOVA_MODE':'production','AUTHOVA_BASE_URL':'https://proxy:8443','AUTHOVA_CONSOLE_URL':self.console_origin,'AUTHOVA_CONSOLE_LISTEN':':8090','AUTHOVA_CONSOLE_KEY_FILE':'/run/ci/session.key','AUTHOVA_CONSOLE_ASSETS':'/opt/authova-console/web','GOMAXPROCS':'2','GOMEMLIMIT':'192MiB','SSL_CERT_FILE':'/run/ci/ca.pem'}
        console_env.update({k:v for k,v in env.items() if k.startswith('AUTHOVA_VALKEY_')})
        cfg['services']['console']=dict(shared,image=binding['candidate']['consoleID'],container_name=f['service_names']['console'],mem_limit='256m',memswap_limit='256m',environment=console_env,
                                       healthcheck={'test':['CMD','wget','-q','-O','/dev/null','http://127.0.0.1:8090/healthz'],'interval':'2s','timeout':'3s','retries':40},volumes=[{'type':'volume','source':'console-credentials','target':'/run/ci','read_only':True,'volume':{'nocopy':True}}])
        self.write('compose.json',cfg)
        self.command('CI_APPLICATIONS_UP',self.compose(binding)+['up','--detach','--no-deps','--no-build','--pull','never','--wait','--wait-timeout','90','core','console'],guard,whole=120,suffix=600)
        self.context={'schema':'authova-ci-http-private/v1','project':binding['project'],'scope':f['scope'],'source':binding['candidate']['source'],
                      'core':self.core_origin,'console':self.console_origin,'ca_pem':self.ca,'recipient':self.secret['recipient'],
                      'control':self.secret['control'],'bootstrap':self.bootstrap,'state':{}}
        self.inventory={r:self.role_id(binding,r,guard) for r in binding['fixture']['service_names']}
        self.write('fixture-inventory.json',{'containers':self.inventory,'network':binding['fixture']['network'],'volumes':[binding['fixture']['mysql_volume'],*binding['fixture']['secret_volumes'].values()],'operation':self.nonce})
        return 'APPLIED'
    def http_actor(self,stage,guard,whole=180):
        rc,raw=self.native.run('CI_HTTP_ACTOR',['exec','-i',self.proxy,'python3','-I','-B','/run/ci/code/test_http.py',stage],guard,body=canonical(self.context),whole=whole,suffix=240)
        value=strict_json(raw)
        if rc!=0:
            if rc==1 and value=={'result':'FAIL','code':'CI_HTTP_ASSERTION'}:
                from deployment_http import KnownUnhealthy
                raise KnownUnhealthy('CI_HTTP_ASSERTION')
            raise Unknown('CI_HTTP_OUTCOME_UNKNOWN')
        need(set(value)=={'context','evidence'} and value['context']['project']==self.context['project'] and value['context']['scope']==self.context['scope'],'CI_HTTP_CONTEXT')
        self.context=value['context'];return value['evidence']
    def prepare_proof(self,binding,guard,tag):
        # Precreate (do not start) before code/consent. No token network until the
        # complete private request arrives. No secret env and no DB mounts.
        args=['create','--pull=never','--interactive','--network','container:'+self.proxy,'--name',binding['project']+'-'+tag,
              '--user','10001:10001','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true',
              '--memory','256m','--memory-swap','256m','--cpus','0.5','--pids-limit','64','--label','io.carvebloom.ci.operation='+self.nonce,
              binding['candidate']['coreID'],'ci-oidc-proof']
        cid=self.command('CI_PROOF_CREATE',args,guard).decode().strip();need(re.fullmatch('[0-9a-f]{64}',cid),'CI_PROOF_ACK_UNKNOWN')
        self.write(tag+'-ack.json',{'id':cid,'operation':self.nonce});return cid
    def prove(self,cid,guard,origin,state_origin):
        request=self.context['state']['proof'];i,m=origin
        def check():
            guard();need(time.time()<request['code_deadline_utc'] and time.time()<request['state_deadline_utc']
                         and i<=time.time()<i+60 and state_origin[0]<=time.time()<state_origin[0]+300
                         and m<=time.monotonic()<m+60 and time.monotonic()<state_origin[1]+300,'CI_PARENT_CODE_EXPIRED')
        def full(whole,suffix):
            guard.full(whole,suffix);check()
            need(time.time()+whole<request['code_deadline_utc'] and time.time()+whole<request['state_deadline_utc'] and time.time()+whole<i+60 and time.time()+whole<state_origin[0]+300
                 and time.monotonic()+whole<m+60 and time.monotonic()+whole<state_origin[1]+300,'CI_PARENT_CODE_FULL')
        check.full=full
        rc,raw=self.native.run('CI_REAL_OIDC_PROOF',['start','--attach','--interactive',cid],check,body=canonical(request),whole=50,suffix=240)
        check();need(rc==0 and strict_json(raw)=={'schema':'authova-ci-oidc-proof/v1','result':'PASS'},'CI_OIDC_ACTUAL_PROOF')
        state=strict_json(self.command('CI_PROOF_WAIT',['inspect','--format','{{json .State}}',cid],guard))
        need(state['Running'] is False and state['ExitCode']==0,'CI_PROOF_WAIT_UNKNOWN')
        self.command('CI_PROOF_REMOVE',['rm',cid],guard)
        need(not self.command('CI_PROOF_ABSENCE',['ps','--all','--quiet','--filter','id='+cid],guard).strip(),'CI_PROOF_ABSENCE_UNKNOWN')
    def cleanup_fixture(self,binding,guard):
        self.phase='CLEANUP'
        guard.full(600,30)
        for role,cid in self.inventory.items():
            value=strict_json(self.command('CI_CLEAN_INSPECT',['inspect',cid],guard,suffix=20))[0]
            need(value['Id']==cid and value['Config']['Labels'].get('io.carvebloom.ci.operation')==self.nonce
                 and value['Config']['Labels'].get('com.docker.compose.service')==role,'CI_CLEAN_OWNERSHIP_UNKNOWN')
            self.command('CI_CLEAN_STOP',['stop','--timeout','10',cid],guard,whole=20,suffix=20)
            self.command('CI_CLEAN_REMOVE',['rm','-v',cid],guard,suffix=20)
            need(not self.command('CI_CLEAN_ABSENCE',['ps','--all','--quiet','--filter','id='+cid],guard,suffix=20).strip(),'CI_CLEAN_ABSENCE_UNKNOWN')
        for name in [binding['fixture']['mysql_volume'],*binding['fixture']['secret_volumes'].values()]:
            item=strict_json(self.command('CI_CLEAN_VOLUME_INSPECT',['volume','inspect',name],guard,suffix=20))[0]
            need(item['Name']==name and item['Labels'].get('io.carvebloom.ci.operation')==self.nonce,'CI_CLEAN_VOLUME_OWNERSHIP')
            self.command('CI_CLEAN_VOLUME_REMOVE',['volume','rm',name],guard,suffix=20)
        name=binding['fixture']['network']
        item=strict_json(self.command('CI_CLEAN_NETWORK_INSPECT',['network','inspect',name],guard,suffix=20))[0]
        need(item['Name']==name and item['Labels'].get('io.carvebloom.ci.operation')==self.nonce,'CI_CLEAN_NETWORK_OWNERSHIP')
        self.command('CI_CLEAN_NETWORK_REMOVE',['network','rm',item['Id']],guard,suffix=20)
    def accept(self,binding,guard):
        self.phase='ACCEPT'
        # Actual CLI/HTTP results only. No prefilled PASS or caller capability bool.
        started=time.time();checks={};details={}
        for name in REQUIRED_CHECKS:
            self.gate=name
            begin=time.time();offset=len(self.native.records);coffset=len(self.compose_native.records)
            evidence={}
            if name=='artifact-identity':
                containers=self.inspect_services(binding,guard)
                for role in ROLES:
                    need(containers[role]['Image']==binding['candidate'][role+'ID'],'CI_ACTUAL_IMAGE')
                    image=self.image(binding['candidate'][role+'ID'],guard)
                    need(image['Config']['Labels']['io.carvebloom.artifact-manifest']==binding['candidate']['manifest'],'CI_ACTUAL_MANIFEST')
                evidence={'same_candidate_images':True}
            elif name=='fresh-fixture-schema-and-privileges':
                rc,frame=self.oneshot(binding,guard,binding['candidate']['coreID'],['schema-check'],self.core_env(),[self.volume_mount(binding,'core')],network=binding['fixture']['network'],tag='CI_SCHEMA_GATE')
                need(rc==0 and strict_json(frame)=={'schema':'authova-runtime-schema/v1','result':'PASS'},'CI_RUNTIME_SCHEMA')
                evidence={'read_only_runtime_check':'PASS','fixture_only_migration':'COMPLETED'}
            elif name=='runtime-configuration-and-hardening':
                literal=" leading 'single' \"double\" = # and trailing "
                script='import os,sys;sys.exit(0 if os.environ.get(\"CI_ENV_CANARY\")=='+repr(literal)+' else 1)'
                rc,_=self.oneshot(binding,guard,self.profile['fixture_images']['tools'],['-I','-B','-c',script],{'CI_ENV_CANARY':literal},[],network='none',tag='CI_ENV_GRAMMAR',entrypoint='python3')
                need(rc==0,'CI_DOCKER_ENV_FILE_SEMANTICS')
                containers=self.inspect_services(binding,guard)
                for role,c in containers.items():
                    host=c['HostConfig'];need(host['ReadonlyRootfs'] is True and host['CapDrop']==['ALL'] and 'no-new-privileges:true' in host['SecurityOpt'],'CI_HARDENING')
                    need(set(c['NetworkSettings']['Networks'])=={binding['fixture']['network']} and len(c['Mounts'])==1
                         and c['Mounts'][0]['Type']=='volume' and c['Mounts'][0]['Name']==binding['fixture']['secret_volumes'][role]
                         and c['Mounts'][0]['RW'] is False,'CI_NO_PRODUCTION_MOUNT_OR_NETWORK')
                    for flag in ('-u','-g'):
                        need(self.command('CI_ACTUAL_UID',['exec',c['Id'],'id',flag],guard).strip()==b'10001','CI_ACTUAL_UID')
                evidence={'actual_uid_gid':'10001:10001','isolated_network_and_ro_credentials':True}
            elif name=='ordinary-app-oidc-pkce-lifecycle':
                a=self.prepare_proof(binding,guard,'oidc-a')
                state_origin=(time.time(),time.monotonic());self.http_actor('oidc-register',guard)
                origin=(time.time(),time.monotonic());self.http_actor('oidc-consent',guard,whole=55);self.prove(a,guard,origin,state_origin)
                self.http_actor('oidc-replay-logout',guard)
                b=self.prepare_proof(binding,guard,'oidc-b')
                state_origin=(time.time(),time.monotonic());self.http_actor('oidc-password',guard)
                origin=(time.time(),time.monotonic());self.http_actor('oidc-consent',guard,whole=55);self.prove(b,guard,origin,state_origin)
                self.http_actor('oidc-replay-logout',guard)
                evidence={'real_library_verifier':'PASS_TWICE','same_server_subject':'RAM_ONLY_EQUAL','ordinary_scope':'NEW_CI_APP_ONLY'}
            else:
                evidence=self.http_actor(name,guard,whole=240 if name=='operator-password-and-session-revocation' else 180)
                if name=='owned-test-io-and-session-closure':
                    self.cleanup_fixture(binding,guard)
                    evidence['fixture_exact_inventory_cleanup']='COMPLETE'
            guard()
            native=self.native.records[offset:]+self.compose_native.records[coffset:]
            need(native and all(r['exit_code']==0 and r['known_return'] is True and r['timely'] is True and r['all_selected_IO_closed'] is True for r in native),'CI_GATE_NATIVE_NOT_PASS')
            safe={'schema':'authova-ci-acceptance-check/v1','check':name,'candidate':binding['candidate'],'project':binding['project'],
                  'scope':binding['fixture']['scope'],'native':native,'facts':evidence}
            self.write(name+'.json',safe)
            details[name]={'result':'PASS','exit_code':0,'known_return':True,'timely':True,'started_at':begin,'finished_at':time.time(),'evidence_sha256':hashlib.sha256(canonical(safe)).hexdigest()}
            checks[name]='PASS'
        return {'schema':'authova-test-deployment/v1','candidate':binding['candidate'],'result':'PASS','project':binding['project'],'scope':binding['fixture']['scope'],
                'checks':checks,'check_evidence':details,'started_at':started,'finished_at':time.time()}
