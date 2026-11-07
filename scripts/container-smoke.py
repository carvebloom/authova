#!/usr/bin/env python3
"""Run the isolated migration OCI smoke; no historical .dev/network assumptions."""
from pathlib import Path
import runpy
runpy.run_path(str(Path(__file__).with_name('migration')/'container-smoke.py'),run_name='__main__')
