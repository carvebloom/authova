#!/usr/bin/env python3
"""Bounded host release plan consumed by trusted packager/release service.
No production execution or approval synthesis in an untrusted checkout/job.
"""
import argparse,hashlib,json,re
from pathlib import Path
from release_contract import Refused,need,candidate as validate_candidate,test_receipt as validate_test_receipt
def plan(candidate,control,test_receipt,review):
 validate_candidate(candidate)
 validate_test_receipt(test_receipt,candidate)
 need(review is not None and review.get('schema')=='authova-host-verified-production-review/v1' and review.get('candidate')==candidate and review.get('reviewed_by')=='rwasayc' and review.get('deployment')=='production' and type(review.get('reviewed_at')) is str and bool(review['reviewed_at']),'REVIEW_BINDING')
 # This object is only produced after the host service independently checks WP API.
 # CLI below only emits a plan: passing a JSON dict does NOT authorize execution.
 need(control['project']=='authova-local-production-20261114','PROJECT')
 need(control['schemaDigest']==candidate['schemaDigest'],'SCHEMA_CHANGE_REQUIRES_SEPARATE_PLAN')
 layers=[control[k] for k in ('retained_compose','retained_prior_override','active_override')]
 need(all(type(p) is str and Path(p).is_absolute() for p in layers),'LAYERS')
 argv=['compose','--project-name',control['project']]
 for layer in layers:argv+=['-f',layer]
 argv+=['-f','HOST_GENERATED_CANDIDATE_OVERRIDE','up','--detach','--no-deps','--no-build','--pull','never','--wait','--wait-timeout','90','core','console']
 return {'schema':'authova-host-release-plan/v1','candidate':candidate,'argv':argv,'allowed_env_delta':{'console':{'AUTHOVA_CONSOLE_ASSETS':'/opt/authova-console/web'}},'DB_migration':False,'unknown_state':'STOP_NO_REPLAY_NO_BLIND_ROLLBACK','rollback':'CAS matching known poststate; old image IDs plus ALL original layers; no data restore','execution_authorized_by_this_plan':False}
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--candidate',type=Path,required=True);p.add_argument('--control',type=Path,required=True);p.add_argument('--test-receipt',type=Path,required=True);p.add_argument('--host-verified-review',type=Path,required=True);a=p.parse_args()
 try:print(json.dumps(plan(*(json.loads(f.read_bytes()) for f in (a.candidate,a.control,a.test_receipt,a.host_verified_review))),sort_keys=True))
 except (Refused,KeyError,TypeError):raise SystemExit('REFUSED_RELEASE_PLAN')
if __name__=='__main__':main()
