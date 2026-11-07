#!/usr/bin/env python3
"""Start only the explicitly isolated migration instance; never sources old env files."""
import base64,json,os,secrets,subprocess,sys
from pathlib import Path
root=Path(__file__).resolve().parents[2]
state=root/'.migration/instance-v1'
database='authova_v1'
command=sys.argv[1] if len(sys.argv)>1 else 'serve'
if command=='init':
 if state.exists():raise SystemExit('Instance directory already exists; refusing to replace keys.')
 compose=['docker-compose','-p','authova-migration','-f',str(root/'deploy/migration/compose.yaml'),'exec','-T','-e','PGPASSWORD=authova-local-database-only','postgres','psql','-U','authova_migrator','-d','authova','-v','ON_ERROR_STOP=1','-At']
 exists=subprocess.check_output(compose+['-c',f"SELECT 1 FROM pg_database WHERE datname='{database}'"],cwd=root,text=True).strip()
 if exists:raise SystemExit('Dedicated v1 database already exists; inspect matching keys before reuse.')
 subprocess.run(compose+['-c',f'CREATE DATABASE {database}'],cwd=root,check=True,stdout=subprocess.DEVNULL)
 target=compose.copy();target[target.index('-d')+1]=database
 subprocess.run(target,input='GRANT USAGE ON SCHEMA public TO authova_runtime; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;',text=True,cwd=root,check=True,stdout=subprocess.DEVNULL)
 state.mkdir(parents=True,exist_ok=False);state.chmod(0o700)
 for name in ['master-key','console-key']:
  p=state/name;p.write_bytes(secrets.token_bytes(32));p.chmod(0o600)
 credentials={'Email':'migration-owner@example.invalid','Password':secrets.token_urlsafe(30),'totp_secret':base64.b32encode(secrets.token_bytes(20)).decode().rstrip('=')}
 p=state/'bootstrap.json';p.write_text(json.dumps(credentials));p.chmod(0o600)
if not state.exists():raise SystemExit('Run init first; this never reuses old keys')
env={k:v for k,v in os.environ.items() if not k.startswith('AUTHOVA_')}
env.update(AUTHOVA_MODE='development',AUTHOVA_LISTEN='127.0.0.1:38080',AUTHOVA_BASE_URL='http://127.0.0.1:38080',AUTHOVA_CONSOLE_URL='http://127.0.0.1:38090',AUTHOVA_CONSOLE_LISTEN='127.0.0.1:38090',AUTHOVA_CONSOLE_ASSETS=str(root/'console/web/dist'),AUTHOVA_MASTER_KEY_FILE=str(state/'master-key'),AUTHOVA_CONSOLE_KEY_FILE=str(state/'console-key'),AUTHOVA_DATABASE_URL='postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:35432/authova_v1?sslmode=disable',AUTHOVA_DATABASE_DRIVER='postgres',AUTHOVA_VALKEY_ADDRESS='127.0.0.1:36379',AUTHOVA_VALKEY_TLS='false',AUTHOVA_SMTP_ADDRESS='127.0.0.1:31025',AUTHOVA_SMTP_FROM='migration@example.invalid')
if command=='init':
 migration=env|{'AUTHOVA_MIGRATION_DATABASE_URL':'postgres://authova_migrator:authova-local-database-only@127.0.0.1:35432/authova_v1?sslmode=disable'}
 subprocess.run([str(root/'bin/authova'),'migrate'],cwd=root,env=migration,check=True)
 subprocess.run([str(root/'bin/authova'),'bootstrap'],cwd=root,env=env,input=(state/'bootstrap.json').read_bytes(),check=True)
 print('Isolated migration instance initialized. Private test credentials:',state/'bootstrap.json')
elif command=='migrate':
 migration=env|{'AUTHOVA_MIGRATION_DATABASE_URL':'postgres://authova_migrator:authova-local-database-only@127.0.0.1:35432/authova_v1?sslmode=disable'}
 subprocess.run([str(root/'bin/authova'),'migrate'],cwd=root,env=migration,check=True)
elif command=='bootstrap':
 subprocess.run([str(root/'bin/authova'),'bootstrap'],cwd=root,env=env,input=(state/'bootstrap.json').read_bytes(),check=True)
elif command=='console':os.execve(root/'bin/authova-console',[str(root/'bin/authova-console')],env)
elif command in {'serve','worker'}:os.execve(root/'bin/authova',[str(root/'bin/authova'),command],env)
else:raise SystemExit('Expected init, migrate, bootstrap, serve, worker or console')
