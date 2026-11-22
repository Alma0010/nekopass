"""Exercise the actual installer prompt in a PTY without installing anything."""
import contextlib
import errno
import io
import os
import pathlib
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[1]
GIT_BASH = pathlib.Path('C:/Program Files/Git/bin/bash.exe')
BASH = shutil.which('bash') or (str(GIT_BASH) if GIT_BASH.is_file() else None)


def installer_function(source, name):
    declaration = f'{name}() {{'
    return declaration + source.split('\n' + declaration, 1)[1].split('\n}\n', 1)[0] + '\n}'


@unittest.skipUnless(sys.platform.startswith('linux'), 'Requires Linux PTY')
class InstallerPromptTests(unittest.TestCase):
    def script(self, default, extra='', yes=False):
        source = (ROOT/'scripts/install-panel.sh').read_text()
        function = installer_function(source, 'prompt')
        # Test constants only; never evaluate user or credential content.
        return 'set -eu\nYES='+str(int(yes))+"\ndie() { printf 'ERROR\\n' >&2; exit 1; }\n" + function + "\nprompt HOST 'Address' '"+default+"'\nprintf '\\nRESULT:%s\\n' \"$HOST\"\n"+extra

    def interact(self, default, keys, tty_fallback=False):
        import pty
        pid, master = pty.fork()
        if pid == 0:
            if tty_fallback:
                # The controlling terminal remains available via /dev/tty.
                null = os.open('/dev/null', os.O_RDONLY)
                os.dup2(null, 0)
                os.close(null)
            os.execve('/bin/bash', ['bash', '--noprofile', '--norc', '-c', self.script(default)], {**os.environ, 'TERM': 'xterm-256color'})
        data = bytearray()
        sent = False
        end = time.monotonic()+8
        try:
            while time.monotonic()<end:
                if not select.select([master], [], [], .2)[0]:
                    continue
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not chunk:
                    break
                data.extend(chunk)
                if not sent and b'Address' in data:
                    os.write(master, keys+b'\r')
                    sent = True
            else:
                self.fail('Installer prompt hung')
            _, status = os.waitpid(pid, 0)
            pid = 0
            self.assertEqual(os.waitstatus_to_exitcode(status), 0)
            self.assertNotIn(b'^[[', data)
            return bytes(data).split(b'RESULT:', 1)[1].splitlines()[0].decode()
        finally:
            os.close(master)
            if pid:
                os.kill(pid, signal.SIGKILL)
                os.waitpid(pid, 0)

    def test_enter_preserves_default(self):
        self.assertEqual(self.interact('10.0.1.18', b''), '10.0.1.18')

    def test_arrow_and_delete_edit_default(self):
        # Left twice, forward-delete "1", insert "2", right, backspace "2", insert "3".
        keys=b'\x1b[D\x1b[D\x1b[3~2\x1b[C\x7f3'
        self.assertEqual(self.interact('10.0.1.18', keys), '10.0.1.23')

    def test_home_end_and_vertical_arrows(self):
        # Empty history must not insert arrow escape sequences into the value.
        keys=b'\x1b[A\x1b[B\x1b[H\x1b[3~2\x1b[F\x7f9'
        self.assertEqual(self.interact('10.0.1.18', keys), '20.0.1.19')

    def test_piped_stdin_uses_controlling_terminal(self):
        self.assertEqual(self.interact('8080', b'\x15' + b'9090', tty_fallback=True), '9090')

    def test_empty_input_falls_back_to_default(self):
        self.assertEqual(self.interact('8080', b'\x15'), '8080')

    def test_backslash_is_preserved(self):
        self.assertEqual(self.interact('', b'fixture\\path'), 'fixture\\path')

    def test_yes_does_not_read_stdin(self):
        result=subprocess.run(['bash', '-c', self.script('8080', "read -r remainder; printf 'UNREAD:%s\\n' \"$remainder\"\n", yes=True)],input='fixture-piped-input\n',capture_output=True,text=True,timeout=5)
        self.assertEqual(result.returncode,0)
        self.assertIn('RESULT:8080',result.stdout)
        self.assertIn('UNREAD:fixture-piped-input',result.stdout)


@unittest.skipUnless(BASH, 'Requires Bash')
class PostgreSQLSelectionTests(unittest.TestCase):
    def select_cluster(self, clusters, **options):
        source = (ROOT / 'scripts/install-panel.sh').read_text(encoding='utf-8')
        functions = '\n'.join(installer_function(source, name) for name in (
            'select_postgres_cluster', 'select_database',
            'ensure_apt_indexes', 'configure_native_postgres',
        ))
        configure = '''
select_database
if ((!external_database)); then configure_native_postgres; fi
printf 'DATABASE_MODE:%s\n' "$external_database"
printf 'DATABASE_PORT:%s\n' "$DB_PORT"
'''
        script = r'''
set -Eeuo pipefail
PG_SOURCE=system
SERVICE=nekopass
APT_REFRESHED=0
apt_indexes_ready=0
external_database=0
log() { printf '%s\n' "$*"; }
die() { log "$*"; exit 1; }
port_ok() { [[ "$1" =~ ^[0-9]{1,5}$ ]] && ((10#$1 >= 1 && 10#$1 <= 65535)); }
postgres_installed() { [[ " $INSTALLED " == *" $1 "* ]]; }
installed_postgres_versions() { printf '%s\n' $INSTALLED; }
postgres_port_in_use() { [[ " $UNMANAGED_PORTS " == *" $1 "* ]]; }
pg_lsclusters() { printf '%s\n' "$CLUSTERS"; }
apt-cache() {
 ((APT_REFRESHED)) || return 1
 printf '%s\n' 'postgresql-16 - PostgreSQL' 'postgresql-17 - PostgreSQL';
}
apt-get() {
 printf 'APT:%s\n' "$*"
 if [[ "$*" == update ]]; then APT_REFRESHED=1; fi
 if [[ "$*" == 'install -y postgresql-17' ]]; then
  INSTALLED="$INSTALLED 17"
  [[ -z "$CREATED_BY_APT" ]] || CLUSTERS="$CREATED_BY_APT"
 fi
}
pg_createcluster() { printf 'CREATE:%s\n' "$*"; }
pg_ctlcluster() { printf 'START:%s\n' "$*"; }
pg_isready() { return 0; }
prompt() {
 local response=$3
 if ((!YES)); then
  printf 'PROMPT:%s\n' "$1"
  if [[ "$1" == choice && -n "$CHOICE" ]]; then
   response=$CHOICE
   CHOICE=''
  fi
 fi
 printf -v "$1" '%s' "$response"
}
'''
        return subprocess.run(
            [BASH, '--noprofile', '--norc', '-s'],
            input=script + functions + '\n' + configure,
            env={**os.environ, 'YES': '0', 'SKIP_DEPS': '0', 'PG_VERSION': '', 'DB_PORT': '',
                 'CLUSTERS': clusters, 'INSTALLED': '16 17', 'CHOICE': '', 'CREATED_BY_APT': '',
                 'UNMANAGED_PORTS': '', 'DATABASE_URL_FILE': '', 'REINSTALL': '0', **options},
            capture_output=True, encoding='utf-8', timeout=10,
        )

    def test_menu_prefers_running_cluster_and_skips_database_install(self):
        result = self.select_cluster('16 standby 5434 online,recovery\n16 backup 5433 down\n17 main 5432 online')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('1) 沿用 PostgreSQL 16 · backup · 5433（已停止）', result.stdout)
        self.assertIn('2) 沿用 PostgreSQL 17 · main · 5432（运行中）', result.stdout)
        self.assertIn('3) 安装或创建新的 PostgreSQL 集群', result.stdout)
        self.assertNotIn('standby', result.stdout)
        self.assertNotIn('PROMPT:PG_SOURCE', result.stdout)
        self.assertNotIn('APT:', result.stdout)
        self.assertIn('START:17 main start', result.stdout)

    def test_new_cluster_uses_another_port_even_if_version_installed(self):
        for skip_deps in ('0', '1'):
            with self.subTest(skip_dependencies=skip_deps):
                result = self.select_cluster('17 main 5432 online', CHOICE='2', SKIP_DEPS=skip_deps)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertNotIn('PROMPT:PG_SOURCE', result.stdout)
                self.assertIn('CREATE:17 nekopass --port 5433 --start', result.stdout)
                self.assertNotIn('START:17 main', result.stdout)
                self.assertNotIn('APT:', result.stdout)

    def test_installed_server_without_cluster_can_create_one_offline(self):
        result = self.select_cluster('', INSTALLED='16', SKIP_DEPS='1')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('CREATE:16 nekopass --port 5432 --start', result.stdout)
        self.assertNotIn('PROMPT:PG_SOURCE', result.stdout)
        self.assertNotIn('APT:', result.stdout)

    def test_standby_only_uses_separate_cluster_with_installed_server(self):
        result = self.select_cluster('17 standby 5432 online,recovery', INSTALLED='17')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('CREATE:17 nekopass --port 5433 --start', result.stdout)
        self.assertNotIn('START:17 standby', result.stdout)
        self.assertNotIn('APT:', result.stdout)

    def test_explicit_version_and_port_honor_existing_stopped_cluster(self):
        result = self.select_cluster('16 backup 5433 down\n17 main 5432 online', YES='1', PG_VERSION='16', DB_PORT='5433')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('START:16 backup start', result.stdout)
        self.assertNotIn('检测到本机 PostgreSQL 集群', result.stdout)
        self.assertNotIn('PROMPT:', result.stdout)
        self.assertNotIn('APT:', result.stdout)

    def test_explicit_version_filters_interactive_cluster_choices(self):
        result = self.select_cluster('16 main 5432 online\n16 backup 5433 down\n17 test 5434 online', PG_VERSION='16', CHOICE='2')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('START:16 backup start', result.stdout)
        self.assertNotIn('17 · test', result.stdout)

    def test_port_claimed_by_another_major_fails_before_apt(self):
        result = self.select_cluster('17 main 5432 online', INSTALLED='17', YES='1', PG_VERSION='16', DB_PORT='5432')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('APT:', result.stdout)
        self.assertNotIn('CREATE:', result.stdout)
        self.assertNotIn('START:', result.stdout)

    def test_unmanaged_database_listener_aborts_before_bootstrapping_server(self):
        result = self.select_cluster('', INSTALLED='', YES='1', UNMANAGED_PORTS='5432')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('--database-url-file', result.stdout + result.stderr)
        self.assertNotIn('APT:', result.stdout)
        self.assertNotIn('CREATE:', result.stdout)

    def test_unmanaged_database_listener_uses_external_mode_interactively(self):
        result = self.select_cluster('', INSTALLED='', UNMANAGED_PORTS='5432')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('DATABASE_MODE:1', result.stdout)
        self.assertIn('DATABASE_PORT:5432', result.stdout)
        self.assertNotIn('APT:', result.stdout)
        self.assertNotIn('CREATE:', result.stdout)

    def test_explicit_unmanaged_port_uses_external_mode(self):
        result = self.select_cluster('17 main 5432 online', DB_PORT='5544', UNMANAGED_PORTS='5544')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('DATABASE_MODE:1', result.stdout)
        self.assertIn('DATABASE_PORT:5544', result.stdout)
        self.assertNotIn('START:', result.stdout)

    def test_default_prefers_eligible_native_cluster_on_another_port(self):
        result = self.select_cluster('17 main 5433 online', UNMANAGED_PORTS='5432')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('DATABASE_MODE:0', result.stdout)
        self.assertIn('DATABASE_PORT:5433', result.stdout)
        self.assertIn('START:17 main start', result.stdout)

    def test_menu_can_select_docker_postgres_with_native_cluster_present(self):
        result = self.select_cluster('17 main 5433 online', CHOICE='3', UNMANAGED_PORTS='5432')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('DATABASE_MODE:1', result.stdout)
        self.assertIn('DATABASE_PORT:5432', result.stdout)
        self.assertNotIn('START:', result.stdout)

    def test_url_file_selects_external_mode_without_touching_registered_cluster(self):
        result = self.select_cluster('17 main 5432 online', YES='1', DATABASE_URL_FILE='/root/fixture-db-url')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('DATABASE_MODE:1', result.stdout)
        self.assertNotIn('APT:', result.stdout)
        self.assertNotIn('START:', result.stdout)

    def test_external_mode_refuses_destructive_reinstall(self):
        result = self.select_cluster('', YES='1', DATABASE_URL_FILE='/root/fixture-db-url', REINSTALL='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('--reinstall', result.stdout + result.stderr)
        self.assertNotIn('APT:', result.stdout)
        self.assertNotIn('CREATE:', result.stdout)

    def test_no_existing_cluster_accepts_cluster_created_by_apt(self):
        result = self.select_cluster('', INSTALLED='', CREATED_BY_APT='17 main 5432 online')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('PROMPT:choice', result.stdout)
        self.assertRegex(result.stdout, r'APT:update\n(?:[^\n]*\n)*APT:install -y postgresql-17')
        self.assertIn('START:17 main start', result.stdout)
        self.assertNotIn('CREATE:', result.stdout)


@unittest.skipUnless(BASH, 'Requires Bash')
class InstallerDependencyTests(unittest.TestCase):
    def prepare_dependencies(self, *, missing='', skip=False, ca_present=True,
                             external=False, client_ready=True):
        source = (ROOT / 'scripts/install-panel.sh').read_text(encoding='utf-8')
        names = ('ensure_apt_indexes', 'prepare_install_dependencies')
        functions = '\n'.join(installer_function(source, name) for name in names)
        fixture = r'''
set -Eeuo pipefail
apt_indexes_ready=0
log() { printf '%s\n' "$*"; }
die() { printf '%s\n' "$*" >&2; exit 1; }
command() {
 [[ "$1" == -v ]] || return 1
 [[ " $MISSING_TOOLS " != *" $2 "* ]]
}
psql() { [[ "$*" == --version && "$CLIENT_READY" == 1 ]]; }
apt-get() {
 printf 'APT:%s\n' "$*"
 if [[ "$1" == install ]]; then MISSING_TOOLS=''; fi
 if [[ "$*" == 'install -y postgresql-client' ]]; then CLIENT_READY=1; fi
 if [[ "$*" == 'install -y ca-certificates' ]]; then printf 'Fixture CA\n' > "$FIXTURE_CA_BUNDLE"; fi
}
'''
        with tempfile.TemporaryDirectory() as temporary:
            ca_bundle = pathlib.Path(temporary) / 'ca-bundle.crt'
            if ca_present:
                ca_bundle.write_text('fixture CA bundle\n', encoding='utf-8')
            return subprocess.run(
                [BASH, '--noprofile', '--norc', '-c',
                 fixture + functions + '\nprepare_install_dependencies "$FIXTURE_CA_BUNDLE"\n'],
                env={**os.environ, 'MISSING_TOOLS': missing, 'SKIP_DEPS': str(int(skip)),
                     'FIXTURE_CA_BUNDLE': ca_bundle.as_posix(), 'external_database': str(int(external)),
                     'CLIENT_READY': str(int(client_ready))},
                capture_output=True, encoding='utf-8', timeout=10,
            )

    def test_available_dependencies_skip_apt_by_default(self):
        result = self.prepare_dependencies()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn('APT:', result.stdout)

    def test_only_missing_dependency_is_installed(self):
        result = self.prepare_dependencies(missing='openssl')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual([line for line in result.stdout.splitlines() if line.startswith('APT:')],
                         ['APT:update', 'APT:install -y openssl'])

    def test_missing_ca_bundle_installs_only_ca_package(self):
        result = self.prepare_dependencies(ca_present=False)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual([line for line in result.stdout.splitlines() if line.startswith('APT:')],
                         ['APT:update', 'APT:install -y ca-certificates'])

    def test_skip_dependencies_fails_on_missing_tools_without_using_apt(self):
        result = self.prepare_dependencies(missing='openssl', skip=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('openssl', result.stdout + result.stderr)
        self.assertNotIn('APT:', result.stdout)

    def test_external_mode_bypasses_native_cluster_tools(self):
        result = self.prepare_dependencies(missing='pg_lsclusters pg_createcluster pg_ctlcluster',
                                           external=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn('APT:', result.stdout)

    def test_external_mode_installs_only_missing_postgresql_client(self):
        result = self.prepare_dependencies(missing='pg_lsclusters pg_createcluster pg_ctlcluster',
                                           external=True, client_ready=False)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual([line for line in result.stdout.splitlines() if line.startswith('APT:')],
                         ['APT:update', 'APT:install -y postgresql-client'])

    def test_external_mode_respects_skip_dependencies(self):
        result = self.prepare_dependencies(external=True, client_ready=False, skip=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('APT:', result.stdout)


class ExternalPostgresConfigTests(unittest.TestCase):
    def setUp(self):
        source = (ROOT / 'scripts/install-panel.sh').read_text(encoding='utf-8')
        shell = installer_function(source, 'external_postgres')
        self.payload = shell.split("<<'PY'\n", 1)[1].rsplit('\nPY\n', 1)[0]
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.config = pathlib.Path(self.temporary.name) / 'control.env'

    def run_external(self, *args):
        with patch.object(sys, 'argv', ['-', str(self.config), *args]):
            exec(compile(self.payload, 'install-panel.sh:external_postgres', 'exec'), {})

    def configure(self):
        with patch.dict(os.environ, {'NEKOPASS_INSTALL_DB_PASSWORD': r'fixture:@\ secret'}):
            with self.assertRaises(SystemExit) as exit_status:
                self.run_external('configure', '', '5544', 'fixture db', 'fixture:owner')
        self.assertEqual(exit_status.exception.code, 0)

    def test_interactive_password_is_encoded_in_config(self):
        self.configure()
        dsn = self.config.read_text(encoding='utf-8').splitlines()[0]
        self.assertEqual(
            dsn,
            'NEKOPASS_DATABASE_URL=postgres://fixture%3Aowner:fixture%3A%40%5C%20secret'
            '@127.0.0.1:5544/fixture%20db?sslmode=disable',
        )

    def test_query_uses_passfile_and_scrubs_inherited_postgres_environment(self):
        self.configure()
        passfile = io.StringIO()
        passfile.name = str(self.config.with_name('fixture-pgpass'))

        def query(args, *, env, **_):
            self.assertEqual(args[:3], ['psql', '-w', '-XAtq'])
            self.assertEqual(env['PGHOST'], '127.0.0.1')
            self.assertEqual(env['PGPORT'], '5544')
            self.assertEqual(env['PGUSER'], 'fixture:owner')
            self.assertEqual(env['PGDATABASE'], 'fixture db')
            self.assertEqual(env['PGPASSFILE'], passfile.name)
            self.assertNotIn('PGPASSWORD', env)
            self.assertNotIn('PGSERVICE', env)
            self.assertNotIn('secret', ' '.join(args))
            self.assertEqual(passfile.getvalue(),
                             r'127.0.0.1:5544:fixture db:fixture\:owner:fixture\:@\\ secret' + '\n')
            return subprocess.CompletedProcess(args, 0, stdout='ok\n')

        with (patch.dict(os.environ, {'PGHOST': 'ignored', 'PGPASSWORD': 'ignored', 'PGSERVICE': 'ignored'}),
              patch.object(tempfile, 'NamedTemporaryFile', return_value=passfile),
              patch.object(subprocess, 'run', side_effect=query)):
            self.run_external('check')

    def test_query_refuses_unsafe_database_before_setup(self):
        self.configure()
        for state, message in (('readonly', '可写'), ('shared', '专用数据库')):
            with self.subTest(state=state):
                result = subprocess.CompletedProcess(['psql'], 0, stdout=state + '\n')
                with patch.object(subprocess, 'run', return_value=result), self.assertRaises(SystemExit) as error:
                    self.run_external('check')
                self.assertIn(message, error.exception.code)

    def test_failed_database_probe_hides_client_stderr(self):
        self.configure()
        result = subprocess.CompletedProcess(['psql'], 1, stdout='', stderr='fixture credential')
        with patch.object(subprocess, 'run', return_value=result), self.assertRaises(SystemExit) as error:
            self.run_external('check')
        self.assertNotIn('fixture credential', error.exception.code)

    def test_rejects_non_loopback_address_before_invoking_client(self):
        self.configure()
        self.config.write_text(self.config.read_text(encoding='utf-8').replace('127.0.0.1', '192.0.2.1'),
                               encoding='utf-8')
        with patch.object(subprocess, 'run') as query, self.assertRaises(SystemExit):
            self.run_external('check')
        query.assert_not_called()

    def test_rejects_database_name_that_pgx_would_trim(self):
        with (patch.dict(os.environ, NEKOPASS_INSTALL_DB_PASSWORD='fixture-password'),
              self.assertRaises(SystemExit) as error):
            self.run_external('configure', '', '5544', '/fixture db', 'fixture:owner')
        self.assertIn('数据库名不能以 / 开头', error.exception.code)
        self.assertFalse(self.config.exists())

        self.configure()
        config = self.config.read_text(encoding='utf-8')
        self.config.write_text(config.replace('/fixture%20db?', '/%2Ffixture%20db?'), encoding='utf-8')
        with patch.object(subprocess, 'run') as query, self.assertRaises(SystemExit) as error:
            self.run_external('check')
        self.assertIn('数据库名不能以 / 开头', error.exception.code)
        query.assert_not_called()

    def test_queries_existing_administrators(self):
        self.configure()
        output = io.StringIO()
        result = subprocess.CompletedProcess(['psql'], 0, stdout='3\n')
        with patch.object(subprocess, 'run', return_value=result) as query, contextlib.redirect_stdout(output):
            self.run_external('admins')
        self.assertEqual(output.getvalue(), '3\n')
        self.assertIn('SELECT count(*) FROM public.users WHERE is_admin', query.call_args.args[0])


if __name__ == '__main__':
    unittest.main()
