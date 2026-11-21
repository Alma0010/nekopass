"""Exercise the bundled panel installer and uninstaller on Linux as root.

The fixture uses its own PostgreSQL database and role and allocates free HTTP
and gRPC ports. It removes the temporary service and accounts after the test.
"""
import argparse
import functools
import http.cookiejar
import http.server
import json
import os
import pathlib
import pty
import re
import shutil
import socket
import ssl
import stat
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

SERVICE = 'nekopass-panel-freshfix'
DIRECTORIES = [pathlib.Path('/opt') / SERVICE, pathlib.Path('/etc') / SERVICE,
               pathlib.Path('/var/lib') / SERVICE,
               pathlib.Path('/etc/systemd/system') / (SERVICE + '.service.d')]
UNITS = [SERVICE + '.service', SERVICE + '-update.service', SERVICE + '-update.path']
AGENT_SERVICES = ('nekopass-agent-removal1', 'nekopass-agent-removal2')
AGENT_UNIT_SUFFIXES = ('-uninstall.path', '-uninstall.service', '-update.path', '-update.service', '.service')


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


def agent_paths(service):
    return [pathlib.Path(parent) / service for parent in ('/etc', '/opt', '/var/lib')]


def agent_units(service):
    return [pathlib.Path('/etc/systemd/system') / (service + suffix) for suffix in AGENT_UNIT_SUFFIXES]


def update_lock(service):
    return pathlib.Path('/run/lock') / (service + '-update.lock')


def clean_agent_fixture():
    for service in AGENT_SERVICES:
        for unit in agent_units(service):
            subprocess.run(['systemctl', 'stop', unit.name], capture_output=True)
            subprocess.run(['systemctl', 'disable', unit.name], capture_output=True)
            assert unit.resolve() == unit
            unit.unlink(missing_ok=True)
        for directory in agent_paths(service):
            assert directory.resolve() == directory and directory.name == service
            if directory.exists():
                shutil.rmtree(directory)
        update_lock(service).unlink(missing_ok=True)
        subprocess.run(['userdel', service], capture_output=True)
        subprocess.run(['groupdel', service], capture_output=True)
    run(['systemctl', 'daemon-reload'])


def test_agent_uninstall(api, agent_installer, agent_binary, grpc_port, temporary):
    class QuietHandler(http.server.SimpleHTTPRequestHandler):
        def log_message(self, *_):
            pass

    for service in AGENT_SERVICES:
        for path in agent_paths(service) + agent_units(service) + [update_lock(service)]:
            assert path.resolve() == path and not path.exists()
        assert subprocess.run(['id', '-u', service], capture_output=True).returncode != 0
        assert subprocess.run(['getent', 'group', service], capture_output=True).returncode != 0

    fixture = pathlib.Path(temporary) / 'agent-download'
    fixture.mkdir(mode=0o700)
    shutil.copyfile(agent_binary, fixture / agent_binary.name)
    key, cert = pathlib.Path(temporary) / 'agent-download.key', pathlib.Path(temporary) / 'agent-download.crt'
    run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-sha256',
         '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1',
         '-keyout', str(key), '-out', str(cert)], capture_output=True)
    handler = functools.partial(QuietHandler, directory=str(fixture))
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(cert, key)

    def read_nodes():
        return {node['id']: node for node in api('/api/v1/admin/nodes')}

    def wait_for(check, message):
        deadline = time.monotonic() + 60
        while not check():
            if time.monotonic() >= deadline:
                raise RuntimeError(message)
            time.sleep(.25)

    with http.server.ThreadingHTTPServer(('127.0.0.1', 0), handler) as server:
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            installed_nodes = []
            for i, service in enumerate(AGENT_SERVICES):
                node = api('/api/v1/admin/nodes', {'name': service, 'enabled': i != 0,
                                                  'udp_idle_timeout_seconds': 30}, 'POST')
                installed_nodes.append(node['id'])
                url = 'https://127.0.0.1:' + str(server.server_port) + '/' + agent_binary.name
                with tempfile.NamedTemporaryFile(dir=temporary) as token_file:
                    token_file.write(node['token'].encode())
                    token_file.flush()
                    install = subprocess.run(['bash', str(agent_installer), '--service-name', service,
                        '--server', 'http://127.0.0.1:' + str(grpc_port), '--token-file', token_file.name,
                        '--binary-url', url, '--version', agent_binary.parent.name], capture_output=True,
                        env={**os.environ, 'CURL_CA_BUNDLE': str(cert)}, timeout=180)
                if install.returncode:
                    raise RuntimeError(f'Native Agent fixture failed to install {service} (exit {install.returncode})')

            def agents_ready():
                nodes = read_nodes()
                return all(nodes[node]['online'] and nodes[node]['uninstall_supported'] and
                           nodes[node]['applied_revision'] >= nodes[node]['config_revision']
                           for node in installed_nodes)

            wait_for(agents_ready, 'Native agents did not advertise automatic uninstall or finish syncing')
            target, sibling = AGENT_SERVICES
            target_node, sibling_node = installed_nodes
            sibling_pid = subprocess.check_output(['systemctl', 'show', sibling, '-p', 'MainPID', '--value'])
            assert sibling_pid.strip() not in (b'', b'0')
            sibling_config = (agent_paths(sibling)[0] / 'agent.env').read_bytes()
            result = api('/api/v1/admin/nodes/' + str(target_node), method='DELETE')
            assert result['uninstall_state'] == 'queued'

            def uninstall_confirmed():
                node = read_nodes().get(target_node)
                if node and (node['uninstall_status'] or {}).get('state') == 'failed':
                    raise RuntimeError('Native Agent reported an uninstall failure')
                return node is None

            wait_for(uninstall_confirmed, 'Panel did not receive Native Agent uninstall confirmation')
            for path in agent_paths(target) + agent_units(target):
                assert not path.exists()
            assert subprocess.run(['id', '-u', target], capture_output=True).returncode != 0
            assert subprocess.run(['systemctl', 'is-active', '--quiet', target]).returncode != 0
            for unit in (sibling, sibling + '-uninstall.path'):
                run(['systemctl', 'is-active', '--quiet', unit])
            assert subprocess.check_output(['systemctl', 'show', sibling, '-p', 'MainPID', '--value']) == sibling_pid
            assert (agent_paths(sibling)[0] / 'agent.env').read_bytes() == sibling_config
            assert read_nodes()[sibling_node]['online']
            print('PASS panel-triggered native Agent uninstall, true result, other Agent remains running')
        finally:
            server.shutdown()
            thread.join()
            clean_agent_fixture()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--installer', type=pathlib.Path, required=True)
    parser.add_argument('--package', type=pathlib.Path, required=True)
    parser.add_argument('--agent-installer', type=pathlib.Path)
    parser.add_argument('--agent-binary', type=pathlib.Path)
    args = parser.parse_args()
    if bool(args.agent_installer) != bool(args.agent_binary):
        parser.error('--agent-installer and --agent-binary must be supplied together')
    if os.geteuid() != 0 or not pathlib.Path('/run/systemd/system').is_dir():
        raise RuntimeError('Native systemd and root required')
    for directory in [*DIRECTORIES, update_lock(SERVICE)]:
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
            install_args = ['bash', str(args.installer.resolve()), '--package', str(args.package.resolve()),
                                     '--service-name', SERVICE, '--host', '127.0.0.1',
                                     '--http-port', str(http_port), '--grpc-port', str(grpc_port),
                                     '--postgres-version', version, '--db-port', db_port,
                                     '--admin', 'fixture-admin', '--agent-tls', 'plain',
                                     '--firewall', 'skip', '--skip-dependencies', '--yes']
            result = subprocess.run(install_args,
                                    cwd='/root', capture_output=True, text=True, timeout=180)
            # Never print captured installer output: it contains a generated password.
            if result.returncode:
                raise RuntimeError('Fresh native installer failed (exit ' + str(result.returncode) + ')')
            assert 'could not change directory' not in result.stderr
            password = re.search(r'管理员密码：([^\r\n]+)', result.stdout)
            assert password and len(password[1]) == 32
            assert result.stdout.count('管理员账号：fixture-admin') == 1
            for unit in [SERVICE, SERVICE + '-update.path']:
                run(['systemctl', 'is-active', '--quiet', unit])
                assert subprocess.check_output(['systemctl', 'is-enabled', unit], text=True).strip() == 'enabled'
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            base = 'http://127.0.0.1:' + str(http_port)

            def api(path, data=None, method=None):
                request = urllib.request.Request(base + path,
                    data=json.dumps(data).encode() if data is not None else None,
                    headers={'Content-Type': 'application/json'}, method=method)
                with opener.open(request, timeout=5) as response:
                    return json.load(response)

            assert api('/healthz')['status'] == 'ok'
            api('/api/v1/login', {'username': 'fixture-admin', 'password': password[1]})
            assert api('/api/v1/me')['is_admin'] is True
            assert sql(db_port, 'SELECT count(*) FROM users WHERE is_admin', SERVICE) == '1'
            settings = api('/api/v1/admin/settings')['settings']
            assert settings['captcha_mode'] == 'image' and settings['referral_mode'] == 'first'
            assert settings['referral_rate'] == '15' and settings['smtp']['port'] == 587
            assert settings['registration_email_verification'] is True
            assert '127.0.0.0/8' in settings['target_deny_cidrs']
            assert settings['agent_transport'] == 'plain' and settings['agent_port'] == grpc_port
            assert settings['panel_url'] == base
            with opener.open(base + '/admin/settings', timeout=5) as response:
                assert response.status == 200 and b'<html' in response.read().lower()
            config_file = pathlib.Path('/etc') / SERVICE / 'control.env'
            original_env = config_file.read_bytes()
            blocked = subprocess.run(install_args, cwd='/root', capture_output=True, text=True, timeout=180)
            assert blocked.returncode != 0 and '此服务已有配置' in blocked.stderr
            assert config_file.read_bytes() == original_env
            # Env alone decides whether this is an installed panel. Existing
            # binary/unit/database must allow retry, without losing the admin.
            settings['captcha_mode'] = 'off'
            settings['referral_mode'] = 'recurring'
            settings['referral_rate'] = '12.5'
            api('/api/v1/admin/settings', settings, 'PUT')
            assert config_file.resolve() == pathlib.Path('/etc') / SERVICE / 'control.env'
            config_file.unlink()
            retried = subprocess.run(install_args, cwd='/root', capture_output=True, text=True, timeout=180)
            if retried.returncode:
                diagnostic = args.installer.resolve().parent / 'reinstall-failure.log'
                diagnostic.write_text(retried.stdout + retried.stderr)
                diagnostic.chmod(0o600)
                raise RuntimeError('Native reinstall with missing env failed (exit ' + str(retried.returncode) + ')')
            assert '复用已有数据库' in retried.stdout and '已有管理员账号及密码已保留' in retried.stdout
            assert '管理员账号：' not in retried.stdout
            assert '管理员密码：' not in retried.stdout and config_file.read_bytes() == original_env
            api('/api/v1/login', {'username': 'fixture-admin', 'password': password[1]})
            retained = api('/api/v1/admin/settings')['settings']
            assert retained['captcha_mode'] == 'off' and retained['referral_mode'] == 'recurring'
            assert retained['referral_rate'] == '12.5'
            # Reset is explicit. Dry run and invalid input must leave it intact.
            planned = subprocess.run(install_args + ['--reinstall', '--dry-run'], capture_output=True, text=True, timeout=30)
            assert planned.returncode == 0 and config_file.read_bytes() == original_env
            assert api('/api/v1/admin/settings')['settings']['referral_rate'] == '12.5'
            invalid = subprocess.run(install_args + ['--reinstall', '--agent-installer', 'http://invalid.example.test/script'], capture_output=True, text=True, timeout=180)
            assert invalid.returncode != 0 and config_file.read_bytes() == original_env
            assert api('/api/v1/admin/settings')['settings']['referral_rate'] == '12.5'
            # Rejecting the destructive confirmation leaves the active panel intact.
            master, slave = pty.openpty()
            try:
                os.write(master, b'\n' * 11 + b'cancel\n')
                canceled = subprocess.run([v for v in install_args if v != '--yes'] + ['--reinstall'],
                                          stdin=slave, cwd='/root', capture_output=True, text=True, timeout=180)
            finally:
                os.close(slave)
                os.close(master)
            assert canceled.returncode != 0 and '已取消清空重装' in canceled.stderr
            assert config_file.read_bytes() == original_env
            assert api('/api/v1/admin/settings')['settings']['referral_rate'] == '12.5'
            agent_config = config_file.parent / 'agent.env'
            agent_config.write_text('NEKOPASS_SERVER=http://127.0.0.1:1\n')
            reset = subprocess.run(install_args + ['--reinstall'], cwd='/root', capture_output=True, text=True, timeout=180)
            if reset.returncode:
                diagnostic = args.installer.resolve().parent / 'reinstall-failure.log'
                diagnostic.write_text(reset.stdout + reset.stderr)
                diagnostic.chmod(0o600)
                raise RuntimeError('Explicit database reset failed (exit ' + str(reset.returncode) + ')')
            replacement_password = re.search(r'管理员密码：([^\r\n]+)', reset.stdout)
            assert replacement_password and replacement_password[1] != password[1]
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            api('/api/v1/login', {'username': 'fixture-admin', 'password': replacement_password[1]})
            clean = api('/api/v1/admin/settings')['settings']
            assert clean['captcha_mode'] == 'image' and clean['referral_mode'] == 'first' and clean['referral_rate'] == '15'
            assert agent_config.read_text() == 'NEKOPASS_SERVER=http://127.0.0.1:1\n'
            assert sql(db_port, 'SELECT count(*) FROM users', SERVICE) == '1'
            assert sql(db_port, 'SELECT count(*) FROM nodes', SERVICE) == '0'
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

            # Uninstall must keep the database, TLS files, and files shared with a local Agent.
            clean['referral_rate'] = '12.5'
            api('/api/v1/admin/settings', clean, 'PUT')
            panel_base, _, panel_state, panel_dropins = DIRECTORIES
            panel_bin = panel_base / 'bin'
            tls_dir = config_file.parent / 'tls'
            tls_dir.mkdir(mode=0o750, exist_ok=True)
            retained_files = {
                panel_bin / 'nekopass-agent': b'fixture Agent executable\n',
                panel_bin / 'nekopass-agent-update-source.json': b'{}\n',
                panel_state / 'state.db': b'fixture Agent state\n',
                tls_dir / 'server.crt': b'fixture TLS certificate\n',
            }
            for path, contents in retained_files.items():
                path.write_bytes(contents)
            retained_files[agent_config] = agent_config.read_bytes()
            shared_updater = (panel_bin / 'nekopass-update').read_bytes()
            shared_cli = cli.read_bytes()
            installed_config = config_file.read_bytes()
            admin_query = "SELECT id, md5(password_hash), is_admin FROM users WHERE username='fixture-admin'"
            retained_admin = sql(db_port, admin_query, SERVICE)
            assert retained_admin.endswith('|t')
            owner_query = "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='" + SERVICE + "'"
            assert sql(db_port, owner_query) == SERVICE
            service_uid = subprocess.check_output(['id', '-u', SERVICE], text=True)
            archive_pattern = '.uninstalled-*/control.env'
            uninstall_args = [str(cli), SERVICE, 'uninstall']

            run(uninstall_args + ['--dry-run'], capture_output=True, timeout=30)
            assert not list(config_file.parent.glob(archive_pattern))
            assert config_file.read_bytes() == installed_config
            run(['systemctl', 'is-active', '--quiet', SERVICE + '-update.path'])
            assert api('/api/v1/admin/settings')['settings']['referral_rate'] == '12.5'

            run(uninstall_args + ['--yes'], capture_output=True, timeout=60)
            for unit in UNITS:
                assert not (pathlib.Path('/etc/systemd/system') / unit).exists()
                assert subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode != 0
                assert subprocess.run(['systemctl', 'is-enabled', '--quiet', unit]).returncode != 0
            for path in (config_file, panel_bin / 'nekopass', panel_base / 'web', panel_dropins / 'update.conf'):
                assert not path.exists()
            archives = list(config_file.parent.glob(archive_pattern))
            assert len(archives) == 1
            archive = archives[0]
            assert archive.read_bytes() == installed_config
            assert stat.S_IMODE(archive.stat().st_mode) == 0o600
            assert stat.S_IMODE(archive.parent.stat().st_mode) == 0o700
            for path, contents in retained_files.items():
                assert path.read_bytes() == contents
            assert (panel_bin / 'nekopass-update').read_bytes() == shared_updater
            assert cli.read_bytes() == shared_cli
            assert sql(db_port, owner_query) == SERVICE
            assert sql(db_port, admin_query, SERVICE) == retained_admin
            assert subprocess.check_output(['id', '-u', SERVICE], text=True) == service_uid

            run(uninstall_args + ['--yes'], capture_output=True, timeout=30)
            assert list(config_file.parent.glob(archive_pattern)) == archives

            # A normal install brings the panel back using the existing DB and credentials.
            restored = run(install_args, cwd='/root', capture_output=True, text=True, timeout=180)
            assert '管理员密码：' not in restored.stdout
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            api('/api/v1/login', {'username': 'fixture-admin', 'password': replacement_password[1]})
            assert api('/api/v1/me')['is_admin'] is True
            assert api('/api/v1/admin/settings')['settings']['referral_rate'] == '12.5'
            assert sql(db_port, 'SELECT count(*) FROM users', SERVICE) == '1'
            assert sql(db_port, admin_query, SERVICE) == retained_admin
            assert archive.read_bytes() == installed_config
            if args.agent_installer:
                test_agent_uninstall(api, args.agent_installer.resolve(), args.agent_binary.resolve(), grpc_port, temporary)
                assert api('/healthz')['status'] == 'ok'
            assert subprocess.check_output(['systemctl', 'show', 'nekopass.service', '-p', 'MainPID', '--value']) == live_pid
            print('PASS native install, database reset, scoped uninstall, and reinstall with preserved data and Agent files')
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
            update_lock(SERVICE).unlink(missing_ok=True)
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
