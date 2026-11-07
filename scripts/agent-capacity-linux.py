#!/usr/bin/env python3
"""Run the local capacity profile entirely inside the development Linux network.

Uses an already prepared, owned Agent acceptance database and a new private
output directory. This scratch image is test infrastructure, not a production
release image: it has no system CA bundle. An explicit private TLS fixture
may supply its own CA for the isolated development Valkey endpoint.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_hashes():
    paths = subprocess.check_output(["rg", "--files", "-g", "*.go"], cwd=ROOT, text=True).splitlines()
    paths += subprocess.check_output(["rg", "--files", "internal/storage/sqlstore/migrations", "-g", "*.sql"], cwd=ROOT, text=True).splitlines()
    return {p: sha256(ROOT / p) for p in paths + ["go.mod", "go.sum"]}


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True, help="Prepared private Agent acceptance directory")
    parser.add_argument("--output", type=Path, required=True, help="New private directory below this checkout's .dev")
    parser.add_argument("--driver", choices=["postgres", "mysql"], required=True)
    parser.add_argument("--duration", default="1h")
    parser.add_argument("--valkey-fixture",type=Path,help="Owned scripts/valkey-tls-gates.py fixture directory, for verified mTLS/ACL")
    parser.add_argument("--qps", type=int, default=5100)
    parser.add_argument("--queue-capacity", type=int, default=2048, help="Bounded generator buffer; all planned latency and final drain remain counted")
    parser.add_argument("--replicas", type=int, choices=[1, 2], default=2)
    parser.add_argument("--db-pool", type=int, default=64)
    parser.add_argument("--app-db-concurrency", type=int, default=56)
    args = parser.parse_args()
    if os.getuid() == 0:
        parser.error("run this local acceptance tool as a non-root user")
    scales = {"ns": 1e-9, "us": 1e-6, "ms": 1e-3, "s": 1, "m": 60, "h": 3600}
    if not re.fullmatch(r"(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|ms|s|m|h))+", args.duration):
        parser.error("invalid Go duration")
    seconds = sum(float(n) * scales[u] for n, u in re.findall(r"([0-9]+(?:\.[0-9]+)?)(ns|us|ms|s|m|h)", args.duration))
    if not 1 <= seconds <= 86400 or not 1 <= args.qps <= 20000 or not 4 <= args.db_pool <= 64 or not 1 <= args.app_db_concurrency <= 256 or not 4 <= args.queue_capacity <= 65536:
        parser.error("duration, QPS or database budget outside local gate bounds")
    source, output = args.directory.resolve(), args.output.resolve()
    if ROOT / ".dev" not in source.parents or ROOT / ".dev" not in output.parents:
        parser.error("source and output must be below this checkout's .dev")
    if output.exists():
        parser.error("output already exists; preserve the previous attempt and select a new directory")
    plan = json.loads((source / "plan.json").read_text())
    database = plan["database"]
    if not re.fullmatch(r"authova_agent_[a-zA-Z0-9_]+", database):
        parser.error("not an owned Agent acceptance database")
    dsn = plan["backends"][args.driver]["source"]
    # The owned development topology is deliberately fixed. Never forward an
    # arbitrary production DSN into a benchmark container or print credentials.
    old, new = (("127.0.0.1:55432", "postgres:5432") if args.driver == "postgres"
                else ("tcp(127.0.0.1:53306)", "tcp(mysql:3306)"))
    if old not in dsn or "/" + database + "?" not in dsn:
        parser.error("source DSN does not match the owned development topology")
    dsn = dsn.replace(old, new)
    key, fixture = source / "master.key", source / (args.driver + "-fixture.json")
    if len(key.read_bytes()) != 32 or not fixture.is_file():
        parser.error("complete private key and seeded fixture are required")
    arch = subprocess.check_output(["docker", "info", "--format", "{{.Architecture}}"], text=True).strip()
    arch = {"aarch64": "arm64", "x86_64": "amd64"}.get(arch, arch)
    if arch not in ["amd64", "arm64"]:
        parser.error("supported Docker architectures: amd64 and arm64")
    output.mkdir(parents=True, mode=0o700)
    build = output / "image"
    build.mkdir(mode=0o700)
    identity = uuid.uuid4().hex[:12]
    name = "authova-capacity-" + identity
    image = "authova-capacity:owned-" + identity
    buildenv = os.environ | {"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": arch}
    sources = source_hashes()
    with (output / "build.log").open("wb") as log:
        subprocess.run(["go", "build", "-trimpath", "-o", str(build / "authova"), "./cmd/authova"], cwd=ROOT, env=buildenv, stdout=log, stderr=log, check=True)
        subprocess.run(["go", "test", "-c", "-o", str(build / "integration.test"), "./internal/integration"], cwd=ROOT, env=buildenv, stdout=log, stderr=log, check=True)
        # umask 077 keeps private run files restricted. Image binaries are
        # owned by root after COPY and must remain executable by the runtime UID.
        for executable in ["authova", "integration.test"]:
            (build / executable).chmod(0o555)
        (build / "Dockerfile").write_text('FROM scratch\nCOPY authova /authova\nCOPY integration.test /integration.test\nUSER 10001:10001\nENTRYPOINT ["/integration.test"]\n')
        (build / ".dockerignore").write_text("*\n!Dockerfile\n!authova\n!integration.test\n")
        subprocess.run(["docker", "build", "--network=none", "-t", image, str(build)], cwd=ROOT, stdout=log, stderr=log, check=True)
    if sources != source_hashes():
        raise SystemExit("source changed during the build; retain this attempt and run again with a fixed source snapshot")
    image_id = subprocess.check_output(["docker", "image", "inspect", image, "--format", "{{.Id}}"], text=True).strip()
    # Confirm the recorded executables are the actual image payload, before
    # mounting any private fixture or starting a measurement.
    check_id = subprocess.check_output(["docker", "create", image_id], text=True).strip()
    try:
        for executable in ["authova", "integration.test"]:
            actual = output / (executable + ".verified")
            subprocess.run(["docker", "cp", check_id + ":/" + executable, str(actual)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
            if sha256(actual) != sha256(build / executable):
                raise SystemExit("image executable does not match the compiled artifact")
            actual.unlink()
    finally:
        subprocess.run(["docker", "rm", check_id], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    shutil.copy2(key, output / "master.key")
    shutil.copy2(fixture, output / fixture.name)
    for item in [output / "master.key", output / fixture.name]:
        item.chmod(0o600)
    runtime_env = {
        "AUTHOVA_MODE": "development", "AUTHOVA_MASTER_KEY_FILE": "/run/acceptance/master.key",
        "AUTHOVA_ENCRYPTION_KEYRING_FILE": "", "AUTHOVA_AGENT_ACCEPTANCE_DIR": "/run/acceptance",
        "AUTHOVA_AGENT_CAPACITY_DURATION": args.duration, "AUTHOVA_AGENT_CAPACITY_QPS": str(args.qps),
        "AUTHOVA_AGENT_CAPACITY_QUEUE": str(args.queue_capacity),
        "AUTHOVA_AGENT_CAPACITY_DRIVER": args.driver, "AUTHOVA_AGENT_CAPACITY_DB_CONCURRENCY": str(args.app_db_concurrency),
        "AUTHOVA_AGENT_CAPACITY_POOL": str(args.db_pool), "AUTHOVA_AGENT_CAPACITY_REPLICAS": str(args.replicas),
        "AUTHOVA_TEST_CORE_BINARY": "/authova", "AUTHOVA_AGENT_CAPACITY_VALKEY_ADDRESS": "valkey:6379",
        "AUTHOVA_TEST_" + args.driver.upper() + "_DSN": dsn,
    }
    if args.valkey_fixture:
        tls_fixture=args.valkey_fixture.resolve()
        if ROOT / ".dev" not in tls_fixture.parents:
            parser.error("Valkey fixture must be below this checkout's .dev")
        tls_summary=json.loads((tls_fixture/"fixture-summary.json").read_text())
        if not re.fullmatch(r"authova-tls-[a-f0-9]{12}",tls_summary["alias"]) or not tls_summary.get("mutual_tls") or not tls_summary.get("default_user_disabled"):
            parser.error("not an owned TLS acceptance fixture")
        config=json.loads((tls_fixture/"clients.json").read_text())["core"]
        runtime_env.update(AUTHOVA_AGENT_CAPACITY_VALKEY_ADDRESS=tls_summary["alias"]+":6379",
                           AUTHOVA_VALKEY_USERNAME=config["Username"], AUTHOVA_VALKEY_TLS="true")
        for field,environment in [("CAFile","TLS_CA_FILE"),("CertFile","TLS_CERT_FILE"),("KeyFile","TLS_KEY_FILE"),("PasswordFile","PASSWORD_FILE")]:
            original=Path(config[field]).resolve()
            if original.parent!=tls_fixture:
                parser.error("TLS material must belong to the owned fixture")
            destination=output/("valkey-"+original.name)
            shutil.copy2(original,destination);destination.chmod(0o600)
            runtime_env["AUTHOVA_VALKEY_"+environment]="/run/acceptance/"+destination.name
    if any("\n" in v or "\r" in v for v in runtime_env.values()):
        parser.error("multiline environment values are not supported")
    env_file = output / "runtime.env"
    env_file.write_text("".join(k + "=" + v + "\n" for k, v in runtime_env.items()))
    metadata = {"started_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                "server_binary_sha256": sha256(build / "authova"), "generator_binary_sha256": sha256(build / "integration.test"),
                "image_id": image_id, "generator_os": "linux", "server_os": "linux", "architecture": arch,
                "cpu_limit": 8, "memory_limit_bytes": 6 * 1024 ** 3,
                "server_gomaxprocs": 4, "server_gomemlimit": "512MiB", "uid": os.getuid(),
                "container_name": name,
                "valkey_transport": "mutual_tls_named_acl" if args.valkey_fixture else "development_plaintext",
                "source_sha256": sources}
    (output / "build-metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    argv = ["docker", "run", "--rm", "--name", name, "--label", "authova.acceptance=agent-capacity-linux-all",
            "--network", "authova-dev_default", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
            "--user", str(os.getuid()) + ":" + str(os.getgid()), "--memory=6g", "--cpus=8",
            "--tmpfs", "/tmp:rw,nosuid,noexec,size=64m", "--mount", "type=bind,source=" + str(output) + ",target=/run/acceptance",
            "--env-file", str(env_file), "--entrypoint", "/integration.test", image_id,
            "-test.run=^TestAgentCapacityHTTP$", "-test.count=1", "-test.v", "-test.timeout=26h"]
    started = time.monotonic()
    try:
        with (output / "run.log").open("wb") as log:
            result = subprocess.run(argv, cwd=ROOT, stdout=log, stderr=log)
    except BaseException:
        # Only the randomly named container created by this invocation is owned.
        subprocess.run(["docker", "stop", "-t", "15", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        raise
    (output / "result.json").write_text(json.dumps({"exit_code": result.returncode, "elapsed_seconds": time.monotonic() - started,
                                                   "finished_utc": datetime.datetime.now(datetime.timezone.utc).isoformat()}, indent=2) + "\n")
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
