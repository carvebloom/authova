import contextlib,hashlib,importlib.util,io,json,tempfile,unittest,sys,os,subprocess
from pathlib import Path
from unittest.mock import patch
ROOT=Path(__file__).resolve().parents[2]
def module(name):
 sys.path.insert(0,str(ROOT/'scripts/ci'))
 spec=importlib.util.spec_from_file_location(name,ROOT/'scripts/ci'/(name+'.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
class CIDriverTests(unittest.TestCase):
 def test_private_make_log_projects_only_fixed_observations_or_unknown(self):
  m=module('driver');fmt=b'test -z "$(gofmt -l cmd internal)"\n';vet=fmt+b'go vet ./...\n';race=vet+b'go test -race -count=1 ./...\n'
  killed=b'github.com/carvebloom/authova/internal/ent: /usr/local/go/pkg/tool/linux_arm64/compile: signal: killed\n'
  vcs=b'error obtaining VCS status: exit status 128\n'
  cases=[(fmt,'CHECK_GOFMT'),(vet+b'PRIVATE_CANARY\n','CHECK_VET'),(race+b'--- FAIL: OWN_ONLY\n','CHECK_RACE'),
         (vet+killed,'COMPILE_SIGNAL_KILLED'),(vet+vcs,'VCS_STATUS_ERROR'),(b'PRIVATE_CANARY\n','UNKNOWN'),
         (b'test said: '+vcs,'UNKNOWN'),(race+b'go vet ./...\n','UNKNOWN'),(vet+killed+vcs,'UNKNOWN'),(b'\xff','UNKNOWN')]
  with tempfile.TemporaryDirectory() as tmp:
   p=Path(tmp)/'own-private.log'
   for body,expected in cases:
    with self.subTest(expected=expected,body_length=len(body)):
     p.write_bytes(body);p.chmod(0o600);value=m.command_diagnostic(p,['make','check'])
     self.assertEqual(value,expected);self.assertIn(value,m.DIAGNOSTICS)
   p.write_bytes(vet+vcs);p.chmod(0o644);self.assertEqual(m.command_diagnostic(p,['make','check']),'UNKNOWN');p.chmod(0o600)
   link=Path(tmp)/'link.log';link.symlink_to(p);self.assertEqual(m.command_diagnostic(link,['make','check']),'UNKNOWN')
   p.write_bytes(b'x'*1048577)
   with patch.object(m.os,'read',side_effect=AssertionError('NO_READ_OVERSIZED_LOG')):self.assertEqual(m.command_diagnostic(p,['make','check']),'UNKNOWN')
   p.write_bytes(vet+vcs);original_close=m.os.close
   def badclose(fd):original_close(fd);raise OSError('PRIVATE_CLOSE_CANARY')
   with patch.object(m.os,'close',badclose):self.assertEqual(m.command_diagnostic(p,['make','check']),'UNKNOWN')
 def test_make_diagnostic_preserves_native_failure_and_skips_successful_logs(self):
  m=module('driver');body=b'test -z "$(gofmt -l cmd internal)"\ngo vet ./...\nerror obtaining VCS status: exit status 128\nPRIVATE_CANARY\n'
  for timeout in (False,True):
   with self.subTest(timeout=timeout),tempfile.TemporaryDirectory() as tmp:
    calls=[]
    def fake(argv,**kw):
     calls.append(argv)
     if argv==['make','check']:
      kw['stdout'].write(body)
      if timeout:raise subprocess.TimeoutExpired(argv,1500,output=b'PRIVATE_CANARY')
      return type('Return',(),{'returncode':2})()
     return type('Return',(),{'returncode':0})()
    with self.assertRaisesRegex(m.Refused,'GATE_FAILED') as caught:m.run_gate('checks','a'*40,Path(tmp),fake)
    raw=(Path(tmp)/'checks.json').read_bytes();receipt=json.loads(raw)
    self.assertEqual(calls,[['python3','scripts/migration/check-boundaries.py'],['make','check']])
    self.assertEqual(receipt['diagnostic'],'VCS_STATUS_ERROR');self.assertEqual(caught.exception.diagnostic,'VCS_STATUS_ERROR')
    self.assertEqual(receipt['failure'],'UNKNOWN_TIMEOUT' if timeout else 'COMMAND_EXIT')
    self.assertEqual(receipt['commands'][-1]['exit_code'],None if timeout else 2)
    self.assertEqual(receipt['commands'][-1]['known_return'],not timeout);self.assertNotIn(b'CANARY',raw)
  with tempfile.TemporaryDirectory() as tmp,patch.object(m,'command_diagnostic',side_effect=AssertionError('NO_DIAGNOSTIC_READ_ON_SUCCESS')):
   m.run_gate('checks','a'*40,Path(tmp),lambda *a,**k:type('Return',(),{'returncode':0})())
   self.assertNotIn('diagnostic',json.loads((Path(tmp)/'checks.json').read_bytes()))
 def test_go_build_diagnostic_accepts_only_fixed_error_signatures(self):
  m=module('driver');argv=['go','build','-trimpath','./cmd/...']
  disk=b'compile: writing output: write $WORK/b123/_pkg_.a: no space left on device\n'
  killed=b'github.com/carvebloom/authova/internal/ent: /usr/local/go/pkg/tool/linux_arm64/compile: signal: killed\n'
  cases=[(disk,'DISK_SPACE_ERROR'),
         (b'go: writing work dir: mkdir /tmp/OWN_CANARY: no space left on device\n','DISK_SPACE_ERROR'),
         (b'go: failed to trim cache: open /tmp/OWN_CANARY/trim.txt: no space left on device\n','DISK_SPACE_ERROR'),
         (b'github.com/carvebloom/authova: write /tmp/OWN_CANARY/importcfg: no space left on device\n','DISK_SPACE_ERROR'),
         (killed,'COMPILE_SIGNAL_KILLED'),(b'error obtaining VCS status: exit status 128\n','VCS_STATUS_ERROR'),
         (b'no space left on device\n','UNKNOWN'),(b'OWN_CANARY says '+disk,'UNKNOWN'),
         (disk.rstrip()+b' extra\n','UNKNOWN'),(b'go: write https://OWN_CANARY: no space left on device\n','UNKNOWN'),
         (b'go vet ./...\n','UNKNOWN'),(disk+killed,'UNKNOWN')]
  with tempfile.TemporaryDirectory() as tmp:
   p=Path(tmp)/'own-private.log'
   for body,expected in cases:
    with self.subTest(expected=expected,body_length=len(body)):
     p.write_bytes(body);p.chmod(0o600);self.assertEqual(m.command_diagnostic(p,argv),expected)
   p.write_bytes(b'test -z "$(gofmt -l cmd internal)"\n'+disk)
   self.assertEqual(m.command_diagnostic(p,['make','check']),'DISK_SPACE_ERROR')
   with patch.object(m.os,'open',side_effect=AssertionError('NO_READ_UNSELECTED_COMMAND')):
    self.assertEqual(m.command_diagnostic(p,['go','build','CALLER_TARGET']),'UNKNOWN')
    self.assertEqual(m.command_diagnostic(p,['npm','run','build']),'UNKNOWN')
 def test_failed_go_build_observes_only_its_closed_log_and_keeps_native_fields(self):
  m=module('driver');argv=['go','build','-trimpath','./cmd/...'];disk=b'compile: writing output: write $WORK/b123/OWN_CANARY: no space left on device\n'
  for timeout in (False,True):
   with self.subTest(timeout=timeout),tempfile.TemporaryDirectory() as tmp:
    calls=[];observed=[];last_log=None;original=m.command_diagnostic
    def fake(command,**kw):
     nonlocal last_log
     calls.append(command);last_log=kw['stdout']
     if command==argv:
      last_log.write(disk)
      if timeout:raise subprocess.TimeoutExpired(command,1500,output=b'PRIVATE_CANARY')
      return type('Return',(),{'returncode':1})()
     return type('Return',(),{'returncode':0})()
    def observe(path,command):
     self.assertTrue(last_log.closed);observed.append((path.name,command));return original(path,command)
    with patch.object(m,'command_diagnostic',observe),self.assertRaisesRegex(m.Refused,'GATE_FAILED') as caught:
     m.run_gate('checks','a'*40,Path(tmp),fake)
    raw=(Path(tmp)/'checks.json').read_bytes();receipt=json.loads(raw)
    self.assertEqual(calls,m.PHASES['checks']);self.assertEqual(observed,[('private-checks-005.log',argv)])
    self.assertEqual(receipt['diagnostic'],'DISK_SPACE_ERROR');self.assertEqual(caught.exception.diagnostic,'DISK_SPACE_ERROR')
    self.assertEqual(receipt['failure'],'UNKNOWN_TIMEOUT' if timeout else 'COMMAND_EXIT')
    self.assertEqual(len(receipt['commands']),6);last=receipt['commands'][-1]
    self.assertEqual(set(last),{'ordinal','exit_code','known_return','I','elapsed'})
    self.assertEqual(last['ordinal'],5);self.assertEqual(last['exit_code'],None if timeout else 1);self.assertEqual(last['known_return'],not timeout)
    self.assertGreater(last['I'],0);self.assertGreaterEqual(last['elapsed'],0);self.assertNotIn(b'CANARY',raw);self.assertNotIn(b'$WORK',raw)
 def test_prepare_checkpoints_success_nonzero_timeout_and_exception_before_next_command(self):
  m=module('prepare_tools')
  for outcome in ('success','nonzero','timeout','exception'):
   with self.subTest(outcome=outcome),tempfile.TemporaryDirectory() as tmp:
    root=Path(tmp)/'source';root.mkdir();out=Path(tmp)/'prepare';out.mkdir();calls=[]
    def fake(argv,**kwargs):
     ordinal=len(calls)
     if ordinal:
      prior=json.loads((out/'prepare.json').read_bytes())
      self.assertEqual(len(prior['records']),ordinal);self.assertEqual(prior['result'],'RUNNING')
     calls.append(argv);self.assertEqual(kwargs['timeout'],900)
     self.assertEqual({k:kwargs['env'][k] for k in ('GOMAXPROCS','GOFLAGS','GOMEMLIMIT','AUTHOVA_CI_RESOURCE_PROFILE')},
                      {'GOMAXPROCS':'1','GOFLAGS':'-p=1','GOMEMLIMIT':'256MiB','AUTHOVA_CI_RESOURCE_PROFILE':'serial768'})
     kwargs['stdout'].write(b'PRIVATE_PREPARE_CANARY')
     if ordinal==1:
      if outcome=='timeout':raise subprocess.TimeoutExpired(argv,900,output=b'PRIVATE_PREPARE_CANARY')
      if outcome=='exception':raise OSError('PRIVATE_PREPARE_CANARY')
     return type('Return',(),{'returncode':7 if ordinal==1 and outcome=='nonzero' else 0})()
    with patch.object(m.driver,'ROOT',root),patch.dict(os.environ,{'GOMAXPROCS':'9','GOFLAGS':'-p=9 -skip=caller','GOMEMLIMIT':'9GiB'}):
     if outcome=='success':m.prepare('a'*40,out,fake)
     else:
      expected={'nonzero':m.driver.Refused,'timeout':subprocess.TimeoutExpired,'exception':OSError}[outcome]
      with self.assertRaises(expected):m.prepare('a'*40,out,fake)
    raw=(out/'prepare.json').read_bytes();receipt=json.loads(raw)
    self.assertNotIn(b'CANARY',raw);self.assertEqual((out/'prepare.json').stat().st_mode&0o777,0o600)
    self.assertEqual(len(calls),7 if outcome=='success' else 2)
    self.assertEqual(calls[0],['go','mod','download'])
    self.assertEqual(calls[1],['go','install','google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11'])
    for number,row in enumerate(receipt['records']):
     self.assertEqual(set(row),{'ordinal','exit_code','known_return','I','elapsed'});self.assertEqual(row['ordinal'],number)
     self.assertGreater(row['I'],0);self.assertGreaterEqual(row['elapsed'],0)
    last=receipt['records'][-1]
    self.assertEqual(receipt['result'],'PASS' if outcome=='success' else 'FAIL')
    self.assertEqual(receipt['failure'],{'success':None,'nonzero':'PREPARE_FAILED','timeout':'PREPARE_TIMEOUT','exception':'PREPARE_EXCEPTION'}[outcome])
    self.assertEqual(last['known_return'],outcome in ('success','nonzero'))
    self.assertEqual(last['exit_code'],0 if outcome=='success' else 7 if outcome=='nonzero' else None)
 def test_prepare_keeps_first_on_close_or_receipt_fault_and_prints_only_fixed_fields(self):
  m=module('prepare_tools');original_open=Path.open
  class CloseFault:
   def __init__(self,log):self.log=log;self.name=log.name
   def close(self):self.log.close();raise OSError('PRIVATE_CLOSE_CANARY')
  for secondary in ('LOG_CLOSE','RECEIPT_WRITE'):
   with self.subTest(secondary=secondary),tempfile.TemporaryDirectory() as tmp:
    root=Path(tmp)/'source';root.mkdir();out=Path(tmp)/'prepare';out.mkdir();first=OSError('PRIVATE_FIRST_CANARY')
    def fail(*args,**kwargs):raise first
    def opened(path,*args,**kwargs):
     log=original_open(path,*args,**kwargs)
     return CloseFault(log) if secondary=='LOG_CLOSE' and path.suffix=='.log' else log
    writer=m.write_receipt
    def write(*args):
     if secondary=='RECEIPT_WRITE':raise OSError('PRIVATE_REPORT_CANARY')
     return writer(*args)
    with patch.object(m.driver,'ROOT',root),patch.object(Path,'open',opened),patch.object(m,'write_receipt',write):
     try:m.prepare('a'*40,out,fail)
     except BaseException as caught:self.assertIs(caught,first)
     else:self.fail('preparation must fail')
    self.assertEqual(first.prepare_secondary,(secondary,));self.assertEqual(first.prepare_ordinal,0)
    stdout=io.StringIO()
    with patch.object(m,'main',side_effect=first),contextlib.redirect_stdout(stdout):self.assertEqual(m.entry(),1)
    self.assertEqual(json.loads(stdout.getvalue()),{'result':'FAIL','code':'PREPARE_EXCEPTION','ordinal':0})
    self.assertNotIn('CANARY',stdout.getvalue())
 def test_missing_or_wrong_source_fails_before_any_command(self):
  m=module('driver')
  with patch.object(m,'git',side_effect=AssertionError('NO_GIT_ON_BAD_INPUT')):
   for value in (None,'','a'*39,True):
    with self.subTest(value=value),self.assertRaisesRegex(m.Refused,'EXPECTED_SHA'):m.guard(value)
  with patch.dict(m.os.environ,{'CI_COMMIT_SHA':'b'*40}),patch.object(m,'git',side_effect=AssertionError('NO_GIT_ON_WRONG_COMMIT')):
   with self.assertRaisesRegex(m.Refused,'COMMIT_MISMATCH'):m.guard('a'*40)
 def test_failed_native_stops_gate_and_cannot_bind_bundle(self):
  m=module('driver');calls=[]
  def fake(argv,**kwargs):
   calls.append(argv)
   self.assertEqual({k:kwargs['env'][k] for k in ('GOMAXPROCS','GOFLAGS','GOMEMLIMIT','AUTHOVA_CI_RESOURCE_PROFILE')},
                    {'GOMAXPROCS':'1','GOFLAGS':'-p=1','GOMEMLIMIT':'256MiB','AUTHOVA_CI_RESOURCE_PROFILE':'serial768'})
   return type('Return',(),{'returncode':2})()
  with tempfile.TemporaryDirectory() as tmp:
   out=Path(tmp)
   with self.assertRaisesRegex(m.Refused,'GATE_FAILED'):m.run_gate('checks','a'*40,out,fake)
   self.assertEqual(len(calls),1);receipt=json.loads((out/'checks.json').read_bytes());self.assertEqual(receipt['result'],'FAIL');self.assertEqual(receipt['commands'][0]['exit_code'],2)
   with self.assertRaisesRegex(m.Refused,'GATE_BINDING'):m.checked_gates(out,'a'*40)
 def test_fixture_only_new_schema_aliases_and_no_password_argv(self):
  m=module('fixture');calls=[]
  def fake(argv,**kw):
   self.assertEqual({k:kw['env'][k] for k in ('GOMAXPROCS','GOFLAGS','GOMEMLIMIT')},{'GOMAXPROCS':'1','GOFLAGS':'-p=1','GOMEMLIMIT':'256MiB'})
   calls.append(argv);self.assertNotIn('CI-ROOT-OWN',repr(argv));return type('Return',(),{'returncode':0})()
  with tempfile.TemporaryDirectory() as tmp,patch.dict(os.environ,{'AUTHOVA_CI_MYSQL_ROOT_PASSWORD':'CI-ROOT-OWN','AUTHOVA_CI_POSTGRES_PASSWORD':'CI-PG-OWN'}):
   out=Path(tmp);m.initialize('a'*40,'both',out,fake);v=json.loads((out/'context.json').read_bytes());self.assertEqual(v['source'],'a'*40);self.assertIn('authova-ci-mysql',v['env']['AUTHOVA_TEST_MYSQL_DSN']);self.assertIn('authova-ci-postgres',v['env']['AUTHOVA_TEST_POSTGRES_DSN']);self.assertRegex(v['env']['AUTHOVA_TEST_DISPOSABLE_DATABASE'],'^authova_quality_[0-9a-f]{12}$');self.assertEqual((out/'context.json').stat().st_mode&0o777,0o600);self.assertTrue(all('docker' not in argv for argv in calls))
 def test_bundle_passes_serial_resources_to_both_compilers_without_rebuilding_ui(self):
  m=module('driver');calls=[]
  with tempfile.TemporaryDirectory() as tmp:
   root=Path(tmp)/'source';web=root/'console/web/dist';web.mkdir(parents=True)
   receipts=Path(tmp)/'receipts';legal=receipts/'web-release';legal.mkdir(parents=True)
   out=Path(tmp)/'compile';out.mkdir()
   artifacts={}
   for label,folder,name in [('web',web,'index.html'),('legal',legal,'notice.txt')]:
    data=b'OWN_ARTIFACT';(folder/name).write_bytes(data)
    artifacts[label]=[{'path':name,'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}]
   def fake(argv,**kw):
    self.assertEqual({k:kw['env'][k] for k in ('GOMAXPROCS','GOFLAGS','GOMEMLIMIT','AUTHOVA_CI_RESOURCE_PROFILE')},
                     {'GOMAXPROCS':'1','GOFLAGS':'-p=1','GOMEMLIMIT':'256MiB','AUTHOVA_CI_RESOURCE_PROFILE':'serial768'})
    self.assertEqual(kw['timeout'],600);self.assertEqual(kw['env']['CGO_ENABLED'],'0')
    calls.append(argv);Path(argv[argv.index('-o')+1]).write_bytes(b'OWN_FAKE_COMPILE')
    return type('Return',(),{'returncode':0})()
   def output(argv,**kw):
    if argv[:3]==['go','version','-m']:return b'go1.27.4\nGOOS=linux\nGOARCH=arm64\nCGO_ENABLED=0\n'
    return {('go','version'):b'go version go1.27.4 linux/arm64',('node','--version'):b'v26.13.0',('npm','--version'):b'OWN_NPM',('protoc','--version'):b'libprotoc 36.2'}[tuple(argv)]
   with patch.object(m,'ROOT',root),patch.object(m,'checked_gates',return_value=[{'artifacts':artifacts}]),patch.object(m,'git',return_value=''),patch.object(m,'schema_digest',return_value='f'*64),patch.object(m.subprocess,'check_output',output),patch.dict(os.environ,{'GOFLAGS':'-p=9 -run=caller'}):
    m.bundle('a'*40,out,receipts,fake)
   self.assertEqual([args[-1] for args in calls],['./cmd/authova','./console/cmd/authova-console'])
   self.assertEqual(json.loads((out/'bundle/manifest.json').read_bytes())['platform'],'linux/arm64')
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
