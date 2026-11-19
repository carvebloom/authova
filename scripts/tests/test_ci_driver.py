import importlib.util,json,tempfile,unittest,sys,os
from pathlib import Path
from unittest.mock import patch
ROOT=Path(__file__).resolve().parents[2]
def module(name):
 sys.path.insert(0,str(ROOT/'scripts/ci'))
 spec=importlib.util.spec_from_file_location(name,ROOT/'scripts/ci'/(name+'.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
class CIDriverTests(unittest.TestCase):
 def test_missing_or_wrong_source_fails_before_any_command(self):
  m=module('driver')
  with patch.object(m,'git',side_effect=AssertionError('NO_GIT_ON_BAD_INPUT')):
   for value in (None,'','a'*39,True):
    with self.subTest(value=value),self.assertRaisesRegex(m.Refused,'EXPECTED_SHA'):m.guard(value)
  with patch.dict(m.os.environ,{'CI_COMMIT_SHA':'b'*40}),patch.object(m,'git',side_effect=AssertionError('NO_GIT_ON_WRONG_COMMIT')):
   with self.assertRaisesRegex(m.Refused,'COMMIT_MISMATCH'):m.guard('a'*40)
 def test_failed_native_stops_gate_and_cannot_bind_bundle(self):
  m=module('driver');calls=[]
  def fake(argv,**kwargs):calls.append(argv);return type('Return',(),{'returncode':2})()
  with tempfile.TemporaryDirectory() as tmp:
   out=Path(tmp)
   with self.assertRaisesRegex(m.Refused,'GATE_FAILED'):m.run_gate('checks','a'*40,out,fake)
   self.assertEqual(len(calls),1);receipt=json.loads((out/'checks.json').read_bytes());self.assertEqual(receipt['result'],'FAIL');self.assertEqual(receipt['commands'][0]['exit_code'],2)
   with self.assertRaisesRegex(m.Refused,'GATE_BINDING'):m.checked_gates(out,'a'*40)
 def test_fixture_only_new_schema_aliases_and_no_password_argv(self):
  m=module('fixture');calls=[]
  def fake(argv,**kw):
   calls.append(argv);self.assertNotIn('CI-ROOT-OWN',repr(argv));return type('Return',(),{'returncode':0})()
  with tempfile.TemporaryDirectory() as tmp,patch.dict(os.environ,{'AUTHOVA_CI_MYSQL_ROOT_PASSWORD':'CI-ROOT-OWN','AUTHOVA_CI_POSTGRES_PASSWORD':'CI-PG-OWN'}):
   out=Path(tmp);m.initialize('a'*40,'both',out,fake);v=json.loads((out/'context.json').read_bytes());self.assertEqual(v['source'],'a'*40);self.assertIn('authova-ci-mysql',v['env']['AUTHOVA_TEST_MYSQL_DSN']);self.assertIn('authova-ci-postgres',v['env']['AUTHOVA_TEST_POSTGRES_DSN']);self.assertRegex(v['env']['AUTHOVA_TEST_DISPOSABLE_DATABASE'],'^authova_quality_[0-9a-f]{12}$');self.assertEqual((out/'context.json').stat().st_mode&0o777,0o600);self.assertTrue(all('docker' not in argv for argv in calls))
 def test_same_candidate_test_and_manual_review_no_schema_change(self):
  m=module('host_release');c=dict(schema='authova-image-candidate/v1',repo='carvebloom/authova',source='a'*40,coreID='sha256:'+'b'*64,consoleID='sha256:'+'c'*64,manifest='d'*64,archive='e'*64,schemaDigest='f'*64)
  control=dict(project='authova-local-production-20261114',schemaDigest='f'*64,retained_compose='/host/base',retained_prior_override='/host/previous',active_override='/host/current');test={'schema':'authova-test-deployment/v1','candidate':c,'result':'PASS','project':'authova-ci-test-'+c['source'][:12]+'-42','scope':'a'*32,'started_at':1,'finished_at':3,'checks':{name:'PASS' for name in module('release_contract').REQUIRED_CHECKS},'check_evidence':{name:dict(result='PASS',exit_code=0,known_return=True,timely=True,started_at=1,finished_at=2,evidence_sha256='a'*64) for name in module('release_contract').REQUIRED_CHECKS}};review={'schema':'authova-host-verified-production-review/v1','candidate':c,'reviewed_by':'rwasayc','reviewed_at':'2026-11-19T00:00:00Z','deployment':'production'}
  result=m.plan(c,control,test,review);self.assertFalse(result['execution_authorized_by_this_plan']);self.assertEqual(result['argv'][-2:],['core','console']);self.assertIn('/host/current',result['argv'])
  with self.assertRaisesRegex(m.Refused,'REVIEW_BINDING'):m.plan(c,control,test,None)
  with self.assertRaisesRegex(m.Refused,'SCHEMA_CHANGE'):m.plan(c,dict(control,schemaDigest='0'*64),test,review)
  with self.assertRaisesRegex(m.Refused,'TEST_BINDING'):m.plan(c,control,dict(test,candidate=dict(c,source='9'*40)),review)
  missing=dict(test,checks={k:v for k,v in test['checks'].items() if k!='ordinary-app-oidc-pkce-lifecycle'})
  with self.assertRaisesRegex(m.Refused,'TEST_REQUIRED_CHECKS'):m.plan(c,control,missing,review)
  forged=dict(test,check_evidence={k:dict(v,exit_code=False) for k,v in test['check_evidence'].items()})
  with self.assertRaisesRegex(m.Refused,'TEST_CHECK_OUTCOME'):m.plan(c,control,forged,review)
  with self.assertRaisesRegex(m.Refused,'TEST_PROJECT'):m.plan(c,control,dict(test,project='authova-local-production-20261114'),review)
if __name__=='__main__':unittest.main()
