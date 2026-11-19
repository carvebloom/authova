"""Fixture reference typing and actual preflight with owned metadata actors only."""
import ast
import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import test_owner as owner
import compose_owner
from release_contract import Refused

def guard():pass

def metadata(role,descriptor=True):
    ref=owner.FIXTURE_PINS[role];digest=ref.split('@')[-1]
    value={'Id':digest,'Os':'linux','Architecture':'arm64'}
    if role!='tools':
        value['RepoDigests']=[owner.FIXTURE_REPOSITORIES[role][-1]+'@'+digest]
        if descriptor:value['Descriptor']={'digest':digest,'mediaType':'application/vnd.oci.image.index.v1+json','size':1234}
    return value

class FixtureImageTests(unittest.TestCase):
    def actor(self,values=None):
        obj=owner.TestOwner.__new__(owner.TestOwner);obj.profile={'fixture_images':dict(owner.FIXTURE_PINS)};calls=[]
        def command(tag,argv,check):
            check();calls.append((tag,argv))
            self.assertEqual(tag,'IMAGE_LOCAL_ONLY');self.assertEqual(argv[:2],['image','inspect'])
            role=next(r for r,v in owner.FIXTURE_PINS.items() if v==argv[2])
            return json.dumps([metadata(role) if values is None else values[role]]).encode()
        obj.command=command
        return obj,calls
    def test_fixed_references_and_content_id_are_separate_and_aliases_are_explicit(self):
        for descriptor in (True,False):
            values={r:metadata(r,descriptor) for r in owner.FIXTURE_PINS};obj,calls=self.actor(values)
            for role in owner.FIXTURE_PINS:self.assertEqual(obj.fixture_image(role,guard),values[role])
            self.assertEqual([a[1][2] for a in calls],list(owner.FIXTURE_PINS.values()))
        for role in ('mysql','valkey'):
            for name in owner.FIXTURE_REPOSITORIES[role]:
                values={r:metadata(r) for r in owner.FIXTURE_PINS};values[role]['RepoDigests']=[name+'@'+values[role]['Id']]
                obj,_=self.actor(values);obj.fixture_image(role,guard)
    def test_wrong_pin_refuses_before_inspect_and_app_ids_stay_exact(self):
        obj,calls=self.actor();obj.profile['fixture_images']['mysql']='mysql:latest'
        with self.assertRaisesRegex(Refused,'CI_FIXTURE_PIN'):obj.fixture_image('mysql',guard)
        self.assertEqual(calls,[])
        for role in ('unknown',True):
            with self.assertRaisesRegex(Refused,'CI_FIXTURE_PIN'):obj.fixture_image(role,guard)
        app=compose_owner.CoreConsoleOwner.__new__(compose_owner.CoreConsoleOwner)
        app.command=lambda *a:json.dumps([{'Id':'sha256:'+'a'*64,'Os':'linux','Architecture':'arm64'}]).encode()
        self.assertEqual(app.image('sha256:'+'a'*64,guard)['Id'],'sha256:'+'a'*64)
        for wrong in ('mysql@sha256:'+'a'*64,'sha256:'+'b'*64):
            with self.assertRaisesRegex(Refused,'IMAGE_IDENTITY'):app.image(wrong,guard)
    def test_wrong_resolved_repo_platform_or_descriptor_never_falls_back(self):
        good=metadata('mysql');bad=[]
        for key,value in [('Id','sha256:'+'0'*64),('Os','windows'),('Architecture','amd64'),
                          ('RepoDigests',[]),('RepoDigests',['evil.invalid/mysql@'+good['Id']]),
                          ('RepoDigests',[good['RepoDigests'][0]]*2),('RepoDigests',['mysql:latest']),('Descriptor',None)]:
            v=copy.deepcopy(good);v[key]=value;bad.append(v)
        for key,value in [('digest','sha256:'+'0'*64),('mediaType','not-image'),('size',True),('size',0),
                          ('size',1048577),('platform',{'os':'linux','architecture':'amd64'}),('urls',['https://outside.invalid'])]:
            v=copy.deepcopy(good);v['Descriptor'][key]=value;bad.append(v)
        for v in bad:
            with self.subTest(v=v):
                obj,_=self.actor({'mysql':v})
                with self.assertRaises(Refused):obj.fixture_image('mysql',guard)
    def test_real_preflight_consumes_all_three_typed_fixtures_before_resource_mutation(self):
        with tempfile.TemporaryDirectory() as tmp:
            obj=owner.TestOwner.__new__(owner.TestOwner);obj.profile={'fixture_images':dict(owner.FIXTURE_PINS)}
            obj.source_bytes={name:b'# OWN_ONLY' for name in (*owner.CODE,'test_materials.py')};obj.nonce='f'*64;calls=[]
            def command(tag,argv,check):
                calls.append((tag,argv));check()
                if tag=='IMAGE_LOCAL_ONLY':
                    role=next(r for r,v in owner.FIXTURE_PINS.items() if v==argv[-1]);return json.dumps([metadata(role)]).encode()
                self.assertEqual(tag,'CI_RESOURCE_ABSENCE');return b''
            obj.command=command;obj.archive_guard=lambda *args:None
            binding={'candidate':{},'fixture':{'root':str(Path(tmp)/'fixture'),'scope':'f'*32,'network':'OWN_NETWORK','mysql_volume':'OWN_DB','secret_volumes':{'core':'OWN_CORE'}}}
            obj.preflight(binding,guard)
            self.assertEqual([a[0] for a in calls],['IMAGE_LOCAL_ONLY']*3+['CI_RESOURCE_ABSENCE']*3)
            self.assertEqual(obj.dependencies(dict(binding,project='OWN_PROJECT',fixture=dict(binding['fixture'],service_names={'mysql':'m','valkey':'v','proxy':'p'})))['services']['mysql']['image'],owner.FIXTURE_PINS['mysql'])
    def test_pin_contract_equals_installed_launcher_without_executing_entry(self):
        source=Path(owner.__file__).with_name('host_runner.py').read_bytes()
        value=next(ast.literal_eval(n.value) for n in ast.parse(source).body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='FIXTURE_IMAGES' for t in n.targets))
        self.assertEqual(owner.FIXTURE_PINS,value)

if __name__=='__main__':unittest.main()
