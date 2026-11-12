#!/usr/bin/env python3
"""Select three local candidates, compare pins, then run real HTTP SDK checks."""
from pathlib import Path
import hashlib, json, os, subprocess, tempfile
root=Path(__file__).resolve().parents[1]
contracts=root/'contracts'; console=root/'console'
manifest=json.loads((console/'contract-inputs/manifest.json').read_text())
for name,source in {'management.yaml':contracts/'openapi/v1/management.yaml','sdk-client.go.txt':contracts/'sdk/client.go'}.items():
    if hashlib.sha256(source.read_bytes()).hexdigest()!=manifest['sha256'][name]:
        raise SystemExit('Candidate combination uses different contract inputs: '+name)
subprocess.run(['python3',str(contracts/'scripts/check-generated.py')],check=True)
subprocess.run(['python3',str(console/'scripts/generate-contracts.py'),'--check'],check=True)
env=os.environ.copy();env['GOWORK']='off'
subprocess.run(['python3','-B',str(root/'scripts/test_profiles.py'),'contract','--race','--dialect',env.get('AUTHOVA_TEST_DATABASE_DIALECT') or 'both'],cwd=root,env=env,check=True)
print('Local candidate combination verified; no remote release implied.')
