#!/usr/bin/env python3
"""Initialize new CI-only DBs using dedicated service aliases. No Docker socket."""
import argparse,json,os,secrets,subprocess,time
from pathlib import Path
import driver

def initialize(sha,dialect,out,runner=subprocess.run):
 schema='authova_quality_'+secrets.token_hex(6);password=secrets.token_hex(24)
 env={k:v for k,v in os.environ.items() if not k.startswith(('AUTHOVA_','COMPOSE_'))}
 hosts={'mysql':'authova-ci-mysql','postgres':'authova-ci-postgres','valkey':'authova-ci-valkey'}
 mysqlroot=os.environ.get('AUTHOVA_CI_MYSQL_ROOT_PASSWORD');pgroot=os.environ.get('AUTHOVA_CI_POSTGRES_PASSWORD')
 driver.need(mysqlroot is not None and (dialect!='both' or pgroot is not None),'FIXTURE_SERVICE_CREDENTIAL_REQUIRED')
 def config(p,data):
  fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
  with os.fdopen(fd,'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
 # Fixed CI service credentials only. Reject newline/option injection.
 for value in (mysqlroot,pgroot):
  if value is not None:driver.need(value and all(x not in value for x in '\r\n\\\"\''),'FIXTURE_SERVICE_CREDENTIAL_SHAPE')
 config(out/'mysql.cnf',('[client]\nhost='+hosts['mysql']+'\nuser=root\npassword="'+mysqlroot+'"\nprotocol=tcp\n').encode())
 if dialect=='both':config(out/'pgpass',(hosts['postgres']+':5432:*:postgres:'+pgroot+'\n').encode())
 env['PGPASSFILE']=str(out/'pgpass');records=[]
 def sql(engine,statement):
  argv=['mysql','--defaults-extra-file='+str(out/'mysql.cnf'),'--batch','--skip-column-names'] if engine=='mysql' else ['psql','-h',hosts['postgres'],'-U','postgres','-d','postgres','-v','ON_ERROR_STOP=1','-At']
  I=time.time();M=time.monotonic();p=runner(argv,input=statement.encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=30);records.append({'role':engine+'-fixture-init','exit_code':p.returncode,'known_return':True,'I':I,'elapsed':time.monotonic()-M});driver.need(p.returncode==0,'FIXTURE_SQL_FAILED')
 sql('mysql',"CREATE DATABASE "+schema+" CHARACTER SET utf8mb4 COLLATE utf8mb4_bin; CREATE USER '"+schema+"'@'%' IDENTIFIED BY '"+password+"'; GRANT SELECT,INSERT,UPDATE,DELETE ON "+schema+".* TO '"+schema+"'@'%';")
 if dialect=='both':
  sql('postgres','CREATE DATABASE '+schema+'; CREATE ROLE '+schema+" LOGIN PASSWORD '"+password+"' NOSUPERUSER NOBYPASSRLS;")
  # Schema exists after migrate; grant setup is completed below against that exact DB.
 key=out/'master.key';config(key,secrets.token_bytes(32))
 e=dict(AUTHOVA_MODE='development',AUTHOVA_BASE_URL='http://authova-ci-core:8080',AUTHOVA_CONSOLE_URL='http://authova-ci-console:8090',AUTHOVA_MASTER_KEY_FILE=str(key),AUTHOVA_TEST_DISPOSABLE_DATABASE=schema,AUTHOVA_TEST_DATABASE_DIALECT=dialect,AUTHOVA_TEST_VALKEY_ADDRESS=hosts['valkey']+':6379')
 e['AUTHOVA_TEST_MYSQL_DSN']=schema+':'+password+'@tcp('+hosts['mysql']+':3306)/'+schema+'?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin';e['AUTHOVA_TEST_MIGRATOR_MYSQL_DSN']='root:'+mysqlroot+'@tcp('+hosts['mysql']+':3306)/'+schema+'?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'
 if dialect=='both':
  from urllib.parse import quote
  e['AUTHOVA_TEST_POSTGRES_DSN']='postgres://'+schema+':'+password+'@'+hosts['postgres']+':5432/'+schema+'?sslmode=disable';e['AUTHOVA_TEST_MIGRATOR_POSTGRES_DSN']='postgres://postgres:'+quote(pgroot,safe='')+'@'+hosts['postgres']+':5432/'+schema+'?sslmode=disable'
 for engine in (('mysql','postgres') if dialect=='both' else ('mysql',)):
  menv=env|e|{'AUTHOVA_DATABASE_DRIVER':engine,'AUTHOVA_DATABASE_URL':e['AUTHOVA_TEST_MIGRATOR_'+engine.upper()+'_DSN']};I=time.time();M=time.monotonic()
  with (out/(engine+'-migrate-private.log')).open('xb') as log:
   os.chmod(log.name,0o600);p=runner(['go','run','./cmd/authova','migrate'],cwd=driver.ROOT,env=menv,stdout=log,stderr=subprocess.STDOUT,timeout=300)
  records.append({'role':engine+'-fixture-migrate','exit_code':p.returncode,'known_return':True,'I':I,'elapsed':time.monotonic()-M});driver.need(p.returncode==0,'FIXTURE_MIGRATE_FAILED')
 if dialect=='both':
  argv=['psql','-h',hosts['postgres'],'-U','postgres','-d',schema,'-v','ON_ERROR_STOP=1','-At'];statement='GRANT CONNECT ON DATABASE '+schema+' TO '+schema+'; GRANT USAGE ON SCHEMA public TO '+schema+'; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO '+schema+';'
  p=runner(argv,input=statement.encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=30);driver.need(p.returncode==0,'FIXTURE_PG_GRANTS')
 driver.dump(out/'context.json',{'schema':'authova-ci-private-fixture/v1','source':sha,'env':e});driver.dump(out/'init.json',{'schema':'authova-ci-fixture-init/v1','source':sha,'dialect':dialect,'result':'PASS','records':records,'production_or_existing_schema_touched':False})

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--dialect',choices=['mysql','both'],required=True);p.add_argument('--out',type=Path,required=True);p.add_argument('--expected-sha',default=os.environ.get('CI_EXPECTED_SHA'));a=p.parse_args();sha=driver.guard(a.expected_sha);driver.need(a.out.is_absolute() and not a.out.is_relative_to(driver.ROOT),'OUTSIDE_CHECKOUT_REQUIRED');a.out.mkdir(mode=0o700,exist_ok=False);initialize(sha,a.dialect,a.out)
if __name__=='__main__':
 try:main()
 except driver.Refused as e:print(json.dumps({'result':'REFUSED','code':e.args[0]}));raise SystemExit(1)
