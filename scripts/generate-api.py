#!/usr/bin/env python3
"""Single pinned Proto -> Go HTTP/OpenAPI -> Go SDK/TS contract pipeline."""
import argparse, os, re, subprocess, tempfile
from pathlib import Path
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('--check',action='store_true');p.add_argument('--install-tools',action='store_true');args=p.parse_args()
go=str(root/'scripts/migration/go.sh')
tools={
 'protoc-gen-go':('google.golang.org/protobuf/cmd/protoc-gen-go','v1.36.11'),
 'protoc-gen-go-http':('github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3','v3.0.0-20261026152050-dd0fb7df1e32'),
 'oapi-codegen':('github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen','v2.8.0'),
}
legacy=root/'.tools';prepared=legacy/'bin'
# Explicit standalone installation retains its historical directory. Otherwise
# any prepared generator selects that complete layout; no partial-layout mixing.
tool_dir=legacy if args.install_tools or not any(os.path.lexists(prepared/name) for name in tools) else prepared
env=os.environ|{'PATH':str(tool_dir)+os.pathsep+os.environ['PATH'],'GOBIN':str(tool_dir)}
if args.check and not args.install_tools:env.update(GOPROXY='off',GOTOOLCHAIN='local',GOWORK='off')
if subprocess.check_output(['protoc','--version'],text=True,env=env).strip()!='libprotoc 36.2':raise SystemExit('Requires protoc 36.2; see docs/migration/toolchain.json')
for name,(module,version) in tools.items():
 target=tool_dir/name
 if args.install_tools:subprocess.run([go,'install',module+'@'+version],env=env,cwd=root,check=True)
 # Old standalone installs contained only the Proto plugins. Keep their pinned
 # source invocation compatible, but check mode may only use cached modules.
 if name=='oapi-codegen' and tool_dir==legacy and not args.install_tools and not target.exists():continue
 if not target.exists():raise SystemExit('Missing pinned '+name+'; run scripts/generate-api.py --install-tools')
 meta=subprocess.check_output([go,'version','-m',str(target)],text=True,cwd=root,env=env)
 if not re.search(r'\b'+re.escape(module.removesuffix('/cmd/'+name))+r'\s+'+re.escape(version)+r'\s',meta):raise SystemExit('Incorrect tool version: '+name)
with tempfile.TemporaryDirectory() as d:
 temp=Path(d);descriptor=temp/'contract.pb'
 protos=['api/authova/wire/options.proto','api/authova/v1/management.proto','api/authova/v1/query.proto']
 subprocess.run(['protoc','-I.','-Ithird_party','--plugin=protoc-gen-go='+str(tool_dir/'protoc-gen-go'),'--plugin=protoc-gen-go-http='+str(tool_dir/'protoc-gen-go-http'),'--go_out='+d,'--go_opt=paths=source_relative','--go-http_out='+d,'--go-http_opt=paths=source_relative','--descriptor_set_out='+str(descriptor),'--include_imports']+protos,cwd=root,env=env,check=True)
 specs=temp/'contracts/openapi/v1'
 subprocess.run([go,'run','./cmd/proto-openapi','--descriptor',str(descriptor),'--output',str(specs)],cwd=root,env=env,check=True)
 for name in ['management','query']:
  config=temp/('codegen-'+name+'.yaml');client=temp/('contracts/generated/'+name+'/client.gen.go');client.parent.mkdir(parents=True)
  source=root/('contracts/codegen.yaml' if name=='management' else 'contracts/codegen-query.yaml')
  config.write_text(source.read_text().replace('output: generated/'+name+'/client.gen.go','output: '+str(client)))
  oapi=[str(tool_dir/'oapi-codegen')] if (tool_dir/'oapi-codegen').exists() else [go,'run','github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0']
  subprocess.run(oapi+['-config',str(config),str(specs/(name+'.yaml'))],cwd=root,env=env,check=True)
  types=temp/('contracts/generated/typescript/'+name+'.ts');types.parent.mkdir(parents=True,exist_ok=True)
  subprocess.run(['npm','exec']+(['--offline'] if args.check and not args.install_tools else [])+['--yes','--package=openapi-typescript@7.10.1','--','openapi-typescript',str(specs/(name+'.yaml')),'-o',str(types)],cwd=root/'console',env=env,check=True)
 for pattern in ['api/**/*.pb.go','contracts/openapi/v1/*.yaml','contracts/generated/**/*.go','contracts/generated/typescript/*.ts']:
  for generated in temp.glob(pattern):
   path=root/generated.relative_to(temp)
   if args.check:
    if not path.exists() or path.read_bytes()!=generated.read_bytes():raise SystemExit('Generated drift: '+str(path.relative_to(root)))
   else:path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(generated.read_bytes())
subprocess.run(['python3',str(root/'contracts/scripts/generate-bindings.py')]+(['--check'] if args.check else []),cwd=root,env=env,check=True)
print('Pinned Proto, HTTP, OpenAPI, Go client and Console TS outputs '+('verified.' if args.check else 'generated.'))
