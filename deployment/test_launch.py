"""Boot-time network tests: local fixtures only, no interfaces or services change."""
import importlib.util
import json
import pathlib
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
            self.assertEqual([p.name for p in path.parent.iterdir()], ['config.json'])

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
