"""Build/cache contracts that run on the build computer without SSH or systemd."""
import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch
import zipfile

spec = importlib.util.spec_from_file_location('deployer', pathlib.Path(__file__).with_name('deploy.py'))
deployer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deployer)


class BuildTests(unittest.TestCase):
    def test_remote_installer_carries_the_exact_startup_launcher(self):
        script = deployer.installer_script()
        namespace = {}
        exec(script.split('\n', 1)[0], namespace)
        self.assertEqual(namespace['NETWORK_LAUNCHER'], pathlib.Path(__file__).with_name('launch.py').read_text(encoding='utf-8'))
        compile(script, '<remote-installer>', 'exec')

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)

    def test_only_runtime_and_validation_inputs_invalidate_source_cache(self):
        (self.root / 'app.go').write_text('package main')
        (self.root / 'README.md').write_text('guide')
        notices = self.root / 'manager-service/licenses'
        notices.mkdir(parents=True)
        (notices / 'frontend.txt').write_text('generated frontend notices')
        (notices / 'flaccompagnon-source.txt').write_text('generated analysis source')
        with patch.object(deployer, 'PROJECT', self.root):
            before = deployer.source_hash([self.root], 'toolchain')
            (self.root / 'README.md').write_text('changed guide')
            (self.root / 'build.log').write_text('diagnostics')
            (notices / 'frontend.txt').write_text('regenerated frontend notices')
            (notices / 'flaccompagnon-source.txt').write_text('regenerated analysis source')
            self.assertEqual(before, deployer.source_hash([self.root], 'toolchain'))
            (notices / 'MIT.txt').write_text('static license input')
            self.assertNotEqual(before, deployer.source_hash([self.root], 'toolchain'))
            (notices / 'MIT.txt').unlink()
            (self.root / 'app.go').write_text('package changed')
            self.assertNotEqual(before, deployer.source_hash([self.root], 'toolchain'))
            self.assertNotEqual(deployer.source_hash([self.root], 'old-node'), deployer.source_hash([self.root], 'new-node'))

    def write_bundle(self, changes=None, extra=None):
        data = {'hifi-manager': b'manager'}
        hashes = {name: hashlib.sha256(value).hexdigest() for name, value in data.items()}
        data.update(changes or {})
        path = self.root / 'bundle.zip'
        with zipfile.ZipFile(path, 'w') as bundle:
            for name, value in data.items():
                bundle.writestr(name, value)
            bundle.writestr('manifest.json', json.dumps({'architecture': 'arm64', 'files': hashes}))
            if extra:
                bundle.writestr(extra, b'unexpected')
        return path

    def test_valid_cache_requires_exact_files_architecture_and_hashes(self):
        path = self.write_bundle()
        self.assertTrue(deployer.cached_bundle_valid(path, 'arm64'))
        self.assertFalse(deployer.cached_bundle_valid(path, 'amd64'))
        self.assertFalse(deployer.cached_bundle_valid(self.write_bundle({'hifi-manager': b'tampered'}), 'arm64'))
        self.assertFalse(deployer.cached_bundle_valid(self.write_bundle(extra='../unexpected'), 'arm64'))

    def test_missing_or_corrupt_cache_can_be_rebuilt(self):
        path = self.root / 'missing.zip'
        self.assertFalse(deployer.cached_bundle_valid(path, 'arm64'))
        path.write_bytes(b'not an archive')
        self.assertFalse(deployer.cached_bundle_valid(path, 'arm64'))
        with zipfile.ZipFile(path, 'w') as bundle:
            bundle.writestr('manifest.json', 'null')
        self.assertFalse(deployer.cached_bundle_valid(path, 'arm64'))


if __name__ == '__main__':
    unittest.main()
