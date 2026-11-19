#!/usr/bin/env python3
"""Explicit network preparation in a fresh trusted-SHA job, never on release host."""
import argparse,json,os,subprocess,time
from pathlib import Path
import driver
GO_TOOLS=(
 ('google.golang.org/protobuf/cmd/protoc-gen-go','v1.36.11'),
 ('github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3','v3.0.0-20261026152050-dd0fb7df1e32'),
 ('github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen','v2.8.0'),
 ('golang.org/x/vuln/cmd/govulncheck','v1.9.0'),
 ('honnef.co/go/tools/cmd/staticcheck','v0.8.2'),
)
FAILURES={'PREPARE_FAILED','PREPARE_TIMEOUT','PREPARE_EXCEPTION','PREPARE_REPORT_FAILED','PREPARE_SETUP_FAILED','PREPARE_REFUSED'}

def close(fd,first):
 try:os.close(fd)
 except BaseException:
  if first is None:raise
  first.prepare_secondary=getattr(first,'prepare_secondary',())+('RECEIPT_CLOSE',)

def write_receipt(out,value,ordinal):
 # Each cumulative snapshot is durable before the next command or failure exit.
 # Fresh per-ordinal EXCL files also refuse replay after an uncertain write.
 temporary=out/('prepare-%02d.json.tmp'%ordinal)
 fd=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600);first=None
 try:
  body=memoryview((json.dumps(value,sort_keys=True,separators=(',',':'),allow_nan=False)+'\n').encode())
  while body:
   count=os.write(fd,body);driver.need(count>0,'PREPARE_REPORT_FAILED');body=body[count:]
  os.fsync(fd)
 except BaseException as exc:first=exc;raise
 finally:close(fd,first)
 os.replace(temporary,out/'prepare.json')
 fd=os.open(out,os.O_RDONLY|os.O_DIRECTORY);first=None
 try:os.fsync(fd)
 except BaseException as exc:first=exc;raise
 finally:close(fd,first)

def prepare(sha,out,runner=None):
 runner=subprocess.run if runner is None else runner
 tools=driver.ROOT/'.tools/bin';tools.mkdir(parents=True,exist_ok=True)
 env=driver.ci_environment(os.environ);env.update(GOBIN=str(tools),GOTOOLCHAIN='local',GOWORK='off');records=[]
 commands=[['go','mod','download']]+[['go','install',module+'@'+version] for module,version in GO_TOOLS]+[['npm','exec','--yes','--package=openapi-typescript@7.10.1','--','openapi-typescript','--version']]
 for number,argv in enumerate(commands):
  I=time.time();M=time.monotonic();code=None;known=False;first=None;failure=None;log=None
  try:
   log=(out/('prepare-%02d.log'%number)).open('xb');os.chmod(log.name,0o600)
   result=runner(argv,cwd=driver.ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,timeout=900)
   driver.need(type(result.returncode) is int,'PREPARE_EXCEPTION')
   code=result.returncode;known=True
   if code:first=driver.Refused('PREPARE_FAILED');failure='PREPARE_FAILED'
  except BaseException as exc:
   first=exc;failure='PREPARE_TIMEOUT' if isinstance(exc,subprocess.TimeoutExpired) else 'PREPARE_EXCEPTION'
  finally:
   if log is not None:
    try:log.close()
    except BaseException as exc:
     if first is None:first=exc;failure='PREPARE_EXCEPTION'
     else:first.prepare_secondary=getattr(first,'prepare_secondary',())+('LOG_CLOSE',)
  elapsed=None
  try:elapsed=time.monotonic()-M
  except BaseException as exc:
   if first is None:first=exc;failure='PREPARE_EXCEPTION'
   else:first.prepare_secondary=getattr(first,'prepare_secondary',())+('COMMAND_CLOCK',)
  records.append({'ordinal':number,'exit_code':code,'known_return':known,'I':I,'elapsed':elapsed})
  value={'schema':'authova-ci-prepare/v1','source':sha,'network':'EXPLICIT','records':records,
         'result':'FAIL' if first is not None else ('PASS' if number==len(commands)-1 else 'RUNNING'),'failure':failure}
  try:write_receipt(out,value,number)
  except BaseException as exc:
   if first is None:first=exc;failure='PREPARE_REPORT_FAILED'
   else:first.prepare_secondary=getattr(first,'prepare_secondary',())+('RECEIPT_WRITE',)
  if first is not None:
   first.prepare_failure=failure;first.prepare_ordinal=number;raise first
 return value

class Parser(argparse.ArgumentParser):
 def error(self,message):raise driver.Refused('PREPARE_REFUSED')

def main():
 p=Parser(description=__doc__);p.add_argument('--out',type=Path,required=True);a=p.parse_args();sha=driver.guard(os.environ.get('CI_EXPECTED_SHA'));driver.need(a.out.is_absolute() and not a.out.is_relative_to(driver.ROOT),'OUTSIDE_CHECKOUT_REQUIRED');a.out.mkdir(mode=0o700,exist_ok=False)
 prepare(sha,a.out)

def entry():
 try:main();return 0
 except BaseException as exc:
  if isinstance(exc,SystemExit) and exc.code==0 and not hasattr(exc,'prepare_failure'):raise
  code=getattr(exc,'prepare_failure','PREPARE_REFUSED' if isinstance(exc,driver.Refused) else 'PREPARE_SETUP_FAILED')
  if code not in FAILURES:code='PREPARE_EXCEPTION'
  ordinal=getattr(exc,'prepare_ordinal',None)
  if type(ordinal) is not int or not 0<=ordinal<=len(GO_TOOLS)+1:ordinal=None
  print(json.dumps({'result':'FAIL','code':code,'ordinal':ordinal},sort_keys=True))
  return 1

if __name__=='__main__':raise SystemExit(entry())
