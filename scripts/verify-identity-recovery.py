#!/usr/bin/env python3
"""Actual old-backup recovery in newly owned PG/MySQL databases only.
Requires development Docker services. Dumps/credentials remain private; stdout
and the public summary contain counts, hashes, exit codes and timings only.
"""
from pathlib import Path
import argparse, hashlib, json, os, secrets, subprocess, tempfile, time
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('--evidence',type=Path,required=True);args=p.parse_args()
out=args.evidence.resolve();out.mkdir(parents=True,exist_ok=True);out.chmod(0o700)
name='authova_recovery_'+secrets.token_hex(6);target=name+'_restored'
compose=['docker','compose','-f',str(root/'deploy/compose.dev.yaml'),'exec','-T']
pg=compose+['-e','PGPASSWORD=authova-local-database-only','postgres','psql','-U','authova_migrator','-v','ON_ERROR_STOP=1']
my=compose+['-e','MYSQL_PWD=authova-local-database-only','mysql','mysql','-uroot']
created=[];gates=[]
def sql(base,text):subprocess.run(base,input=text.encode(),stdout=subprocess.DEVNULL,cwd=root,check=True)
def run(label,command,env):
 start=time.monotonic()
 with (out/(label+'.log')).open('wb') as log:code=subprocess.run(command,cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT).returncode
 gates.append({'gate':label,'exit_code':code,'seconds':round(time.monotonic()-start,3)})
 (out/'summary.json').write_text(json.dumps({'scope':'actual small disposable old-backup roundtrip; not HA/PITR or capacity acceptance','gates':gates},indent=2)+'\n')
 if code:raise SystemExit(label+' failed; inspect private evidence log')
try:
 for db in [name,target]:
  sql(pg+['-d','authova'],f'CREATE DATABASE {db};');created.append(('pg',db))
  sql(pg+['-d',db],f'GRANT CONNECT ON DATABASE {db} TO authova_runtime; GRANT USAGE ON SCHEMA public TO authova_runtime; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;')
  sql(my,f'CREATE DATABASE {db} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin; GRANT SELECT,INSERT,UPDATE,DELETE ON {db}.* TO \'authova_runtime\'@\'%\';');created.append(('my',db))
 with tempfile.TemporaryDirectory() as private:
  key=Path(private)/'master';key.write_bytes(secrets.token_bytes(32));key.chmod(0o600)
  fixture=out/'private-fixtures';fixture.mkdir(mode=0o700,exist_ok=True)
  env=os.environ.copy();env.update(AUTHOVA_IDENTITY_RECOVERY_SOURCE_DATABASE=name,AUTHOVA_IDENTITY_RECOVERY_RESTORED_DATABASE=target,AUTHOVA_MODE='development',AUTHOVA_MASTER_KEY_FILE=str(key),AUTHOVA_ENCRYPTION_KEYRING_FILE='',AUTHOVA_BASE_URL='http://127.0.0.1:18080',AUTHOVA_CONSOLE_URL='http://127.0.0.1:18090',AUTHOVA_IDENTITY_RECOVERY_SNAPSHOT_DIR=str(fixture))
  for dialect in ['postgres','mysql']:
   if dialect=='postgres':
    admin=f'postgres://authova_migrator:authova-local-database-only@127.0.0.1:55432/{name}?sslmode=disable'
    source=f'postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:55432/{name}?sslmode=disable';restored=source.replace('/'+name+'?', '/'+target+'?')
   else:
    admin=f'root:authova-local-database-only@tcp(127.0.0.1:53306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'
    source=f'authova_runtime:authova-local-runtime-only@tcp(127.0.0.1:53306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin';restored=source.replace('/'+name+'?', '/'+target+'?')
   env['AUTHOVA_TEST_'+dialect.upper()+'_DSN']=source;env['AUTHOVA_RESTORE_'+dialect.upper()+'_DSN']=restored
   run('migrate-'+dialect,['go','run','./cmd/authova','migrate'],env|{'AUTHOVA_DATABASE_DRIVER':dialect,'AUTHOVA_DATABASE_URL':admin,'AUTHOVA_MIGRATION_DATABASE_URL':admin})
  command=['go','test','-race','-count=1','-json','-timeout=5m','./internal/integration','-run']
  run('fixture',command+['^TestIdentityRecoverySnapshotFixture$'],env)
  pgdump=out/'private-postgres.dump';mydump=out/'private-mysql.sql'
  for path,cmd in [(pgdump,compose+['-e','PGPASSWORD=authova-local-database-only','postgres','pg_dump','-U','authova_migrator','-Fc','--no-owner','--no-acl',name]),(mydump,compose+['-e','MYSQL_PWD=authova-local-database-only','mysql','mysqldump','-uroot','--single-transaction','--skip-lock-tables','--set-gtid-purged=OFF','--no-tablespaces','--column-statistics=0',name])]:
   with path.open('wb') as output:subprocess.run(cmd,stdout=output,stderr=subprocess.PIPE,cwd=root,check=True)
   path.chmod(0o600)
  run('post-backup-mutation',command+['^TestIdentityRecoverySnapshotMutation$'],env)
  with pgdump.open('rb') as backup:subprocess.run(compose+['-e','PGPASSWORD=authova-local-database-only','postgres','pg_restore','-U','authova_migrator','--no-owner','--no-acl','--exit-on-error','-d',target],stdin=backup,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,cwd=root,check=True)
  with mydump.open('rb') as backup:subprocess.run(my+[target],stdin=backup,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,cwd=root,check=True)
  run('restored-authority',command+['^TestIdentityRecoverySnapshotRestored$'],env)
  # Private artifacts intentionally retain reproducible synthetic facts, while
  # the ephemeral master key is removed and both owned databases are dropped.
  metadata={p.name:{'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in [pgdump,mydump]}
  (out/'backup-metadata.json').write_text(json.dumps(metadata,indent=2)+'\n')
finally:
 for kind,db in reversed(created):
  if kind=='pg':sql(pg+['-d','authova'],f'DROP DATABASE {db} WITH (FORCE);')
  else:sql(my,f'DROP DATABASE {db}; REVOKE SELECT,INSERT,UPDATE,DELETE ON {db}.* FROM \'authova_runtime\'@\'%\';')
print('PG/MySQL actual old-backup identity recovery passed; owned databases removed.')
