"""Exercise the shared management script updater with a fixture installation."""
import contextlib
import io
import os
import pathlib
import stat
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import call, patch


SOURCE = (pathlib.Path(__file__).resolve().parents[1] / 'scripts/nekopassctl.sh').read_text(encoding='utf-8')
PAYLOAD = SOURCE.split("<<'NEKOPASS_PANEL_UNINSTALL_PYTHON'\n", 1)[1].split('\nparser = ', 1)[0]
HEADER = (
    b'#!/usr/bin/env bash\n'
    b'# Native systemd service manager. No application credentials are executed as shell code.\n'
)
SOURCE_URL = 'https://raw.githubusercontent.com/Alma0010/nekopass/main/scripts/nekopassctl.sh'


class ManagerSelfUpdateTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='nekopass-manager-test-')
        self.addCleanup(temp.cleanup)
        self.root = pathlib.Path(temp.name).resolve()
        self.manager = self.root / 'usr/local/bin/nekopassctl'
        self.manager.parent.mkdir(parents=True)
        self.original = HEADER + b'printf "fixture existing\\n"\n'
        self.download = HEADER + b'printf "fixture update\\n"\n'
        self.manager.write_bytes(self.original)
        self.manager.chmod(0o755)
        self.candidate = None
        self.commands = []
        self.fail_command = None
        self.remove_during_download = False

        self.module = types.ModuleType('manager_self_update')
        with patch.dict(sys.modules, fcntl=types.ModuleType('fcntl')):
            exec(compile(PAYLOAD, 'nekopassctl.sh', 'exec'), self.module.__dict__)

        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        self.safe_path = self.module.safe_path
        stack.enter_context(patch.object(self.module, 'safe_path', side_effect=self.fixture_safe_path))
        self.lock = stack.enter_context(
            patch.object(self.module, 'directory_lock', return_value=contextlib.nullcontext()))
        stack.enter_context(patch.object(self.module.subprocess, 'run', side_effect=self.run_command))

    def fixture_safe_path(self, path, root, **kwargs):
        self.assertEqual(root, self.root)
        real_lstat = pathlib.Path.lstat

        def fixture_lstat(item):
            # Emulate root ownership without changing file types or link counts.
            info = real_lstat(item)
            return types.SimpleNamespace(st_uid=0, st_mode=info.st_mode & ~0o022, st_nlink=info.st_nlink)

        with patch.object(pathlib.Path, 'lstat', fixture_lstat):
            return self.safe_path(path, root, **kwargs)

    def run_command(self, command, **kwargs):
        self.commands.append(command[0])
        self.assertTrue(kwargs.get('check'))
        self.assertGreater(kwargs.get('timeout', 0), 0)
        if command[0] == 'curl':
            self.assertEqual(command[1], '-q')
            self.assertIn(SOURCE_URL, command)
            self.candidate = pathlib.Path(command[command.index('-o') + 1])
            self.assertEqual(self.candidate.parent, self.manager.parent)
            self.candidate.write_bytes(self.download)
            if self.remove_during_download:
                self.manager.unlink()
        elif command[0] == 'bash':
            self.assertEqual(command[1:], ['-n', str(self.candidate)])
        else:
            self.fail(f'Unexpected updater command: {command}')
        if self.fail_command == command[0]:
            raise subprocess.CalledProcessError(1, command)
        return subprocess.CompletedProcess(command, 0)

    def update(self):
        with contextlib.redirect_stdout(io.StringIO()):
            return self.module.update_manager(root=self.root)

    def assert_candidate_removed(self):
        self.assertIsNotNone(self.candidate)
        self.assertFalse(self.candidate.exists())

    def test_replaces_only_manager_with_valid_download(self):
        self.assertEqual(self.update(), 0)
        self.assertEqual(self.manager.read_bytes(), self.download)
        self.assertEqual(self.commands, ['curl', 'bash'])
        self.assertIn(call(self.manager.parent), self.lock.call_args_list)
        if os.name == 'posix':
            self.assertEqual(stat.S_IMODE(self.manager.stat().st_mode), 0o755)
        self.assert_candidate_removed()

    def test_identical_download_leaves_existing_manager_in_place(self):
        self.download = self.original
        before = self.manager.stat()
        self.assertEqual(self.update(), 0)
        after = self.manager.stat()
        self.assertEqual((after.st_ino, after.st_mtime_ns), (before.st_ino, before.st_mtime_ns))
        self.assert_candidate_removed()

    def test_non_script_download_leaves_manager_in_place(self):
        self.download = b'<html>Download failed</html>\n'
        with self.assertRaises(ValueError):
            self.update()
        self.assertEqual(self.commands, ['curl'])
        self.assertEqual(self.manager.read_bytes(), self.original)
        self.assert_candidate_removed()

    def test_failed_download_or_syntax_check_leaves_manager_in_place(self):
        for failure in ('curl', 'bash'):
            with self.subTest(failure=failure):
                self.fail_command = failure
                with self.assertRaises(subprocess.CalledProcessError):
                    self.update()
                self.assertEqual(self.manager.read_bytes(), self.original)
                self.assert_candidate_removed()

    def test_custom_or_hard_linked_manager_is_not_replaced(self):
        custom = b'#!/usr/bin/env bash\nprintf "custom manager"\n'
        self.manager.write_bytes(custom)
        with self.assertRaises(ValueError):
            self.update()
        self.assertEqual(self.manager.read_bytes(), custom)

        self.manager.write_bytes(self.original)
        other = self.manager.with_name('manager-link')
        os.link(self.manager, other)
        with self.assertRaises(ValueError):
            self.update()
        self.assertEqual(other.read_bytes(), self.original)
        self.assertEqual(self.manager.read_bytes(), self.original)

    def test_removed_manager_stays_removed_when_download_finishes(self):
        self.remove_during_download = True
        with self.assertRaises(ValueError):
            self.update()
        self.assertFalse(self.manager.exists())
        self.assert_candidate_removed()
