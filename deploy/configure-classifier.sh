#!/usr/bin/env bash
# Configure the running systemd service without putting the API key in history.
set -euo pipefail
if [[ $EUID -ne 0 ]]; then
    echo '请运行 sudo bash deploy/configure-classifier.sh' >&2
    exit 1
fi
if [[ ! -t 0 ]]; then
    echo '请在交互式终端运行，以隐藏输入 API Key。' >&2
    exit 1
fi
classifier_url=${1:-}
if [[ ! "$classifier_url" =~ ^https://[a-zA-Z0-9.-]+(:[0-9]+)?/?$ ]]; then
    echo '服务地址必须为 HTTPS 站点根地址，不带 /v1。' >&2
    exit 1
fi
read -r -s -p '分类服务 API Key（输入不显示）: ' classifier_key
printf '\n'
LC_ALL=C
if [[ ! "$classifier_key" =~ ^[[:graph:]]+$ ]]; then
    echo 'API Key 不得为空或包含空白、非 ASCII 字符。' >&2
    exit 1
fi
# EnvironmentFile uses quoted values; escape literal backslashes and quotes.
classifier_key=${classifier_key//\\/\\\\}
classifier_key=${classifier_key//\"/\\\"}
umask 077
install -d -m 700 /etc/openljmdb
printf 'LJMDB_DECISION_BASE_URL="%s"\nLJMDB_DECISION_API_KEY="%s"\n' "$classifier_url" "$classifier_key" > /etc/openljmdb/classifier.env
chmod 600 /etc/openljmdb/classifier.env
unset classifier_key
install -d -m 755 /etc/systemd/system/ljmdb-backend.service.d
printf '[Service]\nEnvironmentFile=/etc/openljmdb/classifier.env\n' > /etc/systemd/system/ljmdb-backend.service.d/classifier.conf
chmod 644 /etc/systemd/system/ljmdb-backend.service.d/classifier.conf
systemctl daemon-reload
systemctl restart ljmdb-backend
systemctl is-active --quiet ljmdb-backend
echo '分类服务环境变量已配置，后端已重启。请在设置中开启“导入自动分类”。'
