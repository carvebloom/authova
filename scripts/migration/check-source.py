#!/usr/bin/env python3
"""Re-hash original working-tree files without changing the source repositories."""
import hashlib,json
from pathlib import Path
root=Path(__file__).resolve().parents[2]
manifest=json.loads((root/'docs/migration/source-manifest.json').read_text())
base=Path('/Users/chenhuanxiang/Project/infrastructure')
changed=[]
for row in manifest:
 p=base/row['source']/row['path']
 if not p.is_file() or hashlib.sha256(p.read_bytes()).hexdigest()!=row['sha256']: changed.append(str(p))
if changed: raise SystemExit('Source changed since snapshot:\n'+'\n'.join(changed))
print(f'All {len(manifest)} source files unchanged since snapshot.')
