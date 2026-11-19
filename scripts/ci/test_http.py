"""Private RAM HTTP acceptance actor for only one fresh CI fixture.

Executed inside its proxy by the installed owner. Context/stdout stay in private
pipes; only the bounded evidence projection leaves the owner, never this context.
"""
import base64
import hashlib
import hmac
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import secrets
import signal
import struct
import sys
import time
import urllib.parse

sys.path.insert(0,str(Path(__file__).parent))
from deployment_http import Browser, KnownUnhealthy, context
from host_admission import canonical, strict_json
from private_native import Unknown
from release_contract import need


class Fields(HTMLParser):
    def __init__(self,body):
        super().__init__();self.inputs={};self.actions=[];self.feed(body.decode('utf-8'))
    def handle_starttag(self,tag,items):
        attrs=dict(items)
        if tag=='input' and attrs.get('name') in ('_csrf','challenge_id'):
            key=attrs['name'];value=attrs.get('value','')
            need(key not in self.inputs or self.inputs[key]==value,'FORM_DUPLICATE_VALUE')
            self.inputs[key]=value
        if tag=='form' and 'action' in attrs:self.actions.append(attrs['action'])


class Acceptance:
    def __init__(self,c):
        need(type(c) is dict and set(c)=={'schema','project','scope','source','core','console','ca_pem','recipient','control','bootstrap','state'},'CI_HTTP_KEYS')
        need(c['schema']=='authova-ci-http-private/v1' and re.fullmatch('[0-9a-f]{32}',c['scope']) and re.fullmatch('[0-9a-f]{40}',c['source'])
             and re.fullmatch('authova-ci-test-'+c['source'][:12]+'-[1-9][0-9]*',c['project']),'CI_HTTP_SCOPE')
        need(c['recipient']=='ci-'+c['scope']+'@example.invalid' and c['bootstrap']['Email']=='owner-'+c['scope']+'@example.invalid','CI_HTTP_IDENTITY_SCOPE')
        ports={}
        for key,internal in (('core',8443),('console',8444)):
            u=urllib.parse.urlsplit(c[key]);need(u.scheme=='https' and u.hostname=='host.docker.internal' and u.port is not None
                   and not u.path and not u.query and not u.fragment and not u.username and not u.password,'CI_HTTP_ORIGIN')
            ports[u.port]=internal
        need(len(ports)==2,'CI_HTTP_PORTS')
        self.c=c;self.state=c['state'];self.origins=(c['core'],c['console']);self.ssl=context(c['ca_pem']);self.ports=ports
        self.i,self.m=time.time(),time.monotonic();self.calls=[]
        self.cap=230
    def guard(self):
        need(self.i<=time.time()<self.i+self.cap and self.m<=time.monotonic()<self.m+self.cap,'CI_HTTP_ACTOR_EXPIRED')
    def full(self,whole,suffix):
        self.guard();need(time.time()+whole+suffix<self.i+self.cap and time.monotonic()+whole+suffix<self.m+self.cap,'CI_HTTP_ACTOR_SUFFIX')
    def browser(self,key=None,cookies=None):
        def guard():self.guard()
        guard.full=self.full
        b=Browser(self.origins,self.ssl,guard,loopback=True,ports=self.ports,
                  cookies=self.state.get('cookies-'+key,[]) if key and cookies is None else (cookies or []))
        original=b.request
        def request(*args,**kw):
            try:return original(*args,**kw)
            finally:
                if key:self.state['cookies-'+key]=b.cookie_state()
        b.request=request
        self.calls.append(b)
        return b
    def pause(self,seconds):
        end=time.monotonic()+seconds
        while time.monotonic()<end:self.guard();time.sleep(min(.1,end-time.monotonic()))
    def login(self,account,*,bff=False):
        self.guard();steps=self.state.setdefault('totp-steps',{})
        while int(time.time())//30<=steps.get(account['Email'],-1):self.pause(.2)
        raw=account['totp_secret'];key=base64.b32decode(raw+'='*(-len(raw)%8));step=int(time.time())//30
        value=hmac.new(key,struct.pack('>Q',step),hashlib.sha1).digest();offset=value[-1]&15
        code=str((struct.unpack('>I',value[offset:offset+4])[0]&0x7fffffff)%1000000).zfill(6)
        payload={'email':account['Email'],'password':account['Password'],'totp':code}
        b=self.browser('management' if bff else None)
        if bff:
            result=b.json(self.c['console']+'/bff/login',method='POST',body=payload,headers={'Origin':self.c['console']})
        else:
            result=b.json(self.c['core']+'/api/v1/platform/sessions',method='POST',body=payload,expected=(201,))
            self.state.setdefault('tokens',[]).append(result['token'])
        steps[account['Email']]=step
        return result,b
    def api(self,path,*,method='GET',body=None,token=None,expected=(200,)):
        headers={'Authorization':'Bearer '+(token or self.state['owner-token'])}
        if method=='POST':headers['Idempotency-Key']='ci-'+secrets.token_hex(16)
        return self.browser().json(self.c['core']+'/api/v1'+path,method=method,body=body,headers=headers,expected=expected)
    def flow(self):
        a=self.state['app'];client=self.state['client'];verifier=secrets.token_urlsafe(32)
        state=secrets.token_urlsafe(32);nonce=secrets.token_urlsafe(32);now=time.time()
        self.state['flow']={'state':state,'nonce':nonce,'verifier':verifier,'origin':now}
        query=urllib.parse.urlencode({'client_id':client['client_id'],'redirect_uri':self.c['core']+'/ci/callback',
                 'response_type':'code','scope':'openid profile email','state':state,'nonce':nonce,
                 'code_challenge':base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip('='),'code_challenge_method':'S256'})
        b=self.browser('oidc');body,url=self.follow(b,a['issuer']+'/authorize?'+query)
        need('/ui/flows/' in urllib.parse.urlsplit(url).path and Fields(body).inputs.get('_csrf'),'CI_FLOW')
        self.state['flow'].update(url=url,page=body.decode())
        return b,body,url
    def follow(self,b,url,*,method='GET',form=None):
        for _ in range(6):
            body,headers,status=b.request(url,method=method,form=form,headers={'Origin':self.c['core']} if method!='GET' else {},expected=(200,302,303))
            if status==200:return body,url
            url=urllib.parse.urljoin(url,headers['Location']);u=urllib.parse.urlsplit(url)
            if u.path=='/ci/callback':
                need(u.scheme+'://'+u.netloc==self.c['core'],'CI_CALLBACK_ORIGIN')
                q=urllib.parse.parse_qs(u.query,strict_parsing=True)
                need(set(q)=={'code','state'} and len(q['code'])==len(q['state'])==1
                     and hmac.compare_digest(q['state'][0],self.state['flow']['state']),'CI_CALLBACK_STATE')
                now=time.time();f=self.state['flow']
                self.state['proof']={'issuer':self.state['app']['issuer'],'client_id':self.state['client']['client_id'],
                   'client_secret':self.state['client']['client_secret'],'redirect_uri':self.c['core']+'/ci/callback',
                   'code':q['code'][0],'expected_state':f['state'],'callback_state':q['state'][0],'nonce':f['nonce'],
                   'code_verifier':f['verifier'],'expected_subject':self.state['user']['id'],'ca_pem':self.c['ca_pem'],
                   'flow_origin_utc':f['origin'],'state_deadline_utc':f['origin']+300,'code_observed_utc':now,'code_deadline_utc':now+60}
                return b'',url
            need(u.scheme+'://'+u.netloc==self.c['core'],'CI_FLOW_REDIRECT')
            method='GET';form=None
        raise Unknown('CI_FLOW_REDIRECT_LIMIT')
    def form(self,b,url,body,action,data):
        fields=Fields(body);target=urllib.parse.urljoin(url,action)
        need(urllib.parse.urlsplit(target).scheme+'://'+urllib.parse.urlsplit(target).netloc==self.c['core'],'CI_FORM_ORIGIN')
        values=dict(data,_csrf=fields.inputs['_csrf'])
        return self.follow(b,target,method='POST',form=values)
    def read_user(self):
        query=urllib.parse.urlencode({'q':self.c['recipient'],'limit':2})
        users=self.api('/apps/'+self.state['app']['id']+'/users?'+query)
        need(len(users['items'])==1 and users['next_cursor'] is None,'CI_UNIQUE_USER')
        u=users['items'][0];need(u['app_id']==self.state['app']['id'] and u['status']=='active' and u.get('admin_role','')=='' and u.get('email')==self.c['recipient'],'CI_ORDINARY_SERVER_USER')
        if 'user' in self.state:need(u['id']==self.state['user']['id'],'CI_REPEAT_SUBJECT')
        self.state['user']=u
    def run(self,stage):
        if stage=='normal-tls-health-and-static-assets':
            b=self.browser();b.request(self.c['core']+'/readyz');b.request(self.c['console']+'/healthz')
            page,_,_=b.request(self.c['console']+'/')
            assets=list(dict.fromkeys(re.findall(rb'(?:src|href)="(/assets/[A-Za-z0-9_.-]+)"',page)))
            need(assets,'CI_STATIC_ENTRY')
            for path in assets:b.request(self.c['console']+path.decode('ascii'))
        elif stage=='guest-and-authorization-boundaries':
            b=self.browser();b.request(self.c['core']+'/api/v1/apps',expected=(401,))
            b.request(self.c['console']+'/bff/me',expected=(401,))
            b.request(self.c['console']+'/bff/login',method='POST',body={},headers={'Origin':'https://untrusted.invalid'},expected=(403,))
        elif stage=='management-password-mfa-logout':
            bad=dict(self.c['bootstrap'],Password='wrong password for only this new CI owner')
            # Explicit negative, no retry/fallback; no TOTP consumed by bad password.
            b=self.browser();b.request(self.c['core']+'/api/v1/platform/sessions',method='POST',body={'email':bad['Email'],'password':bad['Password'],'totp':'000000'},expected=(401,))
            result,_=self.login(self.c['bootstrap']);self.state['owner-token']=result['token'];self.state['owner-id']=result['operator']['id']
            result,b=self.login(self.c['bootstrap'],bff=True)
            need(any(c.secure and c.has_nonstandard_attr('HttpOnly') and c.get_nonstandard_attr('SameSite')=='Lax' for c in b.jar),'CI_MANAGEMENT_COOKIE')
            b.request(self.c['console']+'/bff/me')
            old=b.cookie_state()
            b.request(self.c['console']+'/bff/logout',method='POST',body={},headers={'Origin':self.c['console']},expected=(403,))
            b.request(self.c['console']+'/bff/logout',method='POST',body={},headers={'Origin':self.c['console'],'X-CSRF-Token':result['csrf_token']},expected=(204,))
            self.browser(cookies=old).request(self.c['console']+'/bff/me',expected=(401,))
        elif stage=='oidc-register':
            self.state['app']=self.api('/apps',method='POST',body={'name':'Isolated CI acceptance','slug':'ci-'+self.c['scope'][:16]},expected=(201,))
            app=self.state['app'];need(app['issuer']==self.c['core']+'/a/'+app['app_key'],'CI_EXACT_ISSUER')
            self.state['client']=self.api('/apps/'+app['id']+'/clients',method='POST',body={'name':'CI single ordinary user','kind':'confidential',
               'redirect_uris':[self.c['core']+'/ci/callback'],'allowed_scopes':['openid','profile','email'],'allowed_audiences':['urn:authova:ci:'+self.c['scope']],
               'allowed_introspection_audiences':[],'grant_types':['authorization_code']},expected=(201,))
            self.state['user-password']=secrets.token_urlsafe(32)
            b,page,url=self.flow();sent=time.time()
            page,url=self.form(b,url,page,url+'/challenges',{'purpose':'register','channel':'email','destination':self.c['recipient']})
            challenge=Fields(page).inputs['challenge_id']
            code=None;deadline=time.monotonic()+45
            for _ in range(46):
                raw,_,status=b.request(self.c['core']+'/_ci/mail',headers={'X-CI-Control':self.c['control'],'X-CI-Since':str(sent)},expected=(200,202))
                if status==200:
                    message=strict_json(raw);need(message['observed']>=sent and re.fullmatch('[0-9]{6}',message['code']),'CI_FRESH_MAIL')
                    code=message['code'];break
                if time.monotonic()>=deadline:break
                self.pause(1)
            need(code is not None,'CI_MAIL_NOT_DELIVERED')
            page,url=self.form(b,url,page,url+'/verify',{'challenge_id':challenge,'code':code})
            page,url=self.form(b,url,page,url+'/register',{'password':self.state['user-password'],'display_name':'CI ordinary user'})
            self.read_user();need(url+'/complete' in Fields(page).actions or urllib.parse.urlsplit(url).path+'/complete' in Fields(page).actions,'CI_EXPLICIT_CONSENT_REQUIRED')
            self.state['flow'].update(page=page.decode(),url=url)
        elif stage=='oidc-password':
            b,page,url=self.flow()
            page,url=self.form(b,url,page,url+'/password',{'kind':'email','identifier':self.c['recipient'],'password':self.state['user-password']})
            self.read_user();need(urllib.parse.urlsplit(url).path+'/complete' in Fields(page).actions or url+'/complete' in Fields(page).actions,'CI_EXPLICIT_CONSENT_REQUIRED')
            self.state['flow'].update(page=page.decode(),url=url)
        elif stage=='oidc-consent':
            f=self.state['flow'];b=self.browser('oidc')
            self.form(b,f['url'],f['page'].encode(),f['url']+'/complete',{})
            need('proof' in self.state and self.state['proof']['expected_state']==f['state'],'CI_CODE_NOT_OBSERVED')
        elif stage=='oidc-replay-logout':
            r=self.state['proof'];client=self.state['client'];b=self.browser('oidc')
            auth=base64.b64encode((client['client_id']+':'+client['client_secret']).encode()).decode()
            b.request(self.state['app']['issuer']+'/oauth/token',method='POST',form={'grant_type':'authorization_code','code':r['code'],'redirect_uri':r['redirect_uri'],'code_verifier':r['code_verifier']},headers={'Authorization':'Basic '+auth},expected=(400,))
            # An App-user cookie is never a management session.
            b.request(self.c['console']+'/bff/apps',expected=(401,))
            url=self.state['app']['issuer']+'/ui/logout';page,_,_=b.request(url);old=b.cookie_state()
            b.request(url,method='POST',form={'_csrf':Fields(page).inputs['_csrf']},headers={'Origin':self.c['core']})
            self.browser(cookies=old).request(self.state['app']['issuer']+'/ui/account',expected=(401,))
        elif stage=='operator-password-and-session-revocation':
            result,_=self.login(self.c['bootstrap']);self.state['owner-token']=result['token']
            password=secrets.token_urlsafe(32);new_password=secrets.token_urlsafe(32)
            created=self.api('/platform/operators',method='POST',body={'email':'admin-'+self.c['scope']+'@example.invalid','password':password,'role':'admin'},expected=(201,))
            target=created['operator'];account={'Email':target['email'],'Password':password,'totp_secret':created['totp_secret']}
            one,_=self.login(account);two,_=self.login(account)
            path='/platform/operators/'+target['id']
            self.api('/platform/operators/'+self.state['owner-id'],method='PATCH',token=one['token'],body={'current_password':password,'password':new_password,'reason':'CI reject admin reset of owner'},expected=(403,))
            self.api(path,method='PATCH',body={'current_password':'invalid owner current password','password':new_password,'reason':'CI reject wrong actor proof'},expected=(403,))
            self.api('/platform/me',token=one['token']);self.api('/platform/me',token=two['token'])
            changed=self.api(path,method='PATCH',body={'current_password':self.c['bootstrap']['Password'],'password':new_password,'reason':'CI own target password acceptance'})
            need(changed=={'updated':True},'CI_PASSWORD_UPDATE_ACK')
            operators=self.api('/platform/operators')
            targets=[v for v in operators['items'] if v['id']==target['id']]
            need(len(targets)==1 and targets[0]['role']=='admin' and targets[0]['status']=='active','CI_UNRELATED_ROLE_CHANGE')
            self.api('/platform/me',token=one['token'],expected=(401,));self.api('/platform/me',token=two['token'],expected=(401,))
            self.api('/platform/me')
            self.api(path,method='PATCH',token=one['token'],body={'current_password':password,'password':secrets.token_urlsafe(32),'reason':'CI stale revoked proof'},expected=(401,))
            while int(time.time())//30<=self.state['totp-steps'].get(account['Email'],-1):self.pause(.2)
            # Negative old password before the one permitted positive replacement login.
            b=self.browser();b.request(self.c['core']+'/api/v1/platform/sessions',method='POST',body={'email':account['Email'],'password':password,'totp':'000000'},expected=(401,))
            account['Password']=new_password;self.login(account)
            self.state['revoked-tokens']=[one['token'],two['token']]
        elif stage=='owned-test-io-and-session-closure':
            revoked=set(self.state.get('revoked-tokens',[]))
            for token in self.state.get('tokens',[]):
                if token not in revoked:
                    b=self.browser();b.request(self.c['core']+'/api/v1/platform/sessions',method='DELETE',headers={'Authorization':'Bearer '+token},expected=(204,))
                self.api('/platform/me',token=token,expected=(401,))
        else:raise Unknown('CI_HTTP_STAGE')
        self.guard()
        return {'stage':stage,'requests':[x for b in self.calls for x in b.calls],'result':'PASS'}


def main():
    raw=sys.stdin.buffer.read(262145)
    need(len(raw)<=262144 and len(sys.argv)==2,'CI_HTTP_INPUT')
    actor=Acceptance(strict_json(raw))
    actor.cap=230 if sys.argv[1]=='operator-password-and-session-revocation' else (45 if sys.argv[1]=='oidc-consent' else 170)
    signal.signal(signal.SIGALRM,lambda *_:(_ for _ in ()).throw(Unknown('CI_HTTP_ACTOR_TIMEOUT')))
    signal.alarm(actor.cap+3)
    try:result={'context':actor.c,'evidence':actor.run(sys.argv[1])}
    finally:signal.alarm(0)
    sys.stdout.buffer.write(canonical(result));sys.stdout.buffer.flush()


if __name__=='__main__':
    try:main()
    except KnownUnhealthy:
        sys.stdout.write('{"result":"FAIL","code":"CI_HTTP_ASSERTION"}');raise SystemExit(1) from None
    except BaseException:
        sys.stdout.write('{"result":"UNKNOWN","code":"CI_HTTP_OUTCOME_UNKNOWN"}');raise SystemExit(2) from None
