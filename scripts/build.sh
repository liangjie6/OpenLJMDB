#!/usr/bin/env bash
set -euo pipefail
mkdir -p dist
python3 scripts/licenses.py
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  target_os="${target%/*}"
  target_arch="${target#*/}"
  artifact="dist/ljmdb-${target_os}-${target_arch}"
  if [ "$target_os" = windows ]; then artifact="${artifact}.exe"; fi
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags='-s -w' -o "$artifact" ./cmd/ljmdb
done
python3 - <<'PY'
import hashlib
from pathlib import Path
artifacts = sorted(Path('dist').glob('ljmdb-*'))
Path('dist/SHA256SUMS').write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in artifacts))
import shutil
shutil.copytree('licenses', 'dist/licenses', dirs_exist_ok=True)
shutil.copyfile('THIRD_PARTY_NOTICES.md', 'dist/THIRD_PARTY_NOTICES.md')
PY
