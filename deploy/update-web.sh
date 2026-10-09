#!/usr/bin/env bash
# 发布已构建的前端，保留上一版本；校验失败时自动恢复。
set -euo pipefail
if [[ $EUID -ne 0 ]]; then
    echo 'Run with sudo bash deploy/update-web.sh' >&2
    exit 1
fi
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
test -f "$project/web/dist/index.html"
previous_release=$(readlink -f /var/www/openljmdb/current)
test -f "$previous_release/index.html"
nginx -t
release_stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
release_dir="/var/www/openljmdb/releases/$release_stamp"
backup_dir="/var/backups/openljmdb-deploy/$release_stamp"
link_path="/var/www/openljmdb/.current-$release_stamp"
install -d -m 755 "$release_dir" "$backup_dir"
curl --noproxy '*' --fail --silent http://127.0.0.1:9955/api/v1/health -o "$backup_dir/health-before.json"
cp -a "$project/web/dist/." "$release_dir/"
chown -R root:root "$release_dir"
chmod -R a+rX "$release_dir"
printf '%s\n' "$previous_release" > "$backup_dir/previous-release.txt"

rollback() {
    ln -sfn "$previous_release" "$link_path"
    mv -Tf "$link_path" /var/www/openljmdb/current
    echo 'Frontend verification failed; previous release restored.' >&2
}
trap rollback ERR
ln -s "$release_dir" "$link_path"
mv -Tf "$link_path" /var/www/openljmdb/current
curl --noproxy '*' --fail --silent http://127.0.0.1:9955/index.html -o "$backup_dir/served-index.html"
cmp "$release_dir/index.html" "$backup_dir/served-index.html"
for asset in "$release_dir"/assets/*.js "$release_dir"/assets/*.css; do
    curl --noproxy '*' --fail --silent "http://127.0.0.1:9955/assets/${asset##*/}" -o "$backup_dir/served-asset"
    cmp "$asset" "$backup_dir/served-asset"
done
curl --noproxy '*' --fail --silent http://127.0.0.1:9955/api/v1/health -o "$backup_dir/health-after.json"
curl --noproxy '*' --fail --silent 'http://[::1]:9955/api/v1/health' -o "$backup_dir/health-ipv6.json"
python3 - "$backup_dir" <<'PY'
import json
import pathlib
import sys
directory = pathlib.Path(sys.argv[1])
before = json.loads((directory / 'health-before.json').read_text())['data']
for name in ('health-after.json', 'health-ipv6.json'):
    after = json.loads((directory / name).read_text())['data']
    assert after['database_status'] == 'ready', after
    for key in ('instance_id', 'data_epoch'):
        assert before[key] == after[key], f'{key} changed during frontend deployment'
PY
systemctl is-active nginx ljmdb-backend nav-portal
trap - ERR
printf 'Deployed release: %s\nPrevious release: %s\n' "$release_dir" "$previous_release"
