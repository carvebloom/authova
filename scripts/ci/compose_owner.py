"""Installed Authova Core/Console operations; private projections stay in RAM."""
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import tarfile

from host_admission import canonical, strict_json
from host_adapter import durable, directory, _token, _close
from private_native import Native, Unknown
from release_contract import need

ROLES=('core','console')
COMPOSE_LABELS={'com.docker.compose.config-hash','com.docker.compose.image',
                'com.docker.compose.project.config_files'}
IMAGE_LABELS={'org.opencontainers.image.revision','io.carvebloom.artifact-manifest',
              'io.carvebloom.schema','io.carvebloom.role'}


def env_map(rows):
    need(type(rows) is list and all(type(v) is str and '=' in v for v in rows),'ENV_TYPE')
    result={}
    for row in rows:
        k,v=row.split('=',1)
        need(k not in result and re.fullmatch('[A-Za-z_][A-Za-z0-9_]*',k),'ENV_KEYS')
        result[k]=v
    return result


def env_file(values):
    # Compose has already interpreted quoting. Docker CLI env-file values are raw:
    # do not pass retained files directly or quote a second time. RAM/pipe only.
    need(type(values) is dict and all(type(k) is str and re.fullmatch('[A-Za-z_][A-Za-z0-9_]*',k)
         and type(v) is str and '\n' not in v and '\r' not in v and '\x00' not in v for k,v in values.items()),'PROBE_ENV_UNSUPPORTED')
    return ''.join(k+'='+v+'\n' for k,v in sorted(values.items())).encode()


def semantic_labels(labels):
    need(type(labels) is dict and all(type(k) is str and type(v) is str for k,v in labels.items()),'LABEL_TYPE')
    return {k:v for k,v in labels.items() if not k.startswith('com.docker.compose.') and k not in IMAGE_LABELS}


def stable(container):
    # Whole Config/HostConfig + stable network/mount semantics. Only ephemeral
    # endpoint/CID allocation is removed, never Env/volumes/resources/hardening.
    config=copy.deepcopy(container['Config'])
    config.pop('Hostname',None)  # default CID hostname, independently checked below
    networks={}
    for name,value in container['NetworkSettings']['Networks'].items():
        networks[name]={k:value.get(k) for k in ('NetworkID','IPAMConfig','Links')}
        for k in ('Aliases','DNSNames'):
            networks[name][k]=sorted(v for v in (value.get(k) or []) if not re.fullmatch('[0-9a-f]{12}|[0-9a-f]{64}',v))
    mounts=[{k:m.get(k) for k in ('Type','Name','Source','Destination','Driver','Mode','RW','Propagation')}
            for m in container['Mounts']]
    return {'config':config,'host':container['HostConfig'],'networks':networks,
            'mounts':sorted(mounts,key=lambda x:x['Destination'])}


def compare_config(old,new,candidate):
    expected=copy.deepcopy(old)
    for role in ROLES:
        expected['services'][role]['image']=candidate[role+'ID']
    expected['services']['console'].setdefault('environment',{})['AUTHOVA_CONSOLE_ASSETS']='/opt/authova-console/web'
    need(new==expected,'COMPOSE_SEMANTIC_DELTA')


def equivalent_representations(old,new,model,role):
    """Only source-justified empty argv and declared named-volume encodings."""
    a,b=old['config'],new['config']
    if a.get('Cmd')!=b.get('Cmd'):
        need(model is not None and model['services'][role].get('command') in (None,[])
             and type(a.get('Entrypoint')) is list and bool(a['Entrypoint'])
             and all(type(v) is str and v for v in a['Entrypoint']) and a['Entrypoint']==b.get('Entrypoint')
             and a.get('Cmd') in (None,[]) and b.get('Cmd') in (None,[]),'PROCESS_REPRESENTATION_UNSUPPORTED')
        a['Cmd']=b['Cmd']=None
    if any(old['host'].get(k)!=new['host'].get(k) for k in ('Binds','Mounts')):
        need(model is not None,'MOUNT_REPRESENTATION_AUTHORITY_REQUIRED')
        declared=[];nocopy={}
        for row in model['services'][role].get('volumes',[]):
            need(type(row) is dict and row.get('type')=='volume' and row.get('read_only') is True
                 and set(row)<= {'type','source','target','read_only','volume'}
                 and type(row.get('volume',{})) is dict and set(row.get('volume',{}))<= {'nocopy'},'MOUNT_DECLARATION_UNSUPPORTED')
            name=model.get('volumes',{}).get(row['source'],{}).get('name')
            need(type(name) is str and re.fullmatch('[A-Za-z0-9][A-Za-z0-9_.-]*',name)
                 and type(row['target']) is str and row['target'].startswith('/') and ':' not in row['target']
                 and type(row.get('volume',{}).get('nocopy',False)) is bool,'MOUNT_DECLARATION_UNSUPPORTED')
            declared.append((name,row['target']));nocopy[(name,row['target'])]=row.get('volume',{}).get('nocopy',False)
        need(bool(declared) and len(declared)==len(set(declared)),'MOUNT_DECLARATION_UNSUPPORTED')
        binds=sorted(n+':'+t+':ro' for n,t in declared)
        for value in (old,new):
            actual=value['mounts'];host=value['host']
            need(all(m['Type']=='volume' and m['RW'] is False for m in actual)
                 and sorted((m['Name'],m['Destination']) for m in actual)==sorted(declared),'MOUNT_ATTACHMENT_DELTA')
            if host.get('Binds'):
                need(type(host['Binds']) is list and sorted(host['Binds'])==binds and not host.get('Mounts')
                     and not any(nocopy.values()),'MOUNT_REPRESENTATION_UNSUPPORTED')
            else:
                rows=host.get('Mounts');need(type(rows) is list and len(rows)==len(declared),'MOUNT_REPRESENTATION_UNSUPPORTED')
                seen=[]
                for m in rows:
                    need(type(m) is dict and set(m)<= {'Type','Source','Target','ReadOnly','Consistency','VolumeOptions'}
                         and m.get('Type')=='volume' and m.get('ReadOnly') is True and m.get('Consistency','')==''
                         and type(m.get('VolumeOptions',{})) is dict and set(m.get('VolumeOptions',{}))<= {'NoCopy'},'MOUNT_REPRESENTATION_UNSUPPORTED')
                    key=(m.get('Source'),m.get('Target'));seen.append(key)
                    need(key in nocopy and type(m.get('VolumeOptions',{}).get('NoCopy',False)) is bool
                         and m.get('VolumeOptions',{}).get('NoCopy',False)==nocopy[key],'MOUNT_REPRESENTATION_UNSUPPORTED')
                need(sorted(seen)==sorted(declared),'MOUNT_REPRESENTATION_UNSUPPORTED')
            host['Binds']=binds;host['Mounts']=None


def post_semantics(before,after,candidate,images,layers,hashes,model=None):
    for role in ROLES:
        old,new=stable(before[role]),stable(after[role])
        expected=copy.deepcopy(old)
        c=expected['config']
        c['Image']=candidate[role+'ID']
        if role=='console':
            values=env_map(c['Env']);values['AUTHOVA_CONSOLE_ASSETS']='/opt/authova-console/web'
            c['Env']=[k+'='+v for k,v in sorted(values.items())]
        # Env is semantically a map but every key/value is compared.
        new['config']['Env']=[k+'='+v for k,v in sorted(env_map(new['config']['Env']).items())]
        c['Env']=[k+'='+v for k,v in sorted(env_map(c['Env']).items())]
        for name in IMAGE_LABELS:
            c.setdefault('Labels',{})[name]=images[role]['Config']['Labels'][name]
        c['Labels']['com.docker.compose.config-hash']=hashes[role]
        c['Labels']['com.docker.compose.image']=candidate[role+'ID']
        c['Labels']['com.docker.compose.project.config_files']=','.join(layers)
        equivalent_representations(expected,new,model,role)
        need(new==expected,'RUNTIME_SEMANTIC_DELTA')
        # Only a Docker-assigned CID hostname may change implicitly.
        need(before[role]['Config']['Hostname'] in (before[role]['Id'][:12],before[role]['Id'])
             and after[role]['Config']['Hostname'] in (after[role]['Id'][:12],after[role]['Id']),'EXPLICIT_HOSTNAME_UNSUPPORTED')


def archive_graph(tar, candidate, guard):
    """Selected Docker29 OCI save graph; never extract, decompress or follow URLs.

    Candidate IDs are index digests, not config digests. Attestations are hashed
    graph members only: their presence confers no provenance/trust qualification.
    """
    index_type='application/vnd.oci.image.index.v1+json'
    manifest_type='application/vnd.oci.image.manifest.v1+json'
    config_type='application/vnd.oci.image.config.v1+json'
    files={};names=set()
    for item in tar:
        guard();need(len(names)<4096 and item.name not in names,'ARCHIVE_MEMBERS')
        names.add(item.name)
        if item.isdir():
            need(item.name in ('blobs','blobs/sha256'),'ARCHIVE_PATH')
        else:
            need(item.isreg() and (item.name in ('index.json','manifest.json','oci-layout')
                 or re.fullmatch('blobs/sha256/[0-9a-f]{64}',item.name)),'ARCHIVE_PATH')
            need(0<=item.size<=512*1024*1024,'ARCHIVE_MEMBER_SIZE');files[item.name]=item
    need(sum(m.size for m in files.values())<=512*1024*1024,'ARCHIVE_MEMBER_SIZE')
    used=set();verified={}
    def read(name, *, limit=1048576, digest=None, size=None, collect=True):
        guard()
        need(name in files,'ARCHIVE_MISSING_BLOB');item=files[name]
        need(item.size<=limit and (size is None or item.size==size),'ARCHIVE_DESCRIPTOR_SIZE')
        used.add(name)
        if digest is not None and name in verified:
            need(verified[name]==(digest,item.size),'ARCHIVE_DESCRIPTOR_SIZE')
            if not collect:return None
        h=hashlib.sha256();chunks=[];count=0
        stream=None;first=None
        try:
            stream=tar.extractfile(item)
            while True:
                guard();chunk=stream.read(65536);guard()
                if not chunk:break
                count+=len(chunk);need(count<=item.size,'ARCHIVE_MEMBER_SIZE');h.update(chunk)
                if collect:chunks.append(chunk)
        except BaseException as exc:first=exc;raise
        finally:
            if stream is not None:
                try:stream.close()
                except BaseException:
                    if first is None:raise
                    first.close_secondary=getattr(first,'close_secondary',())+('ARCHIVE_MEMBER_CLOSE',)
            try:guard()
            except BaseException:
                if first is None:raise
                first.close_secondary=getattr(first,'close_secondary',())+('ARCHIVE_MEMBER_POSTCLOCK',)
        need(count==item.size,'ARCHIVE_MEMBER_SIZE')
        if digest is not None:
            need(h.hexdigest()==digest[7:],'ARCHIVE_BLOB_SHA');verified[name]=(digest,count)
        return b''.join(chunks) if collect else None
    def document(name):return strict_json(read(name))
    def descriptor(d,media,extra=frozenset(),collect=True):
        need(type(d) is dict and set(d)=={'mediaType','digest','size'}|set(extra)
             and d['mediaType']==media and type(d['digest']) is str
             and re.fullmatch('sha256:[0-9a-f]{64}',d['digest'])
             and type(d['size']) is int and 0<=d['size']<=512*1024*1024,'ARCHIVE_DESCRIPTOR')
        return read('blobs/sha256/'+d['digest'][7:],digest=d['digest'],size=d['size'],
                    limit=1048576 if collect else 512*1024*1024,collect=collect)
    runtime_details={}
    def image_manifest(d,extra,attestation=False):
        v=strict_json(descriptor(d,manifest_type,extra))
        need(type(v) is dict and set(v)=={'schemaVersion','mediaType','config','layers'}
             and type(v['schemaVersion']) is int and v['schemaVersion']==2
             and v['mediaType']==manifest_type and type(v['layers']) is list
             and 0<len(v['layers'])<=256,'ARCHIVE_IMAGE_MANIFEST')
        config=strict_json(descriptor(v['config'],config_type))
        need(type(config) is dict and config.get('os')==('unknown' if attestation else 'linux')
             and config.get('architecture')==('unknown' if attestation else 'arm64'),'ARCHIVE_PLATFORM')
        if not attestation:
            rootfs=config.get('rootfs')
            need(type(rootfs) is dict and set(rootfs)=={'type','diff_ids'} and rootfs['type']=='layers'
                 and type(rootfs['diff_ids']) is list and len(rootfs['diff_ids'])==len(v['layers'])
                 and all(type(x) is str and re.fullmatch('sha256:[0-9a-f]{64}',x) for x in rootfs['diff_ids']), 'ARCHIVE_ROOTFS')
        for layer in v['layers']:
            if attestation:
                descriptor(layer,'application/vnd.in-toto+json',{'annotations'},collect=False)
                need(layer['annotations']=={'in-toto.io/predicate-type':'https://slsa.dev/provenance/v1'},'ARCHIVE_ATTESTATION')
            else:
                need(type(layer) is dict and layer.get('mediaType') in
                     ('application/vnd.oci.image.layer.v1.tar+gzip','application/vnd.oci.image.layer.v1.tar'),'ARCHIVE_LAYER_TYPE')
                descriptor(layer,layer['mediaType'],collect=False)
        if not attestation:
            labels=config.get('config',{}).get('Labels') or {}
            need(type(labels) is dict,'ARCHIVE_CONFIG_LABELS')
            runtime_details[d['digest']]={'config':v['config']['digest'],
                'layers':copy.deepcopy(v['layers']),'rootfs_diff_ids':list(config['rootfs']['diff_ids']),
                'provenance_labels':{k:labels.get(k) for k in IMAGE_LABELS}}
        return {'Config':'blobs/sha256/'+v['config']['digest'][7:],'RepoTags':None,
                'Layers':['blobs/sha256/'+x['digest'][7:] for x in v['layers']]}
    need(document('oci-layout')=={'imageLayoutVersion':'1.0.0'},'ARCHIVE_LAYOUT')
    index=document('index.json')
    need(type(index) is dict and set(index)=={'schemaVersion','mediaType','manifests'}
         and type(index['schemaVersion']) is int and index['schemaVersion']==2
         and index['mediaType']==index_type and type(index['manifests']) is list
         and len(index['manifests'])==2,'ARCHIVE_INDEX')
    ids={candidate[r+'ID'] for r in ROLES}
    need(len(ids)==2 and all(type(v) is str and re.fullmatch('sha256:[0-9a-f]{64}',v) for v in ids),'ARCHIVE_CANDIDATES')
    need(all(type(d) is dict and type(d.get('digest')) is str for d in index['manifests'])
         and {d['digest'] for d in index['manifests']}==ids,'ARCHIVE_CANDIDATES')
    expected=[];graph={}
    for d in index['manifests']:
        v=strict_json(descriptor(d,index_type))
        need(type(v) is dict and set(v)=={'schemaVersion','mediaType','manifests'}
             and type(v['schemaVersion']) is int and v['schemaVersion']==2
             and v['mediaType']==index_type and type(v['manifests']) is list
             and len(v['manifests'])==2,'ARCHIVE_IMAGE_INDEX')
        runtime=[m for m in v['manifests'] if type(m) is dict and m.get('platform')=={'os':'linux','architecture':'arm64'}]
        attest=[m for m in v['manifests'] if type(m) is dict and m.get('platform')=={'os':'unknown','architecture':'unknown'}]
        need(len(runtime)==len(attest)==1,'ARCHIVE_PLATFORM')
        expected.append(image_manifest(runtime[0],{'platform'}))
        role=next(r for r in ROLES if candidate[r+'ID']==d['digest'])
        graph[role]=dict(root=d['digest'],runtime_manifest=runtime[0]['digest'],**runtime_details[runtime[0]['digest']])
        image_manifest(attest[0],{'platform','annotations'},attestation=True)
        need(attest[0]['annotations']=={'vnd.docker.reference.digest':runtime[0]['digest'],
             'vnd.docker.reference.type':'attestation-manifest'},'ARCHIVE_ATTESTATION')
    manifest=document('manifest.json')
    need(type(manifest) is list and len(manifest)==2
         and all(type(row) is dict for row in manifest)
         and sorted(manifest,key=lambda r:str(r.get('Config')))==sorted(expected,key=lambda r:r['Config']), 'DOCKER_ARCHIVE_GRAPH')
    need(used==set(files),'ARCHIVE_UNREACHABLE_BLOB')
    need(len({v['config'] for v in graph.values()})==2 and len({v['runtime_manifest'] for v in graph.values()})==2,'ARCHIVE_ROLE_COLLISION')
    return graph


class CoreConsoleOwner:
    def __init__(self, profile, state_root, nonce):
        need(re.fullmatch('[0-9a-f]{64}',nonce) is not None,'OPERATION_NONCE')
        self.profile=profile;self.state_root=state_root;self.nonce=nonce
        self.native=Native(profile['docker'],profile['docker_host'],profile['docker_config'])
        self.compose_native=Native(profile['compose'],profile['docker_host'],profile['docker_config'])
        self.daemon_checked=False
        self.operation=Path(state_root)/'operations'/nonce[:24]
        self.original=None;self.control=None;self.post=None;self.images={};self.up_started=False
        self.cfg=None;self.override=None;self.hashes={};self.old_images={};self.secret_inputs={}
        self.production_graph=None;self.graph_candidate=None
        self.web_assets=None;self.old_web_assets={}

    def compile_assets(self,candidate,guard):
        from host_adapter import read_private
        from deployment_http import manifest_assets
        selected=self.profile['compile_manifest']
        need(type(selected) is dict and set(selected)=={'path','identity'} and type(selected['path']) is str
             and Path(selected['path']).is_absolute() and type(selected['identity']) is list
             and len(selected['identity'])==9 and all(type(v) is int for v in selected['identity']),'WEB_MANIFEST_INPUT')
        p=Path(selected['path']);guard()
        with directory(str(p.parent),os.getuid()) as root:
            before=_token(os.stat(p.name,dir_fd=root,follow_symlinks=False))
            need(list(before)==selected['identity'],'WEB_MANIFEST_IDENTITY')
            raw=read_private(root,p.name,os.getuid(),limit=1024*1024)
            need(before==_token(os.stat(p.name,dir_fd=root,follow_symlinks=False)),'WEB_MANIFEST_DRIFT')
        result=manifest_assets(raw,candidate);guard();return result

    def command(self,tag,args,guard,**kw):
        if not self.daemon_checked:
            value=self.native.require('DAEMON_BINDING',['info','--format','{{.ID}} {{.OSType}} {{.Architecture}}'],guard).decode().split()
            need(value==[self.profile['daemon_id'],'linux','aarch64'],'DAEMON_IDENTITY')
            self.daemon_checked=True
        if args[0]=='compose':return self.compose_native.require(tag,args[1:],guard,**kw)
        return self.native.require(tag,args,guard,**kw)
    def compose_run(self,tag,args,guard,**kw):
        need(args[0]=='compose','COMPOSE_ARGV')
        return self.compose_native.run(tag,args[1:],guard,**kw)
    def compose(self,binding,layers=None):
        argv=['compose','--project-name',binding['project'],'--env-file','/dev/null']
        for p in (layers or binding['current']['layers']):argv+=['-f',p]
        return argv
    def inspect_services(self,binding,guard):
        result={}
        for role in ROLES:
            raw=self.command('SELECT_SERVICE',['ps','--all','--quiet','--no-trunc','--filter',
                   'label=com.docker.compose.project='+binding['project'],'--filter',
                   'label=com.docker.compose.service='+role],guard)
            ids=raw.decode('ascii').split()
            need(len(ids)==1 and re.fullmatch('[0-9a-f]{64}',ids[0]),'SERVICE_SINGLETON')
            value=strict_json(self.command('INSPECT_SERVICE',['inspect',ids[0]],guard))
            need(type(value) is list and len(value)==1 and value[0]['Id']==ids[0],'SERVICE_INSPECT')
            c=value[0];labels=c['Config'].get('Labels') or {}
            need(labels.get('com.docker.compose.project')==binding['project'] and labels.get('com.docker.compose.service')==role
                 and labels.get('com.docker.compose.oneoff')=='False','SERVICE_OWNERSHIP')
            result[role]=c
        return result
    def control_value(self):
        with directory(str(Path(self.profile['active_control']).parent),os.getuid(),private=False) as root:
            # Public control is commonly0644, so read through explicit regular metadata
            # consumer rather than relaxing the private0600 credential reader.
            fd=os.open(Path(self.profile['active_control']).name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=root)
            first=None
            try:
                import stat
                s=os.fstat(fd);need(stat.S_ISREG(s.st_mode) and stat.S_IMODE(s.st_mode) in (0o600,0o644) and s.st_nlink==1 and s.st_uid==os.getuid() and s.st_size<=65536,'CONTROL_METADATA')
                body=os.read(fd,65537);need(len(body)==s.st_size and _token(s)==_token(os.fstat(fd))==_token(os.stat(Path(self.profile['active_control']).name,dir_fd=root,follow_symlinks=False)),'CONTROL_DRIFT')
            except BaseException as exc:first=exc;raise
            finally:_close(fd,first,'CONTROL_CLOSE')
        return strict_json(body)
    def current(self,binding,guard):
        previous=getattr(self,"phase","BOOTSTRAP");self.phase="CURRENT"
        value=self.control_value()
        need(value['project']==binding['project'] and value['keep_all_layers_in_order'] is True,'CONTROL_PROJECT')
        if 'compose_layers' in value:
            need(value['schema']=='authova-two-service-active-control/v1','CONTROL_SCHEMA')
            layers=value['compose_layers']
        else:
            need(value['schema']=='authova-console-static-active-control/v1','CONTROL_SCHEMA')
            layers=[value[k] for k in ('retained_compose','retained_prior_override','active_override')]
        if self.control is not None:need(value==self.control,'CURRENT_CONTROL_WHOLE_CAS')
        else:self.control=value
        containers=self.inspect_services(binding,guard)
        need(value['images']=={r:containers[r]['Image'] for r in ROLES},'CONTROL_ACTUAL_IMAGES')
        self.phase=previous if previous!='BOOTSTRAP' else 'CURRENT'
        return dict(schema='authova-owner-current/v1',project=binding['project'],
                    schemaDigest=binding['current']['schemaDigest'],layers=layers,
                    services={r:{'id':containers[r]['Id'],'image':containers[r]['Image']} for r in ROLES})
    def selected_private_inputs(self,binding,guard):
        # Parse only the retained JSON layers and the three explicitly selected
        # env sources. Reject Compose includes/extends/label_file before the CLI
        # can follow another file (rendering removes label_file after reading it).
        # Body comparison is RAM-only; never content hash.
        base=Path(binding['current']['layers'][0]).parent
        allowed={str(base/r/'service.env') for r in ('core','console','migrator')}
        for path in binding['current']['layers']:
            body=self.private_input(path,guard)
            layer=strict_json(body)
            need(type(layer) is dict and not {'include','extends'}&set(layer),'COMPOSE_FILE_INDIRECTION')
            for role,service in layer.get('services',{}).items():
                need(type(service) is dict and not {'include','extends','label_file'}&set(service),'COMPOSE_SERVICE_INDIRECTION')
                rows=service.get('env_file',[])
                if type(rows) is str:rows=[rows]
                need(type(rows) is list,'ENV_FILE_LIST')
                for row in rows:
                    if type(row) is dict:
                        need(set(row)<= {'path','required'} and row.get('required',True) is True,'ENV_FILE_OPTIONS')
                        row=row['path']
                    need(type(row) is str,'ENV_FILE_PATH')
                    p=str(Path(path).parent/row) if not row.startswith('/') else row
                    need(p in allowed,'ENV_FILE_NOT_SELECTED')
                    self.private_input(p,guard)
    def private_input(self,path,guard):
        from host_adapter import read_private
        guard();p=Path(path)
        with directory(str(p.parent),os.getuid()) as root:
            body=read_private(root,p.name,os.getuid(),limit=1048576)
            token=_token(os.stat(p.name,dir_fd=root,follow_symlinks=False))
        previous=self.secret_inputs.get(path)
        if previous is not None:need(previous==(token,body),'PRIVATE_INPUT_CAS')
        else:self.secret_inputs[path]=(token,body)
        guard();return body
    def input_guard(self,guard):
        for path in tuple(self.secret_inputs):self.private_input(path,guard)
    def render_bytes(self,binding,guard,layers=None):
        self.input_guard(guard)
        body=self.command('COMPOSE_PRIVATE_CONFIG',self.compose(binding,layers)+['config','--format','json'],guard)
        model=strict_json(body)
        need(type(model) is dict and not {'include','extends'}&set(model)
             and model.get('name')==binding['project'] and type(model.get('services')) is dict
             and set(ROLES)<=set(model['services']),'COMPOSE_RESOLVED_MODEL')
        # config --format json resolves env_file and escapes literal dollars.
        # Keep the tool's exact bytes; never dump the parsed model or rewrite $$.
        need(all(type(s) is dict and 'env_file' not in s for s in model['services'].values()),'COMPOSE_UNRESOLVED_ENV_FILE')
        need(all(not {'include','extends','label_file'}&set(s) for s in model['services'].values()),'COMPOSE_SERVICE_INDIRECTION')
        self.input_guard(guard);return body
    def render(self,binding,guard,layers=None):
        return strict_json(self.render_bytes(binding,guard,layers))
    def resolved_hashes(self,binding,guard,body,tag):
        need(tag in ('COMPOSE_HASH','ROLLBACK_HASH'),'COMPOSE_HASH_TAG')
        base=Path(binding['current']['layers'][0])
        need(base.is_absolute(),'COMPOSE_PROJECT_DIRECTORY')
        argv=['compose','--project-name',binding['project'],'--project-directory',str(base.parent),
              '--env-file','/dev/null','-f','-','config','--hash']
        result={}
        for role in ROLES:
            self.input_guard(guard)
            row=self.command(tag,argv+[role],guard,body=body).decode('ascii').split()
            self.input_guard(guard)
            need(len(row)==2 and row[0]==role and re.fullmatch('[0-9a-f]{64}',row[1]),
                 'ROLLBACK_HASH_FORMAT' if tag=='ROLLBACK_HASH' else 'COMPOSE_HASH_FORMAT')
            result[role]=row[1]
        return result
    def image(self,image,guard):
        raw=strict_json(self.command('IMAGE_LOCAL_ONLY',['image','inspect',image],guard))
        need(type(raw) is list and len(raw)==1 and raw[0]['Id']==image and raw[0]['Os']=='linux' and raw[0]['Architecture']=='arm64','IMAGE_IDENTITY')
        return raw[0]
    def setup_directory(self):
        parent=self.operation.parent
        try:parent.mkdir(mode=0o700)
        except FileExistsError:pass
        need(not parent.is_symlink() and (parent.stat().st_mode&0o777)==0o700 and parent.stat().st_uid==os.getuid(),'OPERATION_PARENT')
        self.operation.mkdir(mode=0o700)
    def write(self,name,value):
        with directory(str(self.operation),os.getuid()) as fd:durable(fd,name,value)
        return str(self.operation/name)
    def archive_guard(self,candidate,guard):
        path=self.profile['archive']['path'];expected=self.profile['archive']
        fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW);first=None
        try:
            import stat
            s=os.fstat(fd);need(stat.S_ISREG(s.st_mode) and s.st_nlink==1 and list(_token(s))==expected['identity'] and s.st_size<=512*1024*1024,'ARCHIVE_METADATA')
            h=hashlib.sha256();count=0
            while True:
                guard();body=os.read(fd,65536)
                if not body:break
                h.update(body);count+=len(body)
            need(count==s.st_size and h.hexdigest()==candidate['archive'],'ARCHIVE_SHA')
            os.lseek(fd,0,os.SEEK_SET)
            with os.fdopen(os.dup(fd),'rb') as stream,tarfile.open(fileobj=stream,mode='r:') as tar:
                graph=archive_graph(tar,candidate,guard)
            need(_token(s)==_token(os.fstat(fd))==_token(os.stat(path,follow_symlinks=False)),'ARCHIVE_DRIFT')
        except BaseException as exc:first=exc;raise
        finally:_close(fd,first,'ARCHIVE_CLOSE')
        return graph
    def production_ids(self,binding):
        # Only this production layout resolver can select a config ID. The
        # signed candidate/test receipt still identify the original index roots.
        need(self.profile['role']=='application-production' and binding['action']=='production'
             and self.graph_candidate==binding['candidate'] and type(self.production_graph) is dict
             and set(self.production_graph)==set(ROLES),'PRODUCTION_GRAPH_BINDING')
        result={}
        for role in ROLES:
            node=self.production_graph[role]
            need(node['root']==binding['candidate'][role+'ID'] and node['provenance_labels']=={
                 'org.opencontainers.image.revision':binding['candidate']['source'],
                 'io.carvebloom.artifact-manifest':binding['candidate']['manifest'],
                 'io.carvebloom.schema':binding['candidate']['schemaDigest'],'io.carvebloom.role':role},'PRODUCTION_GRAPH_PROVENANCE')
            result[role+'ID']=node['config']
        return result
    def production_image(self,binding,role,guard):
        image=self.image(self.production_ids(binding)[role+'ID'],guard)
        need(image.get('RootFS')=={'Type':'layers','Layers':self.production_graph[role]['rootfs_diff_ids']},'PRODUCTION_ROOTFS_IDENTITY')
        return image
    def preflight(self,binding,guard):
        self.phase='PREFLIGHT'
        self.setup_directory();self.production_graph=self.archive_guard(binding['candidate'],guard);self.graph_candidate=copy.deepcopy(binding['candidate'])
        self.web_assets=self.compile_assets(binding['candidate'],guard)
        selected_ids=self.production_ids(binding);self.selected_private_inputs(binding,guard)
        from deployment_http import readonly_acceptance
        readonly_acceptance(self.profile,guard,capture=self.old_web_assets)  # current before-bytes only; not new candidate qualification
        self.original=self.inspect_services(binding,guard)
        self.cfg=self.render(binding,guard)
        need(set(ROLES)<=set(self.cfg['services']),'COMPOSE_SERVICES')
        for role,c in self.original.items():
            need(c['State']['Running'] is True and c['State'].get('Health',{}).get('Status')=='healthy','OLD_RUNTIME_HEALTH')
            old=self.image(c['Image'],guard);self.old_images[role]=old  # rollback immutable local image, no pull
            values=env_map(old['Config'].get('Env') or [])
            desired=self.cfg['services'][role].get('environment') or {}
            need(all(type(k) is str and type(v) is str for k,v in desired.items()),'COMPOSE_ENV_UNSUPPORTED')
            values.update(desired)
            need(values==env_map(c['Config']['Env']),'CURRENT_ENV_SEMANTICS')
            env_file(values)  # explicitly rejects env-file newline ambiguity before any mutation
        # Preserve the current container's effective non-provenance labels, not
        # the new image's defaults. They can contain private values: this fixed
        # override stays0600 inside the private operation root, never safe output.
        override={'services':{}}
        expected_cfg=copy.deepcopy(self.cfg)
        for role in ROLES:
            kept=semantic_labels(self.original[role]['Config'].get('Labels') or {})
            configured=semantic_labels(self.cfg['services'][role].get('labels') or {})
            need(all(k in kept and kept[k]==v for k,v in configured.items()),'CURRENT_LABEL_CONFIG_CAS')
            labels=dict(kept,**self.production_graph[role]['provenance_labels'])
            override['services'][role]={'image':selected_ids[role+'ID'],'labels':labels}
            expected_cfg['services'][role]['labels']=labels
        override['services']['console']['environment']={'AUTHOVA_CONSOLE_ASSETS':'/opt/authova-console/web'}
        self.override=self.write('candidate-private-override.json',override)
        self.private_input(self.override,guard)  # RAM held bytes/identity, no private value hash
        layers=binding['current']['layers']+[self.override]
        resolved=self.render_bytes(binding,guard,layers)
        self.prospective_cfg=strict_json(resolved)
        compare_config(expected_cfg,self.prospective_cfg,selected_ids)
        self.hashes=self.resolved_hashes(binding,guard,resolved,'COMPOSE_HASH')
    def oneshot(self,binding,guard,image,args,values,mounts,*,network,body=None,tag='SCHEMA_PROBE',entrypoint=None):
        name=binding['project']+'-'+tag.lower().replace('_','-')+'-'+self.nonce[:12]
        argv=['create','--pull=never','--interactive','--name',name,'--network',network,'--read-only','--cap-drop','ALL',
              '--security-opt','no-new-privileges:true','--user','10001:10001','--memory','256m','--memory-swap','256m','--cpus','0.5','--pids-limit','64',
              '--label','io.carvebloom.ci.operation='+self.nonce,'--env-file','/dev/stdin']
        for m in mounts:
            need(m['RW'] is False and m['Type'] in ('bind','volume'),'PROBE_MOUNT')
            source=m['Name'] if m['Type']=='volume' else m['Source']
            mount='type='+m['Type']+',source='+source+',target='+m['Destination']+',readonly'
            if m['Type']=='volume':mount+=',volume-nocopy'
            argv+=['--mount',mount]
        if entrypoint is not None:argv+=['--entrypoint',entrypoint]
        cid=self.command('HELPER_CREATE',argv+[image,*args],guard,body=env_file(values)).decode().strip()
        need(re.fullmatch('[0-9a-f]{64}',cid) is not None,'HELPER_CREATE_ACK_UNKNOWN')
        self.write(tag+'-ack.json',{'id':cid,'image':image,'operation':self.nonce})
        rc,output=self.native.run('HELPER_START_WAIT',['start','--attach','--interactive',cid],guard,body=body,whole=90,suffix=240)
        state=strict_json(self.command('HELPER_STATE',['inspect','--format','{{json .State}}',cid],guard))
        need(state['Running'] is False and type(state['ExitCode']) is int and state['ExitCode']==rc,'HELPER_WAIT_UNKNOWN')
        self.command('HELPER_REMOVE',['rm',cid],guard)
        remaining=self.command('HELPER_ABSENCE',['ps','--all','--quiet','--no-trunc','--filter','id='+cid],guard)
        need(not remaining.strip(),'HELPER_ABSENCE_UNKNOWN')
        return rc,output
    def apply(self,binding,guard):
        self.phase='APPLY'
        # Loading/creating a read-only schema probe are Docker mutations, so both
        # happen here AFTER durable signed nonce consumption, never in preflight.
        need(self.archive_guard(binding['candidate'],guard)==self.production_graph,'PRODUCTION_GRAPH_CAS')
        selected_ids=self.production_ids(binding)
        self.command('IMAGE_LOAD',['image','load','--input',self.profile['archive']['path']],guard,whole=120,suffix=300)
        for role in ROLES:
            image=self.production_image(binding,role,guard);labels=image['Config'].get('Labels') or {}
            need(image['Config']['User']=='10001:10001' and image['Config']['Entrypoint']==['authova' if role=='core' else 'authova-console']
                 and labels.get('org.opencontainers.image.revision')==binding['candidate']['source']
                 and labels.get('io.carvebloom.artifact-manifest')==binding['candidate']['manifest']
                 and labels.get('io.carvebloom.schema')==binding['candidate']['schemaDigest']
                 and labels.get('io.carvebloom.role')==role,'CANDIDATE_IMAGE_PROVENANCE')
            self.images[role]=image
            values=env_map(image['Config'].get('Env') or []);values.update(self.cfg['services'][role].get('environment') or {})
            original=env_map(self.original[role]['Config']['Env'])
            if role=='console':
                values['AUTHOVA_CONSOLE_ASSETS']='/opt/authova-console/web';original['AUTHOVA_CONSOLE_ASSETS']='/opt/authova-console/web'
            if values!=original:return 'PREFLIGHT_REFUSED'
            # Use the actual rendered prospective configuration, including the
            # protected retained labels. Every extra/new label still refuses.
            actual_labels=dict(image['Config'].get('Labels') or {});actual_labels.update(self.prospective_cfg['services'][role].get('labels') or {})
            old_labels=dict(self.original[role]['Config'].get('Labels') or {})
            for key in list(old_labels):
                if key.startswith('com.docker.compose.') or key in IMAGE_LABELS:old_labels.pop(key)
            for key in list(actual_labels):
                if key.startswith('com.docker.compose.') or key in IMAGE_LABELS:actual_labels.pop(key)
            if old_labels!=actual_labels:return 'PREFLIGHT_REFUSED'
        core=self.original['core'];mounts=core['Mounts']
        values=env_map(core['Config']['Env'])
        files=[values[k] for k in ('AUTHOVA_MASTER_KEY_FILE','AUTHOVA_ENCRYPTION_KEYRING_FILE','AUTHOVA_VALKEY_PASSWORD_FILE',
                                  'AUTHOVA_VALKEY_TLS_CA_FILE','AUTHOVA_VALKEY_TLS_CERT_FILE','AUTHOVA_VALKEY_TLS_KEY_FILE') if values.get(k)]
        needed=set()
        for path in files:
            need(path.startswith('/') and all(v not in ('','.','..') for v in path.split('/')[1:]),'PROBE_FILE_PATH')
            candidates=[m for m in mounts if m['Destination']!='/' and (path==m['Destination'] or path.startswith(m['Destination'].rstrip('/')+'/'))]
            need(bool(candidates),'PROBE_FILE_NOT_MOUNTED')
            m=max(candidates,key=lambda value:len(value['Destination']))
            need(m['RW'] is False and m['Type'] in ('bind','volume'),'PROBE_SELECTED_RO_MOUNTS')
            needed.add(m['Destination'])
        selected=[m for m in mounts if m['Destination'] in needed]
        rc,frame=self.oneshot(binding,guard,selected_ids['coreID'],['schema-check'],env_map(core['Config']['Env']),selected,network='container:'+core['Id'])
        if rc!=0:return 'PREFLIGHT_REFUSED'
        need(strict_json(frame)=={'schema':'authova-runtime-schema/v1','result':'PASS'},'SCHEMA_FRAME')
        need(self.current(binding,guard)==binding['current'] and self.render(binding,guard)==self.cfg,'PRE_UP_CURRENT_CAS')
        current=self.inspect_services(binding,guard)
        need(all(stable(current[r])==stable(self.original[r]) for r in ROLES),'PRE_UP_PRIVATE_CAS')
        guard.full(120,600)
        self.input_guard(guard)
        self.up_started=True
        rc,_=self.compose_run('SELECTIVE_UP',self.compose(binding,binding['current']['layers']+[self.override])+['up','--detach','--no-deps','--no-build','--pull','never','--wait','--wait-timeout','90','core','console'],guard,whole=120,suffix=600)
        self.input_guard(guard)
        self.post=self.inspect_services(binding,guard)
        need(all(self.post[r]['Image'] in (self.original[r]['Image'],selected_ids[r+'ID']) for r in ROLES),'POSTSTATE_UNKNOWN')
        return 'APPLIED' if rc==0 and all(self.post[r]['Image']==selected_ids[r+'ID'] for r in ROLES) else 'FAILED_KNOWN'
    def accept(self,binding,guard):
        self.phase='ACCEPT'
        from deployment_http import readonly_acceptance
        observed=self.inspect_services(binding,guard)
        need(all(observed[r]['Id']==self.post[r]['Id'] and stable(observed[r])==stable(self.post[r]) for r in ROLES),'ACCEPTANCE_CURRENT_CAS_UNKNOWN')
        self.post=observed
        try:
            post_semantics(self.original,self.post,self.production_ids(binding),self.images,binding['current']['layers']+[self.override],self.hashes,self.prospective_cfg)
            if not all(self.post[r]['State']['Running'] is True and self.post[r]['State'].get('Health',{}).get('Status')=='healthy' for r in ROLES):return 'FAIL_KNOWN'
            need(self.compile_assets(binding['candidate'],guard)==self.web_assets,'WEB_MANIFEST_CAS')
            readonly_acceptance(self.profile,guard,expected_assets=self.web_assets)
        except __import__('deployment_http').KnownUnhealthy:
            return 'FAIL_KNOWN'
        self.publish_control(binding,guard)
        return 'PASS'
    def rollback(self,binding,guard):
        self.phase='ROLLBACK'
        # 4 current inspect + 2 renders + 2 hashes + up120 + 4 restored
        # inspect = 480 native whole seconds, plus180 HTTP +20 FS/report. Original
        # dual-clock1800 ceiling remains authoritative; never borrow/renew.
        guard.full(480,200)
        need(self.post is not None,'ROLLBACK_STATE_UNKNOWN')
        need(self.control_value()==self.control,'ROLLBACK_CONTROL_CAS')
        current=self.inspect_services(binding,guard)
        need(all(current[r]['Id']==self.post[r]['Id'] and stable(current[r])==stable(self.post[r]) for r in ROLES),'ROLLBACK_CAS_UNKNOWN')
        need(self.render(binding,guard)==self.cfg,'ROLLBACK_CONFIG_CAS')
        override=self.write('rollback-override.json',{'services':{r:{'image':self.original[r]['Image']} for r in ROLES}})
        self.private_input(override,guard)
        rollback_layers=binding['current']['layers']+[override]
        resolved=self.render_bytes(binding,guard,rollback_layers)
        expected=copy.deepcopy(self.cfg)
        for role in ROLES:expected['services'][role]['image']=self.original[role]['Image']
        need(strict_json(resolved)==expected,'ROLLBACK_CONFIG_CAS')
        rollback_hashes=self.resolved_hashes(binding,guard,resolved,'ROLLBACK_HASH')
        self.input_guard(guard)
        rc,_=self.compose_run('KNOWN_ROLLBACK',self.compose(binding,rollback_layers)+['up','--detach','--no-deps','--no-build','--pull','never','--wait','--wait-timeout','90','core','console'],guard,whole=120,suffix=140)
        self.input_guard(guard)
        need(rc==0,'ROLLBACK_COMMAND_UNKNOWN')
        restored=self.inspect_services(binding,guard)
        need(all(restored[r]['Image']==self.original[r]['Image'] and restored[r]['State']['Running'] is True
                 and restored[r]['State'].get('Health',{}).get('Status')=='healthy' for r in ROLES),'ROLLBACK_RUNTIME_UNKNOWN')
        # Use same semantic comparison with exact old IDs, no data restoration.
        for role in ROLES:
            old,new=stable(self.original[role]),stable(restored[role])
            old['config']['Labels']['com.docker.compose.config-hash']=rollback_hashes[role]
            old['config']['Labels']['com.docker.compose.image']=self.original[role]['Image']
            old['config']['Labels']['com.docker.compose.project.config_files']=','.join(rollback_layers)
            equivalent_representations(old,new,self.cfg,role)
            need(old==new,'ROLLBACK_SEMANTICS_UNKNOWN')
        from deployment_http import readonly_acceptance
        need(bool(self.old_web_assets),'ROLLBACK_ASSET_BASELINE_REQUIRED')
        readonly_acceptance(self.profile,guard,expected_assets=self.old_web_assets,negative=False)
        self.publish_control(binding,guard,outcome='ROLLED_BACK',layers=rollback_layers,images={r:self.original[r]['Image'] for r in ROLES})
        return 'ROLLED_BACK'
    def publish_control(self,binding,guard,*,outcome='PASS_PRODUCTION',layers=None,images=None):
        self.phase='PUBLISH'
        # Public metadata only. No env/DSN/private semantic digest is written.
        path=Path(self.profile['active_control']);guard()
        existing=self.control_value();need(existing==self.control,'PUBLISH_CONTROL_CAS')
        value=copy.deepcopy(existing)
        active_layers=layers or binding['current']['layers']+[self.override]
        value.update(schema='authova-two-service-active-control/v1',compose_layers=active_layers,active_override=active_layers[-1],
                     images=images or {r:self.production_ids(binding)[r+'ID'] for r in ROLES},services_changed=list(ROLES),
                     ci_last_attempt={'candidate':binding['candidate'],'outcome':outcome})
        if outcome=='PASS_PRODUCTION':
            value['ci_candidate']=binding['candidate']
            value['ci_runtime_image_graph']=copy.deepcopy(self.production_graph)
        if 'public_release_summary' in value:value['previous_public_release_summary']=value.pop('public_release_summary')
        parent=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
        first=None
        try:
            temp=path.name+'.'+self.nonce+'.tmp';durable(parent,temp,value)
            need(self.control_value()==existing,'PUBLISH_CONTROL_CAS')
            os.rename(temp,path.name,src_dir_fd=parent,dst_dir_fd=parent);os.fsync(parent)
        except BaseException as exc:first=exc;raise
        finally:_close(parent,first,'CONTROL_PARENT_CLOSE')
        guard()
