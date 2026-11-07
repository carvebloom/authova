#!/usr/bin/env python3
"""Opt-in Agent capacity and recovery gates, exclusively in new private databases.

No demo database, global Docker setting, or existing volume is reset. Dumps and
credentials stay under .dev. Keep the run directory to resume a failed stage.
"""
import argparse
import fcntl
import gzip
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=["prepare", "seed", "resume-fixture", "restore", "verify", "soak", "capacity"])
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--users", type=int, default=12000000)
    parser.add_argument("--duration", default="1h", help="Go duration; formal Agent soak requires at least 1h")
    parser.add_argument("--qps", type=int, default=5100)
    parser.add_argument("--queue-capacity", type=int, default=2048, help="Capacity: bounded generator buffer, including its delay and final drain")
    parser.add_argument("--app-db-concurrency", type=int, default=32, help="Explicit synthetic App database admission budget")
    parser.add_argument("--db-pool", type=int, default=64, help="Per-replica process DB pool, at most 64; does not alter server-global configuration")
    parser.add_argument("--replicas", type=int, default=2, choices=[1, 2], help="Independent real Core processes behind the canonical issuer entrance")
    parser.add_argument("--race-generator", action="store_true", help="Capacity: instrument the HTTP generator/proxy with Go race detection; Core replicas remain normal builds")
    parser.add_argument("--fail-fast-after", help="Stop an already failed zero-error capacity gate after a Go duration (>=2m); requested and observed durations stay separate")
    parser.add_argument("--driver", choices=["postgres", "mysql"], help="Run one backend capacity profile")
    args = parser.parse_args()
    if not re.fullmatch(r"(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|ms|s|m|h))+", args.duration):
        parser.error("duration must use positive Go duration units, for example 1h or 5m30s")
    scales = {"ns": 1e-9, "us": 1e-6, "ms": 1e-3, "s": 1, "m": 60, "h": 3600}
    seconds = sum(float(value) * scales[unit] for value, unit in re.findall(r"([0-9]+(?:\.[0-9]+)?)(ns|us|ms|s|m|h)", args.duration))
    if not 0 < seconds <= 86400 or (args.stage == "soak" and seconds < 600) or (args.stage == "capacity" and seconds < 1):
        parser.error("duration outside gate bounds: soak >=10m, capacity >=1s, maximum 24h")
    if not 1 <= args.qps <= 20000 or not 1 <= args.app_db_concurrency <= 256 or not 4 <= args.db_pool <= 64 or not 4 <= args.queue_capacity <= 65536:
        parser.error("QPS or App database budget is outside the local gate bounds")
    if args.stage == "capacity" and not args.driver:
        parser.error("capacity requires one --driver to avoid two load generators competing")
    if args.fail_fast_after:
        if not re.fullmatch(r"(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|ms|s|m|h))+", args.fail_fast_after):
            parser.error("fail-fast-after must be a positive Go duration")
        boundary = sum(float(value) * scales[unit] for value, unit in re.findall(r"([0-9]+(?:\.[0-9]+)?)(ns|us|ms|s|m|h)", args.fail_fast_after))
        if not 120 <= boundary <= 86400:
            parser.error("fail-fast-after must be between 2m and 24h")
    directory = args.directory.resolve()
    if ROOT / ".dev" not in directory.parents:
        parser.error("private artifacts must be below this repository's .dev")
    if not 1 <= args.users <= 20000000:
        parser.error("users must be between 1 and 20000000")
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    # Serialize restore-plan publication, including a backend-specific restore
    # overlapping a later full run. The OS releases this lock on interruption.
    restore_lock = None
    if args.stage == "restore":
        restore_lock = (directory / ".restore.lock").open("a")
        fcntl.flock(restore_lock, fcntl.LOCK_EX)
    plan_file = directory / "plan.json"
    # Preparation, seeding, soaking and restoring can overlap. Independent
    # stage journals prevent one process overwriting another's completion.
    report_file = directory / ("stages-" + args.stage + ".json")
    compose = ["docker", "compose", "-f", str(ROOT / "deploy/compose.dev.yaml"), "exec", "-T"]
    pg = compose + ["-e", "PGPASSWORD=authova-local-database-only", "postgres"]
    my = compose + ["-e", "MYSQL_PWD=authova-local-database-only", "mysql"]
    report = json.loads(report_file.read_text()) if report_file.exists() else []

    def command(label, argv, env=None, source=None, output=None):
        row = {"stage": label, "status": "running", "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
        report.append(row)
        report_file.write_text(json.dumps(report, indent=2) + "\n")
        started = time.monotonic()
        with (directory / (label + ".log")).open("ab") as log:
            result = subprocess.run(argv, cwd=ROOT, env=env, stdin=source, stdout=output or log, stderr=log)
        row.update(exit_code=result.returncode, elapsed_seconds=time.monotonic() - started,
                   status="passed" if result.returncode == 0 else "failed")
        report_file.write_text(json.dumps(report, indent=2) + "\n")
        if result.returncode:
            raise SystemExit(label + " failed; inspect private log")
        print(label + " passed", flush=True)

    if args.stage == "prepare":
        if plan_file.exists():
            parser.error("run already exists; resume another stage instead")
        name = "authova_agent_" + secrets.token_hex(6)
        (directory / "master.key").write_bytes(secrets.token_bytes(32))
        plan = {"database": name, "target": name + "_restored", "users": args.users, "backends": {}}
        for driver in ["postgres", "mysql"]:
            pg_dsn = f"postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:55432/{name}?sslmode=disable"
            my_dsn = f"authova_runtime:authova-local-runtime-only@tcp(127.0.0.1:53306)/{name}?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin"
            plan["backends"][driver] = {"source": pg_dsn if driver == "postgres" else my_dsn}
        # Save the ownership record before the first mutation, including partial
        # preparation. A retry must not silently overwrite an unknown DB result.
        plan_file.write_text(json.dumps(plan, indent=2) + "\n")
        command("create-postgres", pg + ["psql", "-X", "-U", "authova_migrator", "-d", "authova", "-v", "ON_ERROR_STOP=1", "-c", f"CREATE DATABASE {name}"])
        command("grant-postgres", pg + ["psql", "-X", "-U", "authova_migrator", "-d", name, "-v", "ON_ERROR_STOP=1", "-c", f"GRANT CONNECT ON DATABASE {name} TO authova_runtime; GRANT USAGE ON SCHEMA public TO authova_runtime; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO authova_runtime"])
        command("create-mysql", my + ["mysql", "-uroot", "-e", f"CREATE DATABASE {name} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin; GRANT SELECT,INSERT,UPDATE,DELETE ON {name}.* TO 'authova_runtime'@'%'"])
    else:
        plan = json.loads(plan_file.read_text())

    name, target = plan["database"], plan["target"]
    if not name.startswith("authova_agent_") or not name.replace("_", "").isalnum() or target != name + "_restored":
        raise SystemExit("invalid owned database identity")
    env = os.environ | {"AUTHOVA_MASTER_KEY_FILE": str(directory / "master.key"), "AUTHOVA_ENCRYPTION_KEYRING_FILE": "",
                        "AUTHOVA_MODE": "development", "AUTHOVA_BASE_URL": "http://127.0.0.1:18080",
                        "AUTHOVA_CONSOLE_URL": "http://127.0.0.1:18090", "AUTHOVA_AGENT_ACCEPTANCE_DIR": str(directory),
                        "AUTHOVA_AGENT_CAPACITY_USERS": str(plan["users"])}
    for driver, item in plan["backends"].items():
        env["AUTHOVA_TEST_" + driver.upper() + "_DSN"] = item["source"]

    if args.stage == "prepare":
        for driver, item in plan["backends"].items():
            admin = item["source"].replace("authova_runtime:authova-local-runtime-only", "authova_migrator:authova-local-database-only" if driver == "postgres" else "root:authova-local-database-only")
            command("migrate-" + driver, ["go", "run", "./cmd/authova", "migrate"], env | {"AUTHOVA_DATABASE_DRIVER": driver, "AUTHOVA_DATABASE_URL": admin, "AUTHOVA_MIGRATION_DATABASE_URL": admin})
        command("compile-fixture", ["go", "test", "-c", "-o", str(directory / "integration.test"), "./internal/integration"], env)
    if args.stage == "seed":
        command("capacity-fixture", [str(directory / "integration.test"), "-test.run=^TestAgentCapacityFixture$", "-test.v", "-test.timeout=3h"], env)
    if args.stage == "resume-fixture":
        if args.driver:
            for other in ["postgres", "mysql"]:
                if other != args.driver:
                    env.pop("AUTHOVA_TEST_" + other.upper() + "_DSN", None)
        env["AUTHOVA_AGENT_RESUME_FIXTURE"] = "1"
        command("resume-capacity-" + (args.driver or "both"), ["go", "test", "-count=1", "-v", "-timeout=20m", "-run=^TestAgentCapacityResume$", "./internal/integration"], env)
    if args.stage == "restore":
        for driver, item in plan["backends"].items():
            if args.driver and driver != args.driver:
                continue
            if "target" in item:
                print(driver + " completed restore retained", flush=True)
                continue
            backup = directory / (driver + (".dump" if driver == "postgres" else ".sql.gz"))
            started = time.monotonic()
            if driver == "postgres":
                with backup.open("xb") as output:
                    command("backup-" + driver, pg + ["pg_dump", "-U", "authova_migrator", "-d", name, "-Fc", "--compress=0"], output=output)
                command("create-restore-" + driver, pg + ["psql", "-X", "-U", "authova_migrator", "-d", "authova", "-v", "ON_ERROR_STOP=1", "-c", f"CREATE DATABASE {target}"])
                with backup.open("rb") as source:
                    command("restore-" + driver, pg + ["pg_restore", "-U", "authova_migrator", "-d", target, "--exit-on-error"], source=source)
                command("grant-restore-" + driver, pg + ["psql", "-X", "-U", "authova_migrator", "-d", target, "-v", "ON_ERROR_STOP=1", "-c", f"GRANT CONNECT ON DATABASE {target} TO authova_runtime; GRANT USAGE ON SCHEMA public TO authova_runtime; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO authova_runtime"])
            else:
                with (directory / "backup-mysql.log").open("ab") as log, backup.open("xb") as raw:
                    process = subprocess.Popen(my + ["mysqldump", "-uroot", "--single-transaction", "--hex-blob", "--no-tablespaces", "--set-gtid-purged=OFF", "--skip-add-locks", name], stdout=subprocess.PIPE, stderr=log)
                    with gzip.GzipFile(fileobj=raw, mode="wb", compresslevel=1) as output:
                        shutil.copyfileobj(process.stdout, output, 1024 * 1024)
                    process.stdout.close()
                    if process.wait():
                        raise SystemExit("MySQL backup failed")
                backup_seconds = time.monotonic() - started
                command("create-restore-" + driver, my + ["mysql", "-uroot", "-e", f"CREATE DATABASE {target} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"])
                started_restore = time.monotonic()
                with gzip.open(backup, "rb") as source, (directory / "restore-mysql.log").open("ab") as log:
                    process = subprocess.Popen(my + ["mysql", "-uroot", target], stdin=subprocess.PIPE, stdout=log, stderr=log)
                    process.stdin.write(b"SET autocommit=0;\n")
                    batch = 0
                    for line in source:
                        upper = line.lstrip().upper()
                        if upper.startswith((b"LOCK TABLES", b"UNLOCK TABLES")):
                            process.stdin.write(b"COMMIT;\n")
                            batch = 0
                            continue
                        process.stdin.write(line)
                        if upper.startswith(b"INSERT INTO"):
                            batch += len(line)
                            if batch >= 16 * 1024 * 1024:
                                process.stdin.write(b"COMMIT;\n")
                                batch = 0
                    process.stdin.write(b"COMMIT;\n")
                    process.stdin.close()
                    if process.wait():
                        raise SystemExit("MySQL restore failed; partial target retained")
                report.append({"stage": "backup-mysql", "status": "passed", "elapsed_seconds": backup_seconds})
                report.append({"stage": "restore-mysql", "status": "passed", "elapsed_seconds": time.monotonic() - started_restore})
                command("grant-restore-" + driver, my + ["mysql", "-uroot", "-e", f"GRANT SELECT,INSERT,UPDATE,DELETE ON {target}.* TO 'authova_runtime'@'%'"])
            item.update(target=item["source"].replace("/" + name + "?", "/" + target + "?"), backup_bytes=backup.stat().st_size, total_backup_restore_seconds=time.monotonic() - started)
            plan_file.write_text(json.dumps(plan, indent=2) + "\n")
            print(driver + " full restore complete", flush=True)
    if args.stage == "verify":
        restore_plan = []
        for driver, item in plan["backends"].items():
            env["AUTHOVA_RESTORE_" + driver.upper() + "_DSN"] = item["target"]
            restore_plan.append({"driver": driver, "source": item["source"], "target": item["target"], "fixture": str(directory / (driver + "-fixture.json"))})
        private_plan = directory / "restore-plan.json"
        private_plan.write_text(json.dumps(restore_plan, indent=2) + "\n")
        env["AUTHOVA_CAPACITY_RESTORE_PLAN"] = str(private_plan)
        env["AUTHOVA_AGENT_POST_BACKUP_REVOKE"] = "1"
        command("source-post-backup-revoke", ["go", "test", "-race", "-count=1", "-v", "-timeout=10m", "-run=^TestAgentRevokeAfterBackup$", "./internal/integration"], env)
        names = "TestRestoredEncryptedFacts|TestAgentRecoveredAuthority"
        if plan["users"] >= 12000000:
            names += "|TestCapacityRestore"
        command("verify-restored-authority", ["go", "test", "-race", "-count=1", "-v", "-timeout=20m", "-run=^(" + names + ")$", "./internal/integration"], env)
        binary = directory / "authova-recovery"
        command("compile-recovery-cli", ["go", "build", "-o", str(binary), "./cmd/authova"], env)
        env["AUTHOVA_TEST_CORE_BINARY"] = str(binary)
        command("verify-restored-quarantine", ["go", "test", "-race", "-count=1", "-v", "-timeout=20m", "-run=^TestAgentRestoredQuarantineCLI$", "./internal/integration"], env)
    if args.stage == "soak":
        env.update(AUTHOVA_AGENT_SOAK_DURATION=args.duration, AUTHOVA_PROTOCOL_SOAK_DURATION=args.duration)
        command("agent-protocol-soak", ["go", "test", "-race", "-count=1", "-v", "-timeout=" + str(int(2 * seconds + 1200)) + "s", "-run=^(TestAgentDelegationSoak|TestProtocolSoak)$", "./internal/integration"], env)
    if args.stage == "capacity":
        if not args.driver:
            parser.error("capacity requires one --driver to avoid two load generators competing")
        env.update(AUTHOVA_AGENT_CAPACITY_DURATION=args.duration, AUTHOVA_AGENT_CAPACITY_QPS=str(args.qps), AUTHOVA_AGENT_CAPACITY_QUEUE=str(args.queue_capacity), AUTHOVA_AGENT_CAPACITY_DRIVER=args.driver,
                   AUTHOVA_AGENT_CAPACITY_DB_CONCURRENCY=str(args.app_db_concurrency), AUTHOVA_AGENT_CAPACITY_POOL=str(args.db_pool), AUTHOVA_AGENT_CAPACITY_REPLICAS=str(args.replicas))
        binary = directory / "authova-capacity"
        command("compile-capacity-cli", ["go", "build", "-o", str(binary), "./cmd/authova"], env)
        env["AUTHOVA_TEST_CORE_BINARY"] = str(binary)
        if args.fail_fast_after:
            env["AUTHOVA_AGENT_CAPACITY_FAIL_FAST"] = args.fail_fast_after
        generator = ["go", "test"] + (["-race"] if args.race_generator else [])
        command("agent-capacity-" + args.driver, generator + ["-count=1", "-v", "-timeout=26h", "-run=^TestAgentCapacityHTTP$", "./internal/integration"], env)


if __name__ == "__main__":
    main()
