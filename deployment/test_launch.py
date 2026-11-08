"""Boot-time network tests: local fixtures only, no interfaces or services change."""
import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('launcher', pathlib.Path(__file__).with_name('launch.py'))
launcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launcher)


def interface(name, ip, index=2, up=True):
    return dict(ifname=name, ifindex=index, flags=['UP'] if up else [], operstate='UP' if up else 'DOWN',
                addr_info=[dict(local=ip, scope='global', preferred_life_time=100)])


class LaunchTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.runtime = pathlib.Path(directory.name)
        runtime_patch = patch.object(launcher, 'runtime_dir', return_value=self.runtime)
        runtime_patch.start()
        self.addCleanup(runtime_patch.stop)

    def test_reassigned_ip_and_default_route_win_without_widening_the_network(self):
        addresses = [interface('docker0', '172.17.0.1', 1), interface('wlan0', '192.168.1.2', 4),
                     interface('eth0', '192.168.1.5', 3), interface('down', '192.168.1.6', 5, False)]
        route = b'default via 192.168.1.1 dev eth0 proto dhcp metric 100\ndefault dev wlan0 metric 600\n'
        with patch.object(launcher.subprocess, 'check_output', side_effect=[json.dumps(addresses).encode(), route]):
            self.assertEqual(launcher.current_address(launcher.trusted_network('192.168.1.0/24')), '192.168.1.5')
        with patch.object(launcher.subprocess, 'check_output', side_effect=[json.dumps(addresses[:1]).encode(), b'']):
            self.assertIsNone(launcher.current_address(launcher.trusted_network('192.168.1.0/24')))
        for network in ('0.0.0.0/0', '192.168.0.0/15', 'fc00::/7'):
            with self.assertRaises(ValueError):
                launcher.trusted_network(network)

    def test_single_lan_interface_does_not_query_routes(self):
        wired = interface('eth0', '192.168.1.5')
        wired['addr_info'].append(dict(local='192.168.1.2', scope='global', preferred_life_time=0))
        with patch.object(launcher.subprocess, 'check_output', return_value=json.dumps([wired]).encode()) as run:
            self.assertEqual(launcher.current_address(launcher.trusted_network('192.168.1.0/24')), '192.168.1.5')
        run.assert_called_once_with(['ip', '-j', '-4', 'address', 'show'], timeout=5)

    def test_music_and_manager_use_the_current_ip_and_keep_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            path = root / 'music-service/config.json'
            path.parent.mkdir()
            config = dict(lan_cidr='192.168.1.0/24', browse_listen='192.168.1.2:9792',
                          browse_udn='uuid:stable', backend_description='http://127.0.0.1:9791/stable', name='Music')
            path.write_text(json.dumps(config))
            with patch.object(launcher, 'current_address', return_value='192.168.1.5'), patch.object(launcher.os, 'execv') as execute:
                launcher.start('music', root, 8787)
                saved = json.loads(path.read_text())
                self.assertEqual(saved, dict(config, browse_listen='192.168.1.5:9792'))
                self.assertEqual(execute.call_args[0][1], [str(root / 'music-service/hifi-music-service'), '-home', str(root)])
                launcher.start('manager', root, 8787)
                args = execute.call_args[0][1]
                self.assertEqual(args[args.index('-listen') + 1], '192.168.1.5:8787')
                self.assertEqual(args[args.index('-allow-lan') + 1], '192.168.1.0/24')
                self.assertNotIn('-public-domain', args)
                launcher.start('manager', root, 8787, lan_hostname='nano.local')
                self.assertEqual(execute.call_args[0][1][-2:], ['-lan-hostname', 'nano.local'])
                launcher.start('manager', root, 8787, public_domain='manage.example.net')
                args = execute.call_args[0][1]
                self.assertEqual(args[-6:], ['-public-domain', 'manage.example.net',
                    '-public-tls-cert', str(root / 'config/manager-public-tls-cert.pem'),
                    '-public-tls-key', str(root / 'config/manager-public-tls-key.pem')])
                launcher.start('bubble', root, 8787, public_domain='music.example.net')
                args = execute.call_args[0][1]
                self.assertEqual(args[:4], ['/usr/bin/authbind', '--deep', '/bin/sh', str(root / 'bubbleupnp/launch.sh')])
                self.assertEqual(args[-6:], ['-lanIp', '192.168.1.5', '-httpPort', '80', '-wanHostname', 'music.example.net'])
                launcher.start('bubble-relay', root, 8787, public_domain='music.example.net')
                args = execute.call_args[0][1]
                self.assertEqual(args[-4:], ['-bubble-relay-domain', 'music.example.net', '-bubble-relay-upstream', '192.168.1.5'])
                binding = json.loads((self.runtime / 'hifi-lan' / 'bubble-relay').read_text())
                self.assertEqual(binding, {'address': '192.168.1.5', 'pid': launcher.os.getpid()})
            self.assertEqual([p.name for p in path.parent.iterdir()], ['config.json'])

    @unittest.skipUnless(sys.platform.startswith('linux'), 'Rebinding uses the Linux deployment lock')
    def test_reconcile_restarts_only_current_processes_with_stale_addresses(self):
        root = self.runtime / 'project'
        path = root / 'music-service/config.json'
        path.parent.mkdir(parents=True)
        path.write_text(json.dumps({'lan_cidr': '192.168.1.0/24'}))
        launcher.record_binding('music', '192.168.1.5')
        launcher.record_binding('bubble', '192.168.1.5')
        launcher.record_binding('manager', '192.168.1.2')
        launcher.record_binding('bubble-relay', '192.168.1.5')
        pid = launcher.os.getpid()
        unchanged = b'MainPID=0\nId=hifi-manager.service\n'
        changed = ('MainPID={}\nId=hifi-music.service\n\nMainPID=0\nId=hifi-bubbleupnp.service\n\n'
                   'MainPID={}\nId=hifi-bubble-relay.service\n').format(pid, pid).encode()
        with patch.object(launcher, 'current_address', side_effect=[None, '192.168.1.5', '192.168.1.2']), \
             patch.object(launcher.subprocess, 'check_output', side_effect=[unchanged, changed]) as status, \
             patch.object(launcher.subprocess, 'run') as run:
            launcher.reconcile(root)
            launcher.reconcile(root)
            run.assert_not_called()
            launcher.reconcile(root)
        run.assert_called_once_with(['systemctl', '--user', 'try-restart', 'hifi-music.service',
                                     'hifi-bubble-relay.service'], check=True)
        self.assertEqual(status.call_count, 2)

    def test_delayed_network_is_retried_and_missing_network_times_out(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            path = root / 'music-service/config.json'
            path.parent.mkdir()
            original = json.dumps(dict(lan_cidr='192.168.1.0/24', browse_listen='192.168.1.2:9792'))
            path.write_text(original)
            with patch.object(launcher, 'current_address', side_effect=[None, '192.168.1.5']), \
                 patch.object(launcher.time, 'sleep') as sleep, patch.object(launcher.os, 'execv') as execute:
                launcher.start('manager', root, 8787)
                sleep.assert_called_once_with(1)
                self.assertTrue(execute.called)
            with patch.object(launcher, 'current_address', return_value=None), \
                 patch.object(launcher.time, 'monotonic', side_effect=[0, 121]), patch.object(launcher.os, 'execv') as execute:
                with self.assertRaisesRegex(RuntimeError, 'No active LAN'):
                    launcher.start('music', root, 8787)
                execute.assert_not_called()
            self.assertEqual(path.read_text(), original)


if __name__ == '__main__':
    unittest.main()
