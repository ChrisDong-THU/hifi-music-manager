"""Run the repository's checks without starting or deploying an instance."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys

PROJECT = Path(__file__).resolve().parents[1]
MODULES = ('shared-library', 'manager-service')


def run(args, **kwargs):
    command = [str(arg) for arg in args]
    display = command if len(command) <= 8 else command[:5] + ['... (' + str(len(command) - 5) + ' more arguments)']
    print('> ' + ' '.join(display), flush=True)
    if os.name == 'nt':
        kwargs['creationflags'] = subprocess.CREATE_NO_WINDOW
    if not kwargs.get('capture_output'):
        kwargs.setdefault('stdout', sys.stdout)
        kwargs.setdefault('stderr', sys.stderr)
    return subprocess.run(command, check=True, **kwargs)


def find_go(explicit=None):
    if explicit:
        compiler = shutil.which(explicit)
        if not compiler:
            raise RuntimeError('Go executable not found: ' + explicit)
        return Path(compiler).resolve()
    bundled = PROJECT / 'downloads/go-admin/go/bin' / ('go.exe' if os.name == 'nt' else 'go')
    compiler = str(bundled) if bundled.is_file() else shutil.which('go')
    if not compiler:
        raise RuntimeError('Install Go (see go.mod), or pass --go PATH')
    return Path(compiler).resolve()


def go_environment(race):
    environment = dict(os.environ, GOTOOLCHAIN='local',
                       GOPATH=str(PROJECT / 'downloads/go-manager'),
                       GOCACHE=str(PROJECT / 'downloads/go-manager-cache'),
                       CGO_ENABLED='1' if race else '0')
    # Checks run on the host. Cross-build settings must not make tests unexecutable.
    for name in ('GOOS', 'GOARCH', 'GOWORK'):
        environment.pop(name, None)
    environment['GOWORK'] = 'off'
    return environment


def frontend_checks(test):
    npm = shutil.which('npm.cmd' if os.name == 'nt' else 'npm')
    if not npm:
        raise RuntimeError('Install Node.js/npm to build embedded frontend resources')
    frontend = PROJECT / 'manager-service/frontend'
    run([npm, 'run', 'deps:check', '--prefix', frontend, '--', PROJECT / 'downloads/npm-manager-cache'], cwd=PROJECT)
    if test:
        run([npm, 'test', '--prefix', frontend], cwd=PROJECT)
    run([npm, 'run', 'build', '--prefix', frontend], cwd=PROJECT)


def go_checks(compiler, race, staticcheck=None):
    environment = go_environment(race)
    # Analyzers invoke "go" themselves; bind them to the selected toolchain too.
    environment['PATH'] = str(compiler.parent) + os.pathsep + environment.get('PATH', '')
    environment.pop('GOROOT', None)
    run([compiler, 'version'], env=environment)
    formatter = compiler.with_name('gofmt.exe' if os.name == 'nt' else 'gofmt')
    sources = []
    for module in MODULES:
        files = sorted((PROJECT / module).rglob('*.go'))
        files = [path for path in files if not {'node_modules', 'vendor', 'web'}.intersection(path.relative_to(PROJECT / module).parts)]
        if not files:
            raise RuntimeError('No Go sources found in ' + module)
        sources.extend(files)
    sources.extend(sorted((PROJECT / 'scripts').glob('check-architecture*.go')))
    unformatted = []
    for offset in range(0, len(sources), 50):
        result = run([formatter, '-l'] + sources[offset:offset + 50],
                     capture_output=True, text=True, env=environment)
        unformatted.extend(result.stdout.splitlines())
    if unformatted:
        raise RuntimeError('Run gofmt on:\n' + '\n'.join(unformatted))
    checker = PROJECT / 'scripts/check-architecture.go'
    checker_tests = PROJECT / 'scripts/check-architecture_test.go'
    run([compiler, 'test', '-count=1', checker, checker_tests], cwd=PROJECT, env=environment)
    run([compiler, 'run', checker, PROJECT], cwd=PROJECT, env=environment)
    for module in MODULES:
        directory = PROJECT / module
        run([compiler, 'vet', './...'], cwd=directory, env=environment)
        if staticcheck:
            run([staticcheck, './...'], cwd=directory, env=environment)
        args = [compiler, 'test', '-count=1', '-timeout=180s']
        if race:
            args.append('-race')
        run(args + ['./...'], cwd=directory, env=environment)


def python_checks():
    for directory in ('deployment', 'scripts/tests'):
        run([sys.executable, '-B', '-m', 'unittest', 'discover', '-s', directory, '-p', 'test_*.py'], cwd=PROJECT)


def check(scope='all', race=False, go=None, staticcheck=None):
    if race and scope not in ('all', 'go'):
        raise ValueError('--race requires --scope all or --scope go')
    if staticcheck and scope not in ('all', 'go'):
        raise ValueError('--staticcheck requires --scope all or --scope go')
    analyzer = None
    if staticcheck:
        analyzer = shutil.which(staticcheck)
        if not analyzer:
            raise RuntimeError('Staticcheck executable not found: ' + staticcheck)
        analyzer = str(Path(analyzer).resolve())
    compiler = find_go(go) if scope in ('all', 'go') else None
    if scope in ('all', 'frontend', 'go'):
        frontend_checks(test=scope != 'go')
    if compiler:
        go_checks(compiler, race, analyzer)
    if scope in ('all', 'python'):
        python_checks()
    print('Checks passed (scope=' + scope + ', race=' + ('on' if race else 'off') +
          ', staticcheck=' + ('on' if analyzer else 'off') + ').')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scope', choices=('all', 'frontend', 'go', 'python'), default='all')
    parser.add_argument('--race', action='store_true', help='require working race detection and a C compiler')
    parser.add_argument('--go', help='Go executable; defaults to the bundled toolchain or PATH')
    parser.add_argument('--staticcheck', help='optional preinstalled Staticcheck executable; never downloads tools')
    args = parser.parse_args(argv)
    try:
        check(args.scope, args.race, args.go, args.staticcheck)
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError) as error:
        print('Checks failed: ' + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
