"""Exercise independent HTTP and Agent listener changes against an installed-panel fixture."""
import contextlib
import io
import ipaddress
import os
import pathlib
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import patch


SOURCE = (pathlib.Path(__file__).resolve().parents[1] / 'scripts/nekopassctl.sh').read_text(encoding='utf-8')
PAYLOAD = SOURCE.split("<<'NEKOPASS_PANEL_UNINSTALL_PYTHON'\n", 1)[1].split('\nparser = ', 1)[0]


class PanelListenIPTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='nekopass-bind-test-')
        self.addCleanup(temp.cleanup)
        self.root = pathlib.Path(temp.name).resolve()
        self.service = 'nekopass-panel-test'
        self.unit = self.root / f'etc/systemd/system/{self.service}.service'
        self.unit.parent.mkdir(parents=True)
        self.binary = self.root / f'opt/{self.service}/bin/nekopass'
        self.binary.parent.mkdir(parents=True)
        self.binary.touch()
        self.web = self.binary.parent.parent / 'web'
        self.web.mkdir()
        self.config = self.root / f'etc/{self.service}/control.env'
        self.config.parent.mkdir(parents=True)
        self.config.write_text('NEKOPASS_DATABASE_URL=fixture', encoding='utf-8')
        self.args = f'{self.binary.as_posix()} -web {self.web.as_posix()} -http :8080 -grpc 0.0.0.0:9443'
        self.original = (
            '[Unit]\nDescription=Fixture panel\n'
            f'[Service]\nUser={self.service}\nEnvironmentFile={self.config}\n'
            '# Preserve this comment and existing restart policy.\n'
            f'ExecStart={self.args}\nRestart=always\n'
            '[Install]\nWantedBy=multi-user.target\n'
        ).encode()
        self.unit.write_bytes(self.original)
        self.active_state = 'active'
        self.fragment = self.unit
        self.override = None
        self.actions = []
        self.fail_next_restart = False
        self.module = types.ModuleType('panel_listen_ip')
        with patch.dict(sys.modules, fcntl=types.ModuleType('fcntl')):
            exec(compile(PAYLOAD, 'nekopassctl.sh', 'exec'), self.module.__dict__)

        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        stack.enter_context(patch.object(self.module, 'safe_path', side_effect=self.path_exists))
        stack.enter_context(patch.object(self.module, 'update_lock', return_value=contextlib.nullcontext()))
        stack.enter_context(patch.object(self.module.subprocess, 'run', side_effect=self.systemctl))
        self.wait_ready = self.module.wait_panel_ready
        self.ready = stack.enter_context(patch.object(self.module, 'wait_panel_ready', return_value=True))
        self.read_transport = self.module.read_agent_transport
        self.transport = stack.enter_context(patch.object(self.module, 'read_agent_transport', return_value='tls'))
        if not hasattr(os, 'fchown'):
            stack.enter_context(patch.object(os, 'fchown', create=True))

    def path_exists(self, path, root, *, directory=False, state_file=False):
        self.assertEqual(root, self.root)
        self.assertTrue(path.is_relative_to(root))
        return path.is_dir() if directory else path.is_file()

    def systemctl(self, command, **kwargs):
        self.assertEqual(command[0], 'systemctl')
        if command[1] == 'show':
            self.assertEqual(command[-1], self.unit.name)
            args = self.override or next(line.removeprefix('ExecStart=')
                                         for line in self.unit.read_text(encoding='utf-8').splitlines()
                                         if line.startswith('ExecStart='))
            exe = args.split()[0]
            output = (
                f'LoadState=loaded\nActiveState={self.active_state}\n'
                f'FragmentPath={self.fragment}\nNeedDaemonReload=no\nMainPID=123\n'
                f'User={self.service}\nEnvironmentFiles={self.config} (ignore_errors=no)\n'
                f'ExecStart={{ path={exe} ; argv[]={args} ; }}\n'
            )
            return subprocess.CompletedProcess(command, 0, stdout=output)
        self.actions.append(command[1:])
        if command[1] == 'restart' and self.fail_next_restart:
            self.fail_next_restart = False
            raise subprocess.CalledProcessError(1, command)
        return subprocess.CompletedProcess(command, 0)

    def listen(self, listener, address):
        with contextlib.redirect_stdout(io.StringIO()):
            return self.module.set_listen_ip(self.service, listener, address, root=self.root)

    def expected_unit(self, http, grpc):
        return (self.original
                .replace(b' -http :8080 ', f' -http {http} '.encode())
                .replace(b' -grpc 0.0.0.0:9443\n', f' -grpc {grpc}\n'.encode()))

    def test_http_loopback_changes_only_the_http_address_in_plain_mode(self):
        self.transport.return_value = 'plain'
        expected = self.expected_unit('127.0.0.1:8080', '0.0.0.0:9443')
        with patch.object(self.module, 'atomic_write_unit', wraps=self.module.atomic_write_unit) as write:
            self.listen('http', '127.0.0.1')
        write.assert_called_once_with(self.unit, expected)
        self.transport.assert_not_called()
        self.ready.assert_called_once_with(self.unit, '127.0.0.1', 8080, ('127.0.0.1', 8080))
        self.assertEqual(self.unit.read_bytes(), expected)
        self.assertEqual(self.actions, [['daemon-reload'], ['restart', self.unit.name]])

    def test_grpc_loopback_changes_only_grpc_and_uses_the_existing_web_endpoint(self):
        current = self.expected_unit('192.0.2.20:8080', '0.0.0.0:9443')
        self.unit.write_bytes(current)
        expected = self.expected_unit('192.0.2.20:8080', '[::1]:9443')
        with patch.object(self.module, 'atomic_write_unit', wraps=self.module.atomic_write_unit) as write:
            self.listen('grpc', '::1')
        write.assert_called_once_with(self.unit, expected)
        self.transport.assert_called_once()
        self.assertEqual(self.transport.call_args.args[:2], (self.service, self.root))
        self.ready.assert_called_once_with(self.unit, '::1', 9443, ('192.0.2.20', 8080))
        self.assertEqual(self.unit.read_bytes(), expected)
        self.assertEqual(self.actions, [['daemon-reload'], ['restart', self.unit.name]])

    def test_inactive_listeners_can_independently_use_ipv6_or_all_interfaces(self):
        self.active_state = 'inactive'
        self.listen('http', '::1')
        self.assertEqual(self.unit.read_bytes(), self.expected_unit('[::1]:8080', '0.0.0.0:9443'))
        self.listen('grpc', 'all')
        self.assertEqual(self.unit.read_bytes(), self.expected_unit('[::1]:8080', ':9443'))
        self.listen('http', 'all')
        self.assertEqual(self.unit.read_bytes(), self.expected_unit(':8080', ':9443'))
        self.ready.assert_not_called()
        self.transport.assert_not_called()
        self.assertEqual(self.actions, [['daemon-reload']] * 3)

    def test_preserves_equals_form_and_both_nondefault_ports(self):
        self.active_state = 'inactive'
        old = (self.original
               .replace(b' -http :8080 ', b' -http=192.0.2.1:8890 ')
               .replace(b' -grpc 0.0.0.0:9443\n', b' -grpc=192.0.2.2:9456\n'))
        self.unit.write_bytes(old)
        self.listen('http', '2001:db8::1')
        expected = old.replace(b'192.0.2.1:8890', b'[2001:db8::1]:8890')
        self.assertEqual(self.unit.read_bytes(), expected)
        self.listen('grpc', '2001:db8::2')
        self.assertEqual(self.unit.read_bytes(), expected.replace(b'192.0.2.2:9456', b'[2001:db8::2]:9456'))
        self.assertEqual(self.actions, [['daemon-reload'], ['daemon-reload']])
        self.transport.assert_not_called()

    def test_repeating_current_listen_addresses_does_not_reload_or_restart(self):
        current = self.expected_unit(':8080', ':9443')
        self.unit.write_bytes(current)
        for listener in ('http', 'grpc'):
            with self.subTest(listener=listener):
                self.listen(listener, 'all')
        self.assertEqual(self.actions, [])
        self.assertEqual(self.unit.read_bytes(), current)
        self.transport.assert_not_called()

    def test_invalid_address_leaves_the_unit_and_service_alone(self):
        for listener in ('http', 'grpc'):
            for address in ('localhost', '127.0.0.1:8080', 'fe80::1%eth0', '127.0.0.1\n'):
                with self.subTest(listener=listener, address=address), self.assertRaises(ValueError):
                    self.listen(listener, address)
        self.assertEqual(self.actions, [])
        self.assertEqual(self.unit.read_bytes(), self.original)
        self.transport.assert_not_called()

    def test_plain_agent_transport_cannot_bind_grpc_to_a_loopback_address(self):
        self.transport.return_value = 'plain'
        with patch.object(self.module, 'atomic_write_unit') as write:
            for address in ('127.0.0.1', '127.0.0.8', '::1', '::ffff:127.0.0.1'):
                with self.subTest(address=address), self.assertRaises(ValueError):
                    self.listen('grpc', address)
        write.assert_not_called()
        self.assertEqual(self.transport.call_count, 4)
        self.assertEqual(self.actions, [])
        self.assertEqual(self.unit.read_bytes(), self.original)
        self.ready.assert_not_called()

    def test_existing_grpc_loopback_does_not_bypass_the_transport_guard(self):
        current = self.expected_unit(':8080', '127.0.0.1:9443')
        self.unit.write_bytes(current)
        self.transport.return_value = 'plain'
        with self.assertRaises(ValueError):
            self.listen('grpc', '127.0.0.1')
        self.transport.assert_called_once()
        self.ready.assert_not_called()
        self.assertEqual(self.actions, [])
        self.assertEqual(self.unit.read_bytes(), current)

    def test_unavailable_transport_blocks_grpc_loopback_only(self):
        self.transport.side_effect = ValueError('无法核对节点控制传输方式')
        with self.assertRaisesRegex(ValueError, '无法核对'):
            self.listen('grpc', '127.0.0.1')
        self.assertEqual(self.actions, [])
        self.assertEqual(self.unit.read_bytes(), self.original)
        self.ready.assert_not_called()

        self.transport.reset_mock()
        self.listen('http', '127.0.0.1')
        self.transport.assert_not_called()
        self.assertEqual(self.unit.read_bytes(), self.expected_unit('127.0.0.1:8080', '0.0.0.0:9443'))

    def test_plain_transport_allows_a_nonloopback_grpc_ip(self):
        self.transport.return_value = 'plain'
        self.listen('grpc', '192.0.2.20')
        self.transport.assert_not_called()
        self.ready.assert_called_once_with(self.unit, '192.0.2.20', 9443, ('', 8080))
        self.assertEqual(self.unit.read_bytes(), self.expected_unit(':8080', '192.0.2.20:9443'))

    def test_grpc_readiness_checks_changed_socket_and_existing_http_health(self):
        with (patch.object(self.module, 'unit_info', return_value={'MainPID': '123', 'ActiveState': 'active'}),
              patch.object(self.module, 'process_listens_on', return_value=True) as listening,
              patch.object(self.module.http.client, 'HTTPConnection') as client):
            connection = client.return_value
            response = connection.getresponse.return_value
            response.status = 200
            response.read.return_value = b'{"status":"ok"}'
            self.assertTrue(self.wait_ready(self.unit, '::1', 9443, ('192.0.2.20', 8890)))
        listening.assert_called_once_with('123', '::1', 9443)
        client.assert_called_once_with('192.0.2.20', 8890, timeout=1)
        connection.request.assert_called_once_with('GET', '/healthz')
        connection.close.assert_called_once()

    def test_transport_query_uses_verified_peer_authenticated_settings(self):
        uri = (f'postgres://{self.service}@/{self.service}'
               '?host=/var/run/postgresql&port=5544&sslmode=disable')
        self.config.write_text('NEKOPASS_DATABASE_URL=' + uri + '\n', encoding='utf-8')
        info = {'User': self.service, 'EnvironmentFiles': f'{self.config} (ignore_errors=no)'}
        answer = subprocess.CompletedProcess([], 0, stdout='tls\n')
        with (patch.dict(os.environ, PGPASSWORD='fixture', PGHOST='ignored'),
              patch.object(self.module.subprocess, 'run', return_value=answer) as run):
            self.assertEqual(self.read_transport(self.service, self.root, info), 'tls')
        args = run.call_args.args[0]
        self.assertEqual(args[:-1], [
            'runuser', '-u', self.service, '--', 'psql', '-w', '-XAtq',
            '-h', '/var/run/postgresql', '-p', '5544', '-U', self.service, '-d', self.service, '-c',
        ])
        self.assertTrue(args[-1].startswith('SELECT '))
        self.assertIn('agent_transport', args[-1])
        self.assertNotIn(uri, ' '.join(args))
        self.assertFalse(any(key.startswith('PG') for key in run.call_args.kwargs['env']))

    def test_unverified_transport_dsn_never_invokes_psql(self):
        self.config.write_text(f'NEKOPASS_DATABASE_URL=postgres://{self.service}@localhost/{self.service}\n',
                               encoding='utf-8')
        info = {'User': self.service, 'EnvironmentFiles': f'{self.config} (ignore_errors=no)'}
        with patch.object(self.module.subprocess, 'run') as run, self.assertRaisesRegex(ValueError, '无法核对'):
            self.read_transport(self.service, self.root, info)
        run.assert_not_called()

    def test_refuses_an_effective_execstart_override(self):
        self.override = self.args.replace(' -http :8080 ', ' -http 0.0.0.0:8081 ')
        with self.assertRaises(ValueError):
            self.listen('http', '127.0.0.1')
        self.assertEqual(self.actions, [])
        self.assertEqual(self.unit.read_bytes(), self.original)

    def test_refuses_a_dropin_that_no_longer_matches_after_reload(self):
        self.override = self.args
        with self.assertRaisesRegex(ValueError, '已还原'):
            self.listen('grpc', '127.0.0.1')
        self.ready.assert_not_called()
        self.assertEqual(self.actions, [['daemon-reload'], ['daemon-reload']])
        self.assertEqual(self.unit.read_bytes(), self.original)

    def test_refuses_a_unit_loaded_from_another_fragment(self):
        self.fragment = self.root / f'usr/lib/systemd/system/{self.unit.name}'
        with self.assertRaises(ValueError):
            self.listen('http', '127.0.0.1')
        self.assertEqual(self.actions, [])
        self.assertEqual(self.unit.read_bytes(), self.original)

    def test_unavailable_listener_restores_the_original_unit_and_restarts(self):
        restart = ['restart', self.unit.name]
        for listener in ('http', 'grpc'):
            with self.subTest(listener=listener):
                self.actions.clear()
                self.ready.side_effect = [False, True]
                with self.assertRaisesRegex(ValueError, '已还原'):
                    self.listen(listener, '127.0.0.1')
                self.assertEqual(self.unit.read_bytes(), self.original)
                self.assertEqual(self.actions, [['daemon-reload'], restart, ['daemon-reload'], restart])

    def test_failed_restart_restores_the_original_unit_and_restarts(self):
        self.fail_next_restart = True
        with self.assertRaisesRegex(ValueError, '已还原'):
            self.listen('grpc', '127.0.0.1')
        self.assertEqual(self.unit.read_bytes(), self.original)
        restart = ['restart', self.unit.name]
        self.assertEqual(self.actions, [['daemon-reload'], restart, ['daemon-reload'], restart])


class ProcessListenerTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='nekopass-proc-test-')
        self.addCleanup(temp.cleanup)
        self.proc = pathlib.Path(temp.name) / '123'
        (self.proc / 'fd').mkdir(parents=True)
        (self.proc / 'net').mkdir()
        self.descriptor = self.proc / 'fd/4'
        self.descriptor.touch()
        self.module = types.ModuleType('panel_listener_probe')
        with patch.dict(sys.modules, fcntl=types.ModuleType('fcntl')):
            exec(compile(PAYLOAD, 'nekopassctl.sh', 'exec'), self.module.__dict__)

    def write_table(self, kind, entries):
        rows = ['sl local_address rem_address st tx_queue tr retrnsmt uid timeout inode\n']
        for index, (host, port, state, inode) in enumerate(entries):
            packed = ipaddress.ip_address(host).packed
            address = ''.join(f'{int.from_bytes(packed[i:i+4], sys.byteorder):08X}'
                              for i in range(0, len(packed), 4))
            rows.append(f'{index}: {address}:{port:04X} 00000000:0000 {state} '
                        f'00000000:00000000 00:00000000 00000000 0 0 {inode}\n')
        (self.proc / 'net' / kind).write_text(''.join(rows), encoding='ascii')

    def listens(self, address, port):
        fake_pathlib = types.SimpleNamespace(Path=lambda path: self.proc.parent if path == '/proc' else pathlib.Path(path))
        with patch.object(self.module, 'pathlib', fake_pathlib), patch.object(pathlib.Path, 'readlink', return_value='socket:[700]'):
            return self.module.process_listens_on('123', address, port)

    def test_rejects_unowned_ports_and_connections(self):
        self.write_table('tcp', [
            ('127.0.0.1', 8080, '0A', 701),
            ('127.0.0.1', 8080, '01', 700),
            ('127.0.0.1', 8081, '0A', 700),
            ('0.0.0.0', 8080, '0A', 700),
        ])
        self.assertFalse(self.listens('127.0.0.1', 8080))
        self.assertTrue(self.listens('127.0.0.1', 8081))
        self.assertTrue(self.listens('', 8080))

    def test_matches_ipv6_and_mapped_ipv4_listeners(self):
        self.write_table('tcp6', [
            ('::1', 8080, '0A', 700),
            ('::ffff:127.0.0.1', 8081, '0A', 700),
        ])
        self.assertTrue(self.listens('::1', 8080))
        self.assertFalse(self.listens('127.0.0.1', 8080))
        self.assertTrue(self.listens('127.0.0.1', 8081))
        self.assertFalse(self.listens('', 8080))

    def test_selects_only_the_requested_port_at_the_requested_ip(self):
        self.write_table('tcp', [
            ('127.0.0.1', 8080, '0A', 700),
            ('192.0.2.20', 9443, '0A', 700),
        ])
        self.assertTrue(self.listens('127.0.0.1', 8080))
        self.assertFalse(self.listens('127.0.0.1', 9443))
        self.assertTrue(self.listens('192.0.2.20', 9443))


if __name__ == '__main__':
    unittest.main()
