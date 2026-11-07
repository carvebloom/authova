#!/usr/bin/env python3
"""Explicitly export reviewed inputs to a standalone Console checkout."""
from pathlib import Path
import argparse, hashlib, json, subprocess
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser()
p.add_argument('--console',type=Path,default=root.parent/'console')
p.add_argument('--check',action='store_true')
args=p.parse_args()
target=args.console.resolve()/'contract-inputs'
target.mkdir(parents=True,exist_ok=True)
inputs={'management.yaml':root/'openapi/v1/management.yaml','sdk-client.go.txt':root/'sdk/client.go'}
for name,source in inputs.items():
    if args.check:
        if (target/name).read_bytes()!=source.read_bytes():raise SystemExit('Console input drift: '+name)
    else:(target/name).write_bytes(source.read_bytes())
manifest={'format':1,'source':'github.com/carvebloom/authova/contracts','api_version':'0.1.0-rc.1','selection':'generated from api/authova/v1 Proto authority; content hashes pin consumer inputs','generators':{'go_sdk':'package rename v1','typescript':'openapi-typescript@7.10.1'},'sha256':{name:hashlib.sha256(source.read_bytes()).hexdigest() for name,source in inputs.items()}}
manifest_data=json.dumps(manifest,indent=2)+'\n'
if args.check:
    if (target/'manifest.json').read_text()!=manifest_data:raise SystemExit('Console manifest drift')
else:(target/'manifest.json').write_text(manifest_data)
subprocess.run(['python3',str(args.console.resolve()/'scripts/generate-contracts.py')]+(['--check'] if args.check else []),check=True)
