#!/usr/bin/env python3
"""Initialize only the new migration instance; refuses existing key directories."""
from pathlib import Path
import subprocess
subprocess.run(['python3',str(Path(__file__).with_name('migration')/'instance.py'),'init'],check=True)
