#!/usr/bin/env python3
"""Shared isolated localhost authentication demo profiles. Each profile retains its own DB; never switches the original demo.
Requires deploy/migration/compose.yaml under the authova-migration Compose project.
Secrets stay in .migration/demos/passkey (0600); no demo users are pre-created.
"""
from pathlib import Path
import argparse, base64, hashlib, hmac, json, os, secrets, signal, socket, struct, subprocess, sys, time, urllib.request

ROOT=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__);p.add_argument('action',choices=['start','stop','status']);p.add_argument('--profile',choices=['passkey','agent'],default='passkey');args=p.parse_args()
AGENT=args.profile=='agent';LABEL='Agent' if AGENT else 'Passkey';PREFIX=args.profile
PRIVATE=ROOT/('.migration/demos/v1/'+PREFIX);DATABASE='authova_migration_'+PREFIX+'_v1'
PORT=38280 if AGENT else 38180
CORE=f'http://localhost:{PORT}';RP=f'http://localhost:{PORT+8}';CONSOLE=f'http://localhost:{PORT+10}';RESOURCE=f'http://localhost:{PORT+9}'
PRIVATE.mkdir(parents=True,exist_ok=True);PRIVATE.chmod(0o700)
STATE=PRIVATE/'state.json'
state=json.loads(STATE.read_text()) if STATE.exists() else {'processes':{},'apps':[]}
def save(path,value):
    temporary=path.with_suffix(path.suffix+'.tmp');temporary.write_text(json.dumps(value,indent=2)+'\n');temporary.chmod(0o600);temporary.replace(path)
def persist():save(STATE,state)
def alive(item):
    try:
        command=subprocess.check_output(['ps','-p',str(item['pid']),'-o','command='],text=True).strip()
        return command==item['command']
    except subprocess.CalledProcessError:return False
def health(origin):
    try:
        with urllib.request.urlopen(origin+'/readyz',timeout=2) as r:return r.status==200
    except Exception:return False
if args.action=='status':
    print(json.dumps({'core_ready':health(CORE),'processes':{k:alive(v) for k,v in state['processes'].items()},'urls':{'core':CORE,'rp':RP,'console':CONSOLE}},indent=2));raise SystemExit
if args.action=='stop':
    for item in state['processes'].values():
        if alive(item):os.kill(item['pid'],signal.SIGTERM)
    for _ in range(100):
        if not any(alive(v) for v in state['processes'].values()):break
        time.sleep(.1)
    else:raise SystemExit('Owned processes have not stopped; inspect their logs before restarting.')
    print(f'Verified {LABEL} demo processes stopped; its database is retained.');raise SystemExit
if {'core','worker','rp','console'}.union({'resource'} if AGENT else set()).issubset(state['processes']) and all(alive(v) for v in state['processes'].values()) and health(CORE):
    print(f'{LABEL} demo running: {RP} | Console {CONSOLE}');raise SystemExit
for port in [PORT,PORT+1,PORT+8,PORT+10]+([PORT+9] if AGENT else []):
    with socket.socket() as listener:
        listener.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
        try:listener.bind(('127.0.0.1',port))
        except OSError:raise SystemExit(f'Port {port} is occupied. Inspect the owned demo before restarting.')
lock=PRIVATE/'start.lock'
fd=os.open(lock,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600);os.close(fd)
try:
    env={k:v for k,v in os.environ.items() if not k.startswith('AUTHOVA_')}
    pg=['docker-compose','-p','authova-migration','-f',str(ROOT/'deploy/migration/compose.yaml'),'exec','-T','-e','PGPASSWORD=authova-local-database-only','postgres','psql','-U','authova_migrator','-d','authova','-v','ON_ERROR_STOP=1','-At']
    exists=subprocess.check_output(pg+['-c',f"SELECT 1 FROM pg_database WHERE datname='{DATABASE}'"],env=env,cwd=ROOT,text=True).strip()=='1'
    if not exists:
        subprocess.run(pg+['-c',f'CREATE DATABASE {DATABASE}'],env=env,cwd=ROOT,check=True,stdout=subprocess.DEVNULL)
    elif not (PRIVATE/'bootstrap.json').exists():
        raise SystemExit('Existing dedicated database has no matching local metadata; inspect it before rebuilding.')
    sql=f'GRANT CONNECT ON DATABASE {DATABASE} TO authova_runtime;GRANT USAGE ON SCHEMA public TO authova_runtime;ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime;'
    target=pg.copy();target[target.index('-d')+1]=DATABASE
    subprocess.run(target,input=sql,text=True,env=env,cwd=ROOT,check=True,stdout=subprocess.DEVNULL)
    for name in ['master.key','console.key']:
        path=PRIVATE/name
        if not path.exists():path.write_bytes(secrets.token_bytes(32));path.chmod(0o600)
    bootstrap_path=PRIVATE/'bootstrap.json'
    if not bootstrap_path.exists():save(bootstrap_path,{'email':'admin@'+PREFIX+'.localhost','password':secrets.token_urlsafe(30),'totp_secret':base64.b32encode(secrets.token_bytes(20)).decode().rstrip('=')})
    bootstrap=json.loads(bootstrap_path.read_text())
    env.update(AUTHOVA_MODE='development',AUTHOVA_BASE_URL=CORE,AUTHOVA_CONSOLE_URL=CONSOLE,AUTHOVA_LISTEN=f'127.0.0.1:{PORT}',AUTHOVA_DATABASE_DRIVER='postgres',AUTHOVA_DATABASE_URL=f'postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:35432/{DATABASE}?sslmode=disable',AUTHOVA_MIGRATION_DATABASE_URL=f'postgres://authova_migrator:authova-local-database-only@127.0.0.1:35432/{DATABASE}?sslmode=disable',AUTHOVA_MASTER_KEY_FILE=str(PRIVATE/'master.key'),AUTHOVA_ENCRYPTION_KEYRING_FILE='',AUTHOVA_VALKEY_ADDRESS='127.0.0.1:36379',AUTHOVA_VALKEY_PASSWORD=os.environ.get('AUTHOVA_VALKEY_PASSWORD',''),AUTHOVA_SMTP_ADDRESS='127.0.0.1:31025',AUTHOVA_SMTP_USERNAME='',AUTHOVA_SMTP_PASSWORD='',AUTHOVA_SMTP_FROM='authova@passkey.localhost',AUTHOVA_SMS_URL='',AUTHOVA_SMS_TOKEN='',AUTHOVA_WORKER_METRICS_LISTEN=f'127.0.0.1:{PORT+1}',AUTHOVA_TRACE_OTLP_ENDPOINT=os.environ.get('AUTHOVA_TRACE_OTLP_ENDPOINT',''),AUTHOVA_TRACE_SAMPLE_RATIO=os.environ.get('AUTHOVA_TRACE_SAMPLE_RATIO','0.01'),AUTHOVA_CONSOLE_LISTEN=f'127.0.0.1:{PORT+10}',AUTHOVA_CONSOLE_KEY_FILE=str(PRIVATE/'console.key'),AUTHOVA_CONSOLE_ASSETS=str(ROOT/'console/web/dist'),AUTHOVA_DEMO_CONFIG=str(PRIVATE/'rp.json'))
    # Store the private environment for inspection/recovery, without shell eval.
    save(PRIVATE/'environment.json',{k:v for k,v in env.items() if k.startswith('AUTHOVA_')})
    for command,cwd in [([str(ROOT/'scripts/migration/go.sh'),'build','-trimpath','-o',str(PRIVATE/'authova'),'./cmd/authova'],ROOT),([str(ROOT/'scripts/migration/go.sh'),'build','-trimpath','-o',str(PRIVATE/'demo-rp'),'./cmd/demo-rp'],ROOT),(['npm','--prefix','web','run','build'],ROOT/'console'),([str(ROOT/'scripts/migration/go.sh'),'build','-trimpath','-o',str(PRIVATE/'console'),'./console/cmd/authova-console'],ROOT)]:
        with (PRIVATE/'build.log').open('ab') as log:subprocess.run(command,cwd=cwd,env=env,check=True,stdout=log,stderr=subprocess.STDOUT)
    with (PRIVATE/'migration.log').open('ab') as log:subprocess.run([str(PRIVATE/'authova'),'migrate'],cwd=ROOT,env=env,check=True,stdout=log,stderr=subprocess.STDOUT)
    if not state.get('bootstrapped'):
        with (PRIVATE/'bootstrap.log').open('ab') as log:subprocess.run([str(PRIVATE/'authova'),'bootstrap'],input=json.dumps(bootstrap).encode(),cwd=ROOT,env=env,check=True,stdout=log,stderr=subprocess.STDOUT)
        state['bootstrapped']=True;persist()
    def spawn(name,command):
        with (PRIVATE/(name+'.log')).open('ab') as log:proc=subprocess.Popen(command,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
        state['processes'][name]={'pid':proc.pid,'command':' '.join(command)};persist()
    spawn('core',[str(PRIVATE/'authova'),'serve']);spawn('worker',[str(PRIVATE/'authova'),'worker'])
    for _ in range(40):
        if health(CORE):break
        if not alive(state['processes']['core']):raise SystemExit('Core stopped; inspect private core.log.')
        time.sleep(.25)
    else:raise SystemExit('Core readiness timed out; inspect its private logs.')
    def call(method,path,data=None,token=None,key=None):
        headers={'Content-Type':'application/json'}
        if token:headers['Authorization']='Bearer '+token
        if key:headers['Idempotency-Key']=key
        request=urllib.request.Request(CORE+'/api/v1'+path,data=json.dumps(data).encode() if data is not None else None,method=method,headers=headers)
        with urllib.request.urlopen(request,timeout=20) as response:return json.load(response)
    if not state['apps']:
        if (PRIVATE/'provision-checkpoint.json').exists():raise SystemExit('Incomplete provisioning checkpoint found; inspect authoritative state before any secret-bearing replay.')
        raw=base64.b32decode(bootstrap['totp_secret']+'='*((-len(bootstrap['totp_secret']))%8));digest=hmac.new(raw,struct.pack('>Q',int(time.time())//30),hashlib.sha1).digest();offset=digest[-1]&15;totp=f'{(struct.unpack(">I",digest[offset:offset+4])[0]&0x7fffffff)%1000000:06d}'
        token=call('POST','/platform/sessions',{'email':bootstrap['email'],'password':bootstrap['password'],'totp':totp})['token']
        apps=[]
        for number in [1,2]:
            app=call('POST','/apps',{'name':f'{LABEL} App {number}','slug':f'{PREFIX}-app-{number}'},token,key=f'{PREFIX}-demo-app-{number}')
            config=call('GET',f"/apps/{app['id']}/authentication-config",token=token);config['policy']['passkey_enabled']=not AGENT
            call('PATCH',f"/apps/{app['id']}/authentication-config",{'policy':config['policy'],'config_version':config['config_version'],'reason':'Enable isolated localhost Passkey demonstration'},token)
            audience=RESOURCE+'/mcp' if AGENT and number==1 else f'urn:authova:{PREFIX}-demo:{number}'
            client=call('POST',f"/apps/{app['id']}/clients",{'name':LABEL+' Demo RP','kind':'confidential','redirect_uris':[RP+f"/apps/{app['app_key']}/callback"],'allowed_scopes':['openid','profile','email','offline_access'],'allowed_audiences':[audience],'allowed_introspection_audiences':[audience],'grant_types':['authorization_code','refresh_token','client_credentials']},token,key=f'{PREFIX}-demo-client-{number}')
            apps.append({'id':app['id'],'name':app['name'],'key':app['app_key'],'issuer':app['issuer'],'client_id':client['client_id'],'client_secret':client['client_secret'],'audience':audience})
            save(PRIVATE/'provision-checkpoint.json',{'completed_apps':apps,'warning':'Do not replay an unknown secret-bearing mutation; inspect authoritative state first.'})
        if AGENT:
            first=apps[0]
            agent=call('POST',f"/apps/{first['id']}/agents",{'name':'工单 Agent','description':'本地用户委托与自主运行示例','kind':'confidential','allowed_modes':['delegated','autonomous'],'allowed_scopes':['tickets:read'],'allowed_resources':[RESOURCE+'/mcp'],'redirect_uris':[RP+f"/apps/{first['key']}/callback"],'token_ttl_seconds':300,'grant_ttl_seconds':604800},token,key='agent-demo-agent-create')
            save(PRIVATE/'agent-created.json',agent)
            installation=call('POST',f"/apps/{first['id']}/agent-installations",{'agent_id':agent['agent']['id'],'tenant_id':'','user_id':'','responsible_party':bootstrap['email'],'resource':RESOURCE+'/mcp','allowed_scopes':['tickets:read'],'allowed_modes':['delegated','autonomous'],'expires_at':int(time.time())+30*86400,'reason':'Explicit local Agent demonstration'},token,key='agent-demo-installation-create')
            save(PRIVATE/'agent-resource.json',{'listen':f'127.0.0.1:{PORT+9}','public_url':RESOURCE,'issuer':first['issuer'],'app_id':first['id'],'resource':RESOURCE+'/mcp','resource_client_id':first['client_id'],'resource_client_secret':first['client_secret'],'tenant_id':'','agent_id':agent['agent']['id'],'user_id':''})
            save(PRIVATE/'agent-client.json',{'issuer':first['issuer'],'client_id':agent['agent']['client_id'],'client_secret':agent['client_secret'],'installation_id':installation['id'],'resource':RESOURCE+'/mcp','resource_url':RESOURCE})
            first.update(client_id=agent['agent']['client_id'],client_secret=agent['client_secret'],agent_installation_id=installation['id'],agent_resource_url=RESOURCE,scopes=['openid','offline_access','tickets:read'],name='Agent 用户委托演示')
            state['agent_id']=agent['agent']['id'];state['installation_id']=installation['id']
        state['apps']=apps;persist();save(PRIVATE/'rp.json',{'origin':RP,'apps':apps})
    if AGENT:
        for name,package in [('agent-resource','./contracts/examples/agent-resource'),('agent-client','./contracts/examples/agent-client')]:
            with (PRIVATE/'build.log').open('ab') as log:subprocess.run([str(ROOT/'scripts/migration/go.sh'),'build','-trimpath','-o',str(PRIVATE/name),package],cwd=ROOT,env=env,check=True,stdout=log,stderr=subprocess.STDOUT)
        spawn('resource',[str(PRIVATE/'agent-resource'),'-config',str(PRIVATE/'agent-resource.json')])
    save(PRIVATE/'rp.json',{'origin':RP,'console_url':CONSOLE,'apps':state['apps']})
    spawn('rp',[str(PRIVATE/'demo-rp')]);spawn('console',[str(PRIVATE/'console')])
    print(f'Isolated {LABEL} demo: {RP} | Console: {CONSOLE} | Mailpit: http://127.0.0.1:38025')
    print('Dedicated DB: '+DATABASE+'; private operator credentials: '+str(bootstrap_path))
except BaseException:
    for item in state['processes'].values():
        if alive(item):os.kill(item['pid'],signal.SIGTERM)
    raise
finally:lock.unlink(missing_ok=True)
