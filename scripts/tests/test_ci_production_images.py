"""Real production methods/own archive+FS with fake CLI; no daemon or service."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from test_ci_archive import owner,fixture,archive,guard
from host_adapter import _token
from host_admission import canonical
from release_contract import Refused

guard.full=lambda *args:None


class ProductionImageTests(unittest.TestCase):
    def setup_owner(self,root):
        files,candidate=fixture(provenance=True);body=archive(files)
        candidate.update(source='a'*40,manifest='b'*64,schemaDigest='c'*64,archive=hashlib.sha256(body).hexdigest())
        p=root/'images.tar';p.write_bytes(body)
        o=owner.CoreConsoleOwner.__new__(owner.CoreConsoleOwner)
        o.profile={'role':'application-production','archive':{'path':str(p),'identity':list(_token(p.stat()))},'active_control':str(root/'active.json')}
        o.operation=root/'operations'/('f'*24);o.nonce='f'*64;o.secret_inputs={};o.old_images={};o.images={};o.hashes={};o.up_started=False
        o.production_graph=None;o.graph_candidate=None
        cfg={'services':{r:{'image':'sha256:'+str(i+1)*64,'environment':{}} for i,r in enumerate(owner.ROLES)}}
        cfg['services']['console']['environment']={'AUTHOVA_CONSOLE_ASSETS':'/old'};cfg['services']['worker']={'image':'OWN_KEEP_WORKER'}
        binding={'action':'production','project':'OWN_PROJECT','candidate':candidate,'current':{'layers':['OWN_BASE']}}
        old={}
        for i,r in enumerate(owner.ROLES):
            cid=str(i+3)*64
            old[r]={'Id':cid,'Image':cfg['services'][r]['image'],'State':{'Running':True,'Health':{'Status':'healthy'}},
                    'Config':{'Hostname':cid[:12],'Image':cfg['services'][r]['image'],'Env':[k+'='+v for k,v in cfg['services'][r]['environment'].items()],
                              'Labels':{'com.docker.compose.image':cfg['services'][r]['image'],'com.docker.compose.config-hash':'old','com.docker.compose.project.config_files':'OWN_BASE'}},
                    'HostConfig':{'ReadonlyRootfs':True},'NetworkSettings':{'Networks':{}},'Mounts':[]}
        control={'project':'OWN_PROJECT','keep_all_layers_in_order':True,'compose_layers':['OWN_BASE'],'images':{r:old[r]['Image'] for r in owner.ROLES}}
        (root/'active.json').write_bytes(canonical(control));(root/'active.json').chmod(0o600);o.control=control
        events=[];state={'up':False};post={}
        def inspect(*args):return copy.deepcopy(post if state['up'] else old)
        def render(b,g,layers=None):
            result=copy.deepcopy(cfg)
            if layers:
                override=json.loads(Path(layers[-1]).read_bytes())
                for r in owner.ROLES:result['services'][r].update(override['services'][r])
            return result
        def image_metadata(image):
            for r in owner.ROLES:
                if image==old[r]['Image']:return {'Id':image,'Os':'linux','Architecture':'arm64','Config':{'Env':[]}}
                n=o.production_graph[r]
                if image==n['config']:
                    return {'Id':image,'Os':'linux','Architecture':'arm64','RepoDigests':[],
                            'RootFS':{'Type':'layers','Layers':n['rootfs_diff_ids']},
                            'Config':{'Env':[],'User':'10001:10001','Entrypoint':['authova' if r=='core' else 'authova-console'],'Labels':n['provenance_labels']}}
            raise Refused('OWN_NO_ROOT_ID_ADDRESS')
        def command(tag,args,*a,**kw):
            events.append((tag,args))
            if tag=='IMAGE_LOCAL_ONLY':return canonical([image_metadata(args[-1])])
            if tag=='IMAGE_LOAD':return b'OWN_LOAD'
            if tag=='COMPOSE_HASH':return (args[-1]+' '+'9'*64).encode()
            raise AssertionError('OWN_UNEXPECTED_COMMAND')
        def up(tag,args,*a,**kw):
            events.append((tag,args));self.assertEqual(args[-11:],['up','--detach','--no-deps','--no-build','--pull','never','--wait','--wait-timeout','90','core','console'])
            for i,r in enumerate(owner.ROLES):
                c=copy.deepcopy(old[r]);c['Id']=str(i+5)*64;c['Image']=o.production_ids(binding)[r+'ID'];c['Config']['Hostname']=c['Id'][:12];c['Config']['Image']=c['Image']
                c['Config']['Labels'].update(o.production_graph[r]['provenance_labels'])
                c['Config']['Labels'].update({'com.docker.compose.image':c['Image'],'com.docker.compose.config-hash':o.hashes[r],
                    'com.docker.compose.project.config_files':','.join(binding['current']['layers']+[o.override])})
                if r=='console':c['Config']['Env']=['AUTHOVA_CONSOLE_ASSETS=/opt/authova-console/web']
                post[r]=c
            state['up']=True;return 0,b''
        def probe(b,g,image,args,*a,**kw):
            events.append(('PROBE',image));self.assertEqual(image,o.production_graph['core']['config']);self.assertEqual(args,['schema-check']);return 0,canonical({'schema':'authova-runtime-schema/v1','result':'PASS'})
        o.command=command;o.compose_run=up;o.oneshot=probe;o.render=render;o.inspect_services=inspect;o.selected_private_inputs=lambda *a:None;o.input_guard=lambda *a:None;o.current=lambda *a:binding['current']
        return o,binding,events
    def test_full_production_chain_uses_guarded_config_ids_but_publishes_original_signed_roots(self):
        with tempfile.TemporaryDirectory() as tmp:
            o,b,events=self.setup_owner(Path(tmp).resolve());original=copy.deepcopy(b['candidate'])
            with patch('deployment_http.readonly_acceptance',return_value={}):
                o.preflight(b,guard)
                override=json.loads(Path(o.override).read_bytes());self.assertEqual({r:override['services'][r]['image'] for r in owner.ROLES},{r:o.production_graph[r]['config'] for r in owner.ROLES})
                self.assertEqual(o.apply(b,guard),'APPLIED');self.assertEqual(o.accept(b,guard),'PASS')
            control=json.loads(Path(o.profile['active_control']).read_bytes())
            self.assertEqual(control['ci_candidate'],original);self.assertEqual(b['candidate'],original)
            self.assertEqual(control['ci_runtime_image_graph'],o.production_graph)
            self.assertEqual(control['images'],{r:o.production_graph[r]['config'] for r in owner.ROLES})
            self.assertEqual(sum(tag=='IMAGE_LOAD' for tag,_ in events),1)
            for tag,args in events:
                if tag=='IMAGE_LOCAL_ONLY':self.assertNotIn(args[-1],[original[r+'ID'] for r in owner.ROLES])
    def test_generic_image_still_rejects_index_config_alias_and_resolver_requires_production_graph(self):
        with tempfile.TemporaryDirectory() as tmp:
            o,b,events=self.setup_owner(Path(tmp).resolve())
            with patch('deployment_http.readonly_acceptance',return_value={}):o.preflight(b,guard)
            with self.assertRaisesRegex(Refused,'OWN_NO_ROOT_ID_ADDRESS'):o.image(b['candidate']['coreID'],guard)
            original=o.command;o.command=lambda *a,**kw:canonical([{'Id':o.production_graph['core']['config'],'Os':'linux','Architecture':'arm64'}])
            with self.assertRaisesRegex(Refused,'IMAGE_IDENTITY'):o.image(b['candidate']['coreID'],guard)
            o.command=original
            for change in ('candidate','graph','role','provenance','rootfs'):
                with self.subTest(change=change):
                    if change=='candidate':
                        bad=copy.deepcopy(b);bad['candidate']['source']='d'*40
                        with self.assertRaisesRegex(Refused,'PRODUCTION_GRAPH_BINDING'):o.production_ids(bad)
                    if change=='graph':
                        o.production_graph['core']['config']='sha256:'+'0'*64
                        with self.assertRaisesRegex(Refused,'PRODUCTION_GRAPH_CAS'):o.apply(b,guard)
                        o.production_graph=o.archive_guard(b['candidate'],guard)
                    if change=='role':
                        with self.assertRaisesRegex(Refused,'PRODUCTION_GRAPH_BINDING'):o.production_ids(dict(b,action='test'))
                    if change=='provenance':
                        o.production_graph['core']['provenance_labels']['io.carvebloom.role']='console'
                        with self.assertRaisesRegex(Refused,'PRODUCTION_GRAPH_PROVENANCE'):o.production_ids(b)
                        o.production_graph=o.archive_guard(b['candidate'],guard)
                    if change=='rootfs':
                        image=o.production_image(b,'core',guard);image['RootFS']['Layers']=[]
                        o.command=lambda *a,**kw:canonical([image])
                        with self.assertRaisesRegex(Refused,'PRODUCTION_ROOTFS_IDENTITY'):o.production_image(b,'core',guard)
                        o.command=original
            self.assertFalse(any(tag in ('IMAGE_LOAD','PROBE','SELECTIVE_UP') for tag,_ in events))


if __name__=='__main__':unittest.main()
