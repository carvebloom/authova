#!/usr/bin/env python3
"""Verify the repository's sole Proto contract generation pipeline."""
from pathlib import Path
import subprocess
root=Path(__file__).resolve().parents[2]
subprocess.run(['python3',str(root/'scripts/generate-api.py'),'--check'],cwd=root,check=True)
