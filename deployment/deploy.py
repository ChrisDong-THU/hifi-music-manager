"""Build once and deploy over verified SSH. No device-side compiler is needed."""
import argparse
import hashlib
import json
import os
import pathlib
import shlex
import shutil
import subprocess
import sys
import time
import zipfile

PROJECT = pathlib.Path(__file__).resolve().parents[1]
OUTPUT = PROJECT / 'downloads/deployment'
INSTALLER = pathlib.Path(__file__).with_name('install.py')
SSH_OPTIONS = ['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=12', '-o', 'StrictHostKeyChecking=yes']


def run(args, **kwargs):
    print('> ' + ' '.join(str(x) for x in args[:4]), file=sys.stderr, flush=True)
    return subprocess.run([str(x) for x in args], check=True, **kwargs)


def remote(device, args, script=None):
    result = run(['ssh'] + SSH_OPTIONS + [device, ' '.join(shlex.quote(str(x)) for x in args)],
                 input=script, stdout=subprocess.PIPE, encoding='utf-8', timeout=360)
    return result.stdout.strip()


def installer_script():
    launcher = INSTALLER.with_name('launch.py').read_text(encoding='utf-8')
    return 'NETWORK_LAUNCHER = ' + repr(launcher) + '\n' + INSTALLER.read_text(encoding='utf-8')


def source_hash(paths, toolchain):
    digest = hashlib.sha256(toolchain.encode())
    for base in paths:
        for path in sorted(base.rglob('*')):
            if not path.is_file() or path.suffix.lower() in ('.md', '.log', '.exe', '.test', '.pyc') or any(p in ('node_modules', '__pycache__', 'web') for p in path.relative_to(base).parts):
                continue
            relative = str(path.relative_to(PROJECT)).replace('\\', '/')
            # Generated notices are derived from already-hashed source, locks
            # and generation scripts. Their pre-build contents must not make a
            # successful build miss its own cache on the next invocation.
            if relative in ('manager-service/licenses/frontend.txt',
                            'manager-service/licenses/flaccompagnon-source.txt'):
                continue
            digest.update(relative.encode())
            digest.update(path.read_bytes())
    return digest.hexdigest()


def cached_bundle_valid(bundle_path, architecture):
    try:
        with zipfile.ZipFile(str(bundle_path)) as bundle:
            manifest = json.loads(bundle.read('manifest.json').decode())
            names = {'hifi-manager'}
            return (manifest['architecture'] == architecture and
                    set(manifest['files']) == names and
                    len(bundle.namelist()) == len(names) + 1 and
                    set(bundle.namelist()) == names | {'manifest.json'} and all(
                hashlib.sha256(bundle.read(name)).hexdigest() == expected
                for name, expected in manifest['files'].items()))
    except (OSError, ValueError, KeyError, TypeError, zipfile.BadZipFile):
        return False


def build(architecture):
    compiler = PROJECT / 'downloads/go-admin/go/bin' / ('go.exe' if os.name == 'nt' else 'go')
    if not compiler.is_file():
        compiler = shutil.which('go')
    if not compiler:
        raise RuntimeError('Install Go (see go.mod) on the build computer')
    npm = shutil.which('npm.cmd' if os.name == 'nt' else 'npm')
    if not npm:
        raise RuntimeError('Install Node.js/npm on the build computer')
    environment = dict(os.environ, CGO_ENABLED='0', GOPATH=str(PROJECT / 'downloads/go-manager'),
                       GOCACHE=str(PROJECT / 'downloads/go-manager-cache'))
    environment.pop('GOOS', None)
    environment.pop('GOARCH', None)
    toolchain = subprocess.check_output([str(compiler), 'version'], env=environment).decode()
    toolchain += subprocess.check_output([npm, '--version']).decode()
    node = shutil.which('node')
    if not node:
        raise RuntimeError('Install Node.js on the build computer')
    toolchain += subprocess.check_output([node, '--version']).decode()
    toolchain += sys.version
    fingerprint = source_hash([PROJECT / 'manager-service', PROJECT / 'shared-library', PROJECT / 'scripts', PROJECT / 'deployment'], toolchain)
    output = OUTPUT / architecture / fingerprint
    bundle_path = output / 'bundle.zip'
    if cached_bundle_valid(bundle_path, architecture):
        print('Build cache hit; frontend and Go checks already passed for these inputs.', file=sys.stderr)
        return bundle_path
    output.mkdir(parents=True, exist_ok=True)
    run([sys.executable, '-B', '-m', 'unittest', 'discover', '-s', 'deployment', '-p', 'test_*.py'], cwd=str(PROJECT))
    run([sys.executable, '-B', '-m', 'unittest', 'discover', '-s', 'scripts/tests', '-p', 'test_*.py'], cwd=str(PROJECT))
    frontend = PROJECT / 'manager-service/frontend'
    run([npm, 'run', 'deps:check', '--prefix', frontend, '--', PROJECT / 'downloads/npm-manager-cache'])
    for task in ('test', 'build'):
        run([npm, 'run', task, '--prefix', frontend])
    files = {}
    run([compiler, 'vet', './...'], cwd=str(PROJECT / 'shared-library'), env=environment)
    run([compiler, 'test', '-count=1', '-timeout=90s', './...'], cwd=str(PROJECT / 'shared-library'), env=environment)
    for source, name in [('manager-service', 'hifi-manager')]:
        directory = PROJECT / source
        run([compiler, 'vet', './...'], cwd=str(directory), env=environment)
        run([compiler, 'test', '-count=1', '-timeout=90s', './...'], cwd=str(directory), env=environment)
        target = output / name
        target_env = dict(environment, GOOS='linux', GOARCH=architecture)
        run([compiler, 'build', '-trimpath', '-ldflags=-s -w', '-o', target, '.'], cwd=str(directory), env=target_env)
        files[name] = hashlib.sha256(target.read_bytes()).hexdigest()
    staged = output / 'bundle.zip.next'
    with zipfile.ZipFile(str(staged), 'w', compression=zipfile.ZIP_DEFLATED) as bundle:
        for name in files:
            bundle.write(str(output / name), name)
        bundle.writestr('manifest.json', json.dumps({'architecture': architecture, 'files': files}, sort_keys=True))
    os.replace(str(staged), str(bundle_path))
    return bundle_path


def load_device(path):
    device = json.loads(pathlib.Path(path).read_text(encoding='utf-8-sig'))
    if set(device) != {'ssh', 'profile'} or not isinstance(device['profile'], dict):
        raise ValueError('Device file must contain only ssh and profile')
    # SSH aliases retain the user's existing known_hosts trust and key selection.
    import re
    if not re.fullmatch(r'[A-Za-z0-9_@.:-]+', device['ssh']) or device['ssh'].startswith('-'):
        raise ValueError('Invalid SSH alias/address')
    return device


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--device')
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--check', action='store_true', help='read-only target preflight; no build or upload')
    mode.add_argument('--apply', action='store_true', help='build, install, start and verify; preserve all data')
    mode.add_argument('--reinstall', action='store_true', help='stop and replace the managed deployment; preserve all data')
    mode.add_argument('--build', choices=('arm64', 'amd64'), help='build a reusable offline bundle only')
    args = parser.parse_args()
    started = time.monotonic()
    if args.build:
        print(build(args.build))
        return
    if not args.device:
        parser.error('--device is required for target operations')
    device = load_device(args.device)
    profile = json.dumps(device['profile'], separators=(',', ':'))
    script = installer_script()
    checked = json.loads(remote(device['ssh'], ['python3', '-', '--profile-json', profile, '--check'], script))
    if args.check:
        print(json.dumps(checked, ensure_ascii=False, indent=2))
        return
    bundle = build(checked['architecture'])
    expected = ['--expect-volume', checked['volume_id']] if checked.get('volume_id') else []
    with zipfile.ZipFile(str(bundle)) as archive:
        manifest = json.loads(archive.read('manifest.json').decode())
    if not args.reinstall and checked['binary_sha256'] == manifest['files']:
        print('Executables unchanged; skipping file transfer.', file=sys.stderr)
        result = json.loads(remote(device['ssh'], ['python3', '-', '--profile-json', profile,
                    '--reuse-manifest', json.dumps(manifest), '--apply'] + expected, script))
        result['elapsed_seconds'] = round(time.monotonic() - started, 2)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return
    # The target installer validates the architecture, exact file list and every hash.
    # mktemp creates a private staging directory; never unpack arbitrary archive paths.
    staging = remote(device['ssh'], ['mktemp', '-d', '/tmp/hifi-deploy-XXXXXXXX'])
    import re
    if not re.fullmatch('/tmp/hifi-deploy-[A-Za-z0-9]+', staging):
        raise RuntimeError('Unexpected remote staging directory')
    try:
        run(['scp'] + SSH_OPTIONS + [bundle, device['ssh'] + ':' + staging + '/bundle.zip'])
        command = ['python3', '-', '--profile-json', profile, '--bundle', staging + '/bundle.zip', '--apply'] + expected
        if args.reinstall:
            command.append('--reinstall')
        result = json.loads(remote(device['ssh'], command, script))
        result['elapsed_seconds'] = round(time.monotonic() - started, 2)
        print(json.dumps(result, ensure_ascii=False, indent=2))
    finally:
        # Exact two paths created above; no recursive deletion or computed parent traversal.
        remote(device['ssh'], ['python3', '-c',
            'import os; p=' + repr(staging + '/bundle.zip') + '; os.unlink(p) if os.path.exists(p) else None; os.rmdir(' + repr(staging) + ')'])


if __name__ == '__main__':
    main()
