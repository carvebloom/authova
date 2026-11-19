"""Real Python generation entries with synthetic owned tools; no Go/npm/DB IO."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
PINS = {
    'protoc-gen-go': ('google.golang.org/protobuf/cmd/protoc-gen-go', 'v1.36.11'),
    'protoc-gen-go-http': ('github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3', 'v3.0.0-20261026152050-dd0fb7df1e32'),
    'oapi-codegen': ('github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen', 'v2.8.0'),
    'govulncheck': ('golang.org/x/vuln/cmd/govulncheck', 'v1.9.0'),
    'staticcheck': ('honnef.co/go/tools/cmd/staticcheck', 'v0.8.2'),
}


class GenerationActor:
    def __init__(self, root):
        self.root, self.calls, self.checked = root.resolve(), [], []
        for name in ('scripts/generate-api.py', 'scripts/generate-ent.py', 'scripts/verify-contract-combination.py',
                     'scripts/test_profiles.py', 'contracts/scripts/check-generated.py',
                     'contracts/scripts/generate-bindings.py', 'console/scripts/generate-contracts.py'):
            self.put(name, (ROOT / name).read_bytes())
        self.put('api/authova/v1/management.pb.go', b'OWN_PROTO\n')
        self.put('contracts/sdk/client.go', b'package sdk\n')
        self.put('internal/ent/own.go', b'OWN_ENT\n')
        cases = ('TestPinnedContractCombination', 'TestPinnedContractCombinationQueryCLIAndMCP', 'TestPinnedContractCombinationAgentResourceSDK')
        self.put('internal/integration/own_test.go', '\n'.join('func '+n+'(t *testing.T) {}' for n in cases).encode())
        for name in ('management', 'query'):
            self.put('contracts/openapi/v1/'+name+'.yaml', b'OWN_SPEC\n')
            self.put('contracts/generated/'+name+'/client.gen.go', b'OWN_CLIENT\n')
            self.put('contracts/generated/typescript/'+name+'.ts', b'OWN_TYPES\n')
            self.put('contracts/'+('codegen.yaml' if name == 'management' else 'codegen-query.yaml'), ('output: generated/'+name+'/client.gen.go\n').encode())
        self.put('console/contract-inputs/management.yaml', b'OWN_SPEC\n')
        self.put('console/contract-inputs/sdk-client.go.txt', b'package sdk\n')
        manifest = {'format': 1, 'source': 'github.com/carvebloom/authova/contracts', 'api_version': '0.1.0-rc.1',
                    'selection': 'generated from api/authova/v1 Proto authority; content hashes pin consumer inputs',
                    'generators': {'go_sdk': 'package rename v1', 'typescript': 'openapi-typescript@7.10.1'},
                    'sha256': {'management.yaml': hashlib.sha256(b'OWN_SPEC\n').hexdigest(), 'sdk-client.go.txt': hashlib.sha256(b'package sdk\n').hexdigest()}}
        self.put('console/contract-inputs/manifest.json', (json.dumps(manifest, indent=2)+'\n').encode())
        self.put('console/internal/coreapi/client.go', b'package coreapi\n')
        self.put('console/web/src/api/generated/management.ts', b'OWN_TYPES\n')

    def put(self, name, body):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(body)
        return path

    def install(self, directory, name, version=None):
        module, pinned = PINS[name]
        path = directory / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({'module': module.removesuffix('/cmd/'+name), 'version': version or pinned}))
        path.chmod(0o700)

    def prepare(self):
        sys.path.insert(0, str(ROOT/'scripts/ci'))
        spec = importlib.util.spec_from_file_location('own_prepare', ROOT/'scripts/ci/prepare_tools.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        assert module.GO_TOOLS == tuple(PINS.values())
        out = self.root.parent/'prepare'
        out.mkdir()
        with patch.object(module.driver, 'ROOT', self.root):
            module.prepare('a'*40, out, self.run)
        return json.loads((out/'prepare.json').read_bytes())

    def output(self, argv, **kw):
        self.calls.append((argv, kw))
        if argv == ['protoc', '--version']:
            return 'libprotoc 36.2\n'
        assert argv[1:3] == ['version', '-m']
        path = Path(argv[3])
        meta = json.loads(path.read_bytes())  # Actual prepared OWN path consumption.
        self.checked.append(path)
        return '\tmod\t'+meta['module']+'\t'+meta['version']+'\tOWN\n'

    def run(self, argv, **kw):
        argv = [str(a) for a in argv]
        self.calls.append((argv, kw))
        if argv[:2] == ['go', 'mod']:
            assert argv == ['go', 'mod', 'download']
        elif argv[:2] == ['go', 'install'] or (len(argv) > 1 and argv[1] == 'install'):
            module, version = argv[2].rsplit('@', 1)
            name = next(n for n, pin in PINS.items() if pin[0] == module)
            assert version == PINS[name][1]
            self.install(Path(kw['env']['GOBIN']), name)
        elif argv[0] == 'protoc':
            for name in ('protoc-gen-go', 'protoc-gen-go-http'):
                target = Path(next(a.split('=', 2)[2] for a in argv if a.startswith('--plugin='+name+'=')))
                assert target in self.checked and target.is_file()
            dest = Path(next(a.split('=', 1)[1] for a in argv if a.startswith('--go_out=')))
            path = dest/'api/authova/v1/management.pb.go'
            path.parent.mkdir(parents=True)
            path.write_bytes(b'OWN_PROTO\n')
        elif len(argv) > 2 and argv[1:3] == ['run', './cmd/proto-openapi']:
            dest = Path(argv[argv.index('--output')+1])
            dest.mkdir(parents=True)
            for name in ('management', 'query'):
                (dest/(name+'.yaml')).write_bytes(b'OWN_SPEC\n')
        elif len(argv) > 2 and argv[1:3] == ['run', './cmd/entgen']:
            Path(argv[-1], 'own.go').write_bytes(b'OWN_ENT\n')
        elif Path(argv[0]).name == 'oapi-codegen' or (len(argv) > 2 and argv[1:3] == ['run', PINS['oapi-codegen'][0]+'@v2.8.0']):
            if Path(argv[0]).name == 'oapi-codegen':
                assert Path(argv[0]) in self.checked
            else:
                assert kw['env']['GOPROXY'] == 'off'  # Cached standalone fallback only.
            config = Path(argv[argv.index('-config')+1]).read_text()
            Path(config.strip().removeprefix('output: ')).write_bytes(b'OWN_CLIENT\n')
        elif argv[:2] == ['npm', 'exec']:
            assert '--package=openapi-typescript@7.10.1' in argv
            if '-o' in argv:
                Path(argv[argv.index('-o')+1]).write_bytes(b'OWN_TYPES\n')
        elif argv[0] == 'python3':
            child = argv[1:]
            while child[0] in ('-B', '-I'):child = child[1:]
            with patch.object(sys, 'argv', child), patch.dict(os.environ, kw.get('env', os.environ.copy()), clear=True):
                runpy.run_path(child[0], run_name='__main__')
        elif argv[:2] == ['go', 'test']:
            package = 'github.com/carvebloom/authova/internal/integration'
            for name in ('TestPinnedContractCombination', 'TestPinnedContractCombinationQueryCLIAndMCP', 'TestPinnedContractCombinationAgentResourceSDK'):
                for test in (name, name+'/mysql'):
                    kw['stdout'].write(json.dumps({'Package': package, 'Action': 'pass', 'Test': test})+'\n')
            kw['stdout'].write(json.dumps({'Package': package, 'Action': 'pass'})+'\n')
        else:
            raise AssertionError('UNEXPECTED_OWN_COMMAND')
        return subprocess.CompletedProcess(argv, 0)

    def entry(self, name, *args):
        with patch.object(sys, 'argv', [str(self.root/name), *args]), patch.object(subprocess, 'run', self.run), \
             patch.object(subprocess, 'check_output', self.output), contextlib.redirect_stdout(io.StringIO()):
            runpy.run_path(str(self.root/name), run_name='__main__')


class GeneratorToolTests(unittest.TestCase):
    def test_actual_prepare_to_contract_entry_consumes_bin_and_stays_cache_only(self):
        with tempfile.TemporaryDirectory() as tmp, patch.dict(os.environ, {'PATH': '/OWN', 'AUTHOVA_TEST_DATABASE_DIALECT': 'mysql', 'AUTHOVA_TEST_MYSQL_DSN': 'OWN_CANARY'}, clear=True):
            actor = GenerationActor(Path(tmp)/'source')
            self.assertEqual(actor.prepare()['result'], 'PASS')
            actor.entry('scripts/verify-contract-combination.py')
            actor.entry('scripts/generate-ent.py', '--check')
            self.assertEqual(set(actor.checked), {actor.root/'.tools/bin'/n for n in tuple(PINS)[:3]})
            for tool in ('govulncheck', 'staticcheck'):
                self.assertEqual(shutil.which(tool, path=str(actor.root/'.tools/bin')), str(actor.root/'.tools/bin'/tool))
            for argv, kw in actor.calls:
                if argv[0] == 'npm' and '-o' in argv:
                    self.assertIn('--offline', argv)
                if argv[0].endswith('/go.sh') and len(argv) > 1 and argv[1] != 'install':
                    self.assertEqual(kw['env']['GOPROXY'], 'off')

    def test_standalone_install_and_cached_legacy_layout_remain_supported(self):
        for install in (False, True):
            with self.subTest(install=install), tempfile.TemporaryDirectory() as tmp, patch.dict(os.environ, {'PATH': '/OWN'}, clear=True):
                actor = GenerationActor(Path(tmp)/'source')
                if not install:
                    for name in tuple(PINS)[:2]:actor.install(actor.root/'.tools', name)
                actor.entry('scripts/generate-api.py', *(['--install-tools'] if install else []), '--check')
                self.assertTrue(all(p.parent == actor.root/'.tools' for p in actor.checked))
                if install:
                    self.assertEqual(len([a for a, _ in actor.calls if len(a) > 1 and a[1] == 'install']), 3)
                else:
                    self.assertFalse(any(len(a) > 1 and a[1] == 'install' for a, _ in actor.calls))

    def test_partial_or_wrong_prepared_tool_refuses_before_generation_without_legacy_fallback(self):
        for mode in ('partial', 'wrong-version'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp, patch.dict(os.environ, {'PATH': '/OWN'}, clear=True):
                actor = GenerationActor(Path(tmp)/'source')
                for name in tuple(PINS)[:3]:actor.install(actor.root/'.tools', name)
                for name in (tuple(PINS)[:1] if mode == 'partial' else tuple(PINS)[:3]):
                    actor.install(actor.root/'.tools/bin', name, 'v0.0.0' if mode == 'wrong-version' else None)
                with self.assertRaises(SystemExit):actor.entry('scripts/generate-api.py', '--check')
                self.assertFalse(any(a[0] == 'protoc' and len(a) > 2 or len(a) > 1 and a[1] == 'install' for a, _ in actor.calls))

    def test_wrapper_preserves_explicit_proxy_without_reading_host_sdk_or_cache(self):
        with tempfile.TemporaryDirectory() as tmp:
            own = Path(tmp);cache=own/'cache';cache.mkdir();binary=own/'bin';binary.mkdir()
            wrapper = (ROOT/'scripts/migration/go.sh').read_text().replace('/Users/chenhuanxiang/go/pkg/mod/cache/download',str(cache)).replace('/Users/chenhuanxiang/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.4.darwin-arm64/bin',str(own/'absent-sdk'))
            script=own/'go.sh';script.write_text(wrapper)
            fake=binary/'go';fake.write_text('#!'+sys.executable+'\nimport os,sys\nsys.stdout.write(os.environ.get("GOPROXY","UNSET"))\n');fake.chmod(0o700)
            for value in (None, '', 'off', 'https://owned.invalid'):
                env={'PATH':str(binary)}
                if value is not None:env['GOPROXY']=value
                result=subprocess.run(['/bin/sh',str(script),'version'],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,check=True)
                self.assertEqual(result.stdout,value if value is not None else 'file://'+str(cache)+',https://proxy.golang.org,direct')


if __name__ == '__main__':
    unittest.main()
