"""Pure candidate/test receipt contract; provenance is verified by host, not bools."""
import math,re
CANDIDATE_KEYS={'schema','repo','source','coreID','consoleID','manifest','archive','schemaDigest'}
REQUIRED_CHECKS=(
 'artifact-identity',
 'fresh-fixture-schema-and-privileges',
 'runtime-configuration-and-hardening',
 'normal-tls-health-and-static-assets',
 'guest-and-authorization-boundaries',
 'management-password-mfa-logout',
 'ordinary-app-oidc-pkce-lifecycle',
 'operator-password-and-session-revocation',
 'same-storage-rollback-and-repromotion',
 'owned-test-io-and-session-closure',
)
class Refused(Exception):pass
def need(ok,code):
 if not ok:raise Refused(code)
def hexstring(v,n):return type(v) is str and re.fullmatch('[0-9a-f]{%d}'%n,v) is not None
def finite(v):return type(v) in (int,float) and math.isfinite(v)
def candidate(v):
 need(type(v) is dict and set(v)==CANDIDATE_KEYS,'CANDIDATE_KEYS')
 need(v['schema']=='authova-image-candidate/v1' and v['repo']=='carvebloom/authova','CANDIDATE_SCHEMA')
 need(hexstring(v['source'],40),'SOURCE')
 for field in ('coreID','consoleID'):need(type(v[field]) is str and re.fullmatch('sha256:[0-9a-f]{64}',v[field]) is not None,'IMAGE')
 for field in ('manifest','archive','schemaDigest'):need(hexstring(v[field],64),'DIGEST')
 return v
def test_receipt(v,expected):
 candidate(expected)
 need(type(v) is dict and set(v)=={'schema','candidate','result','project','scope','checks','check_evidence','started_at','finished_at'},'TEST_RECEIPT_KEYS')
 need(v['schema']=='authova-test-deployment/v1' and v['candidate']==expected and v['result']=='PASS','TEST_BINDING')
 need(type(v['project']) is str and re.fullmatch('authova-ci-test-'+expected['source'][:12]+'-[0-9]+',v['project']) is not None,'TEST_PROJECT')
 need(hexstring(v['scope'],32),'TEST_SCOPE')
 need(finite(v['started_at']) and finite(v['finished_at']) and 0<v['started_at']<=v['finished_at'],'TEST_CLOCK')
 need(type(v['checks']) is dict and set(v['checks'])==set(REQUIRED_CHECKS),'TEST_REQUIRED_CHECKS')
 need(all(v['checks'][name]=='PASS' for name in REQUIRED_CHECKS),'TEST_CHECK_OUTCOME')
 need(type(v['check_evidence']) is dict and set(v['check_evidence'])==set(REQUIRED_CHECKS),'TEST_EVIDENCE_KEYS')
 for name in REQUIRED_CHECKS:
  check=v['check_evidence'][name]
  need(type(check) is dict and set(check)=={'result','exit_code','known_return','timely','started_at','finished_at','evidence_sha256'},'TEST_CHECK_KEYS')
  need(check['result']=='PASS' and type(check['exit_code']) is int and check['exit_code']==0 and check['known_return'] is True and check['timely'] is True,'TEST_CHECK_OUTCOME')
  need(finite(check['started_at']) and finite(check['finished_at']) and v['started_at']<=check['started_at']<=check['finished_at']<=v['finished_at'],'TEST_CHECK_CLOCK')
  need(hexstring(check['evidence_sha256'],64),'TEST_EVIDENCE')
 # No signature/authorization/actual execution claim: host must bind original
 # controlled evidence + this receipt digest to the authenticated WP test record.
 return v
