#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python3 scripts/migration/check-boundaries.py
python3 scripts/generate-ent.py --check
python3 scripts/generate-api.py --check
scripts/migration/go.sh vet ./...
python3 scripts/migration/quality.py --evidence ".migration/verify-$(date +%Y%m%d-%H%M%S)"
npm --prefix console/web run typecheck
npm --prefix console/web run lint
npm --prefix console/web run format:check
npm --prefix console/web test
npm --prefix console/web run build
printf '%s\n' 'Source, generated contracts, disposable PG/MySQL and frontend gates passed. Browser and opt-in TLS/endurance gates have separate evidence.'
