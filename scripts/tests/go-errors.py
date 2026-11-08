#!/usr/bin/env python3
"""Disposable error-handling gates; never attaches to migration/Lab resources."""
import argparse,json,os,secrets,subprocess,tempfile,time,tarfile
from pathlib import Path
os.umask(0o077)
ROOT=Path(__file__).resolve().parents[2]
parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--focused',action='store_true',help='Current delivery/storage and transaction gates only');parser.add_argument('--delivery-only',action='store_true',help='Fast final persistence and mapping gate');args=parser.parse_args()
OUT=ROOT/('.migration/go-errors-delivery' if args.delivery_only else '.migration/go-errors-focused' if args.focused else '.migration/go-errors');OUT.mkdir(parents=True,exist_ok=True);OUT.chmod(0o700)
project='authova-errors-'+secrets.token_hex(5)
password=secrets.token_hex(24)
database='authova_quality_'+secrets.token_hex(6)
compose=['docker','compose']
if subprocess.run(compose+['version'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode:compose=['docker-compose']
results=[]
def gate(name,cmd,env=None,cwd=ROOT):
 with (OUT/(name+'.log')).open('wb') as log:r=subprocess.run(cmd,env=env,cwd=cwd,stdout=log,stderr=subprocess.STDOUT)
 results.append({'gate':name,'exit_code':r.returncode})
 (OUT/'results.json').write_text(json.dumps({'project':project,'gates':results},indent=2)+'\n')
 if r.returncode:raise RuntimeError(name+' failed; inspect local evidence log')
with tempfile.TemporaryDirectory(prefix='authova-errors-') as directory:
 d=Path(directory)
 cfg={'services':{'pg':{'image':'postgres:17-alpine','environment':{'POSTGRES_PASSWORD':password,'POSTGRES_DB':database},'ports':['127.0.0.1::5432']},'my':{'image':'mysql:8.4','environment':{'MYSQL_ROOT_PASSWORD':password,'MYSQL_DATABASE':database},'ports':['127.0.0.1::3306']},'cache':{'image':'valkey/valkey:9.1-alpine','ports':['127.0.0.1::6379']}}}
 cfg['volumes']={'pg-data':{},'my-data':{}}
 cfg['services']['pg']['volumes']=['pg-data:/var/lib/postgresql/data']
 cfg['services']['my']['volumes']=['my-data:/var/lib/mysql']
 cfg['services']['cache']['tmpfs']=['/data:rw,noexec,nosuid,size=32m']
 f=d/'compose.json';f.write_text(json.dumps(cfg));f.chmod(0o600)
 c=compose+['-p',project,'-f',str(f)]
 def port(s,p):return subprocess.check_output(c+['port',s,str(p)],text=True).strip().rsplit(':',1)[1]
 def sql(s,statement):
  cmd=c+['exec','-T','-e',('PGPASSWORD=' if s=='pg' else 'MYSQL_PWD=')+password,s]+(['psql','-U','postgres','-d',database,'-v','ON_ERROR_STOP=1','-At'] if s=='pg' else ['mysql','-uroot','-N','-B',database])
  return subprocess.run(cmd,input=statement.replace('authova_errors',database),text=True,capture_output=True)
 try:
  gate('containers-up',c+['up','-d','--pull','never'])
  deadline=time.monotonic()+180
  while True:
   if all(sql(s,'SELECT 1;').returncode==0 for s in ['pg','my']):break
   if time.monotonic()>deadline:raise RuntimeError('disposable databases not ready')
   time.sleep(2)
  key=d/'master';key.write_bytes(secrets.token_bytes(32));key.chmod(0o600)
  env={k:v for k,v in os.environ.items() if not k.startswith('AUTHOVA_') and not k.startswith('COMPOSE_')}
  env.update(AUTHOVA_MODE='development',AUTHOVA_MASTER_KEY_FILE=str(key),AUTHOVA_BASE_URL='http://127.0.0.1:18080',AUTHOVA_CONSOLE_URL='http://127.0.0.1:18090',AUTHOVA_TEST_DISPOSABLE_DATABASE=database,AUTHOVA_TEST_VALKEY_ADDRESS='127.0.0.1:'+port('cache',6379))
  admin={'postgres':f'postgres://postgres:{password}@127.0.0.1:{port("pg",5432)}/{database}?sslmode=disable','mysql':f'root:{password}@tcp(127.0.0.1:{port("my",3306)})/{database}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'}
  archive=d/'old.tar'
  with archive.open('wb') as a:subprocess.run(['git','archive','8f4f01e17477deeb1e982eb8202e8988162e4804'],cwd=ROOT,stdout=a,check=True)
  old=d/'old';old.mkdir()
  with tarfile.open(archive) as a:a.extractall(old)
  gate('build-v1',['go','build','-o',str(d/'old-core'),'./cmd/authova'],env,old)
  gate('build-current',['go','build','-o',str(d/'core'),'./cmd/authova'],env)
  for dialect,s in [('postgres','pg'),('mysql','my')]:
   e=env|{'AUTHOVA_DATABASE_DRIVER':dialect,'AUTHOVA_DATABASE_URL':admin[dialect]}
   gate('v1-init-'+dialect,[str(d/'old-core'),'migrate'],e)
   # A real pre-upgrade row: migration must preserve it and apply unknown defaults.
   insert="INSERT INTO apps(id,app_key,slug,name,status,config_version,created_at) VALUES('old','old','old','old','active',1,0); INSERT INTO outbox(id,app_id,available_at,attempts,completed,payload) VALUES('legacy','old',0,0,0,"+("decode('00','hex')" if s=='pg' else "X'00'")+");"
   assert sql(s,insert).returncode==0
   gate('v1-to-v2-'+dialect,[str(d/'core'),'migrate'],e)
   check=sql(s,"SELECT failure_phase,failure_code,delivery_state,outcome_unknown FROM outbox WHERE id='legacy';")
   assert check.returncode==0 and check.stdout.strip().replace('|','\t').split()==['unknown','unknown','unknown','0']
   assert sql(s,"DELETE FROM outbox WHERE id='legacy'; DELETE FROM apps WHERE id='old';").returncode==0
   gate('repeat-migrate-'+dialect,[str(d/'core'),'migrate'],e)
  pggrant="CREATE ROLE authova_runtime LOGIN PASSWORD '"+password+"' NOSUPERUSER NOBYPASSRLS; GRANT CONNECT ON DATABASE authova_errors TO authova_runtime; GRANT USAGE ON SCHEMA public TO authova_runtime; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO authova_runtime;"
  assert sql('pg',pggrant).returncode==0
  assert sql('my',"CREATE USER 'authova_runtime'@'%' IDENTIFIED BY '"+password+"'; GRANT SELECT,INSERT,UPDATE,DELETE ON authova_errors.* TO 'authova_runtime'@'%';").returncode==0
  env.update(AUTHOVA_TEST_POSTGRES_DSN=admin['postgres'].replace('postgres:'+password,'authova_runtime:'+password),AUTHOVA_TEST_MYSQL_DSN=admin['mysql'].replace('root:'+password,'authova_runtime:'+password),AUTHOVA_TEST_MIGRATOR_POSTGRES_DSN=admin['postgres'],AUTHOVA_TEST_MIGRATOR_MYSQL_DSN=admin['mysql'])
  command=['go','test','-race','-count=1','-json','-timeout=25m']
  if args.delivery_only:command+=['-run','Test(DeliveryFailurePersistsAcrossReopen|EntPhysicalMapping|FailedUnlock|CommitUnknownDiagnostic|ForgedStorage|ClassifiedErrors)','./internal/storage/sqlstore']
  elif args.focused:command+=['-run','Test(DeliveryFailurePersistsAcrossReopen|EntPhysicalMapping|Outbox|Atlas|Maintenance|FailedUnlock|CommitUnknownDiagnostic|ForgedStorage|ClassifiedErrors|ProtoHTTPWaitsForCommit|Databases)','./internal/storage/sqlstore','./internal/integration']
  else:command+=['./...']
  gate('dual-db-race',command,env)
  gate('contract-combination',['python3','scripts/verify-contract-combination.py'],env)
 finally:
  # Only this random fixture's containers and explicitly project-labeled volumes.
  gate('containers-down',c+['down'])
  owned=subprocess.check_output(['docker','volume','ls','-q','--filter','label=com.docker.compose.project='+project],text=True).split()
  for volume in owned:
   item=json.loads(subprocess.check_output(['docker','volume','inspect',volume],text=True))[0]
   assert item.get('Labels',{}).get('com.docker.compose.project')==project and volume.startswith(project+'_')
   gate('owned-volume-cleanup',['docker','volume','rm',volume])
print('Disposable dual-database error gates passed; evidence '+str(OUT.relative_to(ROOT)/'results.json'))
