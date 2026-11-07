#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# The migrated demo uses only new high ports, keys, state and Compose databases.
exec python3 scripts/local_demo.py start --profile passkey
