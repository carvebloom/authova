"""Owner source/OWN components; real own subprocess/FS, no Docker or product DB."""
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import compose_owner as production
import host_adapter
import private_native
from host_admission import canonical
from release_contract import Refused
import test_http as actor
import test_proxy


def guard():pass
guard.full=lambda *args:None


class CIOperationTests(unittest.TestCase):
    def test_ram_transport_preserves_bytes_and_never_logs_canary(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp)/'own-cli'
            p.write_text('#!'+sys.executable+'\nimport sys\ndata=sys.stdin.buffer.read();sys.stdout.buffer.write(data);sys.stderr.write("PRIVATE_STDERR_CANARY")\n');p.chmod(0o700)
            pin={'path':str(p),'identity':None,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}
            native=private_native.Native(pin,'unix:///own-test-only',tmp)
            secret=b'OWN_PRIVATE_BODY_CANARY'+bytes(range(256))+b'\r\n\0'
            rc,body=native.run('OWN_ECHO',[],guard,body=secret,whole=10,suffix=0)
            self.assertEqual((rc,body),(0,secret))
            self.assertEqual(native.observed['identity'],list(host_adapter._token(p.stat())))
            self.assertNotIn('CANARY',repr(native.records))
            self.assertTrue(native.records[0]['known_return'])
            self.assertTrue(native.records[0]['all_selected_IO_closed'])
            with patch.object(native,'popen',side_effect=AssertionError('NO_SPAWN_ON_CAP')):
                def stopped():pass
                stopped.full=lambda *args:(_ for _ in ()).throw(Refused('OWN_NO_FULL_CAP'))
                with self.assertRaisesRegex(Refused,'OWN_NO_FULL_CAP'):native.run('OWN_STOP',[],stopped,whole=10)
    def test_exact_compose_semantics_and_raw_env_quote_contract(self):
        values={'A':" leading 'single' \"double\" = # and trailing ",'B':'plain'}
        self.assertEqual(production.env_file(values),b'A= leading \'single\' "double" = # and trailing \nB=plain\n')
        with self.assertRaisesRegex(Refused,'PROBE_ENV_UNSUPPORTED'):production.env_file({'A':'two\nlines'})
        old={'services':{'core':{'image':'old-core','environment':{'DSN':'OWN_SECRET'}},'console':{'image':'old-console','environment':{'AUTHOVA_CONSOLE_ASSETS':'old-assets'}},'worker':{'image':'old-worker'}}}
        candidate={'coreID':'new-core','consoleID':'new-console'}
        new=copy.deepcopy(old);new['services']['core']['image']='new-core';new['services']['console']['image']='new-console';new['services']['console']['environment']['AUTHOVA_CONSOLE_ASSETS']='/opt/authova-console/web'
        production.compare_config(old,new,candidate)
        for service,key,value in [('core','environment',{'DSN':'changed'}),('worker','image','changed'),('console','ports',['0.0.0.0:8090:8090'])]:
            bad=copy.deepcopy(new);bad['services'][service][key]=value
            with self.subTest(service=service),self.assertRaisesRegex(Refused,'COMPOSE_SEMANTIC_DELTA'):production.compare_config(old,bad,candidate)
    def test_ready_pipe_after_original_command_deadline_sends_no_private_frame(self):
        # Real child/pipes/readiness, but each local clock can cross the command
        # body deadline while select returns. Overall owner time alone is weaker.
        clock=private_native.time.time;mono=private_native.time.monotonic
        selector_type=private_native.selectors.DefaultSelector
        for axis in ('wall','mono'):
            offset={'wall':0,'mono':0}
            class LateSelector:
                def __init__(self):self.inner=selector_type()
                def __getattr__(self,name):return getattr(self.inner,name)
                def select(self,timeout):
                    events=self.inner.select(timeout);offset[axis]=6;return events
            with self.subTest(axis=axis),tempfile.TemporaryDirectory() as tmp:
                p=Path(tmp)/'own-reader'
                p.write_text('#!'+sys.executable+'\nimport sys;sys.stdin.buffer.read()\n');p.chmod(0o700)
                native=private_native.Native({'path':str(p),'identity':None,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()},'unix:///own-test-only',tmp)
                with patch.object(private_native.selectors,'DefaultSelector',LateSelector),patch.object(private_native.time,'time',side_effect=lambda:clock()+offset['wall']),patch.object(private_native.time,'monotonic',side_effect=lambda:mono()+offset['mono']),patch.object(private_native.os,'write',side_effect=AssertionError('NO_PRIVATE_WRITE_AFTER_CAP')) as write:
                    with self.assertRaisesRegex(Refused,'CLI_TIMEOUT_UNKNOWN'):
                        native.run('OWN_READY_LATE',[],guard,body=b'OWN_PRIVATE_CANARY',whole=10,suffix=0)
                    write.assert_not_called()
                self.assertFalse(native.records[0]['timely'])
                self.assertTrue(native.records[0]['all_selected_IO_closed'])
    def test_readonly_schema_refusal_never_up_or_rollback(self):
        # Reuse the real signed boundary and real durable FS from its fixture.
        from test_ci_host_adapter import CIHostAdapterTests,receipt
        fixture=CIHostAdapterTests()
        with tempfile.TemporaryDirectory() as tmp:
            parts=fixture.setup_operation(Path(tmp),'production')
            parts[-1].apply_result='PREFLIGHT_REFUSED'
            result=fixture.run_operation(parts,receipt=receipt())
            self.assertEqual(result['result'],'REFUSED_PREFLIGHT')
            self.assertNotIn('rollback',parts[-1].events)
            self.assertNotIn('accept',parts[-1].events)
    def test_reserved_csrf_and_one_new_mail_only(self):
        fields=actor.Fields(b'<input name="kind" value="email"><input name="kind" value="phone"><input name="_csrf" value="OWN">')
        self.assertEqual(fields.inputs,{'_csrf':'OWN'})
        with self.assertRaisesRegex(Refused,'FORM_DUPLICATE_VALUE'):actor.Fields(b'<input name="_csrf" value="A"><input name="_csrf" value="B">')
        inbox=test_proxy.Inbox('ci-own@example.invalid','OWN_CONTROL')
        body=b'To: ci-own@example.invalid\r\nContent-Type: text/plain\r\n\r\nAuthova verification code: 123456.\r\n'
        with self.assertRaisesRegex(ValueError,'SMTP_SCOPE'):inbox.store('other@example.invalid',body)
        now=time.time();inbox.store('ci-own@example.invalid',body)
        with self.assertRaisesRegex(ValueError,'SMTP_CONTROL'):inbox.consume('OTHER',now)
        with self.assertRaisesRegex(ValueError,'SMTP_TIME'):inbox.consume('OWN_CONTROL',now+100)
        self.assertEqual(inbox.consume('OWN_CONTROL',now)['code'],'123456')
        with self.assertRaisesRegex(ValueError,'SMTP_AMBIGUOUS'):inbox.store('ci-own@example.invalid',body)
    def test_http_actor_rejects_production_and_wrong_identity_before_IO(self):
        c={'schema':'authova-ci-http-private/v1','project':'authova-ci-test-'+'a'*12+'-42','scope':'1'*32,'source':'a'*40,
           'core':'https://host.docker.internal:43100','console':'https://host.docker.internal:43101','ca_pem':'OWN_NOT_REAL_CA',
           'recipient':'ci-'+'1'*32+'@example.invalid','control':'OWN','bootstrap':{'Email':'owner-'+'1'*32+'@example.invalid'},'state':{}}
        for bad,code in [(dict(c,core='https://auth.carvebloom.localhost'),'CI_HTTP_ORIGIN'),(dict(c,recipient='other@example.invalid'),'CI_HTTP_IDENTITY_SCOPE')]:
            with self.assertRaisesRegex(Refused,code):actor.Acceptance(bad)
    def test_installed_bootstrap_holds_bytes_and_late_report_is_silent(self):
        spec=importlib.util.spec_from_file_location('own_runner',Path(actor.__file__).with_name('host_runner.py'))
        runner=importlib.util.module_from_spec(spec);spec.loader.exec_module(runner)
        names=('release_contract','host_admission','host_adapter','private_native','deployment_http','compose_owner','test_owner')
        previous={n:sys.modules.get(n) for n in names}
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();root.chmod(0o700);pins={}
            for name in runner.SOURCES:
                body=Path(actor.__file__).with_name(name).read_bytes();p=root/name;p.write_bytes(body);p.chmod(0o600);pins[name]=hashlib.sha256(body).hexdigest()
            raw=canonical(pins);(root/'code-pins.json').write_bytes(raw);(root/'code-pins.json').chmod(0o600)
            try:
                with patch.object(runner,'__file__',str(root/'host_runner.py')):
                    modules,held=runner.bootstrap(hashlib.sha256(raw).hexdigest())
                    self.assertEqual(set(held),set(runner.SOURCES));self.assertIn('TestOwner',modules['test_owner'].__dict__)
                    (root/'test_http.py').write_bytes(b'raise RuntimeError("MUST_NOT_EXECUTE_UNPINNED")')
                    with self.assertRaisesRegex(ValueError,'CODE_SHA'):runner.bootstrap(hashlib.sha256(raw).hexdigest())
            finally:
                for name,value in previous.items():
                    if value is None:sys.modules.pop(name,None)
                    else:sys.modules[name]=value
        stream=io.BytesIO()
        with patch.object(runner,'ORIGINAL',(100,10)),patch.object(runner,'REPORT_SECONDS',30),patch.object(runner,'STDOUT_ATTEMPTED',False),patch.object(runner.time,'time',return_value=131),patch.object(runner.time,'monotonic',return_value=11),patch.object(runner.sys,'stdout',type('S',(),{'buffer':stream})()):
            with self.assertRaisesRegex(ValueError,'REPORT_EXPIRED'):runner.report({'result':'PASS'})
        self.assertEqual(stream.getvalue(),b'')


if __name__=='__main__':unittest.main()
