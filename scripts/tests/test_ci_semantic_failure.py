"""Closed RAM failure families and durable optional evidence; no Docker or DB."""
import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import compose_owner as owner
import test_owner
import test_ci_production_images as fixtures
from release_contract import Refused


class SemanticFailureTests(unittest.TestCase):
    def test_pinned_explicit_name_replacement_producer_keeps_full_labels_strict(self):
        with tempfile.TemporaryDirectory() as tmp:
            o,b,events=fixtures.ProductionImageTests().setup_owner(Path(tmp).resolve())
            with patch('deployment_http.readonly_acceptance',return_value={}):o.preflight(b,fixtures.guard)
            self.assertEqual(o.apply(b,fixtures.guard),'APPLIED')
            before=copy.deepcopy(o.original);after=copy.deepcopy(o.post)
            old_model=copy.deepcopy(o.cfg);model=copy.deepcopy(o.prospective_cfg)
            for role in owner.ROLES:
                name='OWN_PROJECT-'+role
                old_model['services'][role]['container_name']=model['services'][role]['container_name']=name
                before[role]['Name']=after[role]['Name']='/'+name
                for c in (before[role],after[role]):
                    c['Config']['Labels'].update({'com.docker.compose.project':'OWN_PROJECT','com.docker.compose.service':role})
                after[role]['Config']['Labels']['com.docker.compose.replace']=name
            producer={'compose_sha':owner.COMPOSE_510_SHA,'before_model':old_model,'before_images':o.old_images}
            args=(o.production_ids(b),o.images,b['current']['layers']+[o.override],o.hashes)
            # The original comparator refuses exactly this label delta.
            with self.assertRaisesRegex(Refused,'RUNTIME_SEMANTIC_DELTA'):owner.post_semantics(before,after,*args,model)
            owner.post_semantics(before,after,*args,model,producer)
            for poison in ('pin','declared','image','name','same_cid','implicit','label_value','old_cid_value','other_label'):
                a,z,m,p=copy.deepcopy(before),copy.deepcopy(after),copy.deepcopy(model),copy.deepcopy(producer)
                images=copy.deepcopy(o.images)
                if poison=='pin':p['compose_sha']='0'*64
                if poison=='declared':m['services']['core'].setdefault('labels',{})['com.docker.compose.replace']='OWN_PROJECT-core'
                if poison=='image':images['core']['Config']['Labels']['com.docker.compose.replace']='OWN_PROJECT-core'
                if poison=='name':z['core']['Name']='/FOREIGN'
                if poison=='same_cid':z['core']['Id']=a['core']['Id']
                if poison=='implicit':m['services']['core'].pop('container_name')
                if poison=='label_value':z['core']['Config']['Labels']['com.docker.compose.replace']='FOREIGN'
                if poison=='old_cid_value':z['core']['Config']['Labels']['com.docker.compose.replace']=a['core']['Id']
                if poison=='other_label':z['core']['Config']['Labels']['OWN_UNAPPROVED']='CANARY'
                with self.subTest(poison=poison),self.assertRaises(Refused):owner.post_semantics(a,z,o.production_ids(b),images,args[2],args[3],m,p)

    def test_fixed_families_do_not_export_values_or_dynamic_keys(self):
        base={'config':{},'host':{},'networks':{},'mounts':[]}
        cases=[('config','Env','CONFIG_ENV'),('config','Cmd','CONFIG_PROCESS'),
               ('config','Labels','CONFIG_LABELS'),('config','Image','CONFIG_IMAGE'),
               ('config','OWN_PRIVATE_KEY','CONFIG_OTHER'),('host','Binds','HOST_MOUNT_ENCODING'),
               ('host','CapAdd','HOST_SECURITY'),('host','Memory','HOST_RESOURCES'),
               ('host','NetworkMode','HOST_NETWORK'),('host','OWN_PRIVATE_KEY','HOST_OTHER')]
        families=[]
        for section,key,want in cases:
            actual=copy.deepcopy(base);actual[section][key]='OWN_SECRET_CANARY'
            family=owner.semantic_failure_family(base,actual);self.assertEqual(family,want);families.append(family)
        for section,want in [('networks','NETWORK_ATTACHMENTS'),('mounts','MOUNT_ATTACHMENTS')]:
            actual=copy.deepcopy(base);actual[section]={'OWN_SECRET_CANARY':True}
            self.assertEqual(owner.semantic_failure_family(base,actual),want)
        self.assertEqual(owner.semantic_failure_family({},{}),'OTHER')
        self.assertNotIn('OWN_',json.dumps(families))
        # Presence remains significant; missing and explicit null are unequal.
        actual=copy.deepcopy(base);actual['config']['Env']=None
        self.assertEqual(owner.semantic_failure_family(base,actual),'CONFIG_ENV')

    def test_actual_whole_comparison_keeps_exact_first_and_no_success_on_delta(self):
        with tempfile.TemporaryDirectory() as tmp:
            o,b,events=fixtures.ProductionImageTests().setup_owner(Path(tmp).resolve())
            with patch('deployment_http.readonly_acceptance',return_value={}):o.preflight(b,fixtures.guard)
            self.assertEqual(o.apply(b,fixtures.guard),'APPLIED')
            original=copy.deepcopy(o.post)
            args=(o.production_ids(b),o.images,b['current']['layers']+[o.override],o.hashes,o.prospective_cfg)
            owner.post_semantics(o.original,original,*args)
            original['core']['Config']['Env'].append('OWN_PRIVATE=CANARY')
            first=Refused('RUNTIME_SEMANTIC_DELTA');first.close_secondary=('OWN_PREVIOUS',)
            real_need=owner.need
            def need(ok,code):
                if not ok and code=='RUNTIME_SEMANTIC_DELTA':raise first
                real_need(ok,code)
            with patch.object(owner,'need',need),self.assertRaises(Refused) as caught:owner.post_semantics(o.original,original,*args)
            self.assertIs(caught.exception,first)
            self.assertEqual(first.args,('RUNTIME_SEMANTIC_DELTA',))
            self.assertEqual((first.semantic_role,first.semantic_family),('core','CONFIG_ENV'))
            self.assertEqual(first.close_secondary,('OWN_PREVIOUS',))

    def test_optional_fixed_file_privacy_and_secondary_cannot_replace_first(self):
        with tempfile.TemporaryDirectory() as tmp:
            obj=test_owner.TestOwner.__new__(test_owner.TestOwner);obj.operation=Path(tmp).resolve()
            first=Refused('RUNTIME_SEMANTIC_DELTA');first.semantic_role='core';first.semantic_family='HOST_OTHER'
            obj.semantic_failure_evidence(first,'CI_FIRST_PROMOTION',lambda:None)
            p=obj.operation/'runtime-semantic-failure.json';v=json.loads(p.read_bytes())
            self.assertEqual(set(v),{'schema','operation','role','family'})
            self.assertEqual(v['family'],'HOST_OTHER');self.assertEqual(p.stat().st_mode&0o777,0o600)
            self.assertNotIn('CANARY',p.read_text())
            with patch.object(obj,'write',side_effect=OSError('OWN_SECRET_CANARY')):
                obj.semantic_failure_evidence(first,'CI_FIRST_PROMOTION',lambda:None)
            self.assertEqual(first.args,('RUNTIME_SEMANTIC_DELTA',));self.assertEqual(first.close_secondary,('SEMANTIC_EVIDENCE',))
            with patch.object(obj,'write') as writer:
                first.semantic_family='OWN_SECRET_CANARY'
                obj.semantic_failure_evidence(first,'CI_FIRST_PROMOTION',lambda:None);writer.assert_not_called()
            first.semantic_family='HOST_OTHER';calls=[]
            def late():
                calls.append('guard')
                if len(calls)==2:raise Refused('OWN_LATE')
            with patch.object(obj,'write',return_value=None):obj.semantic_failure_evidence(first,'CI_FIRST_PROMOTION',late)
            self.assertEqual(first.args,('RUNTIME_SEMANTIC_DELTA',))
            self.assertEqual(first.close_secondary,('SEMANTIC_EVIDENCE','SEMANTIC_EVIDENCE'))


if __name__=='__main__':unittest.main()
