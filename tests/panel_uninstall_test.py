"""Exercise the panel uninstaller with a fixture filesystem and mocked systemd."""
import contextlib
import io
import pathlib
import stat
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import patch


SOURCE = (pathlib.Path(__file__).resolve().parents[1] / 'scripts/nekopassctl.sh').read_text(encoding='utf-8')
PAYLOAD = SOURCE.split("<<'NEKOPASS_PANEL_UNINSTALL_PYTHON'\n", 1)[1].split('\nparser = ', 1)[0]


class PanelUninstallTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = pathlib.Path(temp.name).resolve()
        self.module = types.ModuleType('panel_uninstall')
        with patch.dict(sys.modules, fcntl=types.ModuleType('fcntl')):
            exec(compile(PAYLOAD, 'nekopassctl.sh', 'exec'), self.module.__dict__)
        self.calls = []
        self.units = {}
        self.install('nekopass-panel-test')
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        stack.enter_context(patch.object(self.module, 'check_web_mounts'))
        stack.enter_context(patch.object(self.module, 'update_lock', return_value=contextlib.nullcontext()))
        self.real_safe_path = self.module.safe_path
        stack.enter_context(patch.object(self.module, 'safe_path', side_effect=self.path_exists))
        stack.enter_context(patch.object(self.module.subprocess, 'run', side_effect=self.systemctl))
        self.cleanup_manager = stack.enter_context(patch.object(self.module, 'cleanup_manager', create=True))

    def install(self, service):
        self.service = service
        self.config = self.put(f'etc/{self.service}/control.env', 'secret-fixture')
        self.binary = self.put(f'opt/{self.service}/bin/nekopass')
        self.asset = self.put(f'opt/{self.service}/web/assets/main.js')
        self.helper = self.put(f'opt/{self.service}/bin/nekopass-update')
        self.manager = self.put('usr/local/bin/nekopassctl', '#!/usr/bin/env bash\n# Native systemd service manager. No application credentials are executed as shell code.\n')
        self.tls = self.put(f'etc/{self.service}/tls/server.key')
        self.result = self.put(f'var/lib/{self.service}/update-result.json')
        self.request = self.put(f'var/lib/{self.service}/update-request.json')
        self.ready = self.put(f'var/lib/{self.service}/panel-update-ready')
        self.watcher = self.put(f'etc/systemd/system/{self.service}-update.path',
                                f'[Path]\nPathExists={self.request}\nUnit={self.service}-update.service\n')
        self.updater = self.put(f'etc/systemd/system/{self.service}-update.service')
        self.main = self.put(f'etc/systemd/system/{self.service}.service')
        self.dropin = self.put(f'etc/systemd/system/{self.service}.service.d/update.conf',
                              f'[Service]\nStateDirectory={self.service}\n'
                              f'Environment=NEKOPASS_PANEL_UPDATE_DIR={self.request.parent}\n')
        self.override = self.put(f'etc/systemd/system/{self.service}.service.d/override.conf')
        self.units[self.main.name] = dict(User=self.service, EnvironmentFiles=f'{self.config} (ignore_errors=no)',
                                         ExecStart=self.start(self.binary, '-web', self.asset.parents[1], '-http', ':9443'))
        alias = 'panel' if service == 'nekopass' else service
        self.units[self.updater.name] = dict(ExecStart=self.start(self.manager, alias, 'update'))
        self.units[self.watcher.name] = dict(Paths=f'{self.request} (PathExists)', Unit=self.updater.name,
                                             NeedDaemonReload='no')

    def put(self, path, content='fixture'):
        dest = self.root / path
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(content, encoding='utf-8')
        return dest

    def path_exists(self, path, root, *, directory=False, state_file=False):
        self.assertTrue(path.is_relative_to(root))
        return path.is_dir() if directory else path.is_file()

    def start(self, binary, *args):
        return '{ path=' + str(binary) + ' ; argv[]=' + ' '.join(map(str, (binary, *args))) + ' ; }'

    def systemctl(self, command, **kwargs):
        self.assertEqual(command[0], 'systemctl')
        action = command[1]
        if action != 'show':
            self.calls.append(command[1:])
            return subprocess.CompletedProcess(command, 0)
        name = command[-1]
        fragment = self.main.parent / name
        if fragment.exists():
            fields = dict(LoadState='loaded', ActiveState='active', FragmentPath=str(fragment), **self.units[name])
        else:
            fields = dict(LoadState='not-found')
        return subprocess.CompletedProcess(command, 0, '\n'.join(f'{key}={value}' for key, value in fields.items()))

    def uninstall(self, dry_run=False, *, yes=True, expected=0):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(self.module.uninstall(self.service, dry_run, yes, self.root), expected)
        self.assertNotIn('secret-fixture', output.getvalue())
        return output.getvalue()

    def test_uninstalls_and_archives_only_panel(self):
        agent = self.put(f'opt/{self.service}/bin/nekopass-agent')
        agent_config = self.put(f'etc/{self.service}/agent.env')
        other_panel = self.put('opt/nekopass-panel-other/bin/nekopass')
        self.uninstall()
        for removed in (self.main, self.watcher, self.updater, self.config, self.binary,
                        self.asset, self.dropin, self.request, self.ready):
            self.assertFalse(removed.exists(), removed)
        for retained in (agent, agent_config, other_panel, self.helper, self.manager, self.tls, self.result, self.override):
            self.assertTrue(retained.is_file(), retained)
        [archived] = self.config.parent.glob('.uninstalled-*/control.env')
        self.assertEqual(archived.read_text(), 'secret-fixture')
        self.assertEqual(self.calls, [['stop', self.watcher.name], ['stop', self.updater.name],
                                      ['stop', self.main.name], ['disable', self.watcher.name, self.main.name],
                                      ['daemon-reload']])
        self.calls.clear()
        self.uninstall()
        self.assertEqual(self.calls, [])

    def test_preview_and_foreign_unit_never_mutate(self):
        self.assertIn(str(self.helper), self.uninstall(dry_run=True))
        self.units[self.updater.name]['ExecStart'] = self.start(self.manager, 'nekopass-panel-other', 'update')
        with self.assertRaisesRegex(ValueError, self.updater.name + '.*ExecStart'):
            self.uninstall()
        self.assertEqual(self.calls, [])
        self.assertTrue(self.config.is_file())

    def test_removes_dedicated_updater(self):
        self.override.unlink()
        for directory in (self.binary.parent, self.binary.parents[1], self.dropin.parent):
            self.assertIn(f'将移除空目录：{directory}', self.uninstall(dry_run=True))
            self.assertTrue(directory.is_dir())
        self.cleanup_manager.assert_not_called()
        self.uninstall()
        self.assertFalse(self.helper.exists())
        self.assertFalse(self.binary.parent.exists())
        self.assertFalse(self.binary.parents[1].exists())
        self.assertFalse(self.dropin.parent.exists())
        self.cleanup_manager.assert_called_once_with(self.root)

    def test_default_panel_removes_unused_shared_updater(self):
        self.install('nekopass')
        self.uninstall()
        self.assertFalse(self.helper.exists())
        self.assertFalse(self.binary.parent.exists())

    def test_default_panel_preserves_updater_when_agent_unit_exists(self):
        self.install('nekopass')
        agent_unit = self.put('etc/systemd/system/nekopass-agent-uninstall.path')
        self.uninstall()
        self.assertTrue(self.helper.is_file())
        self.assertTrue(agent_unit.is_file())

    def test_rechecks_shared_updater_before_removal(self):
        self.install('nekopass')

        def add_local_agent(command, **kwargs):
            if command == ['systemctl', 'stop', self.main.name]:
                self.put('opt/nekopass/bin/nekopass-agent-uninstall')
            return self.systemctl(command, **kwargs)

        with patch.object(self.module.subprocess, 'run', side_effect=add_local_agent):
            self.uninstall()
        self.assertTrue(self.helper.is_file())

    def test_legacy_retry_prunes_only_empty_directories_after_confirmation(self):
        self.install('nekopass')
        # A legacy uninstall archived the configuration and left two empty
        # directories plus a shared helper whose ownership cannot be proved.
        self.uninstall()
        self.binary.parent.mkdir(parents=True, exist_ok=True)
        self.helper.write_text('retained legacy updater', encoding='utf-8')
        self.override.unlink()
        self.cleanup_manager.reset_mock()
        self.calls.clear()
        with patch.object(self.module, 'verified_archive', return_value=True):
            self.assertIn(f'将移除空目录：{self.dropin.parent}', self.uninstall(dry_run=True))
            self.cleanup_manager.assert_called_once_with(self.root, dry_run=True)
            self.cleanup_manager.reset_mock()
            with patch.object(self.module, 'confirm', return_value=False):
                self.uninstall(yes=False, expected=1)
            self.assertTrue(self.dropin.parent.is_dir())
            self.cleanup_manager.assert_not_called()
            self.uninstall()
        self.assertTrue(self.helper.is_file())
        self.assertTrue(self.tls.is_file())
        self.assertTrue(self.result.is_file())
        self.assertFalse(self.dropin.parent.exists())
        self.assertEqual(self.calls, [])
        self.cleanup_manager.assert_called_once_with(self.root)

    def test_legacy_retry_removes_unused_packaged_updater(self):
        self.install('nekopass')
        self.uninstall()
        self.helper.parent.mkdir(parents=True)
        self.helper.write_bytes(b'#!/usr/bin/python3\n'
                                b'"""Native service updates from Alma0010/nekopass GitHub Releases."""\n')
        with patch.object(self.module, 'verified_archive', return_value=True):
            self.assertIn(str(self.helper), self.uninstall(dry_run=True))
            self.assertTrue(self.helper.is_file())
            self.uninstall()
        self.assertFalse(self.helper.parents[1].exists())
        self.assertTrue(self.tls.is_file())
        self.assertTrue(self.result.is_file())

    def test_rejects_world_writable_opt(self):
        writable_directory = types.SimpleNamespace(st_mode=stat.S_IFDIR | 0o777, st_uid=0)
        with patch.object(pathlib.Path, 'lstat', autospec=True, return_value=writable_directory) as lstat:
            with self.assertRaisesRegex(ValueError, '路径类型、归属或权限不符合安装布局：'):
                self.real_safe_path(self.binary, self.root)
        lstat.assert_called_once_with(self.root / 'opt')


if __name__ == '__main__':
    unittest.main()
