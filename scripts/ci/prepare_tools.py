#!/usr/bin/env python3
"""Explicit network preparation in a fresh trusted-SHA job, never on release host."""
import argparse,os,subprocess
from pathlib import Path
import driver
GO_TOOLS=(
 ('google.golang.org/protobuf/cmd/protoc-gen-go','v1.36.11'),
 ('github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3','v3.0.0-20261026152050-dd0fb7df1e32'),
 ('github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen','v2.8.0'),
 ('golang.org/x/vuln/cmd/govulncheck','v1.9.0'),
 ('honnef.co/go/tools/cmd/staticcheck','v0.8.2'),
)
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--out',type=Path,required=True);a=p.parse_args();driver.guard(os.environ.get('CI_EXPECTED_SHA'));driver.need(a.out.is_absolute() and not a.out.is_relative_to(driver.ROOT),'OUTSIDE_CHECKOUT_REQUIRED');a.out.mkdir(mode=0o700,exist_ok=False)
 tools=driver.ROOT/'.tools/bin';tools.mkdir(parents=True,exist_ok=True)
 env=dict(os.environ);env.update(GOBIN=str(tools),GOMAXPROCS='2',GOFLAGS='-p=2',GOMEMLIMIT='512MiB',GOTOOLCHAIN='local',GOWORK='off');records=[]
 commands=[['go','mod','download']]+[['go','install',module+'@'+version] for module,version in GO_TOOLS]+[['npm','exec','--yes','--package=openapi-typescript@7.10.1','--','openapi-typescript','--version']]
 for number,argv in enumerate(commands):
  with (a.out/('prepare-%02d.log'%number)).open('xb') as log:
   os.chmod(log.name,0o600);p=subprocess.run(argv,cwd=driver.ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,timeout=900)
  records.append({'ordinal':number,'exit_code':p.returncode,'known_return':True});driver.need(p.returncode==0,'PREPARE_FAILED')
 driver.dump(a.out/'prepare.json',{'schema':'authova-ci-prepare/v1','source':os.environ['CI_EXPECTED_SHA'],'network':'EXPLICIT','records':records})
if __name__=='__main__':main()
