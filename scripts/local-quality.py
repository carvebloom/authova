#!/usr/bin/env python3
"""Local dual-database gate in disposable databases with one fresh test root.
Requires the repository's development Docker services; never uses demo DB data.
"""
from pathlib import Path
import argparse, json, os, secrets, subprocess, tempfile
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('--evidence',type=Path,required=True);p.add_argument('--test-run',help='Optional Go test filter for focused disposable-DB verification');p.add_argument('--contract-only',action='store_true',help='Run only the real Core/Contracts combination in fresh disposable databases');p.add_argument('--test-timeout',default='30m',help='Go package test timeout, including real database integration');args=p.parse_args()
if args.contract_only and args.test_run:p.error('--contract-only and --test-run are mutually exclusive')
out=args.evidence.resolve();out.mkdir(parents=True,exist_ok=True)
name='authova_quality_'+secrets.token_hex(6)
compose=['docker','compose','-f',str(root/'deploy/compose.dev.yaml'),'exec','-T']
pg=compose+['-e','PGPASSWORD=authova-local-database-only','postgres','psql','-U','authova_migrator','-v','ON_ERROR_STOP=1']
my=compose+['-e','MYSQL_PWD=authova-local-database-only','mysql','mysql','-uroot']
results=[];created_pg=False;created_my=False

def sql(base,sql):
    subprocess.run(base,input=sql.encode(),stdout=subprocess.DEVNULL,cwd=root,check=True)
def gate(label,command,env):
    with (out/(label+'.log')).open('wb') as log:
        status=subprocess.run(command,cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT).returncode
    results.append({'gate':label,'exit_code':status})
    (out/'local-quality-gates.json').write_text(json.dumps({'isolated_database':name,'gates':results},indent=2)+'\n')
    if status:raise SystemExit(label+' failed; see evidence log')
try:
    sql(pg+['-d','authova'],f'CREATE DATABASE {name};');created_pg=True
    sql(pg+['-d',name],f'GRANT CONNECT ON DATABASE {name} TO authova_runtime; GRANT USAGE ON SCHEMA public TO authova_runtime; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;')
    sql(my,f'CREATE DATABASE {name} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin; GRANT SELECT,INSERT,UPDATE,DELETE ON {name}.* TO \'authova_runtime\'@\'%\';');created_my=True
    with tempfile.TemporaryDirectory() as directory:
        key=Path(directory)/'master';key.write_bytes(secrets.token_bytes(32));key.chmod(0o600)
        env=os.environ.copy()
        env.update(AUTHOVA_TEST_DISPOSABLE_DATABASE=name,AUTHOVA_TEST_VALKEY_ADDRESS=env.get('AUTHOVA_TEST_VALKEY_ADDRESS','127.0.0.1:56379'),AUTHOVA_MODE='development',AUTHOVA_BASE_URL='http://127.0.0.1:18080',AUTHOVA_CONSOLE_URL='http://127.0.0.1:18090',AUTHOVA_MASTER_KEY_FILE=str(key),AUTHOVA_ENCRYPTION_KEYRING_FILE='')
        pg_admin=f'postgres://authova_migrator:authova-local-database-only@127.0.0.1:55432/{name}?sslmode=disable'
        my_admin=f'root:authova-local-database-only@tcp(127.0.0.1:53306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin'
        env.update(AUTHOVA_TEST_POSTGRES_DSN=f'postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:55432/{name}?sslmode=disable',AUTHOVA_TEST_MYSQL_DSN=f'authova_runtime:authova-local-runtime-only@tcp(127.0.0.1:53306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin',AUTHOVA_TEST_MIGRATOR_POSTGRES_DSN=pg_admin,AUTHOVA_TEST_MIGRATOR_MYSQL_DSN=my_admin)
        for dialect,dsn in [('postgres',pg_admin),('mysql',my_admin)]:
            migration=env|{'AUTHOVA_DATABASE_DRIVER':dialect,'AUTHOVA_DATABASE_URL':dsn,'AUTHOVA_MIGRATION_DATABASE_URL':dsn}
            gate('migrate-'+dialect,['go','run','./cmd/authova','migrate'],migration)
        if not args.contract_only:
            command=['go','test','-race','-count=1','-json','-timeout='+args.test_timeout]
            if args.test_run:command+=['-run',args.test_run]
            gate('core-race-focused' if args.test_run else 'core-race-final',command+['./...'],env)
        if not args.test_run:gate('contract-combination-final',['python3','scripts/verify-contract-combination.py'],env)
finally:
    if created_pg:sql(pg+['-d','authova'],f'DROP DATABASE {name} WITH (FORCE);')
    if created_my:sql(my,f'DROP DATABASE {name}; REVOKE SELECT,INSERT,UPDATE,DELETE ON {name}.* FROM \'authova_runtime\'@\'%\';')
label='contract-combination gates' if args.contract_only else ('focused gates' if args.test_run else 'and contract-combination gates')
print('Isolated dual-database '+label+' passed; disposable databases removed.')
