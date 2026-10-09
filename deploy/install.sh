#!/usr/bin/env bash
set -euo pipefail
if [[ $EUID -ne 0 ]]; then
    echo 'Run with sudo bash deploy/install.sh' >&2
    exit 1
fi
project=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
test -x "$project/deploy/bin/ljmdb"
test -f "$project/web/dist/index.html"
deploy_stamp=$(date -u +%Y%m%dT%H%M%SZ)
release_dir="/var/www/openljmdb/releases/$deploy_stamp"
backup_dir="/var/backups/openljmdb-deploy/$deploy_stamp"
install -d -m 755 "$release_dir" "$backup_dir"
for existing in /etc/nginx/sites-available/ljmdb.conf /etc/systemd/system/ljmdb-backend.service; do
    if [[ -f "$existing" ]]; then cp -a "$existing" "$backup_dir/"; fi
done
cp -a "$project/web/dist/." "$release_dir/"
chown -R root:root "$release_dir"
chmod -R a+rX "$release_dir"
if [[ -L /var/www/openljmdb/current ]]; then
    readlink /var/www/openljmdb/current > "$backup_dir/previous-release.txt"
fi
ln -s "$release_dir" /var/www/openljmdb/current.next
mv -Tf /var/www/openljmdb/current.next /var/www/openljmdb/current
install -m 644 "$project/deploy/ljmdb.nginx.conf" /etc/nginx/sites-available/ljmdb.conf
ln -sfn /etc/nginx/sites-available/ljmdb.conf /etc/nginx/sites-enabled/ljmdb.conf
nginx -t
install -m 644 "$project/deploy/ljmdb-backend.service" /etc/systemd/system/ljmdb-backend.service
systemctl daemon-reload
systemctl enable ljmdb-backend.service
systemctl restart ljmdb-backend.service
for attempt in {1..20}; do
    if curl --noproxy '*' --fail --silent http://127.0.0.1:19955/api/v1/health >/dev/null; then break; fi
    sleep 0.5
done
curl --noproxy '*' --fail --silent http://127.0.0.1:19955/api/v1/health >/dev/null
systemctl reload nginx
# IPv6 is already enabled in /etc/default/ufw on this host.
if ! rg -q '^IPV6=yes$' /etc/default/ufw; then
    echo 'UFW IPv6 is disabled; enable IPv6 before exposing this site.' >&2
    exit 1
fi
ufw allow 9955/tcp comment 'LJMDB Nginx IPv4 IPv6'
curl --noproxy '*' --fail --silent http://127.0.0.1:9955/api/v1/health
ufw status | rg '9955|Status'
systemctl is-active ljmdb-backend.service nginx.service
