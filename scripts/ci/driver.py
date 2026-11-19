#!/usr/bin/env python3
"""Exact-source CI gates and config-free compile bundle. No Docker or release."""
import argparse, datetime, hashlib, json, os, re, stat, subprocess, sys, time
from pathlib import Path
ROOT = Path(__file__).resolve().parents[2]
REPO = 'carvebloom/authova'
PHASES = {
 'checks': [['python3','scripts/migration/check-boundaries.py'],['make','check'],['make','test-discover','test-python'],['govulncheck','./...'],['staticcheck','./...'],['go','build','-trimpath','./cmd/...']],
 'generation': [['make','generate-check'],['make','build-all','ent-check'],['git','diff','--exit-code','--','internal/ent','api','contracts/generated','contracts/openapi','console/contract-inputs','console/web/src/api/generated']],
 'web': [['npm','--prefix','console/web','ci'],['npm','--prefix','console/web','run','typecheck'],['npm','--prefix','console/web','run','lint'],['npm','--prefix','console/web','run','format:check'],['npm','--prefix','console/web','audit'],['make','test-web'],['npm','--prefix','console/web','run','build']],
 'mysql': [['make','test-integration','TEST_DATABASE_DIALECT=mysql'],['python3','scripts/verify-contract-combination.py']],
 'postgres-compatibility': [['make','test-integration','TEST_DATABASE_DIALECT=both'],['python3','scripts/verify-contract-combination.py']],
}
PHASES = {'checks':PHASES['checks'], 'console-generation':PHASES['generation']+PHASES['web'], 'mysql-primary':PHASES['mysql'], 'postgres-both':PHASES['postgres-compatibility']}
REQUIRED = tuple(PHASES)
class Refused(Exception): pass
def need(ok, code):
 if not ok: raise Refused(code)
def dump(p, v):
 data=(json.dumps(v,sort_keys=True,separators=(',',':'),allow_nan=False)+'\n').encode()
 fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 try:
  with os.fdopen(fd,'wb') as f: f.write(data);f.flush();os.fsync(f.fileno())
 finally:
  d=os.open(p.parent,os.O_RDONLY|os.O_DIRECTORY)
  try: os.fsync(d)
  finally: os.close(d)
def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT).decode().strip()
def guard(expected):
 need(type(expected) is str and re.fullmatch('[0-9a-f]{40}',expected) is not None,'EXPECTED_SHA')
 need(os.environ.get('CI_COMMIT_SHA')==expected,'COMMIT_MISMATCH')
 need(git('rev-parse','HEAD')==expected and git('rev-parse',expected+'^{commit}')==expected,'SOURCE_MISMATCH')
 need(not git('status','--porcelain','--untracked-files=all'),'DIRTY_SOURCE')
 return expected

def ci_environment(source):
 # A closed resource profile, not a caller-supplied Go flag/selection interface.
 env=dict(source)
 env.update(GOMAXPROCS='1',GOFLAGS='-p=1',GOMEMLIMIT='256MiB',AUTHOVA_CI_RESOURCE_PROFILE='serial768')
 return env

DIAGNOSTICS=frozenset({'CHECK_GOFMT','CHECK_VET','CHECK_RACE','COMPILE_SIGNAL_KILLED','VCS_STATUS_ERROR','DISK_SPACE_ERROR','UNKNOWN'})
def command_diagnostic(path,argv):
 # First-current bounded observation of this command's private log, not a raw
 # export, content hash or proof of its complete historical first cause.
 make_check=argv==['make','check']
 if not make_check and argv!=['go','build','-trimpath','./cmd/...']:return 'UNKNOWN'
 fd=None;answer='UNKNOWN'
 try:
  fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK);s=os.fstat(fd)
  if not (stat.S_ISREG(s.st_mode) and stat.S_IMODE(s.st_mode)==0o600 and s.st_uid==os.geteuid() and s.st_nlink==1 and s.st_size<=1048576):return 'UNKNOWN'
  body=bytearray()
  while True:
   part=os.read(fd,min(65536,1048577-len(body)))
   if not part:break
   body.extend(part)
   if len(body)>1048576:return 'UNKNOWN'
  token=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_nlink,v.st_uid,v.st_gid,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
  if len(body)!=s.st_size or token(s)!=token(os.fstat(fd)) or token(s)!=token(os.stat(path,follow_symlinks=False)):return 'UNKNOWN'
  body.decode('utf-8')
  echoes=[b'test -z "$(gofmt -l cmd internal)"',b'go vet ./...',b'go test -race -count=1 ./...']
  stages=['CHECK_GOFMT','CHECK_VET','CHECK_RACE'];stage=-1;families=set()
  for line in body.splitlines():
   if len(line)>16384 or b'\x00' in line:return 'UNKNOWN'
   if make_check and line in echoes:
    next_stage=echoes.index(line)
    if next_stage!=stage+1:return 'UNKNOWN'
    stage=next_stage
   if re.fullmatch(rb'(?:go: )?error obtaining VCS status: exit status [1-9][0-9]{0,2}',line):families.add('VCS_STATUS_ERROR')
   if re.fullmatch(rb'(?:[A-Za-z0-9_./@+-]+: )?(?:/[A-Za-z0-9_.@+-]+)+/pkg/tool/linux_arm64/compile: signal: killed',line):families.add('COMPILE_SIGNAL_KILLED')
   # Only fixed Go operation forms with the exact terminal ENOSPC text. Other
   # wording, arbitrary quoted messages and unsupported path forms stay unknown.
   if re.fullmatch(rb'(?:go: (?:writing work dir: |failed to trim cache: )?|compile: writing output: |[A-Za-z0-9_./@+-]+: )(?:write|mkdir|open) [A-Za-z0-9_./@$+-]+: no space left on device',line):families.add('DISK_SPACE_ERROR')
  if len(families)>1:return 'UNKNOWN'
  answer=next(iter(families)) if families else stages[stage] if stage>=0 else 'UNKNOWN'
 except BaseException:answer='UNKNOWN'
 finally:
  if fd is not None:
   try:os.close(fd)
   except BaseException:answer='UNKNOWN'
 return answer

def run_gate(phase,sha,out,runner=subprocess.run,fixture=None):
 env={k:v for k,v in os.environ.items() if not k.startswith(('AUTHOVA_','COMPOSE_'))};env.update(GOTOOLCHAIN='local',GOWORK='off',AUTHOVA_WEB_RELEASE_DIR=str(out/'web-release'),NODE_OPTIONS='--max-old-space-size=512')
 if fixture is not None:env.update(fixture)
 env=ci_environment(env)
 if phase in ('mysql-primary','postgres-both'):
  dialect='mysql' if phase=='mysql-primary' else 'both'
  need(env.get('AUTHOVA_TEST_DATABASE_DIALECT')==dialect,'FIXTURE_DIALECT')
  need(re.fullmatch('authova_quality_[0-9a-f]{12}',env.get('AUTHOVA_TEST_DISPOSABLE_DATABASE','')) is not None,'OWNED_FIXTURE_REQUIRED')
  # Existing test_profiles preflight checks required DSNs/keys and no unselected engine.
 started=time.time();mono=time.monotonic();commands=[];first=None;diagnostic='UNKNOWN'
 for number,argv in enumerate(PHASES[phase]):
  path=out/('private-%s-%03d.log'%(phase,number));I=time.time();M=time.monotonic()
  try:
   with path.open('xb') as log:
    os.chmod(path,0o600);p=runner(argv,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,timeout=1500)
   commands.append({'ordinal':number,'exit_code':p.returncode,'known_return':True,'I':I,'elapsed':time.monotonic()-M})
   if p.returncode:
    first='COMMAND_EXIT'
    if phase=='checks':diagnostic=command_diagnostic(path,argv)
    break
  except subprocess.TimeoutExpired:
   commands.append({'ordinal':number,'exit_code':None,'known_return':False,'I':I,'elapsed':time.monotonic()-M});first='UNKNOWN_TIMEOUT'
   if phase=='checks':diagnostic=command_diagnostic(path,argv)
   break
 result={'schema':'authova-ci-gate/v1','repo':REPO,'source':sha,'gate':phase,'result':'PASS' if first is None else 'FAIL','failure':first,'commands':commands,'I':started,'elapsed':time.monotonic()-mono}
 if first is not None:result['diagnostic']=diagnostic
 if first is None and phase=='console-generation':
  result['artifacts']={}
  for label,folder in [('web',ROOT/'console/web/dist'),('legal',out/'web-release')]:
   need(folder.is_dir(),'ARTIFACT_MISSING');items=[]
   for f in sorted(folder.rglob('*')):
    need(not f.is_symlink(),'ARTIFACT_SYMLINK')
    if f.is_file():
     data=f.read_bytes();items.append({'path':f.relative_to(folder).as_posix(),'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()})
   need(bool(items),'ARTIFACT_EMPTY');result['artifacts'][label]=items
 dump(out/(phase+'.json'),result)
 if first is not None:
  error=Refused('GATE_FAILED');error.diagnostic=diagnostic;raise error
 return result

def schema_digest(root=ROOT):
 paths=sorted(p for p in (root/'internal/migrations').rglob('*') if p.is_file() and p.suffix in ('.sql','.sum'))
 need(bool(paths),'SCHEMA_EMPTY')
 rows=[{'path':str(p.relative_to(root)),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in paths]
 return hashlib.sha256(json.dumps(rows,sort_keys=True,separators=(',',':')).encode()).hexdigest()
def checked_gates(directory,sha,required=REQUIRED):
 rows=[]
 for gate in required:
  v=json.loads((directory/(gate+'.json')).read_bytes())
  need(v.get('schema')=='authova-ci-gate/v1' and v.get('repo')==REPO and v.get('source')==sha and v.get('gate')==gate and v.get('result')=='PASS','GATE_BINDING')
  need(len(v['commands'])==len(PHASES[gate]) and all(type(x.get('exit_code')) is int and x['exit_code']==0 and x.get('known_return') is True for x in v['commands']),'GATE_INCOMPLETE');rows.append(v)
 return rows

def bundle(sha,out,receipts,runner=subprocess.run):
 gates=checked_gates(receipts,sha,required=('console-generation',));build_records=[];dest=out/'bundle';dest.mkdir(mode=0o700)
 env=ci_environment(os.environ);env.update(GOOS='linux',GOARCH='arm64',CGO_ENABLED='0',GOTOOLCHAIN='local',GOWORK='off')
 versions={}
 for tool,args in [('go',['go','version']),('node',['node','--version']),('npm',['npm','--version']),('protoc',['protoc','--version'])]:
  value=subprocess.check_output(args,cwd=ROOT,env=env).decode().strip();need(len(value)<256,'VERSION_BOUND');versions[tool]=value
 need('go1.27.4 ' in versions['go'] and versions['node']=='v26.13.0' and versions['protoc']=='libprotoc 36.2','TOOL_VERSION')
 for role,package in [('core','./cmd/authova'),('console','./console/cmd/authova-console')]:
  I=time.time();M=time.monotonic()
  with (out/('private-build-'+role+'.log')).open('xb') as log:
   os.chmod(log.name,0o600);p=runner(['go','build','-trimpath','-buildvcs=false','-ldflags=-s -w','-o',str(dest/role),package],cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,timeout=600)
  build_records.append({'role':role,'exit_code':p.returncode,'known_return':True,'I':I,'elapsed':time.monotonic()-M});need(p.returncode==0,'BINARY_BUILD')
  info=subprocess.check_output(['go','version','-m',str(dest/role)],env=env,cwd=ROOT).decode();need('go1.27.4' in info and 'GOOS=linux' in info and 'GOARCH=arm64' in info and 'CGO_ENABLED=0' in info,'BUILDINFO')
 # Consume exactly the web/legal outputs from this workflow; no second UI build.
 import shutil
 for label,folder in [('web',ROOT/'console/web/dist'),('legal',receipts/'web-release')]:
  actual=[]
  for f in sorted(folder.rglob('*')):
   need(not f.is_symlink(),'ARTIFACT_SYMLINK')
   if f.is_file():
    data=f.read_bytes();actual.append({'path':f.relative_to(folder).as_posix(),'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()})
  need(actual==gates[0]['artifacts'][label],'ARTIFACT_DRIFT');shutil.copytree(folder,dest/label)
 files=[]
 for p in sorted(dest.rglob('*')):
  need(not p.is_symlink(),'BUNDLE_SYMLINK')
  if p.is_file():
   rel=p.relative_to(dest).as_posix();need(rel in ('core','console') or rel.startswith(('web/','legal/')),'BUNDLE_PATH')
   data=p.read_bytes();files.append({'path':rel,'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()})
 need(not git('diff','--name-only','HEAD'),'SOURCE_AFTER')
 manifest={'schema':'authova-compile-bundle/v1','repo':REPO,'source':sha,'main':sha,'platform':'linux/arm64','checks':gates,'native_build':build_records,'tools':versions,'schemaDigest':schema_digest(),'files':files,'console_assets':'/opt/authova-console/web','configuration_included':False,'required_workflows':['authova-checks','authova-mysql','authova-postgres','authova-build'],'cross_workflow_gate_authority':'TRUSTED_HOST_WOODPECKER_API_NOT_COMMAND_SELF_ATTESTATION'}
 dump(dest/'manifest.json',manifest);return manifest

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('phase',choices=(*REQUIRED,'bundle'));p.add_argument('--expected-sha',default=os.environ.get('CI_EXPECTED_SHA'));p.add_argument('--out',type=Path,required=True);p.add_argument('--receipts',type=Path);p.add_argument('--fixture-context',type=Path);a=p.parse_args();sha=guard(a.expected_sha)
 need(a.out.is_absolute() and not a.out.is_relative_to(ROOT),'OUTSIDE_CHECKOUT_REQUIRED');a.out.mkdir(mode=0o700,exist_ok=False)
 if a.phase=='bundle':need(a.receipts is not None,'RECEIPTS_REQUIRED');bundle(sha,a.out,a.receipts)
 else:
  context=None
  if a.phase in ('mysql-primary','postgres-both'):
   need(a.fixture_context is not None,'FIXTURE_CONTEXT_REQUIRED')
   import stat
   fd=os.open(a.fixture_context,os.O_RDONLY|os.O_NOFOLLOW)
   try:
    st=os.fstat(fd);need(stat.S_ISREG(st.st_mode) and stat.S_IMODE(st.st_mode)==0o600 and st.st_uid==os.geteuid() and st.st_nlink==1 and st.st_size<16384,'FIXTURE_CONTEXT_TYPE');data=os.read(fd,16385);need(len(data)==st.st_size,'FIXTURE_CONTEXT_LENGTH')
   finally:os.close(fd)
   v=json.loads(data);need(v['schema']=='authova-ci-private-fixture/v1' and v['source']==sha,'FIXTURE_CONTEXT_BINDING');context=v['env'];allowed={'AUTHOVA_MODE','AUTHOVA_BASE_URL','AUTHOVA_CONSOLE_URL','AUTHOVA_MASTER_KEY_FILE','AUTHOVA_TEST_DATABASE_DIALECT','AUTHOVA_TEST_DISPOSABLE_DATABASE','AUTHOVA_TEST_VALKEY_ADDRESS','AUTHOVA_TEST_MYSQL_DSN','AUTHOVA_TEST_MIGRATOR_MYSQL_DSN','AUTHOVA_TEST_POSTGRES_DSN','AUTHOVA_TEST_MIGRATOR_POSTGRES_DSN'};need(type(context) is dict and set(context)<=allowed and all(type(x) is str for x in context.values()),'FIXTURE_CONTEXT_KEYS')
  run_gate(a.phase,sha,a.out,fixture=context)
if __name__=='__main__':
 try:main()
 except Refused as e:
  value={'result':'REFUSED','code':e.args[0]}
  if getattr(e,'diagnostic',None) in DIAGNOSTICS:value['diagnostic']=e.diagnostic
  print(json.dumps(value));sys.exit(1)
