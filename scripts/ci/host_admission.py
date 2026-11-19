"""Independent Authova HOST-PROTOCOL v1 verifier (no signing or API authority)."""
import hashlib
import hmac
import json
import re

from release_contract import candidate, need

PAYLOAD_KEYS = {
    'schema', 'action', 'candidate', 'candidate_digest', 'issued', 'expires',
    'nonce', 'build_pipeline', 'test_pipeline', 'production_pipeline',
    'reviewed_by', 'reviewed',
}


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False).encode('utf-8')


def candidate_digest(value):
    candidate(value)
    return 'sha256:' + hashlib.sha256(canonical(value)).hexdigest()


def strict_json(body):
    def pairs(items):
        value = {}
        for name, item in items:
            need(name not in value, 'JSON_DUPLICATE')
            value[name] = item
        return value

    def constant(_):
        need(False, 'JSON_NONFINITE')

    need(type(body) is bytes, 'JSON_BYTES')
    # Explicit UTF-8 prevents json.loads from silently accepting UTF-16/32.
    return json.loads(body.decode('utf-8'), object_pairs_hook=pairs, parse_constant=constant)


def verify(envelope, key, expected_candidate, action, now):
    candidate(expected_candidate)
    need(type(key) is bytes and len(key) >= 32, 'SIGNING_KEY')
    need(type(now) is int and now > 0, 'ADMISSION_CLOCK')
    need(type(envelope) is dict and set(envelope) == {'payload', 'signature'}, 'ENVELOPE_KEYS')
    p = envelope['payload']
    need(type(p) is dict and set(p) == PAYLOAD_KEYS, 'PAYLOAD_KEYS')
    sig = envelope['signature']
    need(type(sig) is str and re.fullmatch('hmac-sha256:[0-9a-f]{64}', sig), 'SIGNATURE_FORMAT')
    actual = 'hmac-sha256:' + hmac.new(key, canonical(p), hashlib.sha256).hexdigest()
    need(hmac.compare_digest(sig, actual), 'SIGNATURE')
    candidate(p['candidate'])
    need(action in ('test', 'production') and p['schema'] == 'authova-host-admission/v1'
         and p['action'] == action and p['candidate'] == expected_candidate
         and p['candidate_digest'] == candidate_digest(expected_candidate), 'ADMISSION_BINDING')
    need(type(p['issued']) is int and type(p['expires']) is int
         and 0 < p['issued'] <= now < p['expires']
         and 0 < p['expires'] - p['issued'] <= 300, 'ADMISSION_EXPIRED')
    need(type(p['nonce']) is str and re.fullmatch('[0-9a-f]{64}', p['nonce']), 'NONCE')
    need(type(p['build_pipeline']) is int and p['build_pipeline'] > 0, 'BUILD_PIPELINE')
    if action == 'production':
        need(type(p['test_pipeline']) is int and p['test_pipeline'] > 0
             and type(p['production_pipeline']) is int and p['production_pipeline'] > 0
             and p['reviewed_by'] == 'rwasayc' and type(p['reviewed']) is int
             and 0 < p['reviewed'] <= p['issued'], 'PRODUCTION_REVIEW')
    else:
        need(all(p[k] is None for k in ('test_pipeline', 'production_pipeline', 'reviewed_by', 'reviewed')),
             'TEST_REVIEW_CONFUSION')
    return p
