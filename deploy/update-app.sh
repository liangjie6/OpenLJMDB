#!/usr/bin/env bash
# Publish a verified backend and frontend without changing host configuration.
set -euo pipefail
if [[ $EUID -ne 0 ]]; then
    echo 'Run with sudo bash deploy/update-app.sh' >&2
    exit 1
fi
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
binary="$project/deploy/bin/ljmdb"
candidate="$project/deploy/bin/ljmdb.next"
test -x "$candidate"
test -f "$project/web/dist/index.html"
systemctl is-active --quiet ljmdb-backend nginx
release_stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
backup_dir="/var/backups/openljmdb-deploy/app-$release_stamp"
install -d -m 755 "$backup_dir"
previous_release=$(readlink -f /var/www/openljmdb/current)
backend_pid=$(systemctl show ljmdb-backend --property=MainPID --value)
cp -L "/proc/$backend_pid/exe" "$backup_dir/ljmdb"
chmod 755 "$backup_dir/ljmdb"
printf '%s\n' "$previous_release" > "$backup_dir/previous-release.txt"
curl --noproxy '*' --fail --silent http://127.0.0.1:9955/api/v1/health -o "$backup_dir/health-before.json"

rollback() {
    trap - ERR
    cp "$backup_dir/ljmdb" "$binary.rollback"
    chmod 755 "$binary.rollback"
    mv -Tf "$binary.rollback" "$binary"
    ln -sfn "$previous_release" "/var/www/openljmdb/.rollback-$release_stamp"
    mv -Tf "/var/www/openljmdb/.rollback-$release_stamp" /var/www/openljmdb/current
    systemctl restart ljmdb-backend
    echo "App verification failed; previous backend and frontend restored. Backup: $backup_dir" >&2
}
trap rollback ERR
mv -Tf "$candidate" "$binary"
systemctl restart ljmdb-backend
for attempt in {1..40}; do
    if curl --noproxy '*' --fail --silent http://127.0.0.1:19955/api/v1/health >/dev/null; then break; fi
    sleep 0.5
done
curl --noproxy '*' --fail --silent http://127.0.0.1:19955/api/v1/health >/dev/null
bash "$project/deploy/update-web.sh"
curl --noproxy '*' --fail --silent http://127.0.0.1:9955/api/v1/health -o "$backup_dir/health-after.json"
python3 - "$backup_dir" <<'PY'
import json
import pathlib
import sys
directory = pathlib.Path(sys.argv[1])
before = json.loads((directory / 'health-before.json').read_text())['data']
after = json.loads((directory / 'health-after.json').read_text())['data']
assert after['database_status'] == 'ready', after
for key in ('instance_id', 'data_epoch'):
    assert before[key] == after[key], f'{key} changed during app deployment'
PY
trap - ERR
printf 'App deployed; previous backend and frontend saved in: %s\n' "$backup_dir"
