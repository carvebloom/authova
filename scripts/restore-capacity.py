#!/usr/bin/env python3
"""Real local 12M backup/restore; all dumps and diagnostics remain private."""
import argparse
import atexit
import datetime
import gzip
import json
import os
import shutil
import subprocess
import time
import uuid
from pathlib import Path

root = Path(__file__).resolve().parents[1]
os.chdir(root)
os.umask(0o077)
parser = argparse.ArgumentParser()
parser.add_argument('--retry-mysql-dir')
args = parser.parse_args()
run = uuid.uuid4().hex[:8]
directory = root / ".dev/backups" / ("review-" + run)
directory.mkdir(mode=0o700)
compose = ["docker", "compose", "-f", "deploy/compose.dev.yaml", "exec", "-T"]
postgres = compose + ["-e", "PGPASSWORD=authova-local-database-only", "postgres"]
mysql = compose + ["-e", "MYSQL_PWD=authova-local-database-only", "mysql"]
report = {"date_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "run": run, "scope": "local full capacity databases; not cross-host HA/RPO"}
private = []
if args.retry_mysql_dir:
    report = json.loads((root / 'output/review-capacity-restore.json').read_text())
    private = json.loads((root / '.dev/review-restore.json').read_text())
    candidate = Path(args.retry_mysql_dir).resolve()
    if root / '.dev/backups' not in candidate.parents or not (candidate / 'mysql.sql.gz').is_file():
        raise SystemExit('Retry requires a retained private backup from this tool.')

attempt = None
def save_report():
    (root / "output").mkdir(exist_ok=True)
    (root / "output/review-capacity-restore.json").write_text(json.dumps(report, indent=2) + "\n")

def finish_interrupted():
    if attempt is not None and attempt['status'] == 'running':
        attempt['status'] = 'failed_or_interrupted'
        attempt['elapsed_seconds'] = time.monotonic() - attempt_started
        save_report()
atexit.register(finish_interrupted)

def command(args, input=None, output=None):
    with (directory / "diagnostics.log").open("ab") as diagnostics:
        result = subprocess.run(args, stdin=input, stdout=output or diagnostics, stderr=diagnostics)
    if result.returncode:
        if attempt is not None:
            attempt['command_return_code'] = result.returncode
        raise RuntimeError("Database stage failed; inspect private diagnostics, not public output.")

for driver in (["mysql"] if args.retry_mysql_dir else ["postgres", "mysql"]):
    target = f"authova_review_{driver}_{run}"
    attempt_started = time.monotonic()
    attempt = {'driver': driver, 'target': target, 'stage': 'backup', 'status': 'running', 'date_utc': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    report.setdefault('attempts', []).append(attempt)
    save_report()
    record = {}
    started = time.monotonic()
    print(driver + ": full backup started", flush=True)
    if driver == "mysql" and args.retry_mysql_dir:
        backup = Path(args.retry_mysql_dir).resolve() / "mysql.sql.gz"
    elif driver == "postgres":
        backup = directory / "postgres.dump"
        with backup.open("wb") as output:
            command(postgres + ["pg_dump", "-U", "authova_migrator", "-d", "authova", "-Fc", "--compress=0"], output=output)
    else:
        backup = directory / "mysql.sql.gz"
        with (directory / "diagnostics.log").open("ab") as diagnostics, backup.open("wb") as raw:
            process = subprocess.Popen(mysql + ["mysqldump", "-uroot", "--single-transaction", "--hex-blob", "--no-tablespaces", "--set-gtid-purged=OFF", "--skip-add-locks", "authova_capacity"], stdout=subprocess.PIPE, stderr=diagnostics)
            with gzip.GzipFile(fileobj=raw, mode="wb", compresslevel=1) as output:
                shutil.copyfileobj(process.stdout, output, 1024*1024)
            process.stdout.close()
            if process.wait(): raise RuntimeError("MySQL backup failed; inspect private diagnostics.")
    record["backup_seconds"] = None if args.retry_mysql_dir else time.monotonic() - started
    record["backup_reused"] = bool(args.retry_mysql_dir)
    record["backup_bytes"] = backup.stat().st_size
    print(driver + ": backup completed; new-database restore started", flush=True)
    attempt['stage'] = 'restore'
    attempt['backup_bytes'] = record['backup_bytes']
    save_report()
    started = time.monotonic()
    if driver == "postgres":
        command(postgres + ["psql", "-X", "-U", "authova_migrator", "-d", "authova", "-v", "ON_ERROR_STOP=1", "-c", f"CREATE DATABASE {target} OWNER authova_migrator"])
        with backup.open("rb") as source:
            command(postgres + ["pg_restore", "-U", "authova_migrator", "-d", target, "--exit-on-error"], input=source)
        command(postgres + ["psql", "-X", "-U", "authova_migrator", "-d", target, "-v", "ON_ERROR_STOP=1", "-c", f"GRANT CONNECT ON DATABASE {target} TO authova_runtime; GRANT USAGE ON SCHEMA public TO authova_runtime; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO authova_runtime"])
        source_dsn = "postgres://authova_runtime:authova-local-runtime-only@127.0.0.1:55432/authova?sslmode=disable"
        target_dsn = source_dsn.replace("/authova?", "/" + target + "?")
    else:
        command(mysql + ["mysql", "-uroot", "-e", f"CREATE DATABASE {target} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"])
        with gzip.open(backup, "rb") as source, (directory / "diagnostics.log").open("ab") as diagnostics:
            process = subprocess.Popen(mysql + ["mysql", "-uroot", target], stdin=subprocess.PIPE, stdout=diagnostics, stderr=diagnostics)
            # InnoDB restores must not retain per-table locks/transactions for
            # millions of rows. This filter handles our own mysqldump format.
            process.stdin.write(b"SET autocommit=0;\n")
            batch_bytes=0
            for line in source:
                upper=line.lstrip().upper()
                if upper.startswith(b"LOCK TABLES") or upper.startswith(b"UNLOCK TABLES"):
                    process.stdin.write(b"COMMIT;\n");batch_bytes=0;continue
                process.stdin.write(line)
                if upper.startswith(b"INSERT INTO"):
                    batch_bytes+=len(line)
                    if batch_bytes>=16*1024*1024:
                        process.stdin.write(b"COMMIT;\n");batch_bytes=0
            process.stdin.write(b"COMMIT;\n")
            process.stdin.close()
            if process.wait(): raise RuntimeError("MySQL restore failed; inspect private diagnostics.")
        command(mysql + ["mysql", "-uroot", "-e", f"GRANT SELECT,INSERT,UPDATE,DELETE ON {target}.* TO 'authova_runtime'@'%'"])
        source_dsn = "authova_runtime:authova-local-runtime-only@tcp(127.0.0.1:53306)/authova_capacity?parseTime=true&charset=utf8mb4&collation=utf8mb4_bin"
        target_dsn = source_dsn.replace("/authova_capacity?", "/" + target + "?")
    record["restore_seconds"] = time.monotonic() - started
    if driver == 'mysql':
        record['restore_batch_insert_bytes'] = 16*1024*1024
    attempt['status'] = 'complete'
    attempt['elapsed_seconds'] = time.monotonic() - attempt_started
    report[driver] = record
    private = [item for item in private if item["driver"] != driver]
    private.append({"driver": driver, "source": source_dsn, "target": target_dsn, "fixture": str(root / ".dev" / ("load-" + driver + ".json"))})
    save_report()
    (root / ".dev/review-restore.json").write_text(json.dumps(private, indent=2))
    print(driver + ": restore completed", flush=True)
print("Both full restores completed; run the encrypted-fact verifier before accepting.", flush=True)
