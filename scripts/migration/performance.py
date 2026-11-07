#!/usr/bin/env python3
"""Bounded old/new online introspection comparison on new disposable databases.
Build old/new binaries into .migration/performance/bin first; never runs old env
or deployment scripts. Fixed --users/--qps bounds prevent capacity-tool defaults.
"""
from pathlib import Path
import argparse,base64,hashlib,json,os,platform,secrets,signal,socket,subprocess,time,urllib.request
root=Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser();p.add_argument('--seconds',type=int,default=5);p.add_argument('--qps',type=int,default=100);args=p.parse_args()
if not 2<=args.seconds<=15 or not 1<=args.qps<=200:p.error('bounded comparison: 2..15 seconds, <=200 requests/s')
probe=socket.socket();probe.bind(('127.0.0.1',38180));probe.close()
out=root/'.migration/performance';out.mkdir(parents=True,exist_ok=True)
binaries=out/'bin'
for binary in ['old-authova','new-authova','load','bench']:
 if not(binaries/binary).is_file():raise SystemExit('Missing prebuilt '+binary)
# Do not mix measurements with the regression runner's load on these engines.
processes=subprocess.check_output(['ps','-axo','command='],text=True)
if any('scripts/migration/quality.py' in line and 'performance.py' not in line for line in processes.splitlines()):raise SystemExit('Wait for disposable regression gates to finish before performance comparison')
compose=['docker-compose','-p','authova-migration','-f',str(root/'deploy/migration/compose.yaml'),'exec','-T']
pg=compose+['-e','PGPASSWORD=authova-local-database-only','postgres','psql','-U','authova_migrator','-v','ON_ERROR_STOP=1']
my=compose+['-e','MYSQL_PWD=authova-local-database-only','mysql','mysql','-uroot']
def sql(command,query):subprocess.run(command,input=query.encode(),stdout=subprocess.DEVNULL,cwd=root,check=True)
def run(label,command,env,input=None):
 with (out/(label+'.log')).open('wb')as log:subprocess.run(command,cwd=root,env=env,input=input,stdout=log,stderr=subprocess.STDOUT,check=True)
def hardware(key):return subprocess.check_output(['sysctl','-n',key],text=True).strip()
summary={'date':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'host':{'os':platform.platform(),'cpu':hardware('machdep.cpu.brand_string'),'logical_cpus':int(hardware('hw.ncpu')),'memory_bytes':int(hardware('hw.memsize'))},'workload':{'operation':'POST /a/{key}/oauth/introspect, online live synthetic user/session tokens','synthetic_users':1000,'active_user_samples':100,'target_qps':args.qps,'duration_seconds_per_run':args.seconds,'http_workers':8,'server_gomaxprocs':4,'server_db_pool':16,'order':['old','new','new','old'],'measurement':'scheduled arrival through response, includes generator queue; 0.1ms histogram bins'},'results':{},'limitations':['bounded local key-path comparison, not capacity/endurance acceptance','load generator and databases share host','synthetic token preparation bypasses login/issuance; those use separate behavior tests','physical schema/ciphertext retained; isolated benchmark databases only','other host workloads are not controlled; ABBA order reduces but does not remove noise','no HA/failover or large-population claim']}
# Each binary owns its genuine migration history. Copy only freshly seeded
# synthetic product facts; never edit version rows to make a binary accept a DB.
products=sorted(json.loads((root/'internal/schemaplan/model/postgres.json').read_text()))
summary['binary_sha256']={v:hashlib.sha256((binaries/(v+'-authova')).read_bytes()).hexdigest() for v in ['old','new']}
summary['database_versions']={};summary['schema_versions']={}
summary['workload']['dataset']='identical synthetic facts copied by column name into independent old/new databases; histories never copied or renumbered'
summary['limitations'].append('old/new use distinct databases with independent caches after unpublished baseline consolidation')
def copy_synthetic(dialect,source,target,private):
 if not all(n.startswith('authova_perf_') for n in [source,target]):raise RuntimeError('only owned synthetic benchmark DBs can be copied')
 if dialect=='postgres':
  dump=compose+['-e','PGPASSWORD=authova-local-database-only','postgres','pg_dump','-U','authova_migrator','--data-only','--column-inserts','--disable-triggers','--no-owner','--no-privileges','-d',source]
  for table in products:dump+=['--table','public.'+table]
  restore=pg+['-d',target]
 else:
  dump=compose+['-e','MYSQL_PWD=authova-local-database-only','mysql','mysqldump','-uroot','--no-create-info','--complete-insert','--hex-blob','--skip-add-locks','--single-transaction','--skip-lock-tables','--set-gtid-purged=OFF','--no-tablespaces',source,*products]
  restore=my+['-D',target]
 with (private/'clone.log').open('wb') as log:
  producer=subprocess.Popen(dump,cwd=root,stdout=subprocess.PIPE,stderr=log)
  try:
   status=subprocess.run(restore,cwd=root,stdin=producer.stdout,stdout=subprocess.DEVNULL,stderr=log).returncode
  finally:
   producer.stdout.close();dump_status=producer.wait()
  if status or dump_status:raise RuntimeError('synthetic dataset clone failed; inspect private clone.log')
for dialect in ['postgres','mysql']:
 campaign='authova_perf_'+secrets.token_hex(6);state=out/campaign;state.mkdir(mode=0o700)
 names={v:campaign+'_'+v for v in ['old','new']};owners={};runtimes={};created=[]
 key=state/'master';key.write_bytes(secrets.token_bytes(32));key.chmod(0o600)
 credentials={'Email':'benchmark@example.invalid','Password':secrets.token_urlsafe(30),'totp_secret':base64.b32encode(secrets.token_bytes(20)).decode().rstrip('=')}
 operator=state/'operator.json';operator.write_text(json.dumps(credentials));operator.chmod(0o600);fixture=state/'fixture.json'
 env={k:v for k,v in os.environ.items()if not k.startswith('AUTHOVA_')}
 env.update(GOMAXPROCS='4',AUTHOVA_MODE='development',AUTHOVA_BASE_URL='http://127.0.0.1:38180',AUTHOVA_LISTEN='127.0.0.1:38180',AUTHOVA_CONSOLE_URL='http://127.0.0.1:38190',AUTHOVA_MASTER_KEY_FILE=str(key),AUTHOVA_DATABASE_DRIVER=dialect,AUTHOVA_DB_CONNECTIONS='16',AUTHOVA_HTTP_CONCURRENCY='64',AUTHOVA_KDF_CONCURRENCY='2',AUTHOVA_VALKEY_ADDRESS='')
 process=None
 try:
  print(dialect+': initialize paired isolated databases',flush=True)
  for version,name in names.items():
   if dialect=='postgres':
    sql(pg+['-d','authova'],f'CREATE DATABASE {name};');created.append(name)
    sql(pg+['-d',name],f'GRANT USAGE ON SCHEMA public TO authova_runtime; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;')
    owners[version]=f'postgres://authova_migrator:authova-local-database-only@127.0.0.1:35432/{name}?sslmode=disable'
    runtimes[version]=f'postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:35432/{name}?sslmode=disable'
   else:
    sql(my,f'CREATE DATABASE {name} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;');created.append(name)
    sql(my,f"GRANT SELECT,INSERT,UPDATE,DELETE ON {name}.* TO 'authova_runtime'@'%';")
    owners[version]=f'root:authova-local-database-only@tcp(127.0.0.1:33306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'
    runtimes[version]=f'authova_runtime:authova-local-runtime-only@tcp(127.0.0.1:33306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'
   run(dialect+'-'+version+'-migrate',[str(binaries/(version+'-authova')),'migrate'],env|{'AUTHOVA_DATABASE_URL':runtimes[version],'AUTHOVA_MIGRATION_DATABASE_URL':owners[version]})
  new_env=env|{'AUTHOVA_DATABASE_URL':runtimes['new']}
  run(dialect+'-bootstrap',[str(binaries/'new-authova'),'bootstrap'],new_env,input=json.dumps(credentials).encode())
  run(dialect+'-seed',[str(binaries/'load'),'-users','1000','-operator-file',str(operator),'-output',str(fixture)],new_env)
  copy_synthetic(dialect,names['new'],names['old'],state)
  summary['schema_versions'][dialect]={}
  for version,name in names.items():
   query='SELECT version FROM authova_schema WHERE singleton=1'
   command=pg+['-d',name,'-Atc',query] if dialect=='postgres' else my+['-D',name,'-NBe',query]
   summary['schema_versions'][dialect][version]=int(subprocess.check_output(command,cwd=root,text=True).strip())
  command=pg+['-d',names['new'],'-Atc','SHOW server_version'] if dialect=='postgres' else my+['-NBe','SELECT VERSION()']
  summary['database_versions'][dialect]=subprocess.check_output(command,cwd=root,text=True).strip()
  reports=[]
  for index,version in enumerate(['old','new','new','old']):
   label=f'{dialect}-{index}-{version}';print(label+': measure',flush=True);localenv=env|{'AUTHOVA_DATABASE_URL':runtimes[version]}
   with(out/(label+'-server.log')).open('wb')as log:
    process=subprocess.Popen([str(binaries/(version+'-authova')),'serve'],cwd=root,env=localenv,stdout=log,stderr=subprocess.STDOUT)
    try:
     deadline=time.monotonic()+15
     while True:
      if process.poll()is not None:raise RuntimeError('server exited; see '+label+'-server.log')
      try:
       with urllib.request.urlopen('http://127.0.0.1:38180/healthz',timeout=1)as response:
        if response.status==200:break
      except OSError:
       if time.monotonic()>deadline:raise
       time.sleep(.1)
     report=out/(label+'.json')
     run(label,[str(binaries/'bench'),'-fixture',str(fixture),'-samples','100','-qps',str(args.qps),'-duration',str(args.seconds)+'s','-workers','8','-report',str(report)],localenv)
     result=json.loads(report.read_text());result['version']=version;result['order']=index
     if result['failed']or result['queue_dropped']:raise RuntimeError('benchmark failures: '+label)
     reports.append(result)
    finally:
     if process.poll()is None:process.send_signal(signal.SIGTERM)
     process.wait(timeout=15);process=None
  summary['results'][dialect]=reports
 finally:
  if process is not None and process.poll()is None:process.terminate();process.wait(timeout=15)
  for name in created:
   if dialect=='postgres':sql(pg+['-d','authova'],f'DROP DATABASE {name} WITH (FORCE);')
   else:sql(my,f"DROP DATABASE {name}; REVOKE SELECT,INSERT,UPDATE,DELETE ON {name}.* FROM 'authova_runtime'@'%';")
 (root/'docs/migration/performance-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
print('Bounded dual-database comparison complete; only created databases removed.',flush=True)
