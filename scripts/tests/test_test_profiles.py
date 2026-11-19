"""Prevent false green results from native failure, missing cases or fixture Skip."""
import importlib.util
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("test_profiles", Path(__file__).resolve().parents[1] / "test_profiles.py")
profiles = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(profiles)


def event(package, action, name=None):
    value = {"Package": profiles.MODULE + package, "Action": action, "FutureField": "ignored"}
    if name:
        value["Test"] = name
    return value


class RequiredResults(unittest.TestCase):
    def test_safe_summary_uses_only_registered_top_level_events_and_keeps_gate_failure(self):
        selected = {"internal/domain": {"TestConstraint"}}
        inputs = [event("internal/domain", "run", "TestConstraint"),
                  event("internal/domain", "fail", "TestConstraint/PRIVATE_CANARY"),
                  event("internal/domain", "fail", "TestConstraint"),
                  event("internal/domain", "fail", "TestCallerForged"),
                  event("internal/domain", "fail")]
        observed = profiles.SafeObservation(selected)
        for value in inputs:
            value["Output"] = "PRIVATE_MESSAGE_CANARY"
            observed.observe(value)
        observed.complete = True
        result = observed.summary("database", "mysql", [1], "native Go failure; inspect private Go evidence")
        self.assertEqual(result["verification_failure"], "NATIVE_OR_TEST_FAILURE")
        self.assertEqual(result["native_exits"], [1])
        self.assertEqual(result["tests"], {"started": ["TestConstraint"], "passed": [], "failed": ["TestConstraint"], "skipped": []})
        self.assertEqual(result["counts"]["failed_top_level"], 1)
        self.assertEqual(result["counts"]["fail_events"], 4)
        self.assertNotIn("CANARY", json.dumps(result))
        self.assertNotIn("TestCallerForged", json.dumps(result))
        self.assertTrue(profiles.valid_safe_summary(result, selected, "database", "mysql"))
        for change in ({"native_exits": [True]}, {"verification_failure": "PRIVATE_CANARY"},
                       {"dialect": "both"}, {"path": "/PRIVATE_CANARY"},
                       {"tests": {**result["tests"], "failed": ["TestConstraint/PRIVATE_CANARY"]}},
                       {"counts": {**result["counts"], "fail_events": True}}):
            self.assertFalse(profiles.valid_safe_summary(result | change, selected, "database", "mysql"))
        observed.complete = False
        self.assertEqual(observed.summary("database", "mysql", [1], "unknown")["evidence_state"], "PARTIAL")
        observed.events = 100000
        observed.observe(inputs[0])
        unavailable = observed.summary("database", "mysql", [1], "unknown")
        self.assertEqual(unavailable["evidence_state"], "UNAVAILABLE")
        self.assertIsNone(unavailable["tests"])
        self.assertTrue(profiles.valid_safe_summary(unavailable, selected, "database", "mysql"))

    def test_run_projects_original_go_json_without_changing_native_or_skip_verification(self):
        selected = {"internal/domain": {"TestConstraint"}}
        for mode in ("pass", "native", "skip", "compile", "invalid"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                def fake(argv, **kwargs):
                    rows = [event("internal/domain", "run", "TestConstraint")]
                    if mode != "compile":
                        rows += [event("internal/domain", "skip" if mode == "skip" else "pass", "TestConstraint"),
                                 event("internal/domain", "pass")]
                    for row in rows:
                        row["Output"] = "PRIVATE_CANARY"
                        kwargs["stdout"].write(json.dumps(row) + "\n")
                    if mode == "invalid":
                        kwargs["stdout"].write("INVALID_PRIVATE_CANARY\n")
                    return type("Return", (), {"returncode": 1 if mode in ("native", "compile") else 0})()
                output = io.StringIO()
                with patch.object(profiles, "ROOT", Path(tmp)), patch.object(profiles, "selection", return_value=(selected, [])), \
                     patch.object(profiles, "source_tests", return_value={}), patch.object(profiles.subprocess, "run", fake), \
                     contextlib.redirect_stdout(output):
                    if mode == "pass":
                        profiles.run("component", "mysql", True)
                    else:
                        with self.assertRaisesRegex(ValueError, "Go gate failed"):
                            profiles.run("component", "mysql", True)
                lines = output.getvalue().splitlines()
                original = json.loads(lines[0])
                self.assertIn("evidence", original)  # Original private record is unchanged.
                frames = [line[len(profiles.SAFE_PREFIX):] for line in lines if line.startswith(profiles.SAFE_PREFIX)]
                self.assertEqual(len(frames), 1)
                safe = json.loads(frames[0])
                self.assertTrue(profiles.valid_safe_summary(safe, selected, "component", "mysql"))
                self.assertEqual(safe["native_exits"], original["native_exits"])
                self.assertNotIn("CANARY", frames[0])
                self.assertNotIn("evidence", safe)
                self.assertEqual(safe["verification"], "PASS" if mode == "pass" else "FAIL")
                if mode in ("native", "compile"):
                    self.assertEqual(safe["tests"]["failed"], [])  # Native failure is not a Test FAIL.
                if mode == "skip":
                    self.assertEqual(safe["verification_failure"], "UNEXPECTED_SKIP")
                    self.assertEqual(safe["evidence_state"], "PARTIAL")
                if mode == "invalid":
                    self.assertEqual(safe["verification_failure"], "INVALID_GO_JSON")
                    self.assertEqual(safe["evidence_state"], "PARTIAL")

    def test_serial_ci_resources_do_not_change_required_selection_or_race(self):
        inventory=profiles.source_tests()
        calls=[]
        def fake(argv,**kwargs):
            calls.append((argv,kwargs['env']))
            return type('Return',(),{'returncode':1})()
        for mode in (None,'serial768'):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as tmp:
                env={'PATH':'/usr/bin:/bin','GOMAXPROCS':'9','GOMEMLIMIT':'9GiB','GOFLAGS':'-p=2'}
                if mode is not None:env.update(AUTHOVA_CI_RESOURCE_PROFILE=mode,GOFLAGS='-p=9 -run=caller-forged-selection')
                with patch.dict(os.environ,env,clear=True),patch.object(profiles,'ROOT',Path(tmp)),patch.object(profiles,'source_tests',return_value=inventory),patch.object(profiles.subprocess,'run',fake),contextlib.redirect_stdout(io.StringIO()):
                    with self.assertRaisesRegex(ValueError,'Go gate failed'):
                        profiles.run('component','mysql',True)
        default,serial=calls
        self.assertEqual([v for v in default[0] if not v.startswith(('-p=','-parallel='))],
                         [v for v in serial[0] if not v.startswith(('-p=','-parallel='))])
        self.assertIn('-race',serial[0]);self.assertIn('-timeout=10m',serial[0])
        self.assertIn('-p=2',default[0]);self.assertEqual(default[1]['GOMAXPROCS'],'2')
        self.assertIn('-p=1',serial[0]);self.assertIn('-parallel=1',serial[0])
        self.assertEqual({k:serial[1][k] for k in ('GOMAXPROCS','GOFLAGS','GOMEMLIMIT','AUTHOVA_CI_RESOURCE_PROFILE')},
                         {'GOMAXPROCS':'1','GOFLAGS':'-p=1','GOMEMLIMIT':'256MiB','AUTHOVA_CI_RESOURCE_PROFILE':'serial768'})
        with patch.dict(os.environ,{'AUTHOVA_CI_RESOURCE_PROFILE':'serial768 -skip caller'},clear=True),patch.object(profiles.subprocess,'run',side_effect=AssertionError('NO_GO_ON_UNSUPPORTED_PROFILE')):
            with self.assertRaisesRegex(ValueError,'unsupported CI resource profile'):
                profiles.run('component','mysql',True)

    def test_native_and_selection_are_independent_requirements(self):
        selected = {"internal/domain": {"TestConstraint"}}
        passed = [event("internal/domain", "pass", "TestConstraint"), event("internal/domain", "pass")]
        self.assertEqual(profiles.check_events(passed, selected, "unit", "mysql", 0)["packages"], 1)
        for events, native in [(passed, 1), ([], 0), (passed[:1], 0), (passed[1:], 0),
                               (passed + [event("internal/domain", "fail", "TestOther")], 0)]:
            with self.subTest(events=len(events), native=native), self.assertRaises(ValueError):
                profiles.check_events(events, selected, "unit", "mysql", native)
        result = unittest.TestResult()
        with self.assertRaises(ValueError):
            profiles.check_python_result(result)
        result.testsRun = 1
        profiles.check_python_result(result)
        result.skipped.append((self, "required fixture missing"))
        with self.assertRaises(ValueError):
            profiles.check_python_result(result)

    def test_skip_is_not_an_assertion_even_when_parent_passes(self):
        selected = {"internal/domain": {"TestConstraint"}}
        for name in ("TestConstraint", "TestConstraint/required_leaf"):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "unexpected Skip"):
                profiles.check_events([event("internal/domain", "skip", name),
                                       event("internal/domain", "pass", "TestConstraint"),
                                       event("internal/domain", "pass")], selected, "unit", "mysql", 0)

    def test_database_selected_dialect_must_complete(self):
        names = {"TestEntPhysicalMapping", "TestDeliveryFailurePersistsAcrossReopen", "TestOutboxAppFairness",
                 "TestOutboxWorkerAppIsolation", "TestMaintenanceGates", "TestAtlasProposedUpgrade",
                 "TestKeyRotationRehearsal", "TestAtlasPlanningCLI"}
        selected = {"internal/integration": profiles.INTEGRATION_CASES | profiles.PASSWORD_STORAGE_CASES, "internal/storage/sqlstore": names}
        events = [event("internal/integration", "pass")]
        for name in profiles.INTEGRATION_CASES:
            events += [event("internal/integration", "pass", name), event("internal/integration", "pass", name + "/mysql")]
        for name in profiles.PASSWORD_STORAGE_CASES:
            events.append(event("internal/integration", "pass", name))
        for name in names - {"TestAtlasPlanningCLI"}:
            events += [event("internal/storage/sqlstore", "pass", name + "/mysql"),
                       event("internal/storage/sqlstore", "skip", name + "/postgres"),
                       event("internal/storage/sqlstore", "pass", name)]
        events += [event("internal/storage/sqlstore", "skip", "TestAtlasPlanningCLI"),
                   event("internal/storage/sqlstore", "pass")]
        self.assertGreater(profiles.check_events(events, selected, "database", "mysql", 0)["passed_events"], 0)
        missing = [e for e in events if e.get("Test") != "TestEntPhysicalMapping/mysql"]
        with self.assertRaisesRegex(ValueError, "did not pass"):
            profiles.check_events(missing, selected, "database", "mysql", 0)
        with self.assertRaisesRegex(ValueError, "unexpected Skip"):
            profiles.check_events(events, selected, "database", "both", 0)
        for name in profiles.PASSWORD_STORAGE_CASES:
            with self.subTest(password_storage=name),self.assertRaisesRegex(ValueError,"unexpected Skip"):
                profiles.check_events(events+[event("internal/integration","skip",name)],selected,"database","mysql",0)
        inventory=profiles.source_tests()
        inventory['internal/integration']-=profiles.PASSWORD_STORAGE_CASES
        with self.assertRaisesRegex(ValueError,"password storage-case registration drift"):
            profiles.selection('database',inventory)

    def test_allowlist_cannot_hide_selected_or_nested_skips(self):
        for package, name, dialect in [
            ("internal/storage/sqlstore", "TestEntPhysicalMapping/mysql", "mysql"),
            ("internal/storage/sqlstore", "TestEntPhysicalMapping/postgres/unexpected", "mysql"),
            ("internal/integration", "TestDatabases", "mysql"),
            ("internal/storage/sqlstore", "TestEntPhysicalMapping/postgres", "both"),
        ]:
            with self.subTest(name=name, dialect=dialect):
                self.assertFalse(profiles.allowed_skip(package, name, "database", dialect))

    def test_tagged_consumers_cannot_pass_on_zero_or_one_missing_dialect(self):
        selected = {"internal/integration": profiles.CONTRACT_CASES}
        events = [event("internal/integration", "pass")]
        for name in profiles.CONTRACT_CASES:
            events += [event("internal/integration", "pass", name),
                       event("internal/integration", "pass", name + "/mysql")]
        profiles.check_events(events, selected, "contract", "mysql", 0)
        for bad in (events[:1], [e for e in events if e.get("Test") != "TestPinnedContractCombinationQueryCLIAndMCP/mysql"]):
            with self.subTest(count=len(bad)), self.assertRaises(ValueError):
                profiles.check_events(bad, selected, "contract", "mysql", 0)
        with self.assertRaises(ValueError):
            profiles.check_events(events, selected, "contract", "both", 0)

    def test_missing_and_unselected_configuration_fail_before_go(self):
        env = {"AUTHOVA_TEST_VALKEY_ADDRESS": "fixture", "AUTHOVA_MASTER_KEY_FILE": "fixture",
               "AUTHOVA_TEST_DISPOSABLE_DATABASE": "authova_quality_123456abcdef",
               "AUTHOVA_TEST_MYSQL_DSN": "fixture", "AUTHOVA_TEST_MIGRATOR_MYSQL_DSN": "fixture"}
        profiles.preflight("mysql", env)
        for bad in ({}, env | {"AUTHOVA_TEST_DISPOSABLE_DATABASE": "authova_demo"},
                    env | {"AUTHOVA_TEST_POSTGRES_DSN": "fixture"},
                    env | {"AUTHOVA_TEST_MYSQL_DSN": ""}):
            with self.subTest(keys=len(bad)), self.assertRaises(ValueError):
                profiles.preflight("mysql", bad)
        with self.assertRaises(ValueError):
            profiles.preflight("both", env)


if __name__ == "__main__":
    unittest.main()
