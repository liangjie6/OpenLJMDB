#!/usr/bin/env python3
"""Collect unmodified module license/notice files from the locked Go build graph."""
import json
import shutil
import subprocess
from pathlib import Path

stream = subprocess.check_output(['go', 'list', '-m', '-json', 'all'], text=True)
decoder = json.JSONDecoder()
modules = []
while stream.strip():
    value, end = decoder.raw_decode(stream.lstrip())
    modules.append(value)
    stream = stream.lstrip()[end:]
root = Path('licenses')
root.mkdir(exist_ok=True)
rows = []
for module in modules:
    if module.get('Main') or not module.get('Dir'):
        continue
    directory = Path(module['Dir'])
    names = [p for p in directory.iterdir() if p.is_file() and (
        p.name.upper().startswith(('LICENSE', 'COPYING', 'NOTICE', 'COPYRIGHT')))]
    dest = root / (module['Path'].replace('/', '__') + '@' + module['Version'])
    dest.mkdir(exist_ok=True)
    for file in names:
        shutil.copyfile(file, dest / file.name)
    rows.append(f"| {module['Path']} | {module['Version']} | {', '.join(p.name for p in names) or 'See upstream source'} |")
(root / 'README.md').write_text('# Go module licenses\n\nUnmodified license and notice files from locked modules.\n\n'
    '| Module | Version | Files |\n|---|---|---|\n' + '\n'.join(rows) + '\n')
