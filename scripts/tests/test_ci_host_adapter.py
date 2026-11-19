"""Host controls with own real files/flock/fsync, fake owner; no service actions."""
import hashlib
import hmac
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'ci'))
import host_adapter as adapter
import host_admission as admission
from release_contract import REQUIRED_CHECKS, Refused


def candidate():
    return dict(schema='authova-image-candidate/v1', repo='carvebloom/authova', source='a'*40,
                coreID='sha256:'+'b'*64, consoleID='sha256:'+'c'*64,
                manifest='d'*64, archive='e'*64, schemaDigest='f'*64)


def envelope(action='test', **changes):
    p = dict(schema='authova-host-admission/v1', action=action, candidate=candidate(),
             candidate_digest=admission.candidate_digest(candidate()), issued=1000, expires=1300,
             nonce='1'*64, build_pipeline=42, test_pipeline=None, production_pipeline=None,
             reviewed_by=None, reviewed=None)
    if action == 'production':
        p.update(test_pipeline=43, production_pipeline=44, reviewed_by='rwasayc', reviewed=999, issued=1003, expires=1303)
    p.update(changes)
    return dict(payload=p, signature='hmac-sha256:' + hmac.new(b'K'*32, admission.canonical(p), hashlib.sha256).hexdigest())


def receipt():
    return dict(schema='authova-test-deployment/v1', candidate=candidate(), result='PASS',
                project='authova-ci-test-'+'a'*12+'-42', scope='2'*32,
                started_at=1003, finished_at=1003,
                checks={k:'PASS' for k in REQUIRED_CHECKS},
                check_evidence={k:dict(result='PASS', exit_code=0, known_return=True, timely=True,
                                      started_at=1003, finished_at=1003, evidence_sha256='3'*64)
                                for k in REQUIRED_CHECKS})


class FakeOwner:
    def __init__(self, binding):
        self.binding = binding
        self.events = []
        self.apply_result = 'APPLIED'
        self.accept_result = 'PASS' if binding['action'] == 'production' else receipt()

    def current(self, binding, guard):
        guard()
        self.events.append('current')
        return binding['current']

    def preflight(self, binding, guard):
        guard()
        self.events.append('preflight')

    def apply(self, binding, guard):
        guard()
        self.events.append('apply')
        return self.apply_result

    def accept(self, binding, guard):
        guard()
        self.events.append('accept')
        return self.accept_result

    def rollback(self, binding, guard):
        guard()
        self.events.append('rollback')
        return 'ROLLED_BACK'


class CIHostAdapterTests(unittest.TestCase):
    def test_exact_hmac_and_clock_not_review_or_bool(self):
        e = envelope()
        self.assertEqual(admission.verify(e, b'K'*32, candidate(), 'test', 1299), e['payload'])
        for bad, now, code in (
            (e, 1300, 'ADMISSION_EXPIRED'), (e, 999, 'ADMISSION_EXPIRED'),
            (envelope(issued=True), 1000, 'ADMISSION_EXPIRED'),
            (envelope(build_pipeline=True), 1000, 'BUILD_PIPELINE'),
            (envelope(reviewed_by='rwasayc'), 1000, 'TEST_REVIEW_CONFUSION'),
            (envelope('production', reviewed=1004), 1003, 'PRODUCTION_REVIEW'),
            (dict(e, signature='hmac-sha256:'+'0'*64), 1000, 'SIGNATURE'),
        ):
            with self.subTest(code=code), self.assertRaisesRegex(Refused, '^'+code+'$'):
                admission.verify(bad, b'K'*32, candidate(), bad['payload']['action'], now)
        with self.assertRaisesRegex(Refused, 'JSON_DUPLICATE'):
            admission.strict_json(b'{"payload":1,"payload":2}')
        with self.assertRaises(UnicodeDecodeError):
            admission.strict_json('{"key":1}'.encode('utf-16'))

    def setup_operation(self, root, action='test', actual=None):
        e = envelope(action)
        issued, state = root/'issued', root/'state'
        issued.mkdir(mode=0o700)
        state.mkdir(mode=0o700)
        current = dict(schema='authova-owner-current/v1', project='authova-ci-test-'+'a'*12+'-42',
                       schemaDigest='f'*64, layers=['/owned/test-compose.json'],
                       services={k:dict(id=None, image=None) for k in ('core', 'console')})
        if action == 'production':
            current.update(project='authova-local-production-20261114',
                           layers=['/host/base', '/host/previous', '/host/current'],
                           services={k:dict(id='4'*64, image='sha256:'+'5'*64) for k in ('core', 'console')})
        b = dict(schema='authova-owner-binding/v1', action=action, candidate=candidate(),
                 project=current['project'], current=current, seconds=300, fixture=None)
        if action == 'test':
            fixture_root = str(state.resolve())+'/test/'+'2'*32
            current['layers'] = [fixture_root+'/compose.json']
            b['fixture'] = dict(schema='authova-ci-test-binding/v1', scope='2'*32, root=fixture_root,
                                network=current['project']+'-network', mysql_volume=current['project']+'-mysql',
                                service_names={k:current['project']+'-'+k for k in ('core','console','mysql','valkey')})
        p = e['payload']
        record = {k:p[k] for k in ('nonce', 'action', 'candidate_digest', 'build_pipeline', 'test_pipeline', 'production_pipeline')}
        record.update(schema='authova-host-admission-ledger/v1',
                      payload_sha256=hashlib.sha256(admission.canonical(p)).hexdigest(),
                      test_receipt_sha256=None if action == 'test' else hashlib.sha256(admission.canonical(actual or receipt())).hexdigest())
        fd = os.open(str(issued/(p['nonce']+'.json')), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as file:
            file.write(admission.canonical(record))
        return b, e, issued, state, FakeOwner(b)

    def run_operation(self, parts, **kwargs):
        b, e, issued, state, owner = parts
        return adapter.execute(b, e, b'K'*32, str(issued.resolve()), str(state.resolve()), owner,
                               wall=kwargs.pop('wall', lambda:1003), mono=kwargs.pop('mono', lambda:9), **kwargs)

    def test_nonce_intent_before_mutation_and_restart_refuses(self):
        with tempfile.TemporaryDirectory() as tmp:
            parts = self.setup_operation(Path(tmp))
            b, e, issued, state, owner = parts
            original = owner.apply
            def apply(binding, guard):
                intent = state/(e['payload']['nonce']+'.intent.json')
                self.assertTrue(intent.is_file())
                self.assertEqual(json.loads(intent.read_bytes())['current'], b['current'])
                return original(binding, guard)
            owner.apply = apply
            self.assertEqual(self.run_operation(parts)['result'], 'PASS_TEST')
            self.assertTrue((state/(e['payload']['nonce']+'.test-receipt.json')).is_file())
            before = list(owner.events)
            with self.assertRaisesRegex(Refused, 'NONCE_CONSUMED'):
                self.run_operation(parts)
            self.assertEqual(owner.events, before)
            self.assertEqual(owner.events.count('apply'), 1)

    def test_cas_ledger_receipt_and_lock_reject_before_apply(self):
        for scenario in ('current', 'receipt', 'ledger', 'lock'):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as tmp:
                parts = self.setup_operation(Path(tmp), 'production')
                b, e, issued, state, owner = parts
                r = receipt()
                if scenario == 'current':
                    owner.current = lambda *args:dict(b['current'], layers=['/other'])
                    code = 'CURRENT_CAS'
                elif scenario == 'receipt':
                    r['scope'] = '9'*32
                    code = 'LEDGER_RECEIPT'
                elif scenario == 'ledger':
                    (issued/(e['payload']['nonce']+'.json')).chmod(0o644)
                    code = 'PRIVATE_FILE_METADATA'
                else:
                    original = owner.preflight
                    def replaced(*args):
                        original(*args)
                        (state/'operation.lock').unlink()
                        (state/'operation.lock').touch(mode=0o600)
                    owner.preflight = replaced
                    code = 'LOCK_REPLACED'
                with self.assertRaisesRegex(Refused, code):
                    self.run_operation(parts, receipt=r)
                self.assertNotIn('apply', owner.events)

    def test_unknown_no_rollback_known_failed_rollback_and_expiry(self):
        for outcome, expected in (('UNKNOWN', 'MUTATION_UNKNOWN'), ('FAILED_KNOWN', 'ROLLED_BACK')):
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory() as tmp:
                parts = self.setup_operation(Path(tmp), 'production')
                owner = parts[-1]
                owner.apply_result = outcome
                if outcome == 'UNKNOWN':
                    with self.assertRaisesRegex(Refused, expected):self.run_operation(parts, receipt=receipt())
                    self.assertNotIn('rollback', owner.events)
                else:
                    self.assertEqual(self.run_operation(parts, receipt=receipt())['result'], expected)
                    self.assertEqual(owner.events.count('rollback'), 1)
        # A late close after consumed intent must never start mutation on either axis.
        for axis in ('wall', 'mono'):
            with self.subTest(axis=axis), tempfile.TemporaryDirectory() as tmp:
                parts = self.setup_operation(Path(tmp))
                w, m = [1003], [9]
                original = adapter.durable
                def late(*args):
                    original(*args)
                    if axis == 'wall':w[0] += 300
                    else:m[0] += 300
                with patch.object(adapter, 'durable', late), self.assertRaisesRegex(Refused, 'OPERATION_EXPIRED'):
                    self.run_operation(parts, wall=lambda:w[0], mono=lambda:m[0])
                self.assertNotIn('apply', parts[-1].events)

    def test_private_entry_retains_origin_and_refuses_symlink_key(self):
        for scenario in ('ok', 'symlink', 'late'):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                parts = self.setup_operation(root)
                b, e, issued, state, owner = parts
                private = root/'private'
                private.mkdir(mode=0o700)
                for name, body in (('binding.json', admission.canonical(b)),
                                   ('admission.json', admission.canonical(e)), ('key', b'K'*32)):
                    fd = os.open(str(private/name), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                    with os.fdopen(fd, 'wb') as file:file.write(body)
                if scenario == 'symlink':
                    (private/'key').rename(private/'other')
                    (private/'key').symlink_to('other')
                wall = [1003]
                original = adapter.read_private
                def read(*args, **kw):
                    result = original(*args, **kw)
                    if scenario == 'late':wall[0] = 1303
                    return result
                def execute():
                    return adapter.execute_files(str(private.resolve()), 'admission.json',
                        str(issued.resolve()), str(state.resolve()), owner,
                        wall=lambda:wall[0], mono=lambda:9)
                with patch.object(adapter, 'read_private', read):
                    if scenario == 'symlink':
                        with self.assertRaises(OSError):execute()
                    elif scenario == 'late':
                        with self.assertRaisesRegex(Refused, 'OPERATION_EXPIRED'):execute()
                    else:
                        self.assertEqual(execute()['result'], 'PASS_TEST')
                if scenario != 'ok':self.assertEqual(owner.events, [])

    def test_intent_fsync_first_and_close_secondary_keep_object(self):
        first = OSError('OWN_CANARY_NOT_PUBLIC')
        with tempfile.TemporaryDirectory() as tmp:
            parts = self.setup_operation(Path(tmp))
            original_close = adapter.os.close
            close_calls = []
            def bad_close(fd):
                close_calls.append(fd)
                original_close(fd)
                raise OSError('OWN_SECONDARY')
            # Direct durable boundary: real EXCL file must persist on both faults.
            fd = os.open(str(parts[3]), os.O_RDONLY | os.O_DIRECTORY)
            try:
                with patch.object(adapter.os, 'fsync', side_effect=first), patch.object(adapter.os, 'close', side_effect=bad_close):
                    with self.assertRaises(OSError) as got:adapter.durable(fd, 'consumed.json', {'test':'OWN'})
                self.assertIs(got.exception, first)
                self.assertEqual(first.close_secondary, ('PRIVATE_CLOSE',))
                self.assertEqual(len(close_calls), 1)
                self.assertTrue((parts[3]/'consumed.json').is_file())
            finally:
                original_close(fd)


if __name__ == '__main__':
    unittest.main()
