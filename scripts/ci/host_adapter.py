"""Authova host execution boundary, imported by the pinned private host worker.

The owner supplies its installed two-service operations object; nothing loads code
or commands from an artifact, job, admission or JSON binding. Not a CLI accepting
arbitrary shell. All backend methods must preserve the private/public boundary.
"""
import fcntl
import hashlib
import os
import stat
import time
from contextlib import contextmanager

from host_admission import canonical, candidate_digest, strict_json, verify
from release_contract import Refused, need, test_receipt

LEDGER_KEYS = {'schema', 'nonce', 'action', 'candidate_digest', 'payload_sha256',
               'build_pipeline', 'test_pipeline', 'production_pipeline', 'test_receipt_sha256'}
BINDING_KEYS = {'schema', 'action', 'candidate', 'project', 'current', 'seconds', 'fixture'}


def _token(s):
    return (s.st_dev, s.st_ino, s.st_mode, s.st_nlink, s.st_uid, s.st_gid,
            s.st_size, s.st_mtime_ns, s.st_ctime_ns)


def _close(fd, first, code):
    try:
        os.close(fd)
    except BaseException:
        if first is None:
            raise
        # Keep the original exception object; never expose close exception text.
        previous = getattr(first, 'close_secondary', ())
        first.close_secondary = previous + (code,)


def _regular(s, uid):
    need(stat.S_ISREG(s.st_mode) and stat.S_IMODE(s.st_mode) == 0o600
         and s.st_uid == uid and s.st_nlink == 1, 'PRIVATE_FILE_METADATA')


@contextmanager
def directory(path, uid, *, private=True):
    """Open absolute components without symlink traversal; retain final dirfd."""
    need(type(path) is str and path.startswith('/') and path != '/'
         and all(p not in ('', '.', '..') for p in path.split('/')[1:]), 'PRIVATE_DIRECTORY_PATH')
    fd = os.open('/', os.O_RDONLY | os.O_DIRECTORY)
    first = None
    try:
        for part in path.split('/')[1:]:
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            previous, fd = fd, child
            os.close(previous)
        s = os.fstat(fd)
        need(stat.S_IMODE(s.st_mode) in ((0o700,) if private else (0o700,0o755)) and s.st_uid == uid, 'PRIVATE_DIRECTORY_METADATA')
        yield fd
    except BaseException as exc:
        first = exc
        raise
    finally:
        _close(fd, first, 'PRIVATE_CLOSE')


def read_private(root, name, uid, limit=65536):
    need(type(name) is str and '/' not in name and name not in ('', '.', '..'), 'PRIVATE_NAME')
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=root)
    first = None
    try:
        before = os.fstat(fd)
        _regular(before, uid)
        need(before.st_size <= limit, 'PRIVATE_FILE_SIZE')
        chunks, total = [], 0
        while True:
            part = os.read(fd, min(65536, limit + 1 - total))
            if not part:
                break
            chunks.append(part)
            total += len(part)
            need(total <= limit, 'PRIVATE_FILE_SIZE')
        need(total == before.st_size and _token(before) == _token(os.fstat(fd)), 'PRIVATE_FILE_DRIFT')
        need(_token(before) == _token(os.stat(name, dir_fd=root, follow_symlinks=False)), 'PRIVATE_ENDPATH')
        return b''.join(chunks)
    except BaseException as exc:
        first = exc
        raise
    finally:
        _close(fd, first, 'PRIVATE_CLOSE')


def durable(root, name, value):
    """EXCL is also the persistent single-use marker. Never unlink on error."""
    fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=root)
    first = None
    try:
        body = canonical(value)
        view = memoryview(body)
        while view:
            count = os.write(fd, view)
            need(count > 0, 'DURABLE_WRITE')
            view = view[count:]
        os.fsync(fd)
    except BaseException as exc:
        first = exc
        raise
    finally:
        _close(fd, first, 'PRIVATE_CLOSE')
    os.fsync(root)


@contextmanager
def operation_lock(root, uid):
    fd = os.open('operation.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK,
                 0o600, dir_fd=root)
    first = None
    try:
        _regular(os.fstat(fd), uid)
        # A concurrent owner refuses, rather than waiting beyond its admission.
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        identity = os.fstat(fd)

        def check():
            current = os.stat('operation.lock', dir_fd=root, follow_symlinks=False)
            need(_token(current) == _token(identity) == _token(os.fstat(fd)), 'LOCK_REPLACED')
        check()
        yield check
        check()
    except BaseException as exc:
        first = exc
        raise
    finally:
        # Closing releases flock even if unlock or later bookkeeping fails.
        _close(fd, first, 'PRIVATE_CLOSE')


def ledger(root, payload, receipt, uid):
    record = strict_json(read_private(root, payload['nonce'] + '.json', uid))
    need(type(record) is dict and set(record) == LEDGER_KEYS, 'LEDGER_KEYS')
    need(record['schema'] == 'authova-host-admission-ledger/v1', 'LEDGER_SCHEMA')
    for key in ('nonce', 'action', 'candidate_digest', 'build_pipeline', 'test_pipeline', 'production_pipeline'):
        need(type(record[key]) is type(payload[key]) and record[key] == payload[key], 'LEDGER_BINDING')
    need(record['payload_sha256'] == hashlib.sha256(canonical(payload)).hexdigest(), 'LEDGER_PAYLOAD')
    if payload['action'] == 'production':
        test_receipt(receipt, payload['candidate'])
        need(receipt['finished_at'] <= payload['issued'], 'TEST_RECEIPT_IN_FUTURE')
        # The fixture project itself binds the actual test pipeline ID.
        need(receipt['project'] == 'authova-ci-test-' + payload['candidate']['source'][:12]
             + '-' + str(payload['build_pipeline']), 'TEST_PIPELINE_BINDING')
        need(record['test_receipt_sha256'] == hashlib.sha256(canonical(receipt)).hexdigest(), 'LEDGER_RECEIPT')
    else:
        need(receipt is None and record['test_receipt_sha256'] is None, 'TEST_RECEIPT_CONFUSION')
    return record


def validate_binding(binding, payload, state_root):
    need(type(binding) is dict and set(binding) == BINDING_KEYS
         and binding['schema'] == 'authova-owner-binding/v1', 'OWNER_BINDING')
    need(binding['action'] == payload['action'] and binding['candidate'] == payload['candidate'], 'OWNER_CANDIDATE')
    need(type(binding['seconds']) is int and binding['seconds'] == (3600 if payload['action']=='test' else 1800), 'OWNER_BUDGET')
    if payload['action'] == 'production':
        need(binding['project'] == 'authova-local-production-20261114' and binding['fixture'] is None, 'PRODUCTION_PROJECT')
    else:
        need(binding['project'] == 'authova-ci-test-' + payload['candidate']['source'][:12]
             + '-' + str(payload['build_pipeline']), 'TEST_PROJECT')
        fixture = binding['fixture']
        need(type(fixture) is dict and set(fixture) == {'schema', 'scope', 'root', 'network', 'mysql_volume', 'secret_volumes', 'service_names'}, 'FIXTURE_KEYS')
        import re
        need(fixture['schema'] == 'authova-ci-test-binding/v2' and type(fixture['scope']) is str
             and re.fullmatch('[0-9a-f]{32}', fixture['scope']), 'FIXTURE_SCOPE')
        need(fixture['root'] == state_root + '/test/' + fixture['scope'], 'FIXTURE_ROOT')
        need(fixture['network'] == binding['project'] + '-network'
             and fixture['mysql_volume'] == binding['project'] + '-mysql'
             and fixture['service_names'] == {k:binding['project']+'-'+k for k in ('core', 'console', 'mysql', 'valkey','proxy')}
             and fixture['secret_volumes'] == {k:binding['project']+'-'+k+'-credentials' for k in ('core','console','valkey','proxy')}, 'FIXTURE_RESOURCES')
        need(binding['current']['layers'] == [fixture['root'] + '/compose.json'], 'FIXTURE_COMPOSE')
    current = binding['current']
    need(type(current) is dict and set(current) == {'schema', 'project', 'schemaDigest', 'layers', 'services'}
         and current['schema'] == 'authova-owner-current/v1' and current['project'] == binding['project']
         and current['schemaDigest'] == payload['candidate']['schemaDigest'], 'CURRENT_SCHEMA')
    need(type(current['layers']) is list and len(current['layers']) >= 1
         and all(type(p) is str and p.startswith('/') for p in current['layers']), 'CURRENT_LAYERS')
    need(type(current['services']) is dict and set(current['services']) == {'core', 'console'}, 'CURRENT_SERVICES')
    for service in current['services'].values():
        need(type(service) is dict and set(service) == {'id', 'image'}, 'CURRENT_SERVICE_KEYS')
        # Test is new; existing production service IDs/images are mandatory.
        if payload['action'] == 'test':
            need(service == {'id': None, 'image': None}, 'TEST_MUST_BE_NEW')
        else:
            import re
            need(type(service['id']) is str and re.fullmatch('[0-9a-f]{64}', service['id'])
                 and type(service['image']) is str and re.fullmatch('sha256:[0-9a-f]{64}', service['image']), 'CURRENT_SERVICE_ID')


def execute(binding, envelope, key, ledger_root, state_root, owner, *, receipt=None,
            uid=None, wall=time.time, mono=time.monotonic, _origin=None):
    """Installed owner interface, never invoked directly by a pipeline command.

    owner: current(binding, guard), preflight(binding, guard), apply(binding, guard),
    accept(binding, guard), rollback(binding, guard). Internal Docker/HTTP calls
    must use guard before/after and reserve their own complete suffix/close caps.
    apply returns APPLIED or FAILED_KNOWN; any other outcome is unknown, no rollback.
    Production accept is non-destructive. Test accept returns the full nine-gate
    receipt from only this binding's fresh users/clients/DB. JSON does not implement
    these methods or grant access to a production/fixture runtime.
    """
    original_i, original_m = (wall(), mono()) if _origin is None else _origin
    uid = os.getuid() if uid is None else uid
    p = verify(envelope, key, binding['candidate'], binding['action'], int(original_i))
    validate_binding(binding, p, state_root)
    seconds = binding['seconds']

    def cap():
        w, m = wall(), mono()
        need(original_i <= w < original_i + seconds and original_m <= m < original_m + seconds, 'OPERATION_EXPIRED')
        return w

    with directory(ledger_root, uid) as issued, directory(state_root, uid) as state:
        with operation_lock(state, uid) as lock_check:
            def guard():
                lock_check()
                return cap()
            def full(whole, suffix=0):
                guard()
                need(type(whole) in (int,float) and type(suffix) in (int,float) and whole > 0 and suffix >= 0, 'OPERATION_WHOLE')
                need(wall()+whole+suffix < original_i+seconds and mono()+whole+suffix < original_m+seconds, 'OPERATION_FULL_SUFFIX')
            guard.full = full
            guard()
            ledger(issued, p, receipt, uid)
            try:
                os.stat(p['nonce'] + '.intent.json', dir_fd=state, follow_symlinks=False)
            except FileNotFoundError:
                pass
            else:
                raise Refused('NONCE_CONSUMED')
            need(owner.current(binding, guard) == binding['current'], 'CURRENT_CAS')
            guard()
            # Private config/schema/fixture ownership validation is read-only.
            # Failures here never consume a mutation or call apply.
            owner.preflight(binding, guard)
            guard()
            need(owner.current(binding, guard) == binding['current'], 'CURRENT_CAS')
            verify(envelope, key, binding['candidate'], binding['action'], int(guard()))
            intent = {'schema': 'authova-owner-intent/v1', 'payload': p, 'current': binding['current']}
            try:
                durable(state, p['nonce'] + '.intent.json', intent)
            except FileExistsError:
                raise Refused('NONCE_CONSUMED') from None
            # A close/fsync/clock failure keeps the nonce consumed, zero mutation.
            guard()
            verify(envelope, key, binding['candidate'], binding['action'], int(guard()))
            outcome = owner.apply(binding, guard)
            guard()
            need(outcome in ('APPLIED', 'FAILED_KNOWN', 'PREFLIGHT_REFUSED'), 'MUTATION_UNKNOWN')
            result = 'REFUSED_PREFLIGHT' if outcome == 'PREFLIGHT_REFUSED' else 'FAIL_KNOWN'
            if outcome == 'APPLIED':
                accepted = owner.accept(binding, guard)
                guard()
                if p['action'] == 'test':
                    test_receipt(accepted, p['candidate'])
                    need(original_i <= accepted['started_at'] <= accepted['finished_at'] <= guard(), 'TEST_ORIGINAL_WINDOW')
                    need(accepted['project'] == binding['project'] and accepted['scope'] == binding['fixture']['scope'], 'TEST_PROJECT')
                    result = 'PASS_TEST'
                else:
                    # Owner must return a closed enum only after fresh read-only
                    # health/static/guest/config/same-volume/schema checks.
                    need(accepted in ('PASS', 'FAIL_KNOWN'), 'ACCEPTANCE_UNKNOWN')
                    result = 'PASS_PRODUCTION' if accepted == 'PASS' else 'FAIL_KNOWN'
            # Only explicit known failure plus fresh poststate/ownership CAS in
            # installed rollback may restore old code. No exception fallback.
            if result == 'FAIL_KNOWN' and p['action'] == 'production':
                guard()
                need(owner.rollback(binding, guard) == 'ROLLED_BACK', 'ROLLBACK_UNKNOWN')
                guard()
                result = 'ROLLED_BACK'
            output = {'schema': 'authova-owner-execution/v1', 'action': p['action'],
                      'candidate_digest': candidate_digest(p['candidate']),
                      'build_pipeline': p['build_pipeline'], 'test_pipeline': p['test_pipeline'],
                      'production_pipeline': p['production_pipeline'], 'result': result,
                      'started_at': original_i, 'finished_at': guard()}
            if p['action'] == 'test' and result == 'PASS_TEST':
                durable(state, p['nonce'] + '.test-receipt.json', accepted)
                guard()
            # First exception leaves the intent without a terminal receipt: unknown,
            # still consumed, never synthesized as success or automatically replayed.
            durable(state, p['nonce'] + '.result.json', output)
            guard()
            return output


def execute_files(private_root, admission_name, ledger_root, state_root, owner, *,
                  uid=None, wall=time.time, mono=time.monotonic, _origin=None):
    """Host-only fixed private binding/key, never supplied by a command step.

    root has immutable binding.json/key and (production only) test-receipt.json.
    Input is RAM-only; key is never hashed or serialized. The original operation
    I/M precede all file reads, and execute retains those exact origins.
    """
    origin = (wall(), mono()) if _origin is None else _origin
    uid = os.getuid() if uid is None else uid
    with directory(private_root, uid) as root:
        binding = strict_json(read_private(root, 'binding.json', uid))
        envelope = strict_json(read_private(root, admission_name, uid))
        key = read_private(root, 'key', uid, limit=4096)
        receipt = (strict_json(read_private(root, 'test-receipt.json', uid, limit=262144))
                   if binding['action'] == 'production' else None)
    return execute(binding, envelope, key, ledger_root, state_root, owner, receipt=receipt,
                   uid=uid, wall=wall, mono=mono, _origin=origin)
