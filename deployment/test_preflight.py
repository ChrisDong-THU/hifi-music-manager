"""Portable upload/job safety checks; no actual preflight or target operations."""
import importlib.util
import io
import json
import pathlib
import ssl
import tempfile
import unittest
import urllib.error
import xmlrpc.client
from unittest.mock import Mock, patch
from types import SimpleNamespace

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

    def test_public_bubble_requires_an_initialized_account_without_remote_admin(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            path = root / 'bubbleupnp/configuration.xml'
            path.parent.mkdir()
            with self.assertRaisesRegex(ValueError, 'Complete local'):
                installer.check_bubble_account(root)
            settings = ('<configuration><firstRun>false</firstRun><login>owner</login>'
                        '<password>fixture</password><allowWANConnections>true</allowWANConnections></configuration>')
            path.write_text(settings)
            installer.check_bubble_account(root)
            for value in (settings.replace('>false<', '>true<'), settings.replace('>true<', '>false<'),
                          settings.replace('fixture', 'OBF:1u2a1toa1w8v1tok1u30'),
                          settings.replace('</configuration>', '<allowWANConfigurationUI>true</allowWANConfigurationUI></configuration>')):
                path.write_text(value)
                with self.assertRaises(ValueError):
                    installer.check_bubble_account(root)

    def test_manager_health_requires_an_object_with_boolean_flags(self):
        for body in (b'null', b'[]', b'{"service":"hifi-manager","busy":1,"storage_ready":true}'):
            with self.subTest(body=body), self.assertRaisesRegex(ValueError, 'Invalid manager health response'):
                installer.manager_health(body)

    def test_manager_tls_upgrade_checks_plain_health_before_legacy_state(self):
        protocol_error = ssl.SSLError('wrong version number')
        protocol_error.reason = 'WRONG_VERSION_NUMBER'
        tls = Mock()
        tls.open.side_effect = urllib.error.URLError(protocol_error)
        plain = Mock()
        plain.open.return_value = io.BytesIO(b'{"service":"hifi-manager","busy":true,"storage_ready":true}')
        with patch.object(installer.urllib.request, 'build_opener', return_value=plain):
            self.assertTrue(installer.manager_port_busy('https://192.168.1.5:8787', tls))
        plain.open.assert_called_once_with('http://192.168.1.5:8787/healthz', timeout=3)

    def test_manager_certificate_error_does_not_fall_back_to_http(self):
        tls = Mock()
        tls.open.side_effect = urllib.error.URLError(ssl.CertificateError('certificate mismatch'))
        with patch.object(installer.urllib.request, 'build_opener') as build_opener:
            with self.assertRaises(urllib.error.URLError):
                installer.manager_port_busy('https://192.168.1.5:8787', tls)
        build_opener.assert_not_called()

    def test_health_accepts_live_library_after_mount_path_changes(self):
        selected = '/media/new-mount/hifi-music-nano/music'
        p = dict(root='/home/service/hifi', volume='/media/old-mount', lan_ip='192.168.1.5',
                 web_port=8787, browse_port=9792, browse_udn='uuid:stable', manage_bubble=False,
                 backend_description='http://127.0.0.1:9791/native/Upnp/device.xml')
        state = json.dumps(dict(service='hifi-manager', busy=False, storage_ready=True)).encode()
        def wait(url, predicate, **kwargs):
            body = state if url.endswith('/healthz') else b'ContentDirectory Browse GetProtocolInfo uuid:stable'
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
        state = json.dumps(dict(service='hifi-manager', busy=False, storage_ready=False)).encode()
        checked = []
        def wait(url, predicate, **kwargs):
            self.assertTrue(predicate(state))
            checked.append(url)
            if len(checked) == 2:
                self.assertFalse(predicate(json.dumps(dict(service='hifi-manager', busy=False, storage_ready=True)).encode()))
        with patch.object(installer, 'mount_identity', return_value=None), patch.object(installer, 'wait_http', side_effect=wait):
            installer.health(profile)
        self.assertEqual(checked, ['http://192.168.1.2:8787/healthz'] * 2)

    def test_bubble_readiness_requires_digest_for_connection_and_server_information(self):
        profile = dict(root='/home/service/hifi', volume='/media/missing', lan_ip='192.168.1.2', web_port=8787,
                       bubble_public_domain='music.example.net')
        checked = []

        def check(domain, port, path, accept, body=None):
            self.assertEqual((domain, port), ('music.example.net', installer.BUBBLE_HTTPS_TUNNEL_PORT))
            self.assertTrue(accept(Mock(status=401, getheader=Mock(return_value='Digest realm="BubbleUPnP"'))))
            self.assertFalse(accept(Mock(status=404)))
            checked.append((path, xmlrpc.client.loads(body) if body is not None else None))

        with patch.object(installer, 'mount_identity', return_value=None), \
             patch.object(installer, 'wait_http'), patch.object(installer, 'check_public_https', side_effect=check):
            installer.health(profile)
        self.assertEqual(checked, [('/res/connectiontest', None), ('/xmlrpc', ((), 'upnpbridge.getServerInfo'))])

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
