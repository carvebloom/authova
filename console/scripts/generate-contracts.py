#!/usr/bin/env python3
"""Generate/check bindings from this repository's explicitly pinned inputs."""
from pathlib import Path
import argparse, hashlib, json, subprocess, tempfile
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(); p.add_argument('--check',action='store_true'); args=p.parse_args()
inputs=root/'contract-inputs'
manifest=json.loads((inputs/'manifest.json').read_text())
if manifest['format']!=1 or manifest['generators']['typescript']!='openapi-typescript@7.10.1':
    raise SystemExit('Unsupported contract manifest')
for name in ['management.yaml','sdk-client.go.txt']:
    if hashlib.sha256((inputs/name).read_bytes()).hexdigest()!=manifest['sha256'][name]:
        raise SystemExit('Contract input hash mismatch: '+name)
source=(inputs/'sdk-client.go.txt').read_text()
go=source.replace('package sdk','package coreapi').replace('// Package sdk','// Code generated from authova-contracts/sdk/client.go; DO NOT EDIT.\n// Package coreapi').encode()
with tempfile.TemporaryDirectory() as directory:
    ts=Path(directory)/'management.ts'
    subprocess.run(['npx','--yes','openapi-typescript@7.10.1',str(inputs/'management.yaml'),'-o',str(ts)],cwd=root,check=True)
    outputs={root/'internal/coreapi/client.go':go,root/'web/src/api/generated/management.ts':ts.read_bytes()}
    for target,data in outputs.items():
        if args.check:
            if target.read_bytes()!=data: raise SystemExit('Generated contract drift: '+str(target.relative_to(root)))
        else: target.write_bytes(data)
print('Standalone contract bindings '+('verified.' if args.check else 'generated.'))
