"""Both real acceptance consumers with an owned in-memory HTTP actor, no IO."""
import sys
import hashlib
import json
from pathlib import Path
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import deployment_http as http
import test_http as actor
from release_contract import Refused

CORE='https://own-core.invalid'
CONSOLE='https://own-console.invalid'
HTML=b'''<link rel="icon" href="./favicon.svg"><script src="./assets/index-OWN.js"></script>
<link rel="stylesheet" href="./assets/index-OWN.css"><link rel="preload" href="/assets/index-OWN.js">'''

BODIES={'/index.html':HTML,'/assets/index-OWN.js':b'console.log(1)','/assets/index-OWN.css':b'body{}'}
EXPECTED={p:{'bytes':len(v),'sha256':hashlib.sha256(v).hexdigest()} for p,v in BODIES.items()}

class BrowserActor:
    def __init__(self,page=HTML,fail=None):self.page=page;self.fail=fail;self.calls=[]
    def request(self,url,**kw):
        self.calls.append((url,kw))
        if self.fail and self.fail in url:raise http.KnownUnhealthy('OWN_ASSET_FAILED')
        path=url.removeprefix(CONSOLE)
        body=self.page if path=='/' else BODIES.get(path,b'OWN_RESPONSE')
        mime='text/html' if path=='/' else ('text/javascript' if path.endswith('.js') else 'text/css')
        return (body,{'Content-Type':mime},404 if 'authova-missing-' in path else 200)

class StaticAssetsTests(unittest.TestCase):
    def test_relative_absolute_and_mixed_entries_deduplicate_to_same_canonical_files(self):
        expected=['/assets/index-OWN.js','/assets/index-OWN.css']
        for page in (HTML,HTML.replace(b'./assets/',b'/assets/'),HTML.replace(b'"',b"'")):
            self.assertEqual(http.static_assets(page),expected)
    def test_escape_external_base_ambiguity_query_and_fragment_are_rejected(self):
        for path in ('../assets/x.js','./assets/../private','/assets/../private','//evil.invalid/assets/x.js',
                     'https://evil.invalid/x.js','./assets/x.js?x=1','/assets/x.js#x','/assets/%2e%2e','./assets/x/y.js',
                     '/assets/..','/assets/&#46;&#46;/secret'):
            with self.subTest(path=path),self.assertRaises(Refused):
                http.static_assets(('<script src="'+path+'"></script>').encode())
        for page in (b'<base href="https://evil.invalid/">'+HTML,b'<script src="/assets/a.js" src="/assets/b.js">',
                     b'<link rel="stylesheet" rel="icon" href="https://evil.invalid/a.css">'):
            with self.subTest(page=page),self.assertRaises(Refused):http.static_assets(page)
    def test_both_actual_consumers_get_every_canonical_asset_and_preserve_failure(self):
        expected=[CORE+'/readyz',CONSOLE+'/healthz',CONSOLE+'/',CONSOLE+'/assets/index-OWN.js',CONSOLE+'/assets/index-OWN.css']
        for mode in ('ci','formal'):
            for failing in (False,True):
                with self.subTest(mode=mode,failing=failing):
                    b=BrowserActor(fail='index-OWN.css' if failing else None)
                    if mode=='ci':
                        obj=actor.Acceptance.__new__(actor.Acceptance);obj.c={'core':CORE,'console':CONSOLE,'web_assets':EXPECTED};obj.calls=[b];obj.browser=lambda:b;obj.guard=lambda:None
                        run=lambda:obj.run('normal-tls-health-and-static-assets')
                    else:
                        def run():
                            with patch.object(http,'Browser',return_value=b) as factory,patch.object(http,'context',return_value='OWN_CA'):
                                result=http.readonly_acceptance({'core_origin':CORE,'console_origin':CONSOLE,'ca_pem':'OWN_PUBLIC_CA'},lambda:None,expected_assets=EXPECTED)
                                self.assertEqual(factory.call_args.args[1],'OWN_CA')
                                self.assertEqual(factory.call_args.kwargs,{})  # formal keeps its normal non-loopback transport.
                                return result
                    if failing:
                        with self.assertRaisesRegex(Refused,'OWN_ASSET_FAILED'):run()
                    else:run()
                    self.assertEqual([url for url,_ in b.calls],expected+([] if failing else [CONSOLE+'/assets/authova-missing-'+EXPECTED['/index.html']['sha256']+'.js'])+([CONSOLE+'/bff/me'] if mode=='formal' and not failing else []))
                    if mode=='formal' and not failing:self.assertEqual(b.calls[-1][1],{'expected':(401,)})
    def test_empty_entry_still_refuses_in_both_consumers_without_skipping_gate(self):
        b=BrowserActor(page=b'<html>No compiled entry</html>')
        obj=actor.Acceptance.__new__(actor.Acceptance);obj.c={'core':CORE,'console':CONSOLE,'web_assets':EXPECTED};obj.browser=lambda:b
        with self.assertRaisesRegex(Refused,'STATIC_ASSET_SHA'):obj.run('normal-tls-health-and-static-assets')
        with patch.object(http,'Browser',return_value=b),patch.object(http,'context',return_value='OWN_CA'):
            with self.assertRaisesRegex(Refused,'STATIC_ASSET_ENTRY'):http.readonly_acceptance({'core_origin':CORE,'console_origin':CONSOLE},lambda:None)

    def test_mime_html200_modified_bytes_and_missing200_refuse_both_consumers(self):
        for mode in ('ci','formal'):
            for poison in ('mime','html200','bytes','missing200'):
                with self.subTest(mode=mode,poison=poison):
                    b=BrowserActor();original=b.request
                    def request(url,**kw):
                        data,headers,status=original(url,**kw)
                        if url.endswith('index-OWN.js'):
                            if poison=='mime':headers={'Content-Type':'text/plain'}
                            if poison=='html200':data=HTML;headers={'Content-Type':'text/html'}
                            if poison=='bytes':data+=b'changed'
                        if poison=='missing200' and 'authova-missing-' in url:status=200
                        if status not in kw.get('expected',(200,)):raise http.KnownUnhealthy('HTTP_KNOWN_STATUS')
                        return data,headers,status
                    b.request=request
                    if mode=='ci':
                        obj=actor.Acceptance.__new__(actor.Acceptance);obj.c={'core':CORE,'console':CONSOLE,'web_assets':EXPECTED};obj.browser=lambda:b
                        run=lambda:obj.run('normal-tls-health-and-static-assets')
                    else:
                        def run():
                            with patch.object(http,'Browser',return_value=b),patch.object(http,'context',return_value='OWN_CA'):
                                http.readonly_acceptance({'core_origin':CORE,'console_origin':CONSOLE},lambda:None,EXPECTED)
                    with self.assertRaises(Refused):run()

    def test_signed_manifest_closed_paths_types_and_digest(self):
        value={'schema':'authova-compile-bundle/v1','repo':'carvebloom/authova','source':'a'*40,'main':'a'*40,
               'schemaDigest':'b'*64,'platform':'linux/arm64','configuration_included':False,
               'files':[dict(path='web'+p,**v) for p,v in EXPECTED.items()]}
        def consume(v):
            raw=json.dumps(v).encode();return http.manifest_assets(raw,{'source':'a'*40,'schemaDigest':'b'*64,'manifest':hashlib.sha256(raw).hexdigest()})
        self.assertEqual(consume(value),EXPECTED)
        import copy
        for poison in ('extra','duplicate','escape','bool','sha','source'):
            v=copy.deepcopy(value)
            if poison=='extra':v['files'][0]['locator']='OWN_PRIVATE_CANARY'
            if poison=='duplicate':v['files'].append(v['files'][0])
            if poison=='escape':v['files'][1]['path']='web/assets/../private.js'
            if poison=='bool':v['files'][1]['bytes']=True
            if poison=='sha':v['files'][1]['sha256']='invalid'
            if poison=='source':v['source']='c'*40
            with self.subTest(poison=poison),self.assertRaises(Refused):consume(v)
        with self.assertRaisesRegex(Refused,'WEB_MANIFEST_SHA'):http.manifest_assets(b'{}',{'manifest':'0'*64})

    def test_fresh_before_capture_and_exact_rollback_does_not_require_new_missing_gate(self):
        before={};b=BrowserActor();http.verify_static(b,CONSOLE,capture=before)
        self.assertEqual(before,EXPECTED)
        # Rollback compares bytes to its own newly sampled before-state, never
        # the new candidate and never claiming old broken behavior is fixed.
        b=BrowserActor();http.verify_static(b,CONSOLE,expected=before,negative=False)
        self.assertFalse(any('authova-missing-' in url for url,_ in b.calls))

    def test_owner_consumes_bound_manifest_same_fd_and_rejects_identity_or_content_drift(self):
        import tempfile,os
        import compose_owner
        from host_adapter import _token
        value={'schema':'authova-compile-bundle/v1','repo':'carvebloom/authova','source':'a'*40,'main':'a'*40,
               'schemaDigest':'b'*64,'platform':'linux/arm64','configuration_included':False,
               'files':[dict(path='web'+p,**v) for p,v in EXPECTED.items()]}
        raw=json.dumps(value).encode();candidate={'manifest':hashlib.sha256(raw).hexdigest(),'source':'a'*40,'schemaDigest':'b'*64}
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp).resolve()/'manifest.json';p.write_bytes(raw);p.chmod(0o600)
            obj=compose_owner.CoreConsoleOwner.__new__(compose_owner.CoreConsoleOwner)
            obj.profile={'compile_manifest':{'path':str(p),'identity':list(_token(p.stat()))}}
            self.assertEqual(obj.compile_assets(candidate,lambda:None),EXPECTED)
            # Same bytes under a replaced inode cannot be substituted.
            replacement=p.with_suffix('.new');replacement.write_bytes(raw);replacement.chmod(0o600);os.replace(replacement,p)
            with self.assertRaisesRegex(Refused,'WEB_MANIFEST_IDENTITY'):obj.compile_assets(candidate,lambda:None)
            obj.profile['compile_manifest']['identity']=list(_token(p.stat()))
            p.write_bytes(raw+b' ');obj.profile['compile_manifest']['identity']=list(_token(p.stat()))
            with self.assertRaisesRegex(Refused,'WEB_MANIFEST_SHA'):obj.compile_assets(candidate,lambda:None)

if __name__=='__main__':unittest.main()
