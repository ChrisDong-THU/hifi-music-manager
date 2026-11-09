"""Exercise fresh installation, failed upgrades and data preservation without a device."""
import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch
import zipfile
import sys

spec = importlib.util.spec_from_file_location('installer', pathlib.Path(__file__).with_name('install.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


@unittest.skipUnless(sys.platform.startswith('linux'), 'Installer tests require Linux paths and file modes')
class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = pathlib.Path(self.temp.name).resolve()
        self.root = self.home / 'hifi'
        self.root.mkdir()
        self.profile = dict(root=str(self.root), lan_ip='10.42.7.8',
                            lan_cidr='10.42.7.0/24', web_port=8787, webdav_public=False)
        self.calls = []
        self.active, self.enabled = set(), set()
        home = patch.object(pathlib.Path, 'home', return_value=self.home)
        home.start()
        self.addCleanup(home.stop)

    def systemctl(self, verb, *names):
        self.calls.append((verb,) + names)
        if verb == 'start':
            self.active.update(names)
        elif verb == 'stop':
            self.active.difference_update(names)
        elif verb == 'enable':
            self.enabled.update(names)
        elif verb == 'disable':
            self.enabled.difference_update(names)
        elif verb == 'show':
            _, prop, name = names
            value = {'ActiveState': 'active' if name in self.active else 'inactive',
                     'UnitFileState': 'enabled' if name in self.enabled else 'disabled',
                     'LoadState': 'loaded' if (installer.unit_directory() / name).is_file() else 'not-found'}[prop]
            return prop + '=' + value
        return ''

    def fixture(self):
        files = installer.render(self.profile)
        binary = self.root / installer.BINARIES['hifi-manager']
        files[binary] = (b'new manager', 0o755)
        legacy = installer.legacy_files(self.root)
        for path in legacy:
            installer.atomic(path, b'old owned artifact', 0o600)
        self.active.update((*installer.LEGACY_UNITS, installer.MANAGER, installer.LAN_REBIND))
        self.enabled.update((*installer.LEGACY_UNITS, installer.MANAGER, installer.LAN_REBIND))
        installer.atomic(binary, b'old manager', 0o755)
        preserved = [self.root / 'config/manager-auth.db', self.root / 'music-service/config.json',
                     self.root / 'minimserver/data/vendor-config', self.home / 'disk/hifi-music-nano/library-id',
                     self.home / 'disk/hifi-music-nano/manager/manager.db',
                     self.home / 'disk/hifi-music-nano/music/song.flac']
        for path in preserved:
            installer.atomic(path, b'persisted data', 0o600)
        return files, legacy, preserved, binary

    def apply(self, files, failure=None, reinstall=False):
        with patch.object(installer, 'systemctl', side_effect=self.systemctl), \
             patch.object(installer, 'ensure_manager_idle'), \
             patch.object(installer, 'manager_endpoint', return_value=('https://nano.local:8787', None)), \
             patch.object(installer, 'mount_identity', return_value='mount-a'), \
             patch.object(installer, 'health', side_effect=failure,
                          return_value={'storage_ready': True, 'volume_id': 'ab' * 24}):
            return installer.apply_files(self.profile, files, ('/media/music', 'mount-a', 'ab' * 24), reinstall=reinstall)

    def test_cutover_retires_only_legacy_deployment_files_and_is_idempotent(self):
        files, legacy, preserved, binary = self.fixture()
        result = self.apply(files, reinstall=True)
        self.assertEqual(set(result['retired_files']), {str(path) for path in legacy})
        self.assertFalse(set(installer.LEGACY_UNITS) & (self.active | self.enabled))
        self.assertTrue(all(not path.exists() for path in legacy))
        self.assertTrue(all(path.read_bytes() == b'persisted data' for path in preserved))
        self.assertEqual(binary.read_bytes(), b'new manager')
        self.assertEqual(self.active, {installer.MANAGER, installer.LAN_REBIND})
        manager = files[installer.unit_directory() / installer.MANAGER][0]
        self.assertNotIn(b'hifi-music.service', manager)
        self.assertNotIn(self.profile['lan_ip'].encode(), manager)
        self.calls.clear()
        result = self.apply(files)
        self.assertEqual(result['changed_files'], [])
        self.assertFalse(any(c[0] == 'stop' for c in self.calls))

    def test_failed_webdav_health_restores_old_units_and_keeps_user_data(self):
        files, legacy, preserved, binary = self.fixture()
        with self.assertRaisesRegex(RuntimeError, 'WebDAV not ready'):
            self.apply(files, RuntimeError('WebDAV not ready'))
        self.assertTrue(all(path.read_bytes() == b'old owned artifact' for path in legacy))
        self.assertTrue(all(path.read_bytes() == b'persisted data' for path in preserved))
        self.assertEqual(binary.read_bytes(), b'old manager')
        self.assertTrue(set(installer.LEGACY_UNITS) <= self.active)
        self.assertTrue(set(installer.LEGACY_UNITS) <= self.enabled)

    def test_mount_change_and_legacy_symlinks_are_rejected_before_stopping_services(self):
        files, legacy, _, binary = self.fixture()
        with patch.object(installer, 'ensure_manager_idle'), \
             patch.object(installer, 'manager_endpoint', return_value=('https://nano.local:8787', None)), \
             patch.object(installer, 'mount_identity', return_value='different'):
            with self.assertRaisesRegex(RuntimeError, 'mount changed'):
                installer.apply_files(self.profile, files, ('/media/music', 'mount-a', 'ab' * 24))
        legacy[0].unlink()
        legacy[0].symlink_to(binary)
        with self.assertRaisesRegex(ValueError, 'Unexpected symlink'):
            self.apply(files)
        self.assertEqual(binary.read_bytes(), b'old manager')
        self.assertEqual(self.calls, [])

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


if __name__ == '__main__':
    unittest.main()
