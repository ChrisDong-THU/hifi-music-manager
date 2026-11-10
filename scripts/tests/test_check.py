"""The shared check entrypoint must never turn a skipped or failed check green."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('project_check', Path(__file__).resolve().parents[1] / 'check.py')
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


class CheckTests(unittest.TestCase):
    def test_subprocess_failure_is_nonzero_and_stops_later_stages(self):
        with mock.patch.object(check, 'find_go', return_value=Path('go')), \
                mock.patch.object(check, 'frontend_checks', side_effect=subprocess.CalledProcessError(2, ['npm', 'test'])), \
                mock.patch.object(check, 'go_checks') as go, \
                mock.patch.object(check, 'python_checks') as python:
            self.assertEqual(check.main([]), 1)
            go.assert_not_called()
            python.assert_not_called()

    def test_runner_propagates_a_real_child_failure(self):
        with self.assertRaises(subprocess.CalledProcessError):
            check.run([sys.executable, '-c', 'raise SystemExit(7)'])

    def test_go_scope_builds_resources_and_requires_requested_race(self):
        with mock.patch.object(check, 'find_go', return_value=Path('go')), \
                mock.patch.object(check, 'frontend_checks') as frontend, \
                mock.patch.object(check, 'go_checks', side_effect=RuntimeError('C compiler missing')) as go, \
                mock.patch.object(check, 'python_checks') as python:
            self.assertEqual(check.main(['--scope', 'go', '--race']), 1)
            frontend.assert_called_once_with(test=False)
            go.assert_called_once_with(Path('go'), True, None)
            python.assert_not_called()

    def test_python_scope_needs_no_go_or_node(self):
        with mock.patch.object(check, 'find_go') as go, \
                mock.patch.object(check, 'frontend_checks') as frontend, \
                mock.patch.object(check, 'python_checks') as python:
            self.assertEqual(check.main(['--scope', 'python']), 0)
            go.assert_not_called()
            frontend.assert_not_called()
            python.assert_called_once_with()
            self.assertEqual(check.main(['--scope', 'python', '--race']), 1)
            self.assertEqual(check.main(['--scope', 'python', '--staticcheck', 'staticcheck']), 1)

    def test_go_checks_cannot_inherit_cross_compilation_or_download_a_toolchain(self):
        with mock.patch.dict(check.os.environ, {'GOOS': 'linux', 'GOARCH': 'arm64', 'GOWORK': '/unrelated/go.work', 'GOTOOLCHAIN': 'auto'}):
            environment = check.go_environment(True)
        self.assertNotIn('GOOS', environment)
        self.assertNotIn('GOARCH', environment)
        self.assertEqual(environment['GOWORK'], 'off')
        self.assertEqual(environment['GOTOOLCHAIN'], 'local')
        self.assertEqual(environment['CGO_ENABLED'], '1')

    def test_staticcheck_failure_stops_before_module_tests(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for module in check.MODULES:
                (root / module).mkdir()
                (root / module / 'main.go').write_text('package fixture\n')

            def execute(args, **kwargs):
                if str(args[0]) == 'staticcheck':
                    raise subprocess.CalledProcessError(1, ['staticcheck'])
                return subprocess.CompletedProcess(args, 0, stdout='')

            with mock.patch.object(check, 'PROJECT', root), \
                    mock.patch.object(check, 'run', side_effect=execute) as run:
                with self.assertRaises(subprocess.CalledProcessError):
                    check.go_checks(root / 'toolchain/go', False, 'staticcheck')
                module_tests = [call for call in run.call_args_list if call.args[0][1] == 'test' and './...' in call.args[0]]
                self.assertEqual(module_tests, [])
                analyzer = next(call for call in run.call_args_list if call.args[0][0] == 'staticcheck')
                self.assertTrue(analyzer.kwargs['env']['PATH'].startswith(str(root / 'toolchain') + check.os.pathsep))
                self.assertNotIn('GOROOT', analyzer.kwargs['env'])

    def test_relative_staticcheck_path_survives_module_working_directory(self):
        with mock.patch.object(check, 'find_go', return_value=Path('go')), \
                mock.patch.object(check.shutil, 'which', return_value='tools/staticcheck'), \
                mock.patch.object(check, 'frontend_checks'), \
                mock.patch.object(check, 'go_checks') as go:
            self.assertEqual(check.main(['--scope', 'go', '--staticcheck', 'tools/staticcheck']), 0)
            go.assert_called_once_with(Path('go'), False, str(Path('tools/staticcheck').resolve()))


if __name__ == '__main__':
    unittest.main()
