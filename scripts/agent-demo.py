#!/usr/bin/env python3
"""Start, stop or inspect the isolated agent demonstration."""
from pathlib import Path
import runpy, sys
sys.argv.append("--profile=agent")
runpy.run_path(str(Path(__file__).with_name("local_demo.py")),run_name="__main__")
