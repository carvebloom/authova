"""Synthetic private RAM material, no daemon/files/private data or actual gates."""
import copy
import hashlib
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import current_reconciliation as model


def materials():
    nonce='a'*64;layers=list(model.LAYERS)+[model.BASE+'authova/.local-test/authova-cicd-host01/state/production/operations/'+nonce[:24]+'/candidate-private-override.json']
    compose={'name':model.PROJECT,'services':{}};pre={};images={};hashes={r:'e'*64 for r in model.ROLES}
    for r in model.ROLES:
        cid,image=model.TARGETS[r];labels=model.provenance(r)
        im={'Id':image,'Os':'linux','Architecture':'arm64','RootFS':{'Type':'layers','Layers':model.SEALED_ROOTFS[r]},
            'Config':{'Env':['PRIVATE=OWN_PRIVATE_CANARY'],'Labels':dict(labels)}}
        service={'image':image,'environment':{'PRIVATE':'OWN_PRIVATE_CANARY'},'labels':dict(labels),
                 'entrypoint':['authova' if r=='core' else 'authova-console'],'command':['all'],'user':'10001:10001','working_dir':'/own'}
        current_labels=dict(labels);current_labels.update({'com.docker.compose.project':model.PROJECT,'com.docker.compose.service':r,
            'com.docker.compose.oneoff':'False','com.docker.compose.project.config_files':','.join(layers),
            'com.docker.compose.config-hash':hashes[r],'com.docker.compose.image':image})
        pre[r]={'Id':cid,'Image':image,'Config':{'Image':image,'Env':['PRIVATE=OWN_PRIVATE_CANARY'],'Labels':current_labels,
                    'Cmd':['all'],'Entrypoint':service['entrypoint'],'User':'10001:10001','WorkingDir':'/own'},
                'RestartCount':0,'State':{'Status':'running','Running':True,'Paused':False,'Restarting':False,'OOMKilled':False,'Dead':False,
                    'Pid':123,'ExitCode':0,'Error':'','StartedAt':'2026-11-20T00:00:00Z','FinishedAt':'0001-01-01T00:00:00Z',
                    'Health':{'Status':'healthy','FailingStreak':0,'Log':[]}},'HostConfig':{'ReadonlyRootfs':True,'Privileged':False,'CapAdd':None},
                'Mounts':[],'NetworkSettings':{'Networks':{}}}
        images[r]=im;compose['services'][r]=service
    native=[]
    for j,name in enumerate(model.READS):
        native.append({'role':name,'record':{'tag':name,'exit_code':0,'known_return':True,'timely':True,'all_selected_IO_closed':True,
            'started_at':1000+j,'finished_at':1000+j+.1,'original_monotonic':500+j,'elapsed_monotonic':.1,'whole_seconds':30}})
    private={r:{'identity':[1,2,0o100600,1,501,20,4,10,11],'body':b'OWN!'} for r in model.PRIVATE_ROLES}
    return {'schema':'authova-current23-materials/v1','pre':pre,'post':copy.deepcopy(pre),'images':images,'compose':compose,
            'compose_hashes':hashes,'layers':layers,'transport':{'daemon':model.DAEMON,'docker_sha256':model.DOCKER_SHA,'compose_sha256':model.COMPOSE_SHA,
                'operation_nonce':nonce,'native':native,'clock':{'I':1000,'M':500,'end_I':1011,'end_M':511},
                'private_pre':private,'private_post':copy.deepcopy(private),'control_pre':b'OWN_PUBLIC_CONTROL','control_post':b'OWN_PUBLIC_CONTROL'}}


class CurrentMaterialTests(unittest.TestCase):
    def evaluate(self,m):
        # Explicit OWN dependency injection, not a preimage/qualification of the
        # real ec2022 control or a caller-set authority accepted by evaluate().
        with patch.object(model,'CONTROL_SHA',hashlib.sha256(b'OWN_PUBLIC_CONTROL').hexdigest()):return model.evaluate(m)
    def test_supported_materials_never_turn_missing_producers_or_history_green(self):
        m=materials();before=copy.deepcopy(m);r=self.evaluate(m)
        self.assertEqual(set(r['components']),set(model.CHECKS));self.assertEqual(r['result'],'NOT_ESTABLISHED')
        for c in ('IDENTITY_PROVENANCE','COMPOSE_ORIGIN','ENV','INPUT_AND_CURRENT_CAS'):
            self.assertEqual(r['components'][c]['result'],'MATCH_CURRENT_INTENT')
        for c in ('LABELS','PROCESS','HARDENING_RESOURCES','NETWORK_MOUNTS'):
            self.assertEqual(r['components'][c]['result'],'NOT_ESTABLISHED');self.assertTrue(r['components'][c]['missing_authority'])
        self.assertEqual(m,before);self.assertEqual(r['historical_before'],'NOT_RECORDED')
        self.assertNotIn('OWN_PRIVATE_CANARY',json.dumps(r))
    def test_explicit_process_null_empty_string_list_omitted_and_types(self):
        cases=[('omit','PROCESS_OMITTED_DEFAULT_PRODUCER_MISSING'),(None,'PROCESS_NULL_DEFAULT_PRODUCER_MISSING'),
               ([],'PROCESS_EMPTY_RESET_PRODUCER_MISSING'),('all','PROCESS_STRING_PARSER_UNSUPPORTED'),
               ([False],'PROCESS_ARGUMENT_TYPE_UNSUPPORTED')]
        for value,code in cases:
            m=materials()
            if value=='omit':del m['compose']['services']['core']['command']
            else:m['compose']['services']['core']['command']=value
            with self.subTest(value=value):self.assertIn(code,self.evaluate(m)['components']['PROCESS']['missing_authority'])
        m=materials();m['pre']['core']['Config']['Cmd']=[]
        self.assertIn('PROCESS_EXPLICIT_ARGUMENTS',self.evaluate(m)['components']['PROCESS']['mismatch'])
    def test_identity_env_labels_and_declared_maps_reject_poison_without_values(self):
        cases=[('Id','IDENTITY_PROVENANCE','SELECTED_IDENTITY'),('rootfs','IDENTITY_PROVENANCE','SEALED_RUNTIME_CONFIG_GRAPH'),
               ('env','ENV','EFFECTIVE_ENV'),('label','LABELS','DECLARED_LABEL_VALUE')]
        for change,component,code in cases:
            m=materials()
            if change=='Id':m['pre']['core']['Id']='b'*64
            if change=='rootfs':m['images']['core']['RootFS']['Layers']=[]
            if change=='env':m['pre']['core']['Config']['Env']=['PRIVATE=OTHER_PRIVATE_CANARY']
            if change=='label':m['pre']['core']['Config']['Labels']['io.carvebloom.role']='console'
            result=self.evaluate(m)
            with self.subTest(change=change):self.assertIn(code,result['components'][component]['mismatch'])
            self.assertNotIn('CANARY',json.dumps(result))
        for bad in (None,[],True,{'PRIVATE':None}):
            m=materials();m['compose']['services']['core']['environment']=bad
            self.assertEqual(self.evaluate(m)['components']['ENV']['result'],'NOT_ESTABLISHED')
        m=materials();m['pre']['core']['Config']['Env']=['PRIVATE=ONE','PRIVATE=TWO']
        self.assertIn('ENV_DUPLICATE_OR_INVALID_KEY',self.evaluate(m)['components']['ENV']['missing_authority'])
    def test_extra_fields_caller_authority_objects_and_generated_labels_cannot_pass(self):
        for field in ('expected','approved','authority','before_snapshot'):
            m=materials();m[field]=True
            self.assertEqual(self.evaluate(m)['result'],'NOT_ESTABLISHED')
        m=materials();m['compose']['services']['core']['caller_verified']=True
        r=self.evaluate(m)
        # Unknown service keys cannot fill the comprehensive coverage gap.
        self.assertEqual(r['components']['HARDENING_RESOURCES']['result'],'NOT_ESTABLISHED')
        self.assertIn('UNMODELED_COMPOSE_SERVICE_FIELDS',r['components']['HARDENING_RESOURCES']['missing_authority'])
        m['pre']['core']['Config']['Labels']['com.docker.compose.NEW_UNKNOWN']='OWN_PRIVATE_CANARY'
        self.assertEqual(self.evaluate(m)['components']['LABELS']['result'],'NOT_ESTABLISHED')
        m['pre']['core']=object()
        self.assertEqual(self.evaluate(m)['result'],'NOT_ESTABLISHED')
    def test_cas_native_unknown_close_suffix_ownership_and_control_never_pass(self):
        for change in ('private','current','close','exit-bool','late','suffix','daemon','control','extra-native','private-extra','aliased-current'):
            m=materials();t=m['transport']
            if change=='private':t['private_post']['core-env']['body']=b'NEW!'
            if change=='current':m['post']['core']['HostConfig']['Privileged']=True
            if change=='close':t['native'][0]['record']['all_selected_IO_closed']=False
            if change=='exit-bool':t['native'][0]['record']['exit_code']=False
            if change=='late':t['native'][0]['record']['elapsed_monotonic']=30
            if change=='suffix':t['native'][0]['record']['started_at']=1090
            if change=='daemon':t['daemon']='CI_DAEMON_NOT_APP'
            if change=='control':t['control_post']=b'CHANGED'
            if change=='extra-native':t['native'].append(copy.deepcopy(t['native'][0]))
            if change=='private-extra':t['private_post']['other-source']={'body':b'BAD'}
            if change=='aliased-current':m['post']=m['pre']
            with self.subTest(change=change):self.assertNotEqual(self.evaluate(m)['components']['INPUT_AND_CURRENT_CAS']['result'],'MATCH_CURRENT_INTENT')
    def test_bad_safety_and_missing_independent_ids_remain_explicit(self):
        m=materials();m['pre']['core']['HostConfig']['Privileged']=True
        r=self.evaluate(m);self.assertEqual(r['result'],'CURRENT_INTENT_MISMATCH')
        self.assertIn('MANDATORY_HARDENING',r['components']['HARDENING_RESOURCES']['mismatch'])
        self.assertIn('EXTERNAL_NETWORK_VOLUME_ID_BINDINGS_NOT_RECORDED',r['components']['NETWORK_MOUNTS']['missing_authority'])
    def test_same_cid_restart_and_lifecycle_changes_cannot_match_current_cas(self):
        for change in ('StartedAt','FinishedAt','Pid','RestartCount','Restarting','extra-state','missing','bool-count'):
            m=materials();c=m['post']['core']
            if change in ('StartedAt','FinishedAt'):c['State'][change]='2026-11-20T01:00:00Z'
            if change=='Pid':c['State']['Pid']+=1
            if change=='RestartCount':c['RestartCount']+=1
            if change=='Restarting':c['State']['Restarting']=True
            if change=='extra-state':c['State']['NewLifecycleField']='OWN_CHANGED'
            if change=='missing':del c['State']['StartedAt']
            if change=='bool-count':c['RestartCount']=False
            with self.subTest(change=change):self.assertNotEqual(self.evaluate(m)['components']['INPUT_AND_CURRENT_CAS']['result'],'MATCH_CURRENT_INTENT')
        m=materials();m['post']['core']['State']['Health']['Log']=[{'Output':'OWN_PRIVATE_CANARY'}]
        self.assertEqual(self.evaluate(m)['components']['INPUT_AND_CURRENT_CAS']['result'],'MATCH_CURRENT_INTENT')
    def test_original_intervals_reversed_overlapping_and_wall_regression_reject_without_sort(self):
        for axis in ('both','mono','wall','overlap','equal-boundary'):
            m=materials();rows=m['transport']['native']
            if axis in ('both','mono','wall'):
                for j,row in enumerate(rows):
                    if axis in ('both','wall'):row['record'].update(started_at=1009-j,finished_at=1009-j+.1)
                    if axis in ('both','mono'):row['record']['original_monotonic']=509-j
            if axis=='overlap':rows[1]['record']['original_monotonic']=500.05
            if axis=='equal-boundary':rows[1]['record'].update(original_monotonic=500.1,started_at=1000.1)
            snapshot=copy.deepcopy(m);r=self.evaluate(m)['components']['INPUT_AND_CURRENT_CAS']
            with self.subTest(axis=axis):
                self.assertEqual(r['result'],'MATCH_CURRENT_INTENT' if axis=='equal-boundary' else 'MISMATCH_CURRENT_INTENT')
                self.assertEqual(m,snapshot)
    def test_explicit_resource_integers_do_not_authorize_units_or_defaults(self):
        for value in ('256m',None,True,-2):
            m=materials();m['compose']['services']['core']['mem_limit']=value
            self.assertIn('RESOURCE_EXPLICIT_INTEGER_OR_UNIT_UNSUPPORTED',self.evaluate(m)['components']['HARDENING_RESOURCES']['missing_authority'])
        m=materials();m['compose']['services']['core']['mem_limit']=256;m['pre']['core']['HostConfig']['Memory']=512
        r=self.evaluate(m);self.assertIn('EXPLICIT_RESOURCE_INTEGER',r['components']['HARDENING_RESOURCES']['mismatch'])
        m['pre']['core']['HostConfig']['Memory']=256
        self.assertEqual(self.evaluate(m)['components']['HARDENING_RESOURCES']['result'],'NOT_ESTABLISHED')
    def test_declared_mount_port_network_checks_never_adopt_observed_external_ids(self):
        m=materials();s=m['compose']['services']['core'];c=m['pre']['core']
        s.update(networks={'ingress':{}},volumes=[{'type':'volume','source':'secret','target':'/run/secret','read_only':True}],
                 ports=[{'target':8080,'published':'43100','host_ip':'127.0.0.1','protocol':'tcp'}])
        m['compose'].update(networks={'ingress':{'name':'OWN_INGRESS'}},volumes={'secret':{'name':'OWN_SECRET_VOLUME'}})
        c['NetworkSettings']['Networks']={'OWN_INGRESS':{'NetworkID':'OBSERVED_NOT_AUTHORITY'}}
        c['Mounts']=[{'Type':'volume','Name':'OWN_SECRET_VOLUME','Destination':'/run/secret','RW':False}]
        c['HostConfig']['PortBindings']={'8080/tcp':[{'HostIp':'127.0.0.1','HostPort':'43100'}]}
        r=self.evaluate(m)['components']['NETWORK_MOUNTS']
        self.assertFalse(r['mismatch']);self.assertEqual(r['result'],'NOT_ESTABLISHED')
        self.assertIn('EXTERNAL_NETWORK_VOLUME_ID_BINDINGS_NOT_RECORDED',r['missing_authority'])
        c['Mounts'][0]['RW']=True;c['HostConfig']['PortBindings']['8080/tcp'][0]['HostIp']='0.0.0.0'
        r=self.evaluate(m)['components']['NETWORK_MOUNTS'];self.assertIn('DECLARED_MOUNT_SEMANTICS',r['mismatch']);self.assertIn('DECLARED_FIXED_PORT_BINDINGS',r['mismatch'])


if __name__=='__main__':unittest.main()
