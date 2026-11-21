"""Exercise the actual installer prompt in a PTY without installing anything."""
import errno
import os
import pathlib
import select
import shutil
import signal
import subprocess
import sys
import time
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
GIT_BASH = pathlib.Path('C:/Program Files/Git/bin/bash.exe')
BASH = shutil.which('bash') or (str(GIT_BASH) if GIT_BASH.is_file() else None)


@unittest.skipUnless(sys.platform.startswith('linux'), 'Requires Linux PTY')
class InstallerPromptTests(unittest.TestCase):
    def script(self, default, extra='', yes=False):
        source = (ROOT/'scripts/install-panel.sh').read_text()
        function = 'prompt() {' + source.split('\nprompt() {', 1)[1].split('\n}\n', 1)[0] + '\n}'
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
        choose = source[source.index('select_postgres_cluster() {'):source.index("\nlog '请选择安装配置")]
        configure = source[source.index('initial_pg_clusters=$(pg_lsclusters'):source.index('\nsystemctl enable postgresql')]
        script = r'''
set -Eeuo pipefail
PG_SOURCE=system
SERVICE=nekopass
log() { printf '%s\n' "$*"; }
die() { log "$*"; exit 1; }
port_ok() { [[ "$1" =~ ^[0-9]{1,5}$ ]] && ((10#$1 >= 1 && 10#$1 <= 65535)); }
postgres_installed() { [[ " $INSTALLED " == *" $1 "* ]]; }
pg_lsclusters() { printf '%s\n' "$CLUSTERS"; }
apt-cache() { printf '%s\n' 'postgresql-16 - PostgreSQL' 'postgresql-17 - PostgreSQL'; }
apt-get() {
 printf 'APT:%s\n' "$*"
 if [[ "$*" == 'install -y postgresql-17' ]]; then
  INSTALLED="$INSTALLED 17"
  [[ -z "$CREATED_BY_APT" ]] || CLUSTERS="$CREATED_BY_APT"
 fi
}
pg_createcluster() { printf 'CREATE:%s\n' "$*"; }
pg_ctlcluster() { printf 'START:%s\n' "$*"; }
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
            [BASH, '--noprofile', '--norc', '-c', script + choose + '\n' + configure],
            env={**os.environ, 'YES': '0', 'SKIP_DEPS': '0', 'PG_VERSION': '', 'DB_PORT': '',
                 'CLUSTERS': clusters, 'INSTALLED': '16 17', 'CHOICE': '', 'CREATED_BY_APT': '', **options},
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
        result = self.select_cluster('17 main 5432 online', CHOICE='2', SKIP_DEPS='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('PROMPT:PG_SOURCE', result.stdout)
        self.assertIn('CREATE:17 nekopass --port 5433 --start', result.stdout)
        self.assertNotIn('START:17 main', result.stdout)
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

    def test_no_existing_cluster_accepts_cluster_created_by_apt(self):
        result = self.select_cluster('', INSTALLED='', CREATED_BY_APT='17 main 5432 online')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('PROMPT:choice', result.stdout)
        self.assertIn('APT:install -y postgresql-17', result.stdout)
        self.assertIn('START:17 main start', result.stdout)
        self.assertNotIn('CREATE:', result.stdout)


if __name__ == '__main__':
    unittest.main()
