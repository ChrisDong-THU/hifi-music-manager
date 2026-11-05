"""Exercise real local publication/rollback without starting a manager or using music data."""
import os
import pathlib
import subprocess
import unittest


@unittest.skipUnless(os.name == 'nt', 'Windows-only local manager lifecycle')
class LocalManagerTests(unittest.TestCase):
    def test_publication_lock_and_rollback(self):
        script = pathlib.Path(__file__).with_name('manager-local-lifecycle.test.ps1')
        result = subprocess.run(
            ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', str(script)],
            capture_output=True, text=True, timeout=30, check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('5 local manager lifecycle checks passed.', result.stdout)
