#!/usr/bin/env python3
"""Read-only, working-tree import. Run only in an empty migration destination."""
import hashlib,json,pathlib,subprocess,shutil
root=pathlib.Path(__file__).resolve().parents[2]
sources=pathlib.Path('/Users/chenhuanxiang/Project/infrastructure')
records=[]
for name,dest in [('authova','.'),('authova-contracts','contracts'),('authova-console','console')]:
 src=sources/name
 status=subprocess.check_output(['git','status','--porcelain=v1','-z'],cwd=src).decode().split('\0')
 paths=sorted(set(subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z'],cwd=src).decode().split('\0'))-{''})
 for rel in paths:
  p=src/rel
  if not p.is_file(): continue
  parts=pathlib.PurePosixPath(rel).parts
  skip=bool(set(parts)&{'.git','.dev','node_modules','bin','output','dist','build','.cache','evidence'}) or p.suffix in {'.log','.db','.sqlite','.sqlite3','.pem','.key'} or (p.name.startswith('.env') and not p.name.endswith('.example'))
  if p.is_symlink(): raise RuntimeError('Review symlink before import: '+str(p))
  data=p.read_bytes()
  entry={'source':name,'path':rel,'sha256':hashlib.sha256(data).hexdigest(),'size':len(data),'decision':'excluded-runtime-or-old-output' if skip else 'imported','destination':str(pathlib.PurePosixPath(dest)/rel)}
  records.append(entry)
  if not skip:
   target=root/dest/rel
   if target.exists(): raise RuntimeError('Refusing overwrite '+str(target))
   target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(p,target)
 (root/'docs/migration'/f'{name}-status.json').write_text(json.dumps(status,ensure_ascii=False,indent=2)+'\n')
(root/'docs/migration/source-manifest.json').write_text(json.dumps(records,ensure_ascii=False,indent=2)+'\n')
print('Imported',sum(x['decision']=='imported' for x in records),'files; excluded',sum(x['decision']!='imported' for x in records))
