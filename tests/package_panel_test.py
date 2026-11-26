"""Check the Linux release archive when source scripts have Windows line endings."""
import pathlib
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
PACKAGE_SCRIPT = ROOT / 'scripts/package-panel.py'
PANEL_INSTALLER = ROOT / 'scripts/install-panel.sh'


class PanelPackageTest(unittest.TestCase):
    def test_linux_scripts_have_lf_shebangs_with_windows_checkout(self):
        with tempfile.TemporaryDirectory(prefix='nekopass-package-test-') as directory:
            root = pathlib.Path(directory) / 'repo'
            scripts = root / 'scripts'
            scripts.mkdir(parents=True)
            shutil.copyfile(PACKAGE_SCRIPT, scripts / 'package-panel.py')
            (scripts / 'nekopass-update.py').write_bytes(
                b'#!/usr/bin/python3\r\nprint("ok")\r\n'
            )
            manager = scripts / 'nekopassctl.sh'
            manager.write_bytes(b'#!/usr/bin/env bash\r\necho ok\r\n')
            binary = root / 'fixture-binary'
            binary.write_bytes(b'\x7fELFfixture')
            web = root / 'web'
            (web / 'pages').mkdir(parents=True)
            (web / 'assets').mkdir()
            (web / 'pages/index.html').write_bytes(b'<html></html>')
            asset = b'\x89PNG\r\nfixture'
            (web / 'assets/icon.png').write_bytes(asset)
            archive = root / 'dist' / 'panel.tar.gz'

            result = subprocess.run(
                [sys.executable, str(scripts / 'package-panel.py'),
                 '--binary', str(binary), '--web', str(web),
                 '--manager', str(manager), '--output', str(archive)],
                capture_output=True, text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            with tarfile.open(archive, 'r:gz') as package:
                for name, expected in (
                    ('bin/nekopassctl', b'#!/usr/bin/env bash\necho ok\n'),
                    ('bin/nekopass-update', b'#!/usr/bin/python3\nprint("ok")\n'),
                ):
                    self.assertEqual(package.extractfile(name).read(), expected)
                    self.assertEqual(package.getmember(name).mode, 0o755)
                self.assertEqual(package.extractfile('web/assets/icon.png').read(), asset)


class PanelInstallerFallbackTest(unittest.TestCase):
    def test_unbundled_installer_normalizes_updater_from_both_sources(self):
        bash = shutil.which('bash')
        if not bash:
            self.skipTest('Bash is required')
        script = PANEL_INSTALLER.read_text(encoding='utf-8')
        start = script.index('if ! write_updater_payload > "$WORK/nekopass-update"; then')
        end = script.index('\nif [[ "$TLS_MODE" == existing ]]', start)
        block = script[start:end]
        for use_archive in (True, False):
            with self.subTest(use_archive=use_archive), tempfile.TemporaryDirectory(
                    prefix='nekopass-install-test-') as directory:
                root = pathlib.Path(directory)
                work = root / 'work'
                work.mkdir()
                companion = root / 'nekopass-update.py'
                companion.write_bytes(b'#!/usr/bin/python3\r\nprint("ok")\r\n')
                if use_archive:
                    entry = work / 'release/bin/nekopass-update'
                    entry.parent.mkdir(parents=True)
                    entry.write_bytes(companion.read_bytes())
                harness = root / 'installer-fixture.sh'
                harness.write_text(
                    '#!/usr/bin/env bash\nset -e\nWORK=$1\n'
                    'write_updater_payload() { return 1; }\n'
                    'die() { exit 97; }\n' + block + '\n', encoding='utf-8',
                )
                def shell_path(path):
                    if sys.platform != 'win32':
                        return str(path)
                    return subprocess.check_output(
                        [bash, '-c', 'cygpath -u "$1"', '_', str(path)],
                        text=True,
                    ).strip()
                result = subprocess.run(
                    [bash, shell_path(harness), shell_path(work)],
                    capture_output=True, text=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual((work / 'nekopass-update').read_bytes(),
                                 b'#!/usr/bin/python3\nprint("ok")\n')


if __name__ == '__main__':
    unittest.main()
