"""Run the real uninstaller against a temporary filesystem and mocked systemd/accounts."""
import contextlib
import fnmatch
import io
import os
import pathlib
import stat
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[1]
SOURCE = (ROOT / 'scripts/uninstall-agent.sh').read_text(encoding='utf-8').split("<<'NEKOPASS_UNINSTALL_PYTHON'\n", 1)[1].rsplit('\nNEKOPASS_UNINSTALL_PYTHON', 1)[0]


class UninstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name).resolve()
        self.module = types.ModuleType('uninstall_fixture')
        exec(compile(SOURCE, 'uninstall-agent.sh', 'exec'), self.module.__dict__)
        self.module.ROOT = self.root
        self.calls = []
        self.services = set()
        self.running_units = set()
        self.external_units = set()
        for directory in ['run/systemd/system', 'run/lock', 'etc/systemd/system', 'opt', 'var/lib', 'usr/local/bin']:
            (self.root / directory).mkdir(parents=True, exist_ok=True)
        self.write_mountinfo()
        fakepwd = types.ModuleType('pwd')
        fakepwd.getpwnam = lambda name: (_ for _ in ()).throw(KeyError(name))
        fakepwd.getpwall = lambda: []
        fakegrp = types.ModuleType('grp')
        fakegrp.getgrnam = lambda name: (_ for _ in ()).throw(KeyError(name))
        fakefcntl = types.ModuleType('fcntl')
        fakefcntl.LOCK_EX = 2
        fakefcntl.LOCK_NB = 4
        fakefcntl.flock = lambda *args: None
        self.pwd = fakepwd
        self.grp = fakegrp
        self.fcntl = fakefcntl
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.stack.enter_context(patch.dict(sys.modules, pwd=fakepwd, grp=fakegrp, fcntl=fakefcntl))
        self.stack.enter_context(patch.object(self.module.sys, 'platform', 'linux'))
        self.stack.enter_context(patch.object(self.module.os, 'geteuid', return_value=0, create=True))
        self.stack.enter_context(patch.object(self.module.shutil, 'which', return_value='/bin/systemctl'))
        self.stack.enter_context(patch.object(self.module, 'command', side_effect=self.command))
        self.stack.enter_context(patch.object(self.module.subprocess, 'run', side_effect=lambda args, **_: self.command(*args)))

    def command(self, *args, check=True):
        self.calls.append(args)
        output = ''
        if args[:2] in (('systemctl', 'list-unit-files'), ('systemctl', 'list-units')):
            if args[1] == 'list-unit-files':
                userspace = {item.name for item in (self.root / 'etc/systemd/system').iterdir()
                             if item.name.endswith(('.service', '.path'))}
                units = userspace | self.external_units
                state = 'enabled'
            else:
                units = self.running_units
                state = 'loaded active running fixture'
            patterns = [arg for arg in args[2:] if arg.startswith('nekopass')]
            output = ''.join(f'{unit} {state}\n' for unit in sorted(units)
                             if not patterns or any(fnmatch.fnmatchcase(unit, pattern) for pattern in patterns))
        elif args[:2] == ('systemctl', 'stop'):
            self.running_units.discard(args[2])
        elif args[:2] == ('systemctl', 'show'):
            unit = args[2]
            matching = next((service for service in self.services if unit in self.module.unit_names(service)), None)
            if args[4] == 'LoadState':
                installed = (self.root / 'etc/systemd/system' / unit).exists() or unit in self.external_units
                output = 'loaded' if installed or unit in self.running_units else 'not-found'
            elif args[4] == 'ActiveState':
                output = 'active' if unit in self.running_units else 'inactive'
            elif matching:
                base = '/opt/nekopass' if matching == 'nekopass-agent' else '/opt/' + matching
                binaries = {matching + '.service': 'nekopass-agent', matching + '-uninstall.service': 'nekopass-agent-uninstall'}
                output = 'path=' + base + '/bin/' + binaries.get(unit, 'nekopass-update') + ' ; argv[]=fixture'
        return subprocess.CompletedProcess(args, 0, output, '')

    def put(self, name, content='fixture'):
        target = self.root / name.lstrip('/')
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding='utf-8', newline='\n')
        return target

    def write_mountinfo(self, *points):
        entries = []
        for point in (pathlib.Path(self.root.anchor), *points):
            encoded = (point.as_posix().replace('\\', r'\134').replace(' ', r'\040')
                       .replace('\t', r'\011').replace('\n', r'\012'))
            entries.append(f'49 41 0:28 / {encoded} rw - ext4 /dev/fixture rw\n')
        self.put('/proc/self/mountinfo', ''.join(entries))

    def install(self, service):
        self.services.add(service)
        self.running_units.add(service + '.service')
        base = '/opt/nekopass' if service == 'nekopass-agent' else '/opt/' + service
        config = '/etc/nekopass' if service == 'nekopass-agent' else '/etc/' + service
        for name in ['nekopass-agent', 'nekopass-agent.previous', 'nekopass-agent-uninstall', 'nekopass-update', 'nekopass-agent-update-source.json']:
            self.put(base + '/bin/' + name)
        self.put(config + '/agent.env', 'NEKOPASS_NODE_TOKEN=fixture-secret-never-print')
        self.put(config + '/agent.env.bak.fixture')
        self.put('/var/lib/' + service + '/state.db')
        self.put('/var/lib/' + service + '/state.db.before-reconfigure-fixture')
        for unit in self.module.unit_names(service):
            self.put('/etc/systemd/system/' + unit)
            self.put('/etc/systemd/system/' + unit + '.d/override.conf')
        manager = self.put('/usr/local/bin/nekopassctl',
                           '#!/usr/bin/env bash\n# Native systemd service manager. No application credentials are executed as shell code.\n')
        manager.chmod(0o755)

    def run_uninstall(self, *args):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(self.module.main(list(args)), 0)
        self.assertNotIn('fixture-secret', output.getvalue())
        return output.getvalue()

    def test_default_purges_all_agents_but_preserves_colocated_panel(self):
        self.install('nekopass-agent')
        self.install('nekopass-agent-hk1')
        protected = [self.put('/opt/nekopass/bin/nekopass'), self.put('/etc/nekopass/control.env'),
                     self.put('/var/lib/nekopass/state.db'), self.put('/var/lib/postgresql/fixture'),
                     self.root / 'usr/local/bin/nekopassctl', self.root / 'opt/nekopass/bin/nekopass-update']
        self.run_uninstall()
        for name in protected:
            self.assertTrue(name.is_file(), name)
        for name in ['var/lib/nekopass-agent', 'opt/nekopass-agent-hk1', 'etc/nekopass/agent.env', 'opt/nekopass/bin/nekopass-agent.previous', 'opt/nekopass/bin/nekopass-agent-update-source.json']:
            self.assertFalse((self.root / name).exists(), name)
        self.assertIn(('systemctl', 'daemon-reload'), self.calls)
        self.assertFalse(any('nekopass.service' in call for call in self.calls))

    def test_named_service_removes_only_that_instance(self):
        self.install('nekopass-agent')
        self.install('nekopass-agent-hk1')
        self.run_uninstall('--service-name', 'nekopass-agent-hk1.service')
        self.assertFalse((self.root / 'opt/nekopass-agent-hk1').exists())
        self.assertTrue((self.root / 'etc/nekopass/agent.env').exists())
        self.assertTrue((self.root / 'usr/local/bin/nekopassctl').exists())
        self.assertFalse(any('nekopass-agent.service' in call for call in self.calls))

    def request(self, service, authorize=True):
        self.running_units.add(service + '-uninstall.service')
        binary = self.module.path(self.module.install_base(service) + '/bin/nekopass-agent')
        reporter_fixture = self.put('/staged-reporter-output')
        reporter_fixture.chmod(0o700)
        ticket = 'a' * 64
        results = []

        def send(args, env, fd):
            self.assertEqual(args[:3], [f'/proc/self/fd/{fd}', '-uninstall-result', ticket])
            self.assertEqual(env['NEKOPASS_NODE_TOKEN'], 'fixture-secret-never-print')
            results.append(args[3:])
            if args[-1] == '-uninstall-check':
                self.assertTrue(binary.exists())
                self.assertFalse(any(call[:2] == ('systemctl', 'stop') for call in self.calls))
                return authorize
            return True

        with contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(self.module, 'consume_request', return_value=ticket))
            stack.enter_context(patch.object(self.module, 'report_environment', return_value={'NEKOPASS_NODE_TOKEN': 'fixture-secret-never-print'}))
            stack.enter_context(patch.object(self.module, 'open_regular', side_effect=lambda *a, **kw: reporter_fixture.open('rb')))
            if os.name != 'posix':
                stack.enter_context(patch.object(self.module.os, 'fstat', return_value=types.SimpleNamespace(st_mode=stat.S_IXUSR)))
            stack.enter_context(patch.object(self.module.time, 'sleep'))
            stack.enter_context(patch.object(self.module, 'send_result', side_effect=send))
            stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
            stack.enter_context(contextlib.redirect_stderr(io.StringIO()))
            if not authorize:
                with self.assertRaisesRegex(RuntimeError, '确认卸载授权'):
                    self.module.main(['--service-name', service, '--request'])
                self.assertTrue(binary.exists())
            else:
                code = self.module.main(['--service-name', service, '--request'])
                self.assertEqual(binary.exists(), code != 0)
        return results

    def test_authorized_remote_request_removes_only_its_own_agent(self):
        service = 'nekopass-agent'
        sibling = 'nekopass-agent-hk1'
        self.install(service)
        self.install(sibling)
        retained = [self.put('/opt/nekopass/bin/nekopass'), self.put('/etc/nekopass/control.env'),
                    self.root / 'opt/nekopass/bin/nekopass-update', self.root / 'usr/local/bin/nekopassctl',
                    self.root / 'opt/nekopass-agent-hk1/bin/nekopass-agent']
        self.assertEqual(self.request(service), [['-uninstall-check'], []])
        for file in retained:
            self.assertTrue(file.is_file(), file)
        self.assertIn(('systemctl', 'stop', service + '-uninstall.path'), self.calls)
        self.assertNotIn(('systemctl', 'stop', service + '-uninstall.service'), self.calls)
        self.assertFalse(any(sibling in unit for call in self.calls for unit in call))
        for unit in self.module.unit_names(service):
            self.assertFalse((self.root / 'etc/systemd/system' / unit).exists())

    def test_unauthorized_or_busy_remote_request_leaves_agent_running(self):
        service = 'nekopass-agent-hk1'
        self.install(service)
        self.assertEqual(self.request(service, authorize=False), [['-uninstall-check']])
        with patch.object(self.fcntl, 'flock', side_effect=BlockingIOError):
            checked, failure = self.request(service)
        self.assertEqual(checked, ['-uninstall-check'])
        self.assertEqual(failure[0], '-uninstall-error')
        self.assertNotIn('fixture-secret', failure[1])
        self.assertFalse(any(call[:2] == ('systemctl', 'stop') for call in self.calls))

    def test_remote_request_is_claimed_before_it_is_read(self):
        service = 'nekopass-agent-hk1'
        ticket = 'b' * 64
        request = self.put('/var/lib/' + service + '/uninstall-request.json', '{"ticket":"' + ticket + '"}')
        self.pwd.getpwnam = lambda _: types.SimpleNamespace(pw_uid=999)

        def open_claimed(claimed, owner):
            self.assertFalse(request.exists())
            self.assertEqual(owner, 999)
            self.assertEqual(claimed.parent.parent, self.root / 'var/lib')
            return claimed.open('rb')

        with patch.object(self.module, 'open_regular', side_effect=open_claimed):
            self.assertEqual(self.module.consume_request(service), ticket)
        self.assertFalse(request.exists())
        self.assertEqual(list((self.root / 'var/lib').iterdir()), [request.parent])

    def test_full_uninstall_without_panel_removes_shared_helpers_and_is_idempotent(self):
        self.install('nekopass-agent')
        self.run_uninstall()
        self.assertFalse((self.root / 'opt/nekopass').exists())
        self.assertFalse((self.root / 'etc/nekopass').exists())
        self.assertFalse((self.root / 'usr/local/bin/nekopassctl').exists())
        self.services.clear()
        self.run_uninstall()

    def test_default_agent_uninstall_ignores_old_panel_archive_and_empty_install_root(self):
        self.install('nekopass-agent')
        archive = self.put('/etc/nekopass-panel-hk1/.uninstalled-fixture/control.env')
        (self.root / 'opt/nekopass-panel-hk1').mkdir()
        self.run_uninstall('--service-name', 'nekopass-agent')
        self.assertFalse((self.root / 'opt/nekopass').exists())
        self.assertFalse((self.root / 'usr/local/bin/nekopassctl').exists())
        self.assertTrue(archive.is_file())

    def test_default_agent_uninstall_keeps_default_updater_when_only_panel_config_remains(self):
        self.install('nekopass-agent')
        panel = self.put('/etc/nekopass/control.env')
        self.run_uninstall('--service-name', 'nekopass-agent')
        self.assertTrue(panel.is_file())
        self.assertTrue((self.root / 'opt/nekopass/bin/nekopass-update').exists())
        self.assertTrue((self.root / 'usr/local/bin/nekopassctl').exists())

    def test_default_agent_uninstall_keeps_default_updater_for_external_panel_unit(self):
        self.install('nekopass-agent')
        self.external_units.add('nekopass.service')
        self.run_uninstall('--service-name', 'nekopass-agent')
        self.assertTrue((self.root / 'opt/nekopass/bin/nekopass-update').exists())
        self.assertTrue((self.root / 'usr/local/bin/nekopassctl').exists())

    def test_default_agent_uninstall_removes_its_updater_if_only_named_panel_remains(self):
        self.install('nekopass-agent')
        panel = self.put('/etc/nekopass-panel-hk1/control.env')
        updater = self.put('/opt/nekopass-panel-hk1/bin/nekopass-update')
        self.run_uninstall('--service-name', 'nekopass-agent')
        self.assertFalse((self.root / 'opt/nekopass/bin/nekopass-update').exists())
        self.assertTrue(panel.is_file())
        self.assertTrue(updater.is_file())
        self.assertTrue((self.root / 'usr/local/bin/nekopassctl').exists())

    def test_remote_last_agent_cleans_managed_cli_but_not_its_running_report_unit(self):
        service = 'nekopass-agent-hk1'
        self.install(service)
        self.assertEqual(self.request(service), [['-uninstall-check'], []])
        self.assertFalse((self.root / 'usr/local/bin/nekopassctl').exists())
        self.assertIn(service + '-uninstall.service', self.running_units)

    def test_last_agent_preserves_a_replaced_manager(self):
        self.install('nekopass-agent-test')
        manager = self.put('/usr/local/bin/nekopassctl', '#!/bin/sh\nexec customctl "$@"\n')
        self.run_uninstall('--service-name', 'nekopass-agent-test')
        self.assertEqual(manager.read_text(), '#!/bin/sh\nexec customctl "$@"\n')

    def test_last_agent_preserves_manager_if_systemd_inventory_is_unavailable(self):
        self.install('nekopass-agent-test')
        original = self.command

        def incomplete(args, **_):
            if args[:2] == ['systemctl', 'list-units']:
                return subprocess.CompletedProcess(args, 1, '', 'unavailable')
            return original(*args)

        with patch.object(self.module.subprocess, 'run', side_effect=incomplete):
            self.run_uninstall('--service-name', 'nekopass-agent-test')
        self.assertTrue((self.root / 'usr/local/bin/nekopassctl').is_file())

    def test_mountpoint_in_dedicated_agent_tree_is_rejected_before_stopping(self):
        service = 'nekopass-agent-hk1'
        self.install(service)
        target = self.root / 'opt' / service
        preserved = self.put(f'/opt/{service}/mount point/data')
        for point in (target, preserved.parent):
            with self.subTest(point=point):
                self.write_mountinfo(point)
                with self.assertRaisesRegex(ValueError, '卸载目录包含挂载点'):
                    self.module.main(['--service-name', service])
                self.assertTrue(preserved.is_file())
                self.assertFalse(any(call[:2] == ('systemctl', 'stop') for call in self.calls))

    def test_unreadable_mount_inventory_is_rejected_before_stopping(self):
        service = 'nekopass-agent-hk1'
        self.install(service)
        (self.root / 'proc/self/mountinfo').unlink()
        with self.assertRaisesRegex(ValueError, '无法读取系统挂载信息'):
            self.module.main(['--service-name', service])
        self.assertFalse(any(call[:2] == ('systemctl', 'stop') for call in self.calls))

    def test_invalid_names_fail_before_any_system_operation(self):
        for name in ['nekopass', '../nekopass-agent', 'nekopass-agent-hk1;id', 'nekopass-agent-abc/def', 'nekopass-agent-' + 'a' * 17]:
            with self.assertRaises(ValueError):
                self.module.main(['--service-name', name])
        self.assertEqual(self.calls, [])

    def test_failed_stop_and_busy_updater_never_delete_files(self):
        self.install('nekopass-agent-hk1')
        original = self.module.command
        def fail_stop(*args, **kwargs):
            if args[:2] == ('systemctl', 'stop'):
                raise RuntimeError('fixture stop failure')
            return original(*args, **kwargs)
        with patch.object(self.module, 'command', side_effect=fail_stop), contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(RuntimeError):
                self.module.main([])
        self.assertTrue((self.root / 'var/lib/nekopass-agent-hk1/state.db').exists())
        with patch.object(self.fcntl, 'flock', side_effect=BlockingIOError), contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(RuntimeError):
                self.module.main([])
        self.assertTrue((self.root / 'var/lib/nekopass-agent-hk1/state.db').exists())

    def test_impostor_service_is_rejected_before_stopping(self):
        self.install('nekopass-agent-hk1')
        original = self.module.command
        def impostor(*args, **kwargs):
            result = original(*args, **kwargs)
            if args[:2] == ('systemctl', 'show') and args[4] == 'ExecStart':
                result.stdout = 'path=/opt/nekopass/bin/nekopass ; argv[]=fixture'
            return result
        with patch.object(self.module, 'command', side_effect=impostor):
            with self.assertRaises(ValueError):
                self.module.main([])
        self.assertFalse(any(call[1] == 'stop' for call in self.calls))

    def test_only_installer_system_accounts_are_deleted_without_home_removal(self):
        self.install('nekopass-agent-hk1')
        user = types.SimpleNamespace(pw_name='nekopass-agent-hk1', pw_uid=999, pw_gid=999, pw_dir='/nonexistent', pw_shell='/usr/sbin/nologin')
        self.pwd.getpwnam = lambda name: user
        self.pwd.getpwall = lambda: []
        self.grp.getgrnam = lambda name: types.SimpleNamespace(gr_gid=999, gr_mem=[])
        self.run_uninstall()
        self.assertIn(('userdel', 'nekopass-agent-hk1'), self.calls)
        self.assertIn(('groupdel', 'nekopass-agent-hk1'), self.calls)
        self.assertFalse(any('--remove' in call or '-r' in call for call in self.calls))

    def test_symlinked_installation_directory_is_rejected_before_stopping(self):
        target = self.root / 'opt/nekopass-agent-hk1'
        external = self.root / 'protected'
        external.mkdir()
        try:
            target.symlink_to(external, target_is_directory=True)
        except OSError:
            self.skipTest('Directory symlink creation unavailable')
        with self.assertRaises(ValueError):
            self.module.main([])
        self.assertTrue(external.exists())
        self.assertFalse(any(call[1] == 'stop' for call in self.calls))


if __name__ == '__main__':
    unittest.main()
