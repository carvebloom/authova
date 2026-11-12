"""Prevent false green results from native failure, missing cases or fixture Skip."""
import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("test_profiles", Path(__file__).resolve().parents[1] / "test_profiles.py")
profiles = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(profiles)


def event(package, action, name=None):
    value = {"Package": profiles.MODULE + package, "Action": action, "FutureField": "ignored"}
    if name:
        value["Test"] = name
    return value


class RequiredResults(unittest.TestCase):
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
        selected = {"internal/integration": profiles.INTEGRATION_CASES, "internal/storage/sqlstore": names}
        events = [event("internal/integration", "pass")]
        for name in profiles.INTEGRATION_CASES:
            events += [event("internal/integration", "pass", name), event("internal/integration", "pass", name + "/mysql")]
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
