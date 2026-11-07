#!/usr/bin/env python3
"""Disposable production-mode OCI smoke. Requires built local images and fresh TLS gate material."""
import argparse,json,os,secrets,shutil,subprocess,time,urllib.request,urllib.error
from pathlib import Path
root=Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--valkey-fixture',type=Path,required=True);p.add_argument('--evidence',type=Path,required=True);a=p.parse_args()
tls=a.valkey_fixture.resolve();out=a.evidence.resolve()
if root/'.migration' not in tls.parents or root/'.migration' not in out.parents:p.error('fixtures/evidence must be within this checkout .migration')
out.mkdir(mode=0o700,parents=True,exist_ok=False)
info=json.loads((tls/'fixture-summary.json').read_text());identity=secrets.token_hex(6);db='authova_oci_'+identity;network='authova-oci-'+identity;created=[];db_created=False;net_created=False
compose=['docker-compose','-p','authova-migration','-f',str(root/'deploy/migration/compose.yaml'),'exec','-T','-e','PGPASSWORD=authova-local-database-only','postgres','psql','-U','authova_migrator','-v','ON_ERROR_STOP=1']
def sql(database,query):subprocess.run(compose+['-d',database],input=query.encode(),check=True,stdout=subprocess.DEVNULL)
def docker(*args):return subprocess.check_output(['docker',*args],stderr=subprocess.STDOUT,text=True).strip()
def status(url,data=None,headers=None):
 try:
  with urllib.request.urlopen(urllib.request.Request(url,data=data,headers=headers or {}),timeout=3) as r:return r.status
 except urllib.error.HTTPError as e:return e.code
checks={}
try:
 sql('authova',f'CREATE DATABASE {db};');db_created=True
 sql(db,'GRANT USAGE ON SCHEMA public TO authova_runtime; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;')
 (out/'master.key').write_bytes(secrets.token_bytes(32));(out/'console.key').write_bytes(secrets.token_bytes(32));(out/'master.key').chmod(0o600);(out/'console.key').chmod(0o600)
 env={k:v for k,v in os.environ.items() if not k.startswith('AUTHOVA_')};env.update(AUTHOVA_MODE='development',AUTHOVA_BASE_URL='http://127.0.0.1:38080',AUTHOVA_CONSOLE_URL='http://127.0.0.1:38090',AUTHOVA_MASTER_KEY_FILE=str(out/'master.key'),AUTHOVA_DATABASE_DRIVER='postgres',AUTHOVA_DATABASE_URL=f'postgres://authova_migrator:authova-local-database-only@127.0.0.1:35432/{db}?sslmode=disable')
 with (out/'migrate.log').open('wb') as log:subprocess.run([str(root/'bin/authova'),'migrate'],cwd=root,env=env,check=True,stdout=log,stderr=log)
 docker('network','create','--label','authova.acceptance=oci-smoke',network);net_created=True
 name='authova-oci-valkey-'+identity
 docker('create','--name',name,'--network',network,'--network-alias',info['alias'],'--user',f'{os.getuid()}:{os.getgid()}','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs','/data:rw,noexec,nosuid,size=32m,mode=1777','-v',str(tls)+':/tls:ro','valkey/valkey:9.1-alpine','valkey-server','/tls/valkey.conf');created.append(name);docker('start',name)
 clients=json.loads((tls/'clients.json').read_text())
 for role,port in [('core',8080),('console',8090)]:
  mounts=[];settings={'AUTHOVA_MODE':'production','AUTHOVA_BASE_URL':'https://identity.example.test','AUTHOVA_CONSOLE_URL':'https://console.example.test','AUTHOVA_VALKEY_ADDRESS':info['alias']+':6379','AUTHOVA_VALKEY_TLS':'true','AUTHOVA_VALKEY_USERNAME':clients[role]['Username']}
  for field,setting in [('CAFile','TLS_CA_FILE'),('PasswordFile','PASSWORD_FILE'),('CertFile','TLS_CERT_FILE'),('KeyFile','TLS_KEY_FILE')]:
   target=role+'-'+Path(clients[role][field]).name;shutil.copyfile(clients[role][field],out/target);mounts.append(target);settings['AUTHOVA_VALKEY_'+setting]='/run/authova/'+target
  if role=='core':
   settings.update(AUTHOVA_DATABASE_DRIVER='postgres',AUTHOVA_DATABASE_URL=f'postgres://authova_runtime:authova-local-runtime-only@host.docker.internal:35432/{db}?sslmode=disable',AUTHOVA_MASTER_KEY_FILE='/run/authova/master.key',AUTHOVA_LISTEN=':8080');mounts.append('master.key')
  else:settings.update(AUTHOVA_CONSOLE_KEY_FILE='/run/authova/console.key',AUTHOVA_CONSOLE_LISTEN=':8090');mounts.append('console.key')
  envfile=out/(role+'.env');envfile.write_text(''.join(k+'='+v+'\n' for k,v in settings.items()));envfile.chmod(0o600)
  name='authova-oci-'+role+'-'+identity
  args=['create','--name',name,'--network',network,'--add-host','host.docker.internal:host-gateway','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--pids-limit=256','--memory=512m','--env-file',str(envfile),'-p',f'127.0.0.1::{port}']
  for file in mounts:
   (out/file).chmod(0o444);args+=['-v',f'{out/file}:/run/authova/{file}:ro']
  docker(*args,'authova-migration-'+role+':local');created.append(name);docker('start',name)
  hostport=docker('port',name,f'{port}/tcp').rsplit(':',1)[1];url='http://127.0.0.1:'+hostport
  for attempt in range(80):
   try:
    if status(url+'/healthz')==200:break
   except (OSError,urllib.error.URLError):pass
   time.sleep(.25)
  else:
   (out/(role+'-startup.log')).write_text(docker('logs',name));raise RuntimeError(role+' health unavailable')
  assert docker('inspect',name,'--format','{{.Config.User}}')=='10001:10001'
  assert docker('inspect',name,'--format','{{.HostConfig.ReadonlyRootfs}}')=='true'
  checks[role+'_nonroot_readonly_health']=True
  if role=='core':core=url
  else:
   console=url;actual=json.loads(docker('inspect',name,'--format','{{json .Config.Env}}'));assert not any(x.startswith(('AUTHOVA_DATABASE_','AUTHOVA_MASTER_KEY')) for x in actual);checks['console_no_core_credentials']=True
 for label,url,expected,data,headers in [('core_ready',core+'/readyz',200,None,None),('core_auth',core+'/api/v1/apps',401,None,None),('console_static',console+'/',200,None,None),('console_auth',console+'/bff/me',401,None,None),('console_origin',console+'/bff/login',403,b'{}',{'Content-Type':'application/json','Origin':'https://untrusted.example.test'})]:
  code=status(url,data,headers);checks[label]=code;assert code==expected,(label,code,expected)
 (out/'result.json').write_text(json.dumps({'production_mode':True,'valkey_mtls_acl':True,'ingress_tls_verified':False,'database_tls_verified':False,'checks':checks,'images':{role:docker('image','inspect','authova-migration-'+role+':local','--format','{{.Id}}') for role in ['core','console']}},indent=2)+'\n')
 print('Disposable production-mode OCI security smoke passed.')
finally:
 for name in reversed(created):docker('rm','-f',name)
 if net_created:docker('network','rm',network)
 if db_created:sql('authova',f'DROP DATABASE {db} WITH (FORCE);')
 for f in out.iterdir():
  if f.is_file():f.chmod(0o600)
