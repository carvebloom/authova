"""Pure RAM current23 material checks, NOT a release/authorization executor.

Only the fixed private transport may supply observations. No IO, parser, CLI,
hash of private material, observed-as-expected default or caller authority flag.
Missing producer bindings below are deliberate hard blockers, not TODO PASSes.
"""
import hashlib
import math
import re

ROLES=('core','console')
CHECKS=('IDENTITY_PROVENANCE','COMPOSE_ORIGIN','ENV','LABELS','PROCESS',
        'HARDENING_RESOURCES','NETWORK_MOUNTS','INPUT_AND_CURRENT_CAS')
TARGETS={
    'core':('d54dc9d43ca22cca31f3c257865c218f311d73548bd41442cc0c01cbd4d7c05b',
            'sha256:df9080dfe9492f0849e35638821d89c46a4c122cc805dda1dda7f53580b01167'),
    'console':('6515221c008cb0c733688bba4dcb820fb62af47aac9537c9eb04622bdfd4bbf5',
               'sha256:6d023c657144bebbb40528eaf9b8918795b15b51c64878504bcb155d32a09d47')}
SEALED_ROOTFS={'core': ['sha256:9fd9b3248f2fb670edfab1393d1d1e8cd828eb9c65de376b2fcc23268a45d500', 'sha256:01e1bf15918f8a71c206212c2b824c377c17943926dcbbd5f5633a4d0fde382e', 'sha256:97151395f76877802ce44f56918bbfe3a623403b687474393ba84174cfb9a2df', 'sha256:fb614de053a4a9c104f15d887ad880f069c62997c5bc9b64e1d2de03498ea0dd', 'sha256:0c633ba07c6f0ce527ab3bdffcb8019b1be0d087dd66bbc459d8b481e8372728'], 'console': ['sha256:9fd9b3248f2fb670edfab1393d1d1e8cd828eb9c65de376b2fcc23268a45d500', 'sha256:01e1bf15918f8a71c206212c2b824c377c17943926dcbbd5f5633a4d0fde382e', 'sha256:97151395f76877802ce44f56918bbfe3a623403b687474393ba84174cfb9a2df', 'sha256:67b640d65678c8f16810d015203cc937c23109e41d5aa8df2986a659f06f72a0', 'sha256:6509960b6206e1973992344e621b9a1e13da03eb9d5cc1cb9b389a0e76f78b28', 'sha256:c63dac71ee0176db506120e4dd45c35a39cf18c6c388924f8299cdf5dfc84520']}
PROJECT='authova-local-production-20261114'
SOURCE='ac1a3838f5eea9330fd1384fa22f3e7ba2189248'
MANIFEST='da4725ed2fab76ff09e649c9ec6126c6eba8e25e7861566593e74ab721387a35'
SCHEMA='3448275407d4640b1cb28b958052663eab0be73b7f702f7ebd653e44ed1de722'
COMPOSE_SHA='817dde1748900478a6db1e509c7cf8e8a0957ea6050c5705d60feb147ad0c5a6'
DOCKER_SHA='d79834f5443651e2a3a5a5baf54eed1f41955e90c0eb323b570f913bdf75062d'
DAEMON='e5f1dd67-f739-478a-9b2c-21e775f2f91b'
CONTROL_SHA='ec2022faa83f326189360a3bb4dc15f3c37f208b12c4329c44e5b70be75b96db'
BASE='/Users/chenhuanxiang/Workbench/'
LAYERS=(BASE+'authova/.local-test/authova-local-production-20261114-02/compose.production.json',
        BASE+'.local-test/authova-password-production-cutover-736db550d819/candidate-override.json',
        BASE+'.local-test/authova-ui-release-20261119-c02af79f1f9aa45a/console-override02.json')
PRIVATE_ROLES=('compose-base','password-override','ui-override','release23-override',
               'core-env','console-env','migrator-env')
READS=('DAEMON','CORE_PRE','CONSOLE_PRE','CORE_IMAGE','CONSOLE_IMAGE','COMPOSE',
       'CORE_HASH','CONSOLE_HASH','CORE_POST','CONSOLE_POST')
# No public commit/dependency/default provenance for these actual producers.
# These are SOURCE constants. Materials cannot populate/override them.
COMPOSE_PRODUCER_SOURCE=None
ENGINE_PRODUCER_SOURCE=None
EXTERNAL_ID_BINDINGS=None


class MaterialError(Exception):pass
def require(value,code):
    if not value:raise MaterialError(code)
def data_only(value):
    remaining=[100000];size=[0]
    def visit(v,depth):
        remaining[0]-=1
        require(depth<=32 and remaining[0]>=0,'MATERIAL_BOUNDS')
        kind=type(v)
        if kind in (str,bytes):
            size[0]+=len(v);require(len(v)<=1048576 and size[0]<=16*1048576,'MATERIAL_BOUNDS')
        elif kind is dict:
            require(len(v)<=4096 and all(type(k) is str for k in v),'MATERIAL_DATA_ONLY')
            for k,x in v.items():visit(k,depth+1);visit(x,depth+1)
        elif kind is list:
            require(len(v)<=4096,'MATERIAL_BOUNDS')
            for x in v:visit(x,depth+1)
        else:
            require(v is None or kind in (bool,int,float),'MATERIAL_DATA_ONLY')
            if kind is int:require(abs(v)<=10**30,'MATERIAL_BOUNDS')
            if kind is float:require(math.isfinite(v),'MATERIAL_BOUNDS')
    visit(value,0)
def finite(v):return type(v) in (int,float) and math.isfinite(v)
def result(mismatch=(),missing=()):
    return {'result':'MISMATCH_CURRENT_INTENT' if mismatch else ('NOT_ESTABLISHED' if missing else 'MATCH_CURRENT_INTENT'),
            'mismatch':sorted(set(mismatch)),'missing_authority':sorted(set(missing))}
def stringmap(v,code):
    require(type(v) is dict and all(type(k) is str and type(x) is str for k,x in v.items()),code)
    return v
def envmap(v):
    require(type(v) is list,'ENV_LIST_REQUIRED');out={}
    for item in v:
        require(type(item) is str and '=' in item,'ENV_STRING_REQUIRED')
        key,value=item.split('=',1)
        require(re.fullmatch('[A-Za-z_][A-Za-z0-9_]*',key) and key not in out,'ENV_DUPLICATE_OR_INVALID_KEY')
        out[key]=value
    return out
def provenance(role):
    return {'org.opencontainers.image.revision':SOURCE,'io.carvebloom.artifact-manifest':MANIFEST,
            'io.carvebloom.schema':SCHEMA,'io.carvebloom.role':role}
def services(m):
    require(type(m['compose']) is dict and m['compose'].get('name')==PROJECT,'COMPOSE_PROJECT_BINDING')
    s=m['compose']['services'];require(type(s) is dict and set(ROLES)<=set(s),'COMPOSE_SERVICES_REQUIRED')
    return s
def identity(m):
    bad=[]
    for r in ROLES:
        c=m['pre'][r];im=m['images'][r];cid,image=TARGETS[r]
        if c['Id']!=cid or c['Image']!=image or im['Id']!=image or im['Os']!='linux' or im['Architecture']!='arm64':bad.append('SELECTED_IDENTITY')
        labels=c['Config']['Labels'];expected=provenance(r)
        if c['Config'].get('Image')!=image or im.get('RootFS')!={'Type':'layers','Layers':SEALED_ROOTFS[r]}:bad.append('SEALED_RUNTIME_CONFIG_GRAPH')
        if not all(labels.get(k)==v and im['Config']['Labels'].get(k)==v for k,v in expected.items()):bad.append('PROVENANCE_BINDING')
        if labels.get('com.docker.compose.project')!=PROJECT or labels.get('com.docker.compose.service')!=r or labels.get('com.docker.compose.oneoff')!='False':bad.append('COMPOSE_OWNERSHIP')
        if c['State']['Running'] is not True or c['State'].get('Health',{}).get('Status')!='healthy':bad.append('CURRENT_HEALTH')
    return result(bad)
def origin(m):
    layers=m['layers'];nonce=m['transport']['operation_nonce']
    require(type(nonce) is str and re.fullmatch('[0-9a-f]{64}',nonce),'OPERATION_NONCE_REQUIRED')
    expected=list(LAYERS)+[BASE+'authova/.local-test/authova-cicd-host01/state/production/operations/'+nonce[:24]+'/candidate-private-override.json']
    require(layers==expected,'EXACT23_LAYERS_REQUIRED')
    s=services(m);bad=[]
    for r in ROLES:
        labels=m['pre'][r]['Config']['Labels'];h=m['compose_hashes'][r]
        require(type(h) is str and re.fullmatch('[0-9a-f]{64}',h),'COMPOSE_HASH_REQUIRED')
        if labels.get('com.docker.compose.project.config_files')!=','.join(layers) or labels.get('com.docker.compose.config-hash')!=h or labels.get('com.docker.compose.image')!=TARGETS[r][1] or s[r]['image']!=TARGETS[r][1]:bad.append('COMPOSE_ORIGIN_BINDING')
    return result(bad)
def environment(m):
    s=services(m);bad=[]
    for r in ROLES:
        # A rendered env map must be explicit. Never silently coerce null/list.
        expected=envmap(m['images'][r]['Config']['Env']);expected.update(stringmap(s[r]['environment'],'COMPOSE_ENV_MAP_REQUIRED'))
        if envmap(m['pre'][r]['Config']['Env'])!=expected:bad.append('EFFECTIVE_ENV')
    return result(bad)
def labels(m):
    s=services(m);bad=[]
    for r in ROLES:
        expected=dict(stringmap(m['images'][r]['Config']['Labels'],'IMAGE_LABEL_MAP_REQUIRED'))
        expected.update(stringmap(s[r]['labels'],'COMPOSE_LABEL_MAP_REQUIRED'))
        actual=stringmap(m['pre'][r]['Config']['Labels'],'CURRENT_LABEL_MAP_REQUIRED')
        # Checking this subset is legitimate, but never hides generated fields
        # or marks the entire component MATCH without the exhaustive producer.
        if any(actual.get(k)!=v for k,v in expected.items()):bad.append('DECLARED_LABEL_VALUE')
    return result(bad,('COMPOSE_GENERATED_LABEL_PRODUCER_NOT_RECORDED',))
def process(m):
    s=services(m);bad=[];missing=[]
    for r in ROLES:
        actual=m['pre'][r]['Config']
        for key,docker in (('command','Cmd'),('entrypoint','Entrypoint')):
            if key not in s[r]:missing.append('PROCESS_OMITTED_DEFAULT_PRODUCER_MISSING');continue
            v=s[r][key]
            if v is None:missing.append('PROCESS_NULL_DEFAULT_PRODUCER_MISSING');continue
            if type(v) is str:missing.append('PROCESS_STRING_PARSER_UNSUPPORTED');continue
            if type(v) is not list or any(type(x) is not str for x in v):missing.append('PROCESS_ARGUMENT_TYPE_UNSUPPORTED');continue
            if not v:missing.append('PROCESS_EMPTY_RESET_PRODUCER_MISSING');continue
            if actual.get(docker)!=v:bad.append('PROCESS_EXPLICIT_ARGUMENTS')
        for key,docker in (('user','User'),('working_dir','WorkingDir')):
            if key not in s[r]:missing.append('PROCESS_OMITTED_DEFAULT_PRODUCER_MISSING');continue
            v=s[r][key]
            if type(v) is not str or not v:missing.append('PROCESS_EMPTY_OR_NULL_DEFAULT_PRODUCER_MISSING');continue
            if actual.get(docker)!=v:bad.append('PROCESS_EXPLICIT_IDENTITY')
        # Even fully explicit modeled fields do not cover hostname, domain,
        # stop signal/timeout, shell/TTY/stdin and remaining Config defaults.
    missing.append('COMPLETE_PROCESS_CONFIG_PRODUCER_NOT_RECORDED')
    return result(bad,missing)
def hardening(m):
    bad=[];missing=['COMPOSE_ENGINE_HOSTCONFIG_DEFAULTS_NOT_RECORDED','RESOURCE_UNITS_PRESENCE_PRODUCER_NOT_RECORDED','COMPLETE_HOSTCONFIG_FIELD_COVERAGE_MISSING']
    supported={'image','environment','labels','command','entrypoint','user','working_dir',
               'read_only','privileged','cap_add','cap_drop','security_opt','mem_limit',
               'memswap_limit','pids_limit','cpus','cpu_shares','cpu_quota','cpu_period',
               'restart','healthcheck','networks','volumes','ports'}
    s=services(m)
    for r in ROLES:
        h=m['pre'][r]['HostConfig']
        if set(s[r])-supported:missing.append('UNMODELED_COMPOSE_SERVICE_FIELDS')
        # Mandatory current safety boundaries; not a complete materializer.
        if h.get('ReadonlyRootfs') is not True or h.get('Privileged') is not False or h.get('CapAdd') not in (None,[]):bad.append('MANDATORY_HARDENING')
        for key,docker in (('read_only','ReadonlyRootfs'),('privileged','Privileged')):
            if key not in s[r] or type(s[r][key]) is not bool:missing.append('HARDENING_EXPLICIT_BOOL_REQUIRED')
            elif type(h.get(docker)) is not bool or h[docker]!=s[r][key]:bad.append('EXPLICIT_HARDENING')
        for key,docker in (('mem_limit','Memory'),('memswap_limit','MemorySwap'),('pids_limit','PidsLimit'),('cpu_shares','CpuShares'),('cpu_quota','CpuQuota'),('cpu_period','CpuPeriod')):
            if key not in s[r]:continue  # missing producer/default blocks whole component
            value=s[r][key]
            if type(value) is not int or value < (-1 if key in ('memswap_limit','pids_limit','cpu_quota') else 0):missing.append('RESOURCE_EXPLICIT_INTEGER_OR_UNIT_UNSUPPORTED')
            elif type(h.get(docker)) is not int or h[docker]!=value:bad.append('EXPLICIT_RESOURCE_INTEGER')
        if 'cpus' in s[r]:missing.append('RESOURCE_CPU_DECIMAL_PRODUCER_NOT_RECORDED')
        for key in ('cap_drop','security_opt','restart','healthcheck'):
            if key in s[r]:missing.append('HARDENING_CAP_RESTART_HEALTH_PRODUCER_NOT_RECORDED')
    return result(bad,missing)
def networks(m):
    # Container inspection cannot manufacture independent external identities.
    bad=[];missing=['EXTERNAL_NETWORK_VOLUME_ID_BINDINGS_NOT_RECORDED',
                   'COMPOSE_ENGINE_MOUNT_PORT_NETWORK_PRODUCER_NOT_RECORDED',
                   'HISTORICAL_NETWORK_MOUNT_CONTINUITY_NOT_RECORDED']
    s=services(m)
    for r in ROLES:
        c=m['pre'][r];service=s[r];declared=m['compose'].get('networks',{})
        nets=service.get('networks')
        if type(nets) is not dict or not nets:missing.append('NETWORK_EXPLICIT_RESOLVED_NAMES_REQUIRED')
        else:
            names=[]
            for key in nets:
                item=declared.get(key,{})
                if type(item) is not dict or type(item.get('name')) is not str:missing.append('NETWORK_NAME_PRODUCER_MISSING')
                else:names.append(item['name'])
            if len(names)==len(nets) and set(c['NetworkSettings']['Networks'])!=set(names):bad.append('DECLARED_NETWORK_NAMES')
        mounts=service.get('volumes')
        if type(mounts) is not list:missing.append('MOUNT_EXPLICIT_LIST_REQUIRED')
        else:
            actual=c['Mounts'];require(type(actual) is list,'CURRENT_MOUNTS_REQUIRED')
            targets=[]
            for item in mounts:
                if type(item) is not dict or not {'type','source','target','read_only'}<=set(item) or type(item['read_only']) is not bool:missing.append('MOUNT_TYPED_EXPLICIT_FIELDS_REQUIRED');continue
                target=item['target'];require(type(target) is str,'MOUNT_TARGET_STRING_REQUIRED');targets.append(target)
                rows=[a for a in actual if a.get('Destination')==target]
                if len(rows)!=1:bad.append('DECLARED_MOUNT_TARGETS');continue
                observed=rows[0];source=item['source']
                if item['type']=='volume':
                    v=m['compose'].get('volumes',{}).get(source,{})
                    if type(v) is not dict or type(v.get('name')) is not str:missing.append('VOLUME_RESOLVED_NAME_MISSING');continue
                    expected_source=v['name'];field='Name'
                elif item['type']=='bind' and type(source) is str and source.startswith('/') and all(p not in ('','.','..') for p in source.split('/')[1:]):expected_source=source;field='Source'
                else:missing.append('MOUNT_SOURCE_PRODUCER_UNSUPPORTED');continue
                if observed.get('Type')!=item['type'] or observed.get(field)!=expected_source or type(observed.get('RW')) is not bool or observed['RW']!=(not item['read_only']):bad.append('DECLARED_MOUNT_SEMANTICS')
                if set(item)-{'type','source','target','read_only'}:missing.append('MOUNT_DRIVER_OPTIONS_PRODUCER_NOT_RECORDED')
            if len(targets)!=len(set(targets)):bad.append('DUPLICATE_DECLARED_MOUNT_TARGET')
            if len(targets)==len(mounts) and len(actual)!=len(mounts):bad.append('UNDECLARED_CURRENT_MOUNT')
            if m['images'][r]['Config'].get('Volumes'):missing.append('IMAGE_IMPLICIT_VOLUME_PRODUCER_NOT_RECORDED')
        ports=service.get('ports')
        if type(ports) is not list:missing.append('PORT_EXPLICIT_MAPPING_REQUIRED')
        else:
            expected={};complete=True
            for item in ports:
                if type(item) is not dict or not {'target','published','protocol','host_ip'}<=set(item) or type(item['target']) is not int or not 1<=item['target']<=65535 or item['protocol'] not in ('tcp','udp') or type(item['host_ip']) is not str or type(item['published']) not in (str,int) or type(item['published']) is bool or not str(item['published']).isdecimal() or not 1<=int(item['published'])<=65535:
                    missing.append('PORT_TYPED_FIXED_MAPPING_REQUIRED');complete=False;continue
                key=str(item['target'])+'/'+item['protocol'];expected.setdefault(key,[]).append({'HostIp':item['host_ip'],'HostPort':str(item['published'])})
                if set(item)-{'target','published','protocol','host_ip'}:missing.append('PORT_MODE_DEFAULT_PRODUCER_NOT_RECORDED')
            if complete and c['HostConfig'].get('PortBindings')!=expected:bad.append('DECLARED_FIXED_PORT_BINDINGS')
    return result(bad,missing)
def cas(m):
    t=m['transport'];require(type(t) is dict and set(t)=={'daemon','docker_sha256','compose_sha256','operation_nonce','clock','native','private_pre','private_post','control_pre','control_post'},'TRANSPORT_FIELDS_REQUIRED')
    require(m['pre'] is not m['post'] and all(m['pre'][r] is not m['post'][r] for r in ROLES)
            and t['private_pre'] is not t['private_post'],'INDEPENDENT_CURRENT_FRAMES_REQUIRED')
    bad=[]
    if (t['daemon'],t['docker_sha256'],t['compose_sha256'])!=(DAEMON,DOCKER_SHA,COMPOSE_SHA):bad.append('PHYSICAL_TRANSPORT_BINDING')
    c=t['clock'];require(type(c) is dict and set(c)=={'I','M','end_I','end_M'} and all(finite(v) for v in c.values()),'TRANSPORT_CLOCK_REQUIRED')
    if not (0<=c['end_I']-c['I']<480 and 0<=c['end_M']-c['M']<480):bad.append('TRANSPORT_CLOCK')
    native=t['native'];require(type(native) is list and len(native)==10,'EXACT10_NATIVE_REQUIRED')
    previous_wall=c['I'];previous_mono=c['M']
    for ordinal,(name,row) in enumerate(zip(READS,native)):
        require(type(row) is dict and set(row)=={'role','record'} and row['role']==name,'ORDERED_NATIVE_REQUIRED')
        v=row['record'];require(type(v) is dict and set(v)=={'tag','exit_code','known_return','timely','all_selected_IO_closed','started_at','original_monotonic','elapsed_monotonic','whole_seconds','finished_at'},'ORIGINAL_NATIVE_FIELDS_REQUIRED')
        require(all(finite(v[k]) for k in ('started_at','original_monotonic','elapsed_monotonic','whole_seconds','finished_at')),'ORIGINAL_NATIVE_CLOCK_REQUIRED')
        require(v['tag']==name,'ORIGINAL_NATIVE_TAG_REQUIRED')
        # Actual dispatch is serial. Never sort, clamp or repair observations.
        # This contract rejects wall regressions as well as monotonic overlap.
        if v['started_at']<previous_wall:bad.append('NATIVE_WALL_ORDER')
        if v['original_monotonic']<previous_mono:bad.append('NATIVE_MONOTONIC_ORDER')
        previous_wall=v['finished_at'];previous_mono=v['original_monotonic']+v['elapsed_monotonic']
        if type(v['exit_code']) is not int or v['exit_code']!=0 or any(v[k] is not True for k in ('known_return','timely','all_selected_IO_closed')):bad.append('NATIVE_NOT_COMPLETE')
        if v['whole_seconds']!=30 or not (c['I']<=v['started_at']<=v['finished_at']<=c['end_I'] and c['M']<=v['original_monotonic']<=v['original_monotonic']+v['elapsed_monotonic']<=c['end_M'] and 0<=v['elapsed_monotonic']<30 and v['finished_at']-v['started_at']<30):bad.append('NATIVE_CLOCK_BOUNDARY')
        remaining_native=(10-ordinal)*30
        if not (v['started_at']+remaining_native+60<c['I']+390 and v['original_monotonic']+remaining_native+60<c['M']+390):bad.append('NATIVE_FULL_SUFFIX_NOT_FIT')
    for group in ('private_pre','private_post'):
        require(type(t[group]) is dict and set(t[group])==set(PRIVATE_ROLES),'EXACT_PRIVATE_SOURCE_ROLES_REQUIRED')
        for item in t[group].values():
            require(type(item) is dict and set(item)=={'identity','body'} and type(item['identity']) is list and len(item['identity'])==9 and all(type(x) is int for x in item['identity']) and type(item['body']) is bytes and len(item['body'])<=1048576,'PRIVATE_IDENTITY_AND_BYTES_REQUIRED')
            token=item['identity']
            require(token[2]==0o100600 and token[3]==1 and token[4]==501 and token[6]==len(item['body']),'PRIVATE_REGULAR0600_OWNER_SINGLELINK_REQUIRED')
    if t['private_pre']!=t['private_post']:bad.append('PRIVATE_INPUT_CAS')
    require(type(t['control_pre']) is bytes and type(t['control_post']) is bytes and len(t['control_pre'])<=65536 and len(t['control_post'])<=65536,'PUBLIC_CONTROL_BYTES_REQUIRED')
    # Only the explicitly public control is hashed, never a private value/body.
    if t['control_pre']!=t['control_post'] or hashlib.sha256(t['control_pre']).hexdigest()!=CONTROL_SHA:bad.append('STALE_PUBLIC_CONTROL_CAS')
    for r in ROLES:
        a,b=m['pre'][r],m['post'][r]
        # Current-current CAS, not a substitution for historical before23.
        if any(a.get(k)!=b.get(k) for k in ('Id','Image','Config','HostConfig','Mounts','NetworkSettings')):bad.append('CURRENT_WHOLE_CAS')
        states=[]
        for container in (a,b):
            count=container.get('RestartCount');state=container.get('State')
            require(type(count) is int and count>=0 and type(state) is dict,'CURRENT_LIFECYCLE_FIELDS_REQUIRED')
            require(all(type(state.get(k)) is str and state[k] for k in ('Status','StartedAt','FinishedAt'))
                    and type(state.get('Pid')) is int and state['Pid']>0
                    and type(state.get('ExitCode')) is int and type(state.get('Error')) is str
                    and all(type(state.get(k)) is bool for k in ('Running','Paused','Restarting','OOMKilled','Dead')),
                    'CURRENT_LIFECYCLE_FIELDS_REQUIRED')
            health=state.get('Health')
            require(type(health) is dict and type(health.get('Status')) is str and type(health.get('FailingStreak')) is int and health['FailingStreak']>=0,'CURRENT_HEALTH_FIELDS_REQUIRED')
            if state['Status']!='running' or state['Running'] is not True or any(state[k] for k in ('Paused','Restarting','Dead')) or state['ExitCode']!=0 or state['Error']!='' or health['Status']!='healthy':bad.append('CURRENT_HEALTH')
            # Health probe history is observational/volatile, not process
            # lifecycle identity. All other State/Health fields stay whole.
            states.append((count,{k:v for k,v in state.items() if k!='Health'},
                           {k:v for k,v in health.items() if k!='Log'}))
        if states[0]!=states[1]:bad.append('CURRENT_LIFECYCLE_CAS')
    return result(bad)


def evaluate(m):
    """No output field is derived from a free input key/value/exception string.

    This checks private-worker observations; it cannot authenticate their OS/API
    provenance. Platform must bind original transport and signed23 lineage, not
    expose this function as a job-supplied admission or fabricate expected data.
    """
    checks={name:result(missing=('MATERIAL_SCHEMA_UNSUPPORTED',)) for name in CHECKS}
    try:data_only(m)
    except MaterialError as exc:
        return {'schema':'authova-current23-comparison/v1','result':'NOT_ESTABLISHED','historical_before':'NOT_RECORDED','components':{name:result(missing=(exc.args[0],)) for name in CHECKS}}
    if type(m) is not dict or set(m)!={'schema','pre','post','images','compose','compose_hashes','layers','transport'} or m.get('schema')!='authova-current23-materials/v1':
        return {'schema':'authova-current23-comparison/v1','result':'NOT_ESTABLISHED','historical_before':'NOT_RECORDED','components':checks}
    for name in ('pre','post','images','compose_hashes'):
        if type(m[name]) is not dict or set(m[name])!=set(ROLES):
            return {'schema':'authova-current23-comparison/v1','result':'NOT_ESTABLISHED','historical_before':'NOT_RECORDED','components':checks}
    functions=(identity,origin,environment,labels,process,hardening,networks,cas)
    for name,fn in zip(CHECKS,functions):
        try:checks[name]=fn(m)
        except MaterialError as exc:checks[name]=result(missing=(exc.args[0],))
        except (KeyError,TypeError,ValueError,AttributeError,IndexError):checks[name]=result(missing=('COMPONENT_MATERIAL_SHAPE_UNSUPPORTED',))
    overall='CURRENT_INTENT_MISMATCH' if any(v['result']=='MISMATCH_CURRENT_INTENT' for v in checks.values()) else 'NOT_ESTABLISHED'
    # No all-MATCH path until missing source authorities are actually qualified.
    return {'schema':'authova-current23-comparison/v1','result':overall,'historical_before':'NOT_RECORDED','components':checks}
