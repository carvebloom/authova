#!/usr/bin/env python3
"""Start, stop or inspect the isolated passkey demonstration."""
from pathlib import Path
import runpy, sys
sys.argv.append("--profile=passkey")
runpy.run_path(str(Path(__file__).with_name("local_demo.py")),run_name="__main__")
