"""Remote staging cleanup must not replace the actual deployment outcome."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zipfile

spec = importlib.util.spec_from_file_location('cleanup_deployer', Path(__file__).with_name('deploy.py'))
deployer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deployer)


class CleanupTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.bundle = Path(temp.name) / 'bundle.zip'
        with zipfile.ZipFile(self.bundle, 'w') as archive:
            archive.writestr('manifest.json', json.dumps({'files': {'hifi-manager': 'new'}, 'architecture': 'arm64'}))
        self.checked = json.dumps({'architecture': 'arm64', 'binary_sha256': {'hifi-manager': 'old'}})
        self.staging = '/tmp/hifi-deploy-ABC12345'
        self.device = {'ssh': 'fixture-device', 'profile': {}}

    def invoke(self, responses, copy_error=None):
        with patch.object(deployer.sys, 'argv', ['deploy.py', '--device', 'fixture.json', '--apply']), \
                patch.object(deployer, 'load_device', return_value=self.device), \
                patch.object(deployer, 'build', return_value=self.bundle), \
                patch.object(deployer, 'installer_script', return_value='fixture installer'), \
                patch.object(deployer, 'run', side_effect=copy_error), \
                patch.object(deployer, 'remote', side_effect=responses) as remote:
            deployer.main()
            return remote.call_args_list

    def test_copy_or_install_failure_survives_cleanup_failure(self):
        for stage in ('copy', 'install'):
            with self.subTest(stage=stage):
                failure = subprocess.CalledProcessError(17, [stage])
                cleanup = subprocess.TimeoutExpired(['ssh'], 360)
                responses = [self.checked, self.staging]
                if stage == 'install':
                    responses.append(failure)
                responses.append(cleanup)
                warning = io.StringIO()
                with contextlib.redirect_stderr(warning), self.assertRaises(subprocess.CalledProcessError) as caught:
                    self.invoke(responses, failure if stage == 'copy' else None)
                self.assertIs(caught.exception, failure)
                self.assertIn('cleanup', warning.getvalue().lower())
                self.assertIn(self.staging, warning.getvalue())

    def test_success_is_reported_even_if_remote_cleanup_fails(self):
        output, warning = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(warning):
            calls = self.invoke([self.checked, self.staging, json.dumps({'installed': True}),
                                 subprocess.CalledProcessError(1, ['ssh'])])
        result = json.loads(output.getvalue())
        self.assertTrue(result['installed'])
        self.assertIn('elapsed_seconds', result)
        self.assertIn('cleanup', warning.getvalue().lower())
        self.assertIn(self.staging, warning.getvalue())
        self.assertEqual(calls[-1][0][1][:2], ['python3', '-c'])


if __name__ == '__main__':
    unittest.main()
