"""Portable upload/job safety checks; no actual preflight or target operations."""
import importlib.util
import io
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location('installer_safety', pathlib.Path(__file__).with_name('install.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class PreflightSafetyTests(unittest.TestCase):
    def test_health_accepts_live_library_after_mount_path_changes(self):
        selected = '/media/new-mount/hifi-music-nano/music'
        p = dict(root='/home/service/hifi', volume='/media/old-mount', lan_ip='192.168.1.5',
                 web_port=8787, browse_port=9792, browse_udn='uuid:stable', manage_bubble=False,
                 backend_description='http://127.0.0.1:9791/native/Upnp/device.xml')
        state = json.dumps(dict(stats=dict(tracks=0), music_path=selected, storage=dict(ready=True))).encode()
        def wait(url, predicate):
            body = state if url.endswith('/api/state') else b'ContentDirectory Browse GetProtocolInfo uuid:stable'
            self.assertTrue(predicate(body), url)
        opener = SimpleNamespace(open=lambda *a, **k: io.BytesIO(b'<r><TotalMatches>4</TotalMatches></r>'))
        for first in (selected, None):
            with self.subTest(first_selection=first), \
                 patch.object(installer, 'active_library', side_effect=[first, selected, selected]), \
                 patch.object(installer, 'mount_identity', return_value=None), \
                 patch.object(installer, 'wait_http', side_effect=wait), \
                 patch.object(installer.urllib.request, 'build_opener', return_value=opener):
                installer.health(p)

    def test_active_library_follows_verified_mount_generation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory) / 'project'
            mount = pathlib.Path(directory) / 'new-mount'
            project = mount / 'hifi-music-nano'
            project.mkdir(parents=True)
            identity = 'ab' * 24
            (project / 'library-id').write_text(identity)
            (root / 'logs').mkdir(parents=True)
            selected = str(project / 'music')
            state = dict(selected=selected, mount=str(mount), data_root=str(project), volume=identity, mount_id='generation-b')
            (root / 'logs/library-state.json').write_text(json.dumps(state))
            with patch.object(installer, 'mount_identity', return_value='generation-b'):
                self.assertEqual(installer.active_library(root), selected)
            for generation in (None, 'generation-a'):
                with patch.object(installer, 'mount_identity', return_value=generation):
                    self.assertIsNone(installer.active_library(root))

    def test_absent_disk_is_distinct_from_read_only_mount(self):
        with patch.object(pathlib.Path, 'read_text', return_value='31 1 8:1 / /media/music rw - exfat /dev/sda1 rw\n'):
            self.assertEqual(installer.mount_identity('/media/music'), '31')
            self.assertIsNone(installer.mount_identity('/media/missing'))
        with patch.object(pathlib.Path, 'read_text', return_value='31 1 8:1 / /media/music ro - exfat /dev/sda1 ro\n'):
            with self.assertRaisesRegex(ValueError, 'read-only'):
                installer.mount_identity('/media/music')

    def test_offline_health_requires_web_to_report_no_active_storage(self):
        profile = dict(root='/home/service/hifi', volume='/media/missing', lan_ip='192.168.1.2', web_port=8787)
        state = json.dumps(dict(stats={}, music_path='', storage=dict(ready=False))).encode()
        checked = []
        def wait(url, predicate):
            self.assertTrue(predicate(state))
            checked.append(url)
            if len(checked) == 2:
                self.assertFalse(predicate(json.dumps(dict(stats={}, music_path='/stale', storage=dict(ready=True))).encode()))
        with patch.object(installer, 'mount_identity', return_value=None), patch.object(installer, 'wait_http', side_effect=wait):
            installer.health(profile)
        self.assertEqual(checked, ['http://192.168.1.2:8787/api/state'] * 2)

    def test_first_native_start_waits_for_discovery(self):
        with patch.object(installer, 'discover_backend', side_effect=[ValueError('starting'), 'ready']) as discover, patch.object(installer.time, 'sleep'):
            self.assertEqual(installer.wait_backend('192.168.1.2'), 'ready')
        self.assertEqual(discover.call_count, 2)

    def test_first_native_start_has_bounded_wait(self):
        with patch.object(installer, 'discover_backend', side_effect=ValueError('not ready')), patch.object(installer.time, 'monotonic', side_effect=[0, 46]):
            with self.assertRaisesRegex(ValueError, 'not ready'):
                installer.wait_backend('192.168.1.2')

    def test_uploads_and_queued_file_jobs_block_an_update(self):
        self.assertTrue(installer.manager_busy({'active_mutations': 1, 'jobs': []}))
        for kind in ('edit', 'import', 'scan', 'disks'):
            for state in ('queued', 'running'):
                self.assertTrue(installer.manager_busy({'jobs': [{'kind': kind, 'state': state}]}))

    def test_idle_history_and_playback_notifications_do_not_block(self):
        self.assertFalse(installer.manager_busy({'active_mutations': 0, 'jobs': []}))
        self.assertFalse(installer.manager_busy({'jobs': [
            {'kind': 'import', 'state': 'done'}, {'kind': 'sync', 'state': 'running'}]}))


if __name__ == '__main__':
    unittest.main()
