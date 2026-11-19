"""Owned synthetic OCI graphs; no daemon/load/application or external artifact IO."""
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'ci'))
import compose_owner as owner
from host_adapter import _token
from release_contract import Refused

INDEX='application/vnd.oci.image.index.v1+json'
MANIFEST='application/vnd.oci.image.manifest.v1+json'
CONFIG='application/vnd.oci.image.config.v1+json'
LAYER='application/vnd.oci.image.layer.v1.tar+gzip'

def raw(v):return json.dumps(v,sort_keys=True,separators=(',',':')).encode()
def guard():pass

def fixture(poison=None):
    files={};rows=[];roots=[]
    def blob(body,media,**extra):
        digest='sha256:'+hashlib.sha256(body).hexdigest()
        files['blobs/sha256/'+digest[7:]]=body
        return dict(mediaType=media,digest=digest,size=len(body),**extra)
    # Shared blob deliberately exercises descriptor-size checks even after hashing.
    common=blob(b'OWN_COMPRESSED_LAYER',LAYER)
    for role in ('core','console'):
        cfg=blob(raw(dict(os='linux',architecture='arm64',role=role,
             rootfs=dict(type='layers',diff_ids=['sha256:'+'1'*64]))),CONFIG)
        if poison=='config-platform' and role=='core':
            cfg=blob(raw(dict(os='linux',architecture='amd64',rootfs=dict(type='layers',diff_ids=['sha256:'+'1'*64]))),CONFIG)
        layer=copy.deepcopy(common)
        if poison=='shared-size' and role=='console':layer['size']+=1
        runtime=blob(raw(dict(schemaVersion=2,mediaType=MANIFEST,config=cfg,layers=[layer])),MANIFEST,
                     platform=dict(os='linux',architecture='arm64'))
        attestcfg=blob(raw(dict(os='unknown',architecture='unknown')),CONFIG)
        evidence=blob(b'OWN_ATTESTATION_NOT_TRUST_PROOF','application/vnd.in-toto+json',
                      annotations={'in-toto.io/predicate-type':'https://slsa.dev/provenance/v1'})
        attest=blob(raw(dict(schemaVersion=2,mediaType=MANIFEST,config=attestcfg,layers=[evidence])),MANIFEST,
                    platform=dict(os='unknown',architecture='unknown'),annotations={
                        'vnd.docker.reference.type':'attestation-manifest',
                        'vnd.docker.reference.digest':runtime['digest'] if poison!='attestation-link' else 'sha256:'+'0'*64})
        if poison=='platform':runtime['platform']['architecture']='amd64'
        if poison=='boolean-size':runtime['size']=True
        root=blob(raw(dict(schemaVersion=2,mediaType=INDEX,manifests=[runtime,attest])),INDEX)
        roots.append(root)
        rows.append(dict(Config='blobs/sha256/'+cfg['digest'][7:],RepoTags=None,Layers=['blobs/sha256/'+layer['digest'][7:]]))
    if poison=='docker-cross-link':rows[0]['Layers']=[]
    files.update({'oci-layout':raw({'imageLayoutVersion':'1.0.0'}),
                  'index.json':raw(dict(schemaVersion=2,mediaType=INDEX,manifests=roots)),
                  'manifest.json':raw(rows)})
    if poison=='layer-sha':files['blobs/sha256/'+common['digest'][7:]]=b'X'*len(b'OWN_COMPRESSED_LAYER')
    if poison=='missing':del files['blobs/sha256/'+common['digest'][7:]]
    if poison=='unreachable':blob(b'UNREACHABLE',LAYER)
    if poison=='traversal':files['../outside']=b'NO'
    if poison=='absolute':files['/outside']=b'NO'
    candidate=dict(coreID=roots[0]['digest'],consoleID=roots[1]['digest'])
    if poison=='wrong-candidate':candidate['coreID']='sha256:'+'0'*64
    return files,candidate


def archive(files,duplicate=False,link=False):
    stream=io.BytesIO()
    with tarfile.open(fileobj=stream,mode='w') as t:
        for name,body in files.items():
            item=tarfile.TarInfo(name);item.size=len(body);t.addfile(item,io.BytesIO(body))
        if duplicate:
            b=files['index.json'];item=tarfile.TarInfo('index.json');item.size=len(b);t.addfile(item,io.BytesIO(b))
        if link:
            item=tarfile.TarInfo('link');item.type=tarfile.SYMTYPE;item.linkname='/outside';t.addfile(item)
    return stream.getvalue()


class ArchiveTests(unittest.TestCase):
    def consume(self,body,candidate,check=guard):
        with tarfile.open(fileobj=io.BytesIO(body),mode='r:') as tar:owner.archive_graph(tar,candidate,check)
    def test_valid_distinct_index_config_ids_shared_layer_and_attestations(self):
        files,candidate=fixture();self.assertNotEqual(candidate['coreID'][7:],json.loads(files['manifest.json'])[0]['Config'].split('/')[-1])
        self.consume(archive(files),candidate)
    def test_poisoned_closed_graph_refuses_even_with_rebound_index_ids(self):
        for bad in ('config-platform','shared-size','attestation-link','platform','boolean-size','docker-cross-link',
                    'layer-sha','missing','unreachable','traversal','absolute','wrong-candidate'):
            with self.subTest(bad=bad):
                files,candidate=fixture(bad)
                with self.assertRaises(Refused):self.consume(archive(files),candidate)
    def test_duplicate_link_and_late_refuse(self):
        files,candidate=fixture()
        for kw in ({'duplicate':True},{'link':True}):
            with self.subTest(kw=kw),self.assertRaises(Refused):self.consume(archive(files,**kw),candidate)
        def late():raise Refused('OWN_LATE')
        with self.assertRaisesRegex(Refused,'OWN_LATE'):self.consume(archive(files),candidate,late)
    def test_read_error_remains_first_when_member_close_also_fails(self):
        files,candidate=fixture()
        class BadMember:
            def read(self,*args):raise Refused('OWN_FIRST')
            def close(self):raise OSError('PRIVATE_CLOSE_CANARY')
        with patch.object(tarfile.TarFile,'extractfile',return_value=BadMember()):
            with self.assertRaisesRegex(Refused,'OWN_FIRST') as caught:self.consume(archive(files),candidate)
        self.assertEqual(caught.exception.close_secondary,('ARCHIVE_MEMBER_CLOSE',))

    def test_actual_archive_guard_preserves_outer_sha_identity_and_original_failure(self):
        files,candidate=fixture();body=archive(files)
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp)/'images.tar';p.write_bytes(body)
            obj=owner.CoreConsoleOwner.__new__(owner.CoreConsoleOwner)
            obj.profile={'archive':{'path':str(p),'identity':list(_token(p.stat()))}}
            candidate['archive']=hashlib.sha256(body).hexdigest()
            obj.archive_guard(candidate,guard)
            with self.assertRaisesRegex(Refused,'ARCHIVE_SHA'):obj.archive_guard(dict(candidate,archive='0'*64),guard)
            obj.profile['archive']['identity'][1]+=1
            with self.assertRaisesRegex(Refused,'ARCHIVE_METADATA'):obj.archive_guard(candidate,guard)

if __name__=='__main__':unittest.main()
