"""Linux/root: run the bundled panel installer on a fresh isolated service.

Uses a dedicated database/role and free HTTP/gRPC ports. Existing installations
and the shared CLI are preserved; temporary services and accounts are removed.
"""
import argparse
import http.cookiejar
import json
import os
import pathlib
import re
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

SERVICE = 'nekopass-panel-freshfix'
DIRECTORIES = [pathlib.Path('/opt') / SERVICE, pathlib.Path('/etc') / SERVICE,
               pathlib.Path('/var/lib') / SERVICE,
               pathlib.Path('/etc/systemd/system') / (SERVICE + '.service.d')]
UNITS = [SERVICE + '.service', SERVICE + '-update.service', SERVICE + '-update.path']


def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def free_port():
    with socket.socket() as sock:
        sock.bind(('0.0.0.0', 0))
        return sock.getsockname()[1]


def sql(port, command, database='postgres'):
    return subprocess.check_output(['runuser', '-u', 'postgres', '--', 'psql',
                                    '-p', str(port), '-XAtq', '-v', 'ON_ERROR_STOP=1',
                                    '-d', database, '-c', command], cwd='/', text=True).strip()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--installer', type=pathlib.Path, required=True)
    parser.add_argument('--package', type=pathlib.Path, required=True)
    args = parser.parse_args()
    if os.geteuid() != 0 or not pathlib.Path('/run/systemd/system').is_dir():
        raise RuntimeError('Native systemd and root required')
    for directory in DIRECTORIES:
        assert directory.resolve() == directory and not directory.exists()
    for unit in UNITS:
        assert not (pathlib.Path('/etc/systemd/system') / unit).exists()
    assert subprocess.run(['id', '-u', SERVICE], capture_output=True).returncode != 0
    clusters = subprocess.check_output(['pg_lsclusters', '--no-header'], text=True).splitlines()
    version, _, db_port, *_ = next(line.split() for line in clusters if line.split()[3] == 'online')
    assert int(version) >= 14
    assert sql(db_port, "SELECT count(*) FROM pg_database WHERE datname='" + SERVICE + "'") == '0'
    assert sql(db_port, "SELECT count(*) FROM pg_roles WHERE rolname='" + SERVICE + "'") == '0'
    http_port, grpc_port = free_port(), free_port()
    while grpc_port == http_port:
        grpc_port = free_port()
    cli = pathlib.Path('/usr/local/bin/nekopassctl')
    assert not cli.is_symlink()
    live_pid = subprocess.check_output(['systemctl', 'show', 'nekopass.service', '-p', 'MainPID', '--value'])
    with tempfile.TemporaryDirectory(prefix='nekopass-panel-fresh-e2e-', dir='/var/tmp') as temporary:
        backup = pathlib.Path(temporary) / 'original-cli'
        original_stat = cli.stat() if cli.exists() else None
        if original_stat:
            shutil.copy2(cli, backup)
        try:
            result = subprocess.run(['bash', str(args.installer.resolve()), '--package', str(args.package.resolve()),
                                     '--service-name', SERVICE, '--host', '127.0.0.1',
                                     '--http-port', str(http_port), '--grpc-port', str(grpc_port),
                                     '--postgres-version', version, '--db-port', db_port,
                                     '--admin', 'fixture-admin', '--agent-tls', 'plain',
                                     '--firewall', 'skip', '--skip-dependencies', '--yes'],
                                    cwd='/root', capture_output=True, text=True, timeout=180)
            # Never print captured installer output: it contains a generated password.
            if result.returncode:
                raise RuntimeError('Fresh native installer failed (exit ' + str(result.returncode) + ')')
            assert 'could not change directory' not in result.stderr
            password = re.search(r'管理员密码：([^\r\n]+)', result.stdout)
            assert password and len(password[1]) == 32
            for unit in [SERVICE, SERVICE + '-update.path']:
                run(['systemctl', 'is-active', '--quiet', unit])
                assert subprocess.check_output(['systemctl', 'is-enabled', unit], text=True).strip() == 'enabled'
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            base = 'http://127.0.0.1:' + str(http_port)

            def api(path, data=None):
                request = urllib.request.Request(base + path,
                    data=json.dumps(data).encode() if data is not None else None,
                    headers={'Content-Type': 'application/json'})
                with opener.open(request, timeout=5) as response:
                    return json.load(response)

            assert api('/healthz')['status'] == 'ok'
            api('/api/v1/login', {'username': 'fixture-admin', 'password': password[1]})
            assert api('/api/v1/me')['is_admin'] is True
            settings = api('/api/v1/admin/settings')['settings']
            assert settings['captcha_mode'] == 'image' and settings['referral_mode'] == 'first'
            assert settings['referral_rate'] == '15' and settings['smtp']['port'] == 587
            assert settings['registration_email_verification'] is True
            assert '127.0.0.0/8' in settings['target_deny_cidrs']
            assert settings['agent_transport'] == 'plain' and settings['agent_port'] == grpc_port
            assert settings['panel_url'] == base
            with opener.open(base + '/admin/settings', timeout=5) as response:
                assert response.status == 200 and b'<html' in response.read().lower()
            run(['systemctl', 'restart', SERVICE])
            deadline = time.monotonic() + 10
            while True:
                try:
                    assert api('/healthz')['status'] == 'ok'
                    break
                except urllib.error.URLError:
                    if time.monotonic() >= deadline:
                        raise RuntimeError('Fresh panel did not return after restart')
                    time.sleep(.1)
            assert api('/api/v1/me')['is_admin'] is True
            assert subprocess.check_output(['systemctl', 'show', 'nekopass.service', '-p', 'MainPID', '--value']) == live_pid
            print('PASS fresh bundled installer: defaults, random administrator password/login, HTTP UI, native services, autostart and restart; existing panel unchanged')
        finally:
            for unit in UNITS:
                subprocess.run(['systemctl', 'disable', '--now', unit], capture_output=True)
            for unit in UNITS:
                file = pathlib.Path('/etc/systemd/system') / unit
                assert file.resolve() == file
                file.unlink(missing_ok=True)
            for directory in DIRECTORIES:
                assert directory.resolve() == directory and SERVICE in directory.name
                if directory.exists():
                    shutil.rmtree(directory)
            run(['systemctl', 'daemon-reload'])
            run(['runuser', '-u', 'postgres', '--', 'dropdb', '-p', db_port, '--if-exists', SERVICE], cwd='/')
            subprocess.run(['runuser', '-u', 'postgres', '--', 'dropuser', '-p', db_port, '--if-exists', SERVICE], cwd='/', check=True)
            if original_stat:
                shutil.copy2(backup, cli)
                os.chown(cli, original_stat.st_uid, original_stat.st_gid)
            else:
                cli.unlink(missing_ok=True)
            subprocess.run(['userdel', SERVICE], capture_output=True)
            subprocess.run(['groupdel', SERVICE], capture_output=True)


if __name__ == '__main__':
    main()
