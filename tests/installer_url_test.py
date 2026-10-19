"""Check the installer's actual URL selection without downloads or installation."""
import os
import pathlib
import shutil
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
BASH = shutil.which('bash')
if not BASH and os.name == 'nt':
    candidate = pathlib.Path('C:/Program Files/Git/bin/bash.exe')
    if candidate.is_file():
        BASH = str(candidate)


@unittest.skipUnless(BASH, 'Requires Bash')
class InstallerURLTests(unittest.TestCase):
    def test_release_download_urls(self):
        source = (ROOT / 'scripts/install-agent.sh').read_text(encoding='utf-8')
        start = source.index('FILE="nekopass-agent-linux-$ARCH"')
        end = source.index('\nlog "Downloading Agent ', start)
        script = source[start:end] + '\nprintf "%s" "$BINARY_URL"\n'
        repo = 'https://github.com/Alma0010/nekopass'
        proxy = 'https://v4.gh-proxy.org/' + repo
        other_proxy = 'https://proxy.example/github/' + repo
        cases = [
            (repo, 'latest', 'amd64', '', repo + '/releases/latest/download/nekopass-agent-linux-amd64'),
            (proxy, 'latest', 'amd64', '', proxy + '/releases/latest/download/nekopass-agent-linux-amd64'),
            (other_proxy, 'latest', 'arm64', '', other_proxy + '/releases/latest/download/nekopass-agent-linux-arm64'),
            (proxy, 'v0.19.1', 'amd64', '', proxy + '/releases/download/v0.19.1/nekopass-agent-linux-amd64'),
            (repo, 'v0.19.1', 'arm64', '', repo + '/releases/download/v0.19.1/nekopass-agent-linux-arm64'),
            ('https://oss.example', 'latest', 'amd64', '', 'https://oss.example/releases/download/latest/nekopass-agent-linux-amd64'),
            (proxy, 'latest', 'amd64', 'https://cdn.example/custom-agent', 'https://cdn.example/custom-agent'),
        ]
        for base, version, arch, binary, expected in cases:
            with self.subTest(base=base, version=version, arch=arch, override=bool(binary)):
                result = subprocess.run(
                    [BASH, '-eu', '-c', script],
                    env={**os.environ, 'DOWNLOAD_BASE': base + '/releases/download',
                         'VERSION': version, 'ARCH': arch, 'BINARY_URL': binary},
                    capture_output=True, text=True, timeout=10,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, expected)


if __name__ == '__main__':
    unittest.main()
