#!/usr/bin/env python3
"""Rehearse a genuine v1 -> v2 compiled-catalog upgrade in owned disposable DBs.
The proposed v2 exists only in a private source copy, never the product catalog.
"""
from pathlib import Path
import argparse,hashlib,json,os,secrets,shutil,signal,socket,subprocess,time,urllib.request
root=Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--evidence',type=Path,required=True);a=p.parse_args();out=a.evidence.resolve()
if root/'.migration' not in out.parents:p.error('evidence must be in this checkout .migration')
out.mkdir(parents=True,exist_ok=False,mode=0o700)
copy=out/'future-source';copy.mkdir()
for file in ['go.mod','go.sum']:shutil.copyfile(root/file,copy/file)
for directory in ['cmd','internal','api']:shutil.copytree(root/directory,copy/directory)
for dialect in ['postgres','mysql']:
 table='public.apps' if dialect=='postgres' else 'apps'
 (copy/'internal/migrations/sql'/dialect/'002_upgrade_probe.sql').write_text(f'-- Disposable acceptance proposal, never a product migration.\nALTER TABLE {table} ADD COLUMN engineering_version_probe VARCHAR(64);\nCREATE INDEX engineering_version_probe_idx ON {table}(engineering_version_probe);\n')
def run(label,command,env=None,**kw):
 with (out/(label+'.log')).open('wb') as log:return subprocess.run(command,env=env,cwd=kw.pop('cwd',root),stdout=log,stderr=subprocess.STDOUT,check=True,**kw)
run('future-checksum',[str(root/'scripts/migration/go.sh'),'run','./cmd/migration-hash'],cwd=copy)
future=out/'authova-v2';run('future-build',[str(root/'scripts/migration/go.sh'),'build','-trimpath','-o',str(future),'./cmd/authova'],cwd=copy)
compose=['docker-compose','-p','authova-migration','-f',str(root/'deploy/migration/compose.yaml'),'exec','-T']
pg=compose+['-e','PGPASSWORD=authova-local-database-only','postgres','psql','-U','authova_migrator','-v','ON_ERROR_STOP=1','-At']
my=compose+['-e','MYSQL_PWD=authova-local-database-only','mysql','mysql','-uroot','-N']
def sql(dialect,name,query):
 command=pg+['-d',name,'-c',query] if dialect=='postgres' else my+['-D',name,'-e',query]
 return subprocess.check_output(command,cwd=root,text=True,stderr=subprocess.STDOUT).strip()
def owner_sql(dialect,query):return sql(dialect,'authova',query)
results=[]
for dialect in ['postgres','mysql']:
 name='authova_v1_upgrade_'+secrets.token_hex(6);created=False;process=None
 key=out/(dialect+'-master');key.write_bytes(secrets.token_bytes(32));key.chmod(0o600)
 with socket.socket() as reservation:reservation.bind(('127.0.0.1',0));port=reservation.getsockname()[1]
 env={k:v for k,v in os.environ.items() if not k.startswith('AUTHOVA_')}
 env.update(AUTHOVA_MODE='development',AUTHOVA_BASE_URL=f'http://127.0.0.1:{port}',AUTHOVA_LISTEN=f'127.0.0.1:{port}',AUTHOVA_CONSOLE_URL='http://127.0.0.1:38090',AUTHOVA_MASTER_KEY_FILE=str(key),AUTHOVA_DATABASE_DRIVER=dialect,AUTHOVA_VALKEY_ADDRESS='')
 try:
  if dialect=='postgres':
   owner_sql(dialect,f'CREATE DATABASE {name}');created=True
   sql(dialect,name,'GRANT USAGE ON SCHEMA public TO authova_runtime; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;')
   owner=f'postgres://authova_migrator:authova-local-database-only@127.0.0.1:35432/{name}?sslmode=disable';runtime=f'postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:35432/{name}?sslmode=disable'
  else:
   owner_sql(dialect,f'CREATE DATABASE {name} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin');created=True
   owner_sql(dialect,f"GRANT SELECT,INSERT,UPDATE,DELETE ON {name}.* TO 'authova_runtime'@'%'")
   owner=f'root:authova-local-database-only@tcp(127.0.0.1:33306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin';runtime=f'authova_runtime:authova-local-runtime-only@tcp(127.0.0.1:33306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'
  env.update(AUTHOVA_DATABASE_URL=runtime,AUTHOVA_MIGRATION_DATABASE_URL=owner)
  run(dialect+'-v1-migrate',[str(root/'bin/authova'),'migrate'],env)
  sql(dialect,name,"INSERT INTO apps(id,app_key,slug,name,status,config_version,created_at,issuer) VALUES('00000000-0000-0000-0000-000000000001','upgrade-probe','upgrade-probe','Preserved App','active',1,0,'https://upgrade.example/a/upgrade-probe')")
  facts_query='SELECT id,app_key,slug,name,status,config_version,created_at,issuer,key_epoch,authentication_policy,resource_limits FROM apps ORDER BY id'
  before=sql(dialect,name,facts_query);version_before=sql(dialect,name,'SELECT version FROM authova_schema WHERE singleton=1');assert version_before=='1'
  run(dialect+'-v2-migrate',[str(future),'migrate'],env);run(dialect+'-v2-rerun',[str(future),'migrate'],env)
  after=sql(dialect,name,facts_query);assert before==after,'upgrade changed existing App facts'
  def history():return [int(sql(dialect,name,q)) for q in ['SELECT version FROM authova_schema WHERE singleton=1','SELECT COUNT(*) FROM authova_migration_history','SELECT COUNT(*) FROM authova_atlas_revisions']]
  assert history()==[2,2,2],history()
  with (out/(dialect+'-older-refusal.log')).open('wb') as log:old=subprocess.run([str(root/'bin/authova'),'migrate'],cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT)
  assert old.returncode!=0 and history()==[2,2,2],'v1 binary changed v2 history'
  # Runtime uses only the DML account; migration credentials are removed.
  runtime_env={k:v for k,v in env.items() if k!='AUTHOVA_MIGRATION_DATABASE_URL'}
  with (out/(dialect+'-runtime.log')).open('wb') as log:process=subprocess.Popen([str(future),'serve'],cwd=root,env=runtime_env,stdout=log,stderr=subprocess.STDOUT)
  deadline=time.monotonic()+15
  while True:
   if process.poll()is not None:raise RuntimeError(dialect+' upgraded runtime stopped')
   try:
    with urllib.request.urlopen(f'http://127.0.0.1:{port}/readyz',timeout=1) as response:
     if response.status==200:break
   except OSError:
    if time.monotonic()>deadline:raise
    time.sleep(.1)
  results.append({'dialect':dialect,'initial_schema_version':1,'upgraded_schema_version':2,'authova_history_rows':2,'atlas_revision_rows':2,'same_facts_sha256':hashlib.sha256(before.encode()).hexdigest(),'rerun_passed':True,'older_binary_refused_without_renumbering':True,'restricted_runtime_ready':200})
  (out/'result.json').write_text(json.dumps(results,indent=2)+'\n')
 finally:
  if process is not None:
   if process.poll()is None:process.send_signal(signal.SIGTERM)
   process.wait(timeout=15)
  if created:
   if dialect=='postgres':owner_sql(dialect,f'DROP DATABASE {name} WITH (FORCE)')
   else:owner_sql(dialect,f"DROP DATABASE {name}; REVOKE SELECT,INSERT,UPDATE,DELETE ON {name}.* FROM 'authova_runtime'@'%';")
print('Real v1 -> v2 catalog, histories, rerun, older-binary refusal and restricted HTTP runtime passed on both databases.')
