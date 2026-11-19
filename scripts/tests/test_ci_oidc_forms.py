"""HTTP actor with source-derived forms and RAM-only fake HTTP, no network/DB."""
import base64
import copy
import hashlib
from pathlib import Path
import re
import sys
import tempfile
import types
import unittest
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit

sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import test_http as actor
from test_owner import TestOwner
from deployment_http import Browser, KnownUnhealthy
from host_admission import canonical, strict_json
from release_contract import Refused
from private_native import Unknown

ROOT=Path(__file__).resolve().parents[2]
CORE='https://host.docker.internal:43101'
ISSUER=CORE+'/a/'+'a'*32


def forms(base,verbs,csrf='OWN_CSRF'):
    # Read the actual tracked template's form blocks, no Go template execution.
    template=(ROOT/'internal/transport/assets/login.html').read_text()
    selected=[]
    for verb in verbs:
        source=re.findall(r'<form\b[^>]*action="\{\{\.Base\}\}/'+verb+r'"[^>]*>.*?</form>',template)
        if verb=='register':source=[s for s in source if 'name="password"' in s]
        assert len(source)==1
        selected.append(source[0].replace('{{.Base}}',urlsplit(base).path).replace('{{.CSRF}}',csrf).replace('{{.ChallengeID}}','OWN_CHALLENGE'))
    return ''.join(selected).encode()


class OfficialFlow:
    def __init__(self):self.calls=[];self.posts=[];self.count=0;self.subject='OWN_SUBJECT';self.flow=None;self.csrf='OWN_CSRF';self.sent=None
    def request(self,url,**kw):
        self.calls.append((url,copy.deepcopy(kw)))
        method=kw.get('method','GET');body=kw.get('body');form=kw.get('form');path=urlsplit(url).path
        def result(data,status=200,headers=None):return data,headers or {},status
        if path=='/api/v1/apps':
            assert method=='POST' and kw['expected']==(201,) and body['slug'].startswith('ci-')
            return result(canonical({'id':'OWN_APP','app_key':'a'*32,'issuer':ISSUER}),201)
        if path=='/api/v1/apps/OWN_APP/clients':
            assert kw['expected']==(201,) and body['kind']=='confidential' and body['grant_types']==['authorization_code']
            assert body['allowed_scopes']==['openid','profile','email'] and body['redirect_uris']==[CORE+'/ci/callback']
            return result(canonical({'client_id':'OWN_CLIENT','client_secret':'OWN_SECRET'}),201)
        if path==urlsplit(ISSUER).path+'/authorize':
            q=parse_qs(urlsplit(url).query);assert q['code_challenge_method']==['S256']
            assert q['scope']==['openid profile email'] and q['redirect_uri']==[CORE+'/ci/callback']
            self.count+=1;self.flow=ISSUER+'/ui/flows/'+str(self.count)*43;self.query=q;self.csrf='OWN_CSRF'
            return result(b'',302,{'Location':self.flow})
        if path=='/_ci/mail':return result(canonical({'observed':float(kw['headers']['X-CI-Since'])+0.01,'code':'123456'}))
        if path=='/api/v1/apps/OWN_APP/users':
            assert parse_qs(urlsplit(url).query)=={'q':['ci-'+'1'*32+'@example.invalid'],'limit':['2']}
            return result(canonical({'items':[self.user()],'next_cursor':None}))
        if path=='/api/v1/apps/OWN_APP/users/'+self.subject:return result(canonical(self.user()))
        if url==self.flow and method=='GET':return result(forms(self.flow,('password','challenges')))
        if method=='POST':
            self.posts.append(url)
            if kw['headers']!={'Origin':CORE} or form['_csrf']!=self.csrf:raise KnownUnhealthy('OWN_CSRF_OR_ORIGIN')
            verb=url.removeprefix(self.flow+'/')
            if '/' in verb:raise KnownUnhealthy('OWN_NESTED_ACTION')
            self.csrf='OWN_CSRF_'+verb
            if verb=='challenges':
                assert form['purpose']=='register' and form['channel']=='email'
                return result(forms(self.flow,('verify',),self.csrf))
            if verb=='verify':
                assert form['challenge_id']=='OWN_CHALLENGE' and form['code']=='123456'
                return result(forms(self.flow,('register',),self.csrf))
            if verb in ('register','password'):
                assert len(form['password'])>=15
                return result(forms(self.flow,('complete','cancel'),self.csrf))
            if verb=='complete':
                return result(b'',303,{'Location':CORE+'/ci/callback?code=OWN_CODE&state='+self.query['state'][0]})
        raise AssertionError('OWN_UNEXPECTED_REQUEST')
    def user(self):
        # Official ListUsers/GetUser both mask email; ordinary AdminRole is
        # omitted by the public DTO. No plaintext-email fake compatibility.
        return {'id':self.subject,'app_id':'OWN_APP','display_name':'OWN_USER','status':'active',
                'email':'c***@example.invalid','session_epoch':0,'created_at':'2026-11-20T00:00:00Z'}


class FakeBrowser:
    def __init__(self,server):self.server=server;self.calls=[]
    def cookie_state(self):return []
    def request(self,url,**kw):
        try:
            result=self.server.request(url,**kw);self.calls.append({'status':result[2]});return result
        except KnownUnhealthy:
            self.calls.append({'status':400});raise
    json=Browser.json


def setup_actor():
    c={'schema':'authova-ci-http-private/v1','project':'authova-ci-test-'+'a'*12+'-12','scope':'1'*32,'source':'a'*40,
       'core':CORE,'console':'https://host.docker.internal:43102','ca_pem':'OWN_CA',
       'recipient':'ci-'+'1'*32+'@example.invalid','control':'OWN_CONTROL',
       'bootstrap':{'Email':'owner-'+'1'*32+'@example.invalid'},'state':{'owner-token':'OWN_OWNER'}}
    with patch.object(actor,'context',return_value=None):obj=actor.Acceptance(c)
    return obj


class OIDCFormTests(unittest.TestCase):
    def test_source_forms_registration_password_and_explicit_consent_share_stable_base(self):
        obj=setup_actor();server=OfficialFlow()
        with patch.object(actor,'Browser',side_effect=lambda *a,**kw:FakeBrowser(server)):
            obj.run('oidc-register');first=obj.state['flow']['url']
            self.assertNotIn('proof',obj.state)
            obj.run('oidc-consent');self.assertEqual(obj.state['proof']['expected_subject'],server.subject)
            first_proof=copy.deepcopy(obj.state.pop('proof'))
            obj.run('oidc-password');second=obj.state['flow']['url'];obj.run('oidc-consent')
        self.assertEqual(server.posts,[first+'/'+v for v in ('challenges','verify','register','complete')]+[second+'/'+v for v in ('password','complete')])
        self.assertNotEqual(first,second);self.assertEqual(obj.state['proof']['expected_subject'],first_proof['expected_subject'])
        for proof in (first_proof,obj.state['proof']):
            challenge=base64.urlsafe_b64encode(hashlib.sha256(proof['code_verifier'].encode()).digest()).decode().rstrip('=')
            self.assertEqual(len(challenge),43);self.assertEqual(proof['expected_state'],proof['callback_state'])
            self.assertEqual(proof['issuer'],ISSUER);self.assertTrue(proof['nonce'])
    def test_post_200_response_url_is_not_the_next_form_base(self):
        server=OfficialFlow();server.flow=ISSUER+'/ui/flows/'+'2'*43
        old=server.flow+'/challenges/verify'
        with self.assertRaisesRegex(KnownUnhealthy,'OWN_NESTED_ACTION'):
            server.request(old,method='POST',headers={'Origin':CORE},form={'_csrf':server.csrf})
        obj=setup_actor();obj.state.update(app={'issuer':ISSUER},flow={'url':server.flow})
        page=forms(server.flow,('verify',));b=FakeBrowser(server)
        obj.form(b,server.flow+'/challenges',page,'verify',{'challenge_id':'OWN_CHALLENGE','code':'123456'})
        self.assertEqual(server.posts[-1],server.flow+'/verify')
    def test_exact_identifier_query_masked_detail_and_repeat_subject(self):
        obj=setup_actor();server=OfficialFlow();obj.state['app']={'id':'OWN_APP'}
        with patch.object(actor,'Browser',side_effect=lambda *a,**kw:FakeBrowser(server)):
            obj.read_user();obj.read_user()
        expected=CORE+'/api/v1/apps/OWN_APP/users?q=ci-'+'1'*32+'%40example.invalid&limit=2'
        self.assertEqual([u for u,_ in server.calls],[expected,CORE+'/api/v1/apps/OWN_APP/users/OWN_SUBJECT']*2)
        self.assertEqual(obj.state['user']['id'],server.subject)
        self.assertEqual(obj.state['user']['email'],'c***@example.invalid')
        # A plaintext assertion cannot pass the actual masked DTO.
        self.assertNotEqual(obj.state['user']['email'],obj.c['recipient'])
    def test_user_readback_rejects_ambiguity_types_role_and_changed_identity(self):
        obj=setup_actor();obj.state['app']={'id':'OWN_APP'};user=OfficialFlow().user()
        good={'items':[user],'next_cursor':None}
        cases=[(None,user),(dict(good,items=None),user),(dict(good,items=[user,user]),user),
               (dict(good,next_cursor=''),user),({'items':[user]},user)]
        for change in ({'id':None},{'id':True},{'app_id':'OTHER'},{'status':'disabled'},{'admin_role':'owner'},
                       {'admin_role':None},{'email':'ci-'+'1'*32+'@example.invalid'},{'email':'c***@other.invalid'},{'email':True}):
            bad=dict(user,**change);cases.extend([(dict(good,items=[bad]),user),(good,bad)])
        cases.append((good,dict(user,id='OTHER_SUBJECT')))
        for page,detail in cases:
            calls=[]
            def api(path):calls.append(path);return page if len(calls)==1 else detail
            obj.api=api
            with self.subTest(page=page,detail=detail),self.assertRaises(Refused):obj.read_user()
            self.assertNotIn('user',obj.state)
            self.assertLessEqual(len(calls),2)
        obj.state['user']=dict(user,id='PREVIOUS_SUBJECT');obj.api=lambda path:good if '?' in path else user
        with self.assertRaisesRegex(Refused,'CI_REPEAT_SUBJECT'):obj.read_user()
        self.assertEqual(obj.state['user']['id'],'PREVIOUS_SUBJECT')
    def test_missing_duplicate_cross_origin_wrong_flow_and_consent_are_rejected_before_post(self):
        obj=setup_actor();base=ISSUER+'/ui/flows/'+'2'*43;obj.state.update(app={'issuer':ISSUER},flow={'url':base})
        b=types.SimpleNamespace(request=lambda *a,**kw:self.fail('NO_POST'))
        for action in ('https://other.invalid/verify',urlsplit(base).path+'/other/verify',urlsplit(base).path+'/register',''):
            page=('<form action="'+action+'"><input name="_csrf" value="OWN"></form>').encode()
            with self.subTest(action=action),self.assertRaises(Refused):obj.form(b,base+'/challenges',page,'verify',{})
        page=forms(base,('verify',))
        with self.assertRaises(Refused):obj.form(b,base,page+page,'verify',{})
        with self.assertRaisesRegex(Refused,'CI_EXPLICIT_CONSENT_REQUIRED'):obj.consent(page)
        obj.state['flow']['url']='https://other.invalid/ui/flows/'+'2'*43
        with self.assertRaisesRegex(Refused,'CI_FLOW'):obj.form(b,base,page,'verify',{})
    def test_failure_projection_and_owner_only_persist_closed_metadata_preserve_first(self):
        obj=setup_actor();obj.checkpoint='VERIFY';obj.last_status=400;obj.request_ordinal=8
        frame=actor.failure_frame(obj,True)
        for malformed in (dict(frame,status=True),dict(frame,ordinal=True),dict(frame,checkpoint='OWN_PRIVATE_CANARY'),dict(frame,body='OWN_PRIVATE_CANARY')):
            self.assertFalse(actor.valid_failure_frame(malformed,1))
        with tempfile.TemporaryDirectory() as tmp:
            owner=TestOwner.__new__(TestOwner);owner.proxy='OWN_PROXY';owner.operation=Path(tmp).resolve();owner.operation.chmod(0o700);owner.context={'private':'OWN_PRIVATE_CANARY'}
            owner.native=types.SimpleNamespace(run=lambda *a,**kw:(1,canonical(frame)))
            with self.assertRaisesRegex(KnownUnhealthy,'CI_HTTP_ASSERTION'):owner.http_actor('oidc-register',lambda:None)
            raw=(owner.operation/'http-failure.json').read_bytes();self.assertNotIn(b'CANARY',raw)
            self.assertEqual(strict_json(raw)['observation'],frame)
            with patch.object(owner,'write',side_effect=OSError('OWN_PRIVATE_CANARY')):
                with self.assertRaises(KnownUnhealthy) as first:owner.http_actor('oidc-register',lambda:None)
                self.assertEqual(first.exception.args,('CI_HTTP_ASSERTION',));self.assertEqual(first.exception.close_secondary,('HTTP_FAILURE_REPORT',))
            owner.native=types.SimpleNamespace(run=lambda *a,**kw:(2,canonical(actor.failure_frame(obj,False))))
            with patch.object(owner,'write') as write,self.assertRaises(Unknown):owner.http_actor('oidc-register',lambda:None)
            self.assertEqual(write.call_args.args[1]['observation']['code'],'CI_HTTP_OUTCOME_UNKNOWN')
            for malformed in (dict(frame,body='OWN_PRIVATE_CANARY'),dict(frame,checkpoint='OWN_PRIVATE_CANARY'),dict(frame,status=True)):
                owner.native=types.SimpleNamespace(run=lambda *a,**kw:(1,canonical(malformed)))
                with patch.object(owner,'write') as write,self.assertRaises(Unknown):owner.http_actor('oidc-register',lambda:None)
                write.assert_not_called()
    def test_actor_exception_frame_never_serializes_private_exception_or_context(self):
        import io
        for error,expected_rc in ((KnownUnhealthy('OWN_PRIVATE_CANARY'),1),(Unknown('OWN_PRIVATE_CANARY'),2)):
            obj=setup_actor();obj.checkpoint='REGISTER';obj.last_status=409;obj.request_ordinal=9
            output=types.SimpleNamespace(buffer=io.BytesIO())
            # __main__ uses its own Acceptance definition; replace only main so
            # the actual guarded exception boundary and projector run unchanged.
            source=(ROOT/'scripts/ci/test_http.py').read_text();source=source.replace('try:main()','try:OWN_main()')
            space={'__name__':'__main__','__file__':str(ROOT/'scripts/ci/test_http.py')}
            def own_main():space['ACTIVE_ACTOR']=obj;raise error
            space['OWN_main']=own_main
            with patch.object(sys,'stdout',output),self.assertRaises(SystemExit) as result:
                exec(compile(source,'OWN_PINNED_BOUNDARY','exec'),space)
            self.assertEqual(result.exception.code,expected_rc)
            value=strict_json(output.buffer.getvalue());self.assertNotIn(b'CANARY',output.buffer.getvalue())
            self.assertTrue(actor.valid_failure_frame(value,expected_rc))
            self.assertEqual((value['checkpoint'],value['status'],value['ordinal']),('REGISTER',409,9))


if __name__=='__main__':unittest.main()
