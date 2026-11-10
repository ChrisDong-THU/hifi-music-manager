"""Portable upload/job safety checks; no actual preflight or target operations."""
import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('installer_safety', pathlib.Path(__file__).with_name('install.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class PreflightSafetyTests(unittest.TestCase):
    def test_public_manager_domain_is_safe_as_a_unit_argument(self):
        self.assertEqual(installer.public_domain('Manage.Example.NET'), 'manage.example.net')
        for value in ('', 'localhost', '127.0.0.1', 'https://manage.example.net', 'manage.example.net:443',
                      'manage.example.net\nExecStart=', '*.example.net', 'manage_.example.net', None):
            with self.subTest(value=value), self.assertRaises(ValueError):
                installer.public_domain(value)

    def test_manager_health_requires_an_object_with_boolean_flags(self):
        for body in (b'null', b'[]', b'{"service":"hifi-manager","busy":1,"storage_ready":true}'):
            with self.subTest(body=body), self.assertRaisesRegex(ValueError, 'Invalid manager health response'):
                installer.manager_health(body)

    def test_active_manager_upload_blocks_install_even_if_new_port_differs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            saved = root / 'deployment/state.json'
            saved.parent.mkdir()
            saved.write_text(json.dumps({'web_port': 8787, 'manager_lan_hostname': 'nano.local'}))
            profile = dict(root=str(root), lan_ip='192.168.1.2', web_port=9793)
            with patch.object(installer, 'unit_state', return_value='active'), \
                 patch.object(installer, 'manager_endpoint', return_value=('https://nano.local:8787', None)) as endpoint, \
                 patch.object(installer, 'read_health', return_value={'busy': True}):
                with self.assertRaisesRegex(RuntimeError, 'upload'):
                    installer.ensure_manager_idle(profile)
            self.assertEqual(endpoint.call_args[0][0]['web_port'], 8787)
            self.assertEqual(endpoint.call_args[0][0]['manager_lan_hostname'], 'nano.local')

    def test_saved_offline_disk_is_not_replaced_by_configured_mount(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / 'config').mkdir()
            preference = root / 'config/data-disk.json'
            preference.write_text(json.dumps({'volume_id': 'ab' * 24}))
            with patch.object(pathlib.Path, 'read_text', side_effect=[preference.read_text(), '']), \
                 patch.object(installer, 'safe_path') as fallback:
                self.assertIsNone(installer.find_volume(root, '/media/another-disk'))
                fallback.assert_not_called()

    def test_absent_disk_is_distinct_from_read_only_mount(self):
        with patch.object(pathlib.Path, 'read_text', return_value='31 1 8:1 / /media/music rw - exfat /dev/sda1 rw\n'):
            self.assertEqual(installer.mount_identity('/media/music'), '31')
            self.assertIsNone(installer.mount_identity('/media/missing'))
        with patch.object(pathlib.Path, 'read_text', return_value='31 1 8:1 / /media/music ro - exfat /dev/sda1 ro\n'):
            with self.assertRaisesRegex(ValueError, 'read-only'):
                installer.mount_identity('/media/music')


if __name__ == '__main__':
    unittest.main()
