"""Owned rollback sequencing and API witness controls; no Docker/DB qualification."""
import copy
from pathlib import Path
import sys
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import test_owner
import test_http
from release_contract import Refused,REQUIRED_CHECKS


class RollbackTests(unittest.TestCase):
    def test_required_gate_orders_writes_old_pair_and_repromotion_and_stops_unknown(self):
        self.assertIn('same-storage-rollback-and-repromotion',REQUIRED_CHECKS)
        for fail in (None,'CI_KNOWN_ROLLBACK','rollback-read-write'):
            with self.subTest(fail=fail):
                o=test_owner.TestOwner.__new__(test_owner.TestOwner);o.candidate_override='OWN_CANDIDATE'
                events=[]
                def step(label):
                    events.append(label)
                    if label==fail:raise Refused('OWN_UNKNOWN')
                o.http_actor=lambda stage,*a:step(stage)
                o.switch_pair=lambda b,g,p,path,tag:step(tag)
                o.private_input=lambda *a:None;o.write=lambda *a:'OWN_OLD_OVERRIDE'
                def guard():pass
                guard.full=lambda *a:events.append(a)
                binding={'candidate':{'coreID':'OWN_NEW_CORE','consoleID':'OWN_NEW_CONSOLE'}}
                if fail:
                    with self.assertRaisesRegex(Refused,'OWN_UNKNOWN'):o.rollback_gate(binding,guard)
                    self.assertEqual(events[-1],fail)
                else:
                    self.assertTrue(o.rollback_gate(binding,guard)['old_pair_write_survived_repromotion'])
                    self.assertEqual(events,[(1800,600),'rollback-write','CI_KNOWN_ROLLBACK','rollback-read-write','CI_REPROMOTION','repromotion-read'])
        o.http_actor=lambda *a:self.fail('late actor ran')
        def guard():pass
        guard.full=lambda *a:(_ for _ in ()).throw(Refused('OWN_CAP'))
        with self.assertRaisesRegex(Refused,'OWN_CAP'):o.rollback_gate(binding,guard)

    def test_real_actor_api_witness_preserves_both_new_writes_and_detects_loss(self):
        obj=test_http.Acceptance.__new__(test_http.Acceptance)
        obj.c={'scope':'1'*32,'core':'https://own-core.invalid','console':'https://own-console.invalid','web_assets':{}}
        obj.state={'rollback-web-before':{'OWN_BEFORE':{}}};obj.calls=[];obj.guard=lambda:None
        stored={};calls=[]
        def api(path,method='GET',body=None,**kw):
            calls.append((path,method))
            if method=='POST':
                value=dict(body,id=str(len(stored)+1),app_key='own'+str(len(stored)+1))
                value['issuer']=obj.c['core']+'/a/'+value['app_key'];stored[value['id']]=value
                return copy.deepcopy(value)
            return copy.deepcopy(stored[path.rsplit('/',1)[1]])
        obj.api=api;obj.browser=lambda:None
        with patch.object(test_http,'verify_static') as check:
            obj.run('rollback-write');self.assertEqual(len(stored),1)
            obj.run('rollback-read-write');self.assertEqual(len(stored),2)
            obj.run('repromotion-read');self.assertEqual(len(stored),2)
            self.assertEqual(sum(method=='POST' for _,method in calls),2)
            self.assertFalse(check.call_args.kwargs.get('negative') is False)  # final signed candidate includes negative
            stored['1']['name']='OWN_LOST_OR_CHANGED'
            with self.assertRaisesRegex(Refused,'CI_WITNESS_LOST'):obj.run('repromotion-read')


if __name__=='__main__':unittest.main()
