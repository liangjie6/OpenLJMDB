#!/usr/bin/env python3
"""Exercise a built backend over real loopback HTTP in a temporary dataset."""
import json
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else 'dist/ljmdb').resolve())

def unused_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]

with tempfile.TemporaryDirectory(prefix='ljmdb-http-smoke-') as directory:
    port = unused_port()
    base = f'http://127.0.0.1:{port}/api/v1'
    process = subprocess.Popen([binary, '--data-dir', directory, '--port', str(port), '--no-open'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    epoch = ''

    def request(method, path, body=None, expected=200, key=None):
        headers = {'Content-Type': 'application/json', 'X-Data-Epoch': epoch}
        if key:
            headers['Idempotency-Key'] = key
        req = urllib.request.Request(base + path, data=json.dumps(body, ensure_ascii=False).encode() if body is not None else None, headers=headers, method=method)
        try:
            response = urllib.request.urlopen(req, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        raw = response.read()
        assert response.status == expected, (method, path, response.status, raw)
        return json.loads(raw) if raw else None

    def await_job(job_id):
        for _ in range(300):
            job = request('GET', '/jobs/' + job_id)['data']
            if job['state'] == 'succeeded':
                return job
            assert job['state'] not in ('failed', 'cancelled'), job
            time.sleep(0.05)
        raise AssertionError('job did not complete: ' + job_id)

    try:
        for attempt in range(100):
            try:
                health = request('GET', '/health')['data']
                break
            except (OSError, AssertionError):
                if process.poll() is not None:
                    raise AssertionError(process.communicate())
                time.sleep(0.05)
        else:
            raise AssertionError('backend readiness timed out')
        epoch = health['data_epoch']
        library = request('POST', '/knowledge-bases', {'name': 'HTTP 验收', 'description': '临时合成数据'}, 201, 'library')['data']
        duplicate = request('POST', '/knowledge-bases', {'name': 'HTTP 验收', 'description': '临时合成数据'}, 201, 'library')['data']
        assert duplicate['id'] == library['id']
        doc = request('POST', '/knowledge-bases/' + library['id'] + '/documents', {'parent_id': None, 'title': '稳定链接', 'markdown': '# 中文\n\n检索正文 100% <script>alert(1)</script>', 'expected_tree_revision': 1}, 201, 'document')['data']
        saved = request('PUT', '/documents/' + doc['id'], {'title': '稳定链接', 'markdown': '历史恢复保护\n\n检索正文 100%', 'expected_revision': 1, 'snapshot': True})['data']
        assert saved['revision'] == 2
        request('PUT', '/documents/' + doc['id'], {'title': '覆盖', 'markdown': '旧页面', 'expected_revision': 1}, 409)
        found = request('GET', '/search?q=' + urllib.parse.quote('检索'))
        assert found['meta']['total'] == 1
        assert request('GET', '/documents/' + doc['id'] + '/status')['data']['status'] == 'active'
        backup_id = request('POST', '/backups', {}, 202, 'backup')['data']['job_id']
        backup = await_job(backup_id)
        assert backup['download_url']
        validated = request('POST', '/backups/validate', {'backup_id': backup_id})['data']
        restore_body = {k: validated[k] for k in ('validated_backup_id', 'confirmation_token')}
        restore_id = request('POST', '/backups/restore', restore_body, 202, 'restore')['data']['job_id']
        restored = await_job(restore_id)
        assert restored['result']['reload_required']
        request('POST', '/knowledge-bases', {'name': '旧世代'}, 409)
        replay = request('POST', '/backups/restore', restore_body, 202, 'restore')['data']
        assert replay['job_id'] == restore_id
        after = request('GET', '/health')['data']
        assert after['instance_id'] == health['instance_id'] and after['data_epoch'] != epoch
        epoch = after['data_epoch']
        assert request('GET', '/documents/' + doc['id'])['data']['markdown'] == saved['markdown']
        assert len(request('GET', '/documents/' + doc['id'] + '/history')['data']) >= 1
        duplicate_process = subprocess.run([binary, '--data-dir', directory, '--port', str(unused_port()), '--no-open'], capture_output=True, text=True, timeout=10)
        assert duplicate_process.returncode != 0 and '占用' in duplicate_process.stderr
        nonloopback = subprocess.run([binary, '--data-dir', directory, '--host', '0.0.0.0', '--no-open'], capture_output=True, text=True, timeout=10)
        assert nonloopback.returncode != 0 and '回环' in nonloopback.stderr
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0))
            occupied.listen()
            unused_dir = str(Path(directory) / 'occupied-port')
            rejected = subprocess.run([binary, '--data-dir', unused_dir, '--port', str(occupied.getsockname()[1]), '--no-open'], capture_output=True, text=True, timeout=10)
            assert rejected.returncode != 0 and not Path(unused_dir).exists()
        print('PASS: real HTTP readiness, persistence, idempotency, conflict, Chinese search, history, backup/restore, epoch, restore replay, directory lock, loopback and occupied port')
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
        stdout, stderr = process.communicate(timeout=30)
        if process.returncode:
            print(stdout, stderr, file=sys.stderr)
