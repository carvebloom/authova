"""Both real acceptance consumers with an owned in-memory HTTP actor, no IO."""
import sys
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

class BrowserActor:
    def __init__(self,page=HTML,fail=None):self.page=page;self.fail=fail;self.calls=[]
    def request(self,url,**kw):
        self.calls.append((url,kw))
        if self.fail and self.fail in url:raise http.KnownUnhealthy('OWN_ASSET_FAILED')
        return (self.page if url==CONSOLE+'/' else b'OWN_RESPONSE',{},200)

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
                        obj=actor.Acceptance.__new__(actor.Acceptance);obj.c={'core':CORE,'console':CONSOLE};obj.calls=[b];obj.browser=lambda:b;obj.guard=lambda:None
                        run=lambda:obj.run('normal-tls-health-and-static-assets')
                    else:
                        def run():
                            with patch.object(http,'Browser',return_value=b) as factory,patch.object(http,'context',return_value='OWN_CA'):
                                result=http.readonly_acceptance({'core_origin':CORE,'console_origin':CONSOLE,'ca_pem':'OWN_PUBLIC_CA'},lambda:None)
                                self.assertEqual(factory.call_args.args[1],'OWN_CA')
                                self.assertEqual(factory.call_args.kwargs,{})  # formal keeps its normal non-loopback transport.
                                return result
                    if failing:
                        with self.assertRaisesRegex(Refused,'OWN_ASSET_FAILED'):run()
                    else:run()
                    self.assertEqual([url for url,_ in b.calls],expected+([CONSOLE+'/bff/me'] if mode=='formal' and not failing else []))
                    if mode=='formal' and not failing:self.assertEqual(b.calls[-1][1],{'expected':(401,)})
    def test_empty_entry_still_refuses_in_both_consumers_without_skipping_gate(self):
        b=BrowserActor(page=b'<html>No compiled entry</html>')
        obj=actor.Acceptance.__new__(actor.Acceptance);obj.c={'core':CORE,'console':CONSOLE};obj.browser=lambda:b
        with self.assertRaisesRegex(Refused,'CI_STATIC_ENTRY'):obj.run('normal-tls-health-and-static-assets')
        with patch.object(http,'Browser',return_value=b),patch.object(http,'context',return_value='OWN_CA'):
            with self.assertRaisesRegex(Refused,'STATIC_ASSET_ENTRY'):http.readonly_acceptance({'core_origin':CORE,'console_origin':CONSOLE},lambda:None)

if __name__=='__main__':unittest.main()
