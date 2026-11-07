#!/usr/bin/env python3
"""Provision only Apps and RP Clients through the real management API; no demo users."""
import base64
import hashlib
import hmac
import json
import struct
import time
import urllib.request
import urllib.error
from pathlib import Path

root=Path(__file__).resolve().parents[1]
config_path=root/'.dev/rp.json'
saved=json.loads(config_path.read_text()) if config_path.exists() else {'apps': []}
bootstrap=json.loads((root/'.dev/bootstrap.json').read_text())
key=base64.b32decode(bootstrap['totp_secret']+'='*((-len(bootstrap['totp_secret']))%8))
def current_code():
    digest=hmac.new(key,struct.pack('>Q',int(time.time())//30),hashlib.sha1).digest()
    offset=digest[-1]&15
    return f'{(struct.unpack(">I",digest[offset:offset+4])[0]&0x7fffffff)%1000000:06d}'
origin='http://127.0.0.1:18080'
def call(method,path,data=None,token=None):
    req=urllib.request.Request(origin+'/api/v1'+path,method=method,data=json.dumps(data).encode() if data is not None else None)
    req.add_header('Content-Type','application/json')
    req.add_header('Idempotency-Key','demo-'+str(time.time_ns()))
    if token:req.add_header('Authorization','Bearer '+token)
    with urllib.request.urlopen(req,timeout=20) as response:return json.load(response)
for attempt in range(2):
    try:
        login=call('POST','/platform/sessions',{'email':bootstrap['email'],'password':bootstrap['password'],'totp':current_code()})
        break
    except urllib.error.HTTPError as error:
        if error.code != 401 or attempt:
            raise SystemExit(f'Operator login rejected ({error.code}); inspect local credentials and operator status.')
        print('Waiting for a fresh TOTP window before one login retry.')
        time.sleep(31-int(time.time())%30)
token=login['token']
apps=[]
existing=call('GET','/apps',token=token)['items']
for slug,name in [('demo-notes','Notes / App 1'),('demo-studio','Studio / App 2')]:
    app=next((a for a in existing if a['slug']==slug),None)
    if app is None:app=call('POST','/apps',{'name':name,'slug':slug},token)
    previous=next((a for a in saved['apps'] if a['key']==app['app_key']),None)
    if previous:
        current=call('GET',f"/apps/{app['id']}/clients/{previous['client_id']}",token=token)
        current=call('PATCH',f"/apps/{app['id']}/clients/{previous['client_id']}",{'name':current['name'],'config_version':current['config_version'],'redirect_uris':[f"http://127.0.0.1:18088/apps/{app['app_key']}/callback"],'allowed_scopes':current['allowed_scopes'],'allowed_audiences':current['allowed_audiences'],'allowed_introspection_audiences':current['allowed_introspection_audiences'],'grant_types':current['grant_types']},token)
        client={**current,'client_secret':previous['client_secret']}
    else:
        client=call('POST',f"/apps/{app['id']}/clients",{'name':'Demo RP','kind':'confidential','redirect_uris':[f"http://127.0.0.1:18088/apps/{app['app_key']}/callback"],'allowed_scopes':['openid','profile','email','phone','offline_access'],'allowed_audiences':[f"urn:authova:demo:{slug}"],'allowed_introspection_audiences':[f"urn:authova:demo:{slug}"],'grant_types':['authorization_code','refresh_token','client_credentials']},token)
    if slug=='demo-notes': upstream_app=app
    if slug=='demo-studio': downstream_app=app
    call('POST',f"/apps/{app['id']}/management-client",{},token)

    apps.append({'name':name,'key':app['app_key'],'issuer':app['issuer'],'client_id':client['client_id'],'client_secret':client['client_secret'],'audience':f'urn:authova:demo:{slug}'})
connections=call('GET',f"/apps/{downstream_app['id']}/connections",token=token)['items']
if not any(c['name']=='Local OIDC provider' for c in connections):
    upstream=call('POST',f"/apps/{upstream_app['id']}/clients",{'name':'Local OIDC identity provider test','kind':'confidential','redirect_uris':[downstream_app['issuer']+'/ui/federation/callback'],'allowed_scopes':['openid','profile','email'],'allowed_audiences':['urn:authova:local-idp'],'allowed_introspection_audiences':[],'grant_types':['authorization_code']},token)
    call('POST',f"/apps/{downstream_app['id']}/connections",{'name':'Local OIDC provider','issuer':upstream_app['issuer'],'client_id':upstream['client_id'],'client_secret':upstream['client_secret']},token)
config_path.write_text(json.dumps({'origin':'http://127.0.0.1:18088','apps':apps},indent=2))
config_path.chmod(0o600)
print('Two Apps and real RP Clients provisioned. Register users through the browser and Mailpit.')
