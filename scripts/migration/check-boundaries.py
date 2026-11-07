#!/usr/bin/env python3
"""Enforce module boundaries without requiring sibling repositories."""
from pathlib import Path
import re
root=Path(__file__).resolve().parents[2]
violations=[]
for folder in ['internal/domain','internal/application','internal/ports','contracts/sdk','contracts/mcpquery','console']:
 for path in (root/folder).rglob('*.go'):
  for imp in re.findall(r'"(github.com/carvebloom/authova/[^"\n]+)"',path.read_text()):
   if folder.startswith('internal/'):
    invalid='/internal/ent' in imp or '/internal/storage/' in imp
   elif folder=='console':
    invalid=('/internal/' in imp and '/console/internal/' not in imp and imp!='github.com/carvebloom/authova/internal/runtimehttp')
   else: invalid='/internal/' in imp
   if invalid:violations.append(str(path.relative_to(root))+': '+imp)
if violations:raise SystemExit('\n'.join(violations))
print('Domain/Ent, public SDK and Console process boundaries verified.')
