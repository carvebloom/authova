#!/usr/bin/env python3
"""Write a locked CycloneDX module inventory and copy actual upstream notice files."""
import hashlib,json,subprocess,uuid
from pathlib import Path
root=Path(__file__).resolve().parents[1]
output=root/'release'
output.mkdir(exist_ok=True)
raw=subprocess.check_output([str(root/'scripts/migration/go.sh'),'list','-m','-json','all'],cwd=root,text=True)
decoder=json.JSONDecoder();modules=[]
while raw.strip():
 raw=raw.lstrip();module,index=decoder.raw_decode(raw);modules.append(module);raw=raw[index:]
components=[];notices=[]
for module in modules:
 path=module['Path'];version=module.get('Version','0.1.0-rc.1')
 component={'type':'library','name':path,'version':version,'bom-ref':path+'@'+version,'purl':'pkg:golang/'+path+'@'+version}
 if module.get('Sum'):component['properties']=[{'name':'go:module:checksum','value':module['Sum']}]
 components.append(component)
 directory=module.get('Dir')
 if not directory and module.get('Version'):
  info=json.loads(subprocess.check_output([str(root/'scripts/migration/go.sh'),'mod','download','-json',path+'@'+version],cwd=root,text=True))
  directory=info.get('Dir')
 if directory:
  for name in ['LICENSE','LICENSE.txt','LICENSE.md','COPYING','NOTICE']:
   source=Path(directory)/name
   if source.is_file():
    data=source.read_bytes();target=output/'notices'/hashlib.sha256((path+'@'+version).encode()).hexdigest()[:16]/name
    target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data)
    notices.append({'module':path,'version':version,'file':str(target.relative_to(output)),'sha256':hashlib.sha256(data).hexdigest()})
bom={'bomFormat':'CycloneDX','specVersion':'1.6','serialNumber':'urn:uuid:'+str(uuid.uuid4()),'version':1,'metadata':{'component':{'type':'application','name':modules[0]['Path'],'version':'0.1.0-rc.1'},'properties':[{'name':'inventory:scope','value':'Go source module dependencies; not an OS image vulnerability scan or legal review'}]},'components':components}
(output/'sbom-go.cdx.json').write_text(json.dumps(bom,indent=2)+'\n')
(output/'dependency-notices.json').write_text(json.dumps(notices,indent=2)+'\n')
print(f'Locked {len(components)} Go modules and {len(notices)} upstream license/notice files.')
