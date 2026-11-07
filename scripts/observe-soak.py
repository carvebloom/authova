#!/usr/bin/env python3
"""Redacted process/DB-pool samples; no environment or credential inspection."""
import argparse
import datetime
import json
import subprocess
import time
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--pid-file", required=True)
parser.add_argument("--origin", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--minutes", type=int, default=75)
args = parser.parse_args()
pid = int(Path(args.pid_file).read_text().strip())
started = time.monotonic()
with Path(args.output).open("w") as output:
    for index in range(args.minutes):
        row = {"date_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "elapsed_seconds": time.monotonic()-started, "pid": pid}
        try:
            fields = subprocess.check_output(["ps", "-p", str(pid), "-o", "rss=,pcpu="], text=True).split()
            row["rss_bytes"], row["cpu_percent"] = int(fields[0])*1024, float(fields[1])
            with urllib.request.urlopen(args.origin+"/metrics", timeout=5) as response:
                text = response.read(64*1024).decode()
            row["metrics"] = {line.rsplit(" ",1)[0]: float(line.rsplit(" ",1)[1]) for line in text.splitlines() if line and not line.startswith("#")}
            with urllib.request.urlopen(args.origin+"/readyz", timeout=5) as response:
                row["ready_status"] = response.status
        except Exception as error:
            row["observation_error"] = type(error).__name__
        output.write(json.dumps(row)+"\n")
        output.flush()
        remaining = started+(index+1)*60-time.monotonic()
        if remaining > 0: time.sleep(remaining)
