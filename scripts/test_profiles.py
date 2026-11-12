#!/usr/bin/env python3
"""Authova's test selection/result gate; Go --list performs source discovery only.

Owns no databases, listeners, containers or migrations. Raw Go output stays private.
The existing fixture runners remain responsible for provisioning and cleanup.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
MODULE = "github.com/carvebloom/authova/"
UNIT = frozenset({
    "internal/application", "internal/config", "internal/diagnostic", "internal/domain",
    "internal/federation", "internal/migrations", "internal/protojsoncompat",
    "internal/resource", "internal/schemaplan", "internal/securityevents",
    "console/internal/diagnostic",
})
DATABASE = frozenset({"internal/integration", "internal/storage/sqlstore"})
# These cases need an explicitly owned Valkey fixture, not the component profile.
EXTERNAL = {
    "console/cmd/authova-console": {
        "TestSessionDependencyAndCSRF", "TestPublicRefreshIsSerializedAndNeverReplayed",
        "TestRefreshCannotResurrectLogout", "TestConsoleValkeyTLSOutageRecovery",
        "TestBFFAdmissionDuringBlockedCore", "TestMeErrorsUseSameSafeProjectionAndHeaderCorrelation",
        "TestRejectedWriteCannotRehydrateOnPageReload", "TestDelayedRejectionPreservesNewCredentialGeneration",
    },
    "console/internal/valkeyconfig": {"TestValkeyTLSACL"},
    "internal/valkeyconfig": {"TestValkeyTLSACL"},
    "internal/transport": {"TestValkeyTLSOutageRecovery", "TestSharedAppRateBudgetAcrossReplicas"},
}
# Separate capacity/backup/restore/browser campaigns; never represented as DB PASS.
CAMPAIGNS = frozenset({
    "TestAgentCapacityHTTP", "TestAgentCapacityResume", "TestAgentCapacityFixture",
    "TestAgentRecoveredAuthority", "TestAgentRevokeAfterBackup", "TestAgentRestoredQuarantineCLI",
    "TestAgentDelegationSoak", "TestProtocolSoak", "TestCapacityRestore", "TestRestoredEncryptedFacts",
    "TestOTPBrowserFixture", "TestIdentityRecoverySnapshotFixture",
    "TestIdentityRecoverySnapshotMutation", "TestIdentityRecoverySnapshotRestored",
})
OTHER_DIALECT_CASES = frozenset({
    "TestAtlasPlanning", "TestAtlasProposedUpgrade", "TestEntPhysicalMapping",
    "TestMaintenanceGates", "TestOutboxAppFairness", "TestOutboxWorkerAppIsolation",
    "TestDeliveryFailurePersistsAcrossReopen", "TestKeyRotationRehearsal",
})
CONTRACT_CASES = frozenset({"TestPinnedContractCombination", "TestPinnedContractCombinationQueryCLIAndMCP",
                            "TestPinnedContractCombinationAgentResourceSDK"})
INTEGRATION_CASES = frozenset({
    "TestDatabases", "TestProductionTLS", "TestReadOnlyAgentQueries", "TestEntBoundTransactions",
    "TestAgentConcurrentRefreshReplay", "TestAgentRecoveryQuarantine", "TestCredentialWriteConcurrency",
    "TestIdentityRecoveryLifecycle",
})
PYTHON_UNIT_FILES = ("test_local_test.py", "test_test_profiles.py")


def source_tests(root=ROOT, contract=False):
    """Lexical inventory, NOT Go compiler/build-tag or runtime verification."""
    result = {}
    for base in ("cmd", "internal", "console", "contracts"):
        for directory, dirs, files in os.walk(root / base):
            dirs[:] = sorted(d for d in dirs if not d.startswith(".") and d not in {"node_modules", "vendor", "dist"})
            for name in sorted(files):
                if not name.endswith("_test.go"):
                    continue
                path = Path(directory) / name
                text = path.read_text()
                if "//go:build contractcombo" in text and not contract:
                    continue
                package = path.parent.relative_to(root).as_posix()
                names = set(re.findall(r"^func (Test\w+)\(\w+ \*testing\.T\)", text, re.M))
                if names:
                    result.setdefault(package, set()).update(names)
    return result


def selection(profile, inventory):
    if profile == "contract":
        names = {n for n in inventory.get("internal/integration", ()) if n.startswith("TestPinnedContractCombination")}
        if not CONTRACT_CASES <= names:
            raise ValueError("required contract-combination cases missing")
        return {"internal/integration": names}, []
    if not UNIT <= inventory.keys() or not DATABASE <= inventory.keys():
        raise ValueError("registered Go package missing; review TESTING.md and profile")
    packages = sorted(UNIT if profile == "unit" else
                      DATABASE | {"internal/delivery", "internal/runtimehttp"} if profile == "database" else
                      inventory.keys() - UNIT - DATABASE)
    excluded = CAMPAIGNS if profile == "database" else set().union(*EXTERNAL.values()) if profile == "component" else set()
    selected = {p: inventory[p] - excluded for p in packages}
    if not selected or any(not names for names in selected.values()):
        raise ValueError("required Go selection is empty")
    if profile == "component" and any(not names <= inventory.get(p, set()) for p, names in EXTERNAL.items()):
        raise ValueError("external-case registration drift; review profile instead of silently dropping it")
    if profile == "database" and not CAMPAIGNS <= inventory["internal/integration"]:
        raise ValueError("campaign registration drift; review separate acceptance entry")
    if profile == "database" and not INTEGRATION_CASES <= selected["internal/integration"]:
        raise ValueError("critical database-case registration drift")
    return selected, sorted(excluded)


def preflight(dialect, env, contract=False):
    engines = ("mysql", "postgres") if dialect == "both" else (dialect,)
    required = [] if contract else ["AUTHOVA_TEST_VALKEY_ADDRESS", "AUTHOVA_MASTER_KEY_FILE"]
    for engine in engines:
        required.append(f"AUTHOVA_TEST_{engine.upper()}_DSN")
        if not contract:
            required.append(f"AUTHOVA_TEST_MIGRATOR_{engine.upper()}_DSN")
    if any(not env.get(key) for key in required):
        raise ValueError("selected profile lacks required fixture configuration; no Skip fallback")
    if not contract and not re.fullmatch(r"authova_quality_[a-f0-9]{12}", env.get("AUTHOVA_TEST_DISPOSABLE_DATABASE", "")):
        raise ValueError("database profile requires the owning runner's random disposable-database marker")
    for engine in {"mysql", "postgres"} - set(engines):
        if env.get(f"AUTHOVA_TEST_{engine.upper()}_DSN") or env.get(f"AUTHOVA_TEST_MIGRATOR_{engine.upper()}_DSN"):
            raise ValueError("unselected database configured; select both explicitly or use an isolated fixture environment")


def allowed_skip(package, name, profile, dialect):
    if profile != "database" or package != "internal/storage/sqlstore" or dialect == "both":
        return False
    other = "postgres" if dialect == "mysql" else "mysql"
    # Exact absent-dialect leaves only; a selected dialect or whole suite cannot skip.
    if name in {f"{case}/{other}" for case in OTHER_DIALECT_CASES}:
        return True
    return (name == "TestAtlasPlanningCLI" or
            (dialect == "postgres" and name == "TestOutboxMySQLIdentifierCase"))


def check_events(events, selected, profile, dialect, native_exit):
    packages, passed, skipped = set(), set(), set()
    failures = 0
    for event in events:
        package = event.get("Package", "").removeprefix(MODULE)
        action, name = event.get("Action"), event.get("Test")
        if action == "fail":
            failures += 1
        if package not in selected:
            continue
        if name and action == "skip":
            if not allowed_skip(package, name, profile, dialect):
                raise ValueError("unexpected Skip in required profile; inspect private Go evidence")
            skipped.add((package, name))
        elif name and action == "pass":
            passed.add((package, name))
        elif not name and action == "pass":
            packages.add(package)
    if native_exit != 0 or failures:
        raise ValueError("native Go failure; inspect private Go evidence")
    if packages != set(selected):
        raise ValueError("required Go package did not complete")
    required = {(p, n) for p, names in selected.items() for n in names}
    if profile == "database":
        # Only these exact skips are legal top-level completion alternatives.
        required -= {item for item in skipped if allowed_skip(*item, profile, dialect)}
        for engine in (("mysql", "postgres") if dialect == "both" else (dialect,)):
            required.update(("internal/integration", f"{name}/{engine}") for name in INTEGRATION_CASES)
            for name in ("TestEntPhysicalMapping", "TestDeliveryFailurePersistsAcrossReopen",
                         "TestOutboxAppFairness", "TestOutboxWorkerAppIsolation", "TestMaintenanceGates",
                         "TestAtlasProposedUpgrade", "TestKeyRotationRehearsal"):
                required.add(("internal/storage/sqlstore", f"{name}/{engine}"))
    if profile == "contract":
        for engine in (("mysql", "postgres") if dialect == "both" else (dialect,)):
            required.update(("internal/integration", f"{name}/{engine}") for name in selected["internal/integration"])
    if not required or not required <= passed:
        raise ValueError("required Go test did not pass; empty/mismatched selection is not success")
    return {"packages": len(packages), "passed_events": len(passed), "allowed_skip_events": len(skipped)}


def run(profile, dialect, race):
    selected, excluded = selection(profile, source_tests(contract=profile == "contract"))
    env = os.environ.copy()
    if profile in {"database", "contract"}:
        preflight(dialect, env, contract=profile == "contract")
        env["AUTHOVA_TEST_DATABASE_DIALECT"] = dialect
    else:
        # Optional integration tests must not attach to a caller's Authova instance.
        env = {k: v for k, v in env.items() if not k.startswith(("AUTHOVA_", "COMPOSE_"))}
    # Top-level Go never downloads dependencies. Existing CLI test helpers also
    # invoke migration/go.sh; its cache/network fallback is documented separately.
    env.update(GOMAXPROCS="2", GOTOOLCHAIN="local", GOPROXY="off", GOWORK="off")
    command = ["go", "test", "-json", "-count=1", "-p=2", "-parallel=2", "-timeout=10m"]
    if profile == "contract":
        command.extend(("-tags=contractcombo", "-run=^TestPinnedContractCombination"))
    if race:
        command.append("-race")
    if excluded:
        command.extend(("-skip", "^(" + "|".join(excluded) + ")$"))
    command.extend("./" + package for package in selected)
    parent = ROOT / ".migration" / "testing"
    parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    evidence = Path(tempfile.mkdtemp(prefix=profile + "-", dir=parent))
    # mkdtemp creates 0700; exclusive, explicitly 0600 raw outputs never become artifacts.
    def private_file(name):
        fd = os.open(evidence / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        return os.fdopen(fd, "w")
    # Preserve CI's ordering: integration mutations finish before storage/DDL
    # cases use the same owned schema. -p=2 is not database isolation.
    base_command = command[:-len(selected)]
    groups = [["internal/integration"], [p for p in selected if p != "internal/integration"]] if profile == "database" else [list(selected)]
    native_exit, outputs, native_exits = 0, [], []
    for index, group in enumerate(groups):
        filename = f"go-{index}.jsonl"
        outputs.append(evidence / filename)
        with private_file(filename) as output, private_file(f"stderr-{index}.log") as stderr:
            result = subprocess.run(base_command + ["./" + p for p in group], cwd=ROOT, env=env, stdout=output, stderr=stderr)
        native_exits.append(result.returncode)
        if result.returncode:
            native_exit = result.returncode
            break
    summary, failure = {}, None
    try:
        def events():
            for path in outputs:
                with path.open() as stream:
                    yield from (json.loads(line) for line in stream)
        summary = check_events(events(), selected, profile, dialect, native_exit)
    except json.JSONDecodeError:
        failure = "invalid Go JSON evidence"
    except (TypeError, AttributeError):
        failure = "invalid Go event shape"
    except ValueError as error:
        # check_events raises only fixed local messages, never Output/error text.
        failure = str(error)
    record = {"profile": profile, "dialect": dialect if profile in {"database", "contract"} else None,
              "native_exits": native_exits, "verification": "FAIL" if failure else "PASS",
              "failure": failure, **summary}
    with private_file("result.json") as output:
        json.dump(record, output)
    print(json.dumps({**record, "evidence": str(evidence.relative_to(ROOT))}))
    if failure:
        raise ValueError("Go gate failed; private evidence: " + str(evidence.relative_to(ROOT))) from None


def check_python_result(result):
    if not result.wasSuccessful() or not result.testsRun or result.skipped or result.expectedFailures:
        raise ValueError("required Python suite failed, was empty or skipped; inspect test output")


def run_python(unit_only=False):
    directory = ROOT / "scripts/tests"
    if unit_only:
        if any(not (directory / name).is_file() for name in PYTHON_UNIT_FILES):
            raise ValueError("registered Python unit file missing")
        suite = unittest.TestSuite(unittest.defaultTestLoader.discover(str(directory), pattern=name)
                                   for name in PYTHON_UNIT_FILES)
    else:
        suite = unittest.defaultTestLoader.discover(str(directory), pattern="test_*.py")
    check_python_result(unittest.TextTestRunner(verbosity=2).run(suite))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("profile", choices=("unit", "component", "database", "contract", "python", "python-unit"))
    parser.add_argument("--list", action="store_true", help="lexical source inventory only; does not run Go or read configuration")
    parser.add_argument("--dialect", choices=("mysql", "postgres", "both"), default="mysql")
    parser.add_argument("--race", action="store_true")
    args = parser.parse_args()
    try:
        if args.profile in {"python", "python-unit"}:
            if args.list:
                raise ValueError("--list is Go source inventory only; use Python AST for static discovery")
            run_python(unit_only=args.profile == "python-unit")
        elif args.list:
            selected, excluded = selection(args.profile, source_tests(contract=args.profile == "contract"))
            print(json.dumps({"profile": args.profile, "source_tests": {p: sorted(n) for p, n in selected.items()},
                              "excluded_cases": excluded, "runtime_verified": False}, indent=2))
        else:
            run(args.profile, args.dialect, args.race)
    except ValueError as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()
