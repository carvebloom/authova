#!/usr/bin/env python3
"""Reproduce the pinned Ent generator without modifying checked-in files."""
from pathlib import Path
import argparse,subprocess,tempfile
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('--check',action='store_true');args=p.parse_args()
with tempfile.TemporaryDirectory() as d:
 temp=Path(d)
 subprocess.run([str(root/'scripts/migration/go.sh'),'run','./cmd/entgen','--target',d],cwd=root,check=True)
 for source in temp.rglob('*.go'):
  target=root/'internal/ent'/source.relative_to(temp)
  if args.check:
   if not target.exists()or target.read_bytes()!=source.read_bytes():raise SystemExit('Ent output drift: '+str(target.relative_to(root)))
  else:target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(source.read_bytes())
 expected={p.relative_to(temp)for p in temp.rglob('*.go')}
 actual={p.relative_to(root/'internal/ent')for p in(root/'internal/ent').rglob('*.go')if'schema'not in p.relative_to(root/'internal/ent').parts}
 if expected!=actual:raise SystemExit('Unexpected Ent generated files: '+str(actual-expected))
 print('Ent generation '+('verified'if args.check else'updated')+': '+str(len(expected))+' files')
