"""Exercise fresh installation, failed upgrades and data preservation without a device."""
import importlib.util
import io
import json
import os
import pathlib
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch
import zipfile
import sys

spec = importlib.util.spec_from_file_location('installer', pathlib.Path(__file__).with_name('install.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


@unittest.skipUnless(sys.platform.startswith('linux'), 'Installer tests require Linux filesystem and service semantics')
class DeploymentTests(unittest.TestCase):
    def test_service_units_do_not_pin_the_last_assigned_address(self):
        files = installer.render(self.profile)
        for name in ('hifi-music.service', 'hifi-manager.service'):
            text = files[self.home / '.config/systemd/user' / name][0].decode()
            self.assertNotIn(self.profile['lan_ip'], text)
            self.assertNotIn('ExecStartPre=', text)
            self.assertIn('/usr/bin/python3', text)
            self.assertIn('deployment/launch-service.py', text)
        launcher = self.root / 'deployment/launch-service.py'
        self.assertIn(launcher, files)
        self.assertEqual(installer.restart_set([launcher]), {'hifi-music.service', 'hifi-manager.service', installer.LAN_REBIND})
        watcher = files[self.home / '.config/systemd/user' / installer.LAN_REBIND][0].decode()
        self.assertIn('launch-service.py watch ', watcher)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = pathlib.Path(self.temp.name)
        self.root = self.home / 'hifi'
        self.root.mkdir()
        self.profile = dict(root=str(self.root), volume=str(self.home / 'usb'), lan_ip='10.42.7.8',
                            lan_cidr='10.42.7.0/24', web_port=8787, browse_port=9792,
                            name='Living room', manage_minim=True, manage_bubble=False,
                            backend_description='http://127.0.0.1:9791/native/Upnp/device.xml',
                            browse_udn='uuid:12345678-1234-1234-1234-123456789012')
        self.calls = []
        self.home_patch = patch.object(pathlib.Path, 'home', return_value=self.home)
        self.home_patch.start()
        self.addCleanup(self.home_patch.stop)

    def systemctl(self, *args):
        self.calls.append(args)
        return 'enabled' if args[0] == 'is-enabled' else ''

    def apply(self, files, health=None, reinstall=False):
        with patch.object(installer, 'systemctl', side_effect=self.systemctl), \
             patch.object(installer, 'command', return_value='valid'), \
             patch.object(installer, 'mount_identity', return_value='mount-a'), \
             patch.object(installer, 'activate', side_effect=lambda p, s: self.calls.append(('activate', sorted(s)))), \
             patch.object(installer, 'health', side_effect=health):
            return installer.apply_files(self.profile, files, 'mount-a', reinstall=reinstall)

    def test_fresh_install_then_identical_update_does_not_restart(self):
        files = installer.render(self.profile)
        for name, relative in installer.BINARIES.items():
            files[self.root / relative] = (name.encode(), 0o755)
        first = self.apply(files)
        self.assertIn('hifi-music.service', first['restarted_services'])
        saved = json.loads((self.root / 'deployment/state.json').read_text())
        self.assertEqual(saved['browse_udn'], self.profile['browse_udn'])
        second = self.apply(files)
        self.assertEqual(second['changed_files'], [])
        self.assertEqual(second['restarted_services'], [])

    def test_reinstall_stops_existing_services_and_replaces_identical_artifacts(self):
        files = installer.render(self.profile)
        binary = self.root / 'manager-service/hifi-manager'
        files[binary] = (b'manager program', 0o755)
        self.apply(files)
        with binary.open('rb') as old:
            self.calls.clear()
            result = self.apply(files, reinstall=True)
            self.assertNotEqual(os.fstat(old.fileno()).st_ino, binary.stat().st_ino)
        self.assertEqual(set(result['changed_files']), {str(path) for path in files})
        self.assertLess(self.calls.index(('stop', 'hifi-manager.service')), self.calls.index(('daemon-reload',)))
        self.assertIn(('activate', []), self.calls)

    def test_failed_health_restores_executables_config_and_preserves_data(self):
        files = installer.render(self.profile)
        binary = self.root / 'manager-service/hifi-manager'
        files[binary] = (b'old program', 0o755)
        self.apply(files)
        database = self.home / 'usb/hifi-music-nano/manager/manager.db'
        database.parent.mkdir(parents=True)
        database.write_bytes(b'existing database')
        marker = self.home / 'usb/hifi-music-nano/library-id'
        marker.parent.mkdir(parents=True,exist_ok=True)
        marker.write_text('existing-id')
        song = self.home / 'usb/hifi-music-nano/music/user.flac'
        song.parent.mkdir()
        song.write_bytes(b'user audio')
        updated = dict(files)
        updated[binary] = (b'new program', 0o755)
        config = self.root / 'music-service/config.json'
        old_config = config.read_bytes()
        updated[config] = (b'{"bad":"configuration"}', 0o600)
        with self.assertRaisesRegex(RuntimeError, 'not ready'):
            self.apply(updated, health=RuntimeError('not ready'), reinstall=True)
        self.assertEqual(binary.read_bytes(), b'old program')

        self.assertEqual(config.read_bytes(), old_config)
        self.assertEqual(database.read_bytes(), b'existing database')
        self.assertEqual(marker.read_text(), 'existing-id')
        self.assertEqual(song.read_bytes(), b'user audio')

    def test_mount_replacement_rejects_upgrade_before_replacing_program(self):
        binary = self.root / 'manager-service/hifi-manager'
        installer.atomic(binary, b'old program', 0o755)
        with patch.object(installer, 'mount_identity', return_value='mount-b'), \
             patch.object(installer, 'systemctl', side_effect=self.systemctl):
            with self.assertRaisesRegex(RuntimeError, 'mount changed'):
                installer.apply_files(self.profile, {binary: (b'new program', 0o755)}, 'mount-a')
        self.assertEqual(binary.read_bytes(), b'old program')
        self.assertEqual(self.calls, [], 'preflight failure must not stop existing services')

    def test_offline_install_never_creates_the_configured_disk_directory(self):
        files = installer.render(self.profile)
        with patch.object(installer, 'systemctl', side_effect=self.systemctl), \
             patch.object(installer, 'command', return_value='valid'), \
             patch.object(installer, 'mount_identity', return_value=None), \
             patch.object(installer, 'activate'), patch.object(installer, 'health'):
            result = installer.apply_files(self.profile, files, None)
        self.assertTrue(result['data_preserved'])
        self.assertFalse(pathlib.Path(self.profile['volume']).exists())
        self.assertEqual(json.loads((self.root / 'music-service/config.json').read_text())['browse_udn'], self.profile['browse_udn'])

    def test_device_config_preserves_identity_and_rejects_network_or_volume_migration(self):
        config = self.root / 'music-service/config.json'
        installer.atomic(config, json.dumps(dict(browse_udn=self.profile['browse_udn'],
                         backend_description=self.profile['backend_description'])).encode(), 0o600)
        inventory = json.dumps([{'addr_info': [{'local': '10.42.7.8', 'prefixlen': 24}]}])
        with patch.object(installer, 'command', return_value=inventory):
            p = dict(self.profile)
            p.pop('browse_udn')
            p.pop('lan_cidr')
            self.assertEqual(installer.resolve(p)['browse_udn'], self.profile['browse_udn'])
            self.assertEqual(installer.resolve(p)['lan_cidr'], '10.42.7.0/24')
            with self.assertRaisesRegex(ValueError, 'identity'):
                installer.resolve(dict(p, browse_udn='uuid:aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'))
            installer.atomic(self.root / 'deployment/state.json', json.dumps(self.profile).encode(), 0o600)
            self.assertEqual(installer.resolve(dict(p, volume='/media/another-disk'))['volume'], '/media/another-disk')
        for network in ('0.0.0.0/0', '172.16.0.0/11', '192.168.0.0/15', '127.0.0.0/8', 'fc00::/7'):
            with self.assertRaises(ValueError):
                installer.private_network(network)

    def test_verified_live_selection_overrides_the_initial_mount_hint(self):
        mount = self.home / 'new-automount-name'
        selected = str(mount / 'hifi-music-nano/music')
        inventory = json.dumps([{'addr_info': [{'local': '10.42.7.8', 'prefixlen': 24}]}])
        with patch.object(installer, 'active_library', return_value=selected), \
             patch.object(installer, 'command', return_value=inventory):
            resolved = installer.resolve(self.profile)
        self.assertEqual(resolved['volume'], str(mount))
        self.assertEqual(resolved['browse_udn'], self.profile['browse_udn'])

    def test_tampered_wrong_architecture_and_path_traversal_bundles_rejected(self):
        bundle = self.home / 'bundle.zip'
        elf = bytearray(64)
        elf[:6] = b'\x7fELF\x02\x01'
        elf[18:20] = (183).to_bytes(2, 'little')
        data = bytes(elf)
        hashes = {name: installer.sha(data) for name in installer.BINARIES}
        def write(architecture='arm64', tamper=False, traversal=False):
            with zipfile.ZipFile(str(bundle), 'w') as archive:
                archive.writestr('manifest.json', json.dumps({'architecture': architecture, 'files': hashes}))
                for name in hashes:
                    archive.writestr(name, data + (b'tampered' if tamper else b''))
                if traversal:
                    archive.writestr('../outside', b'bad')
        write()
        self.assertEqual(set(installer.payload(bundle, 'arm64')), set(installer.BINARIES))
        for options in ({'architecture': 'amd64'}, {'tamper': True}, {'traversal': True}):
            write(**options)
            with self.assertRaises(ValueError):
                installer.payload(bundle, 'arm64')

    def test_restart_follows_application_dependencies(self):
        self.assertEqual(installer.restart_set([pathlib.Path('hifi-minimserver-lab/hifi-bubbleupnp.service')]),
                         {'hifi-bubbleupnp.service'})
        for path in ('hifi-minimserver.service', 'hifi-minimserver.service.d/usb-storage.conf'):
            self.assertEqual(installer.restart_set([pathlib.Path(path)]),
                             {'hifi-minimserver.service', 'hifi-music.service', 'hifi-manager.service'})

    def test_first_device_cli_generates_uuid_once_and_reuses_it(self):
        # Exercise the real CLI/config/payload/transaction path on an empty root.
        profile = dict(self.profile)
        profile.pop('browse_udn')
        elf = bytearray(64)
        elf[:6] = b'\x7fELF\x02\x01'
        elf[18:20] = (183).to_bytes(2, 'little')
        bundle = self.home / 'fresh.zip'
        with zipfile.ZipFile(str(bundle), 'w') as archive:
            for name in installer.BINARIES:
                archive.writestr(name, bytes(elf))
            archive.writestr('manifest.json', json.dumps({'architecture': 'arm64',
                'files': {name: installer.sha(bytes(elf)) for name in installer.BINARIES}}))
        inventory = json.dumps([{'addr_info': [{'local': '10.42.7.8', 'prefixlen': 24}]}])
        argv = ['install.py', '--profile-json', json.dumps(profile), '--bundle', str(bundle), '--apply']
        with patch('sys.argv', argv), patch.dict(os.environ, XDG_RUNTIME_DIR=str(self.home)), \
             patch.object(installer.platform, 'machine', return_value='aarch64'), \
             patch.object(installer, 'preflight', return_value='mount-a'), \
             patch.object(installer, 'mount_identity', return_value='mount-a'), \
             patch.object(installer, 'command', return_value=inventory), \
             patch.object(installer, 'systemctl', side_effect=self.systemctl), \
             patch.object(installer, 'activate'), patch.object(installer, 'health'):
            with redirect_stdout(io.StringIO()):
                installer.main()
            config = self.root / 'music-service/config.json'
            identity = json.loads(config.read_text())['browse_udn']
            self.assertTrue(identity.startswith('uuid:'))
            with redirect_stdout(io.StringIO()) as output:
                installer.main()
            self.assertEqual(json.loads(config.read_text())['browse_udn'], identity)
            self.assertEqual(json.loads(output.getvalue())['changed_files'], [])

if __name__ == '__main__':
    unittest.main()
