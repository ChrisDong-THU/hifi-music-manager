"""Target-side installer. Python 3.6+, standard library, no destructive reset path."""
import argparse
import base64
import hashlib
import http.client
import ipaddress
import json
import os
import pathlib
import platform
import re
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request
import urllib.error
import zipfile
import xml.etree.ElementTree as ET


BINARIES = {"hifi-manager": "manager-service/hifi-manager"}
PRIVATE = [ipaddress.ip_network(n) for n in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")]
DNS_LABEL = r'[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?'
MANAGER_TUNNEL_PORT = 18787
LAN_REBIND = 'hifi-lan-rebind.service'


def public_domain(value):
    if (not isinstance(value, str) or len(value) > 253 or
            not re.fullmatch(r'(?:' + DNS_LABEL + r'\.)+' + DNS_LABEL, value) or
            re.fullmatch(r'[0-9.]+', value)):
        raise ValueError('Use a full public DNS name without a scheme or port')
    return value.lower()

def launcher_source():
    # deploy.py transmits the same source with this installer over stdin.
    return globals().get('NETWORK_LAUNCHER') or pathlib.Path(__file__).with_name('launch.py').read_text()

def command(*args):
    return subprocess.check_output(list(args), stderr=subprocess.PIPE, timeout=100).decode().strip()


def systemctl(*args):
    return command("systemctl", "--user", *args)


def private_network(value):
    network = ipaddress.ip_network(value, strict=False)
    if network.version != 4 or not any(network.network_address in n and network.broadcast_address in n for n in PRIVATE):
        raise ValueError("lan_cidr must be entirely inside an RFC1918 IPv4 network")
    return network


def safe_path(value):
    path = pathlib.Path(os.path.expanduser(value))
    # These paths are also used in systemd units. Keep a small, portable syntax.
    if not path.is_absolute() or str(path) == '/' or not re.fullmatch(r"/[A-Za-z0-9_./-]+", str(path)) or '..' in path.parts:
        raise ValueError("Use an absolute Linux path without spaces, escapes or '..': " + str(path))
    return path


def mount_identity(volume):
    """Return the live generation, or None while the configured disk is absent."""
    for line in pathlib.Path('/proc/self/mountinfo').read_text().splitlines():
        fields = line.split()
        if len(fields) > 6 and fields[4].replace('\\040', ' ') == volume:
            if 'rw' not in fields[5].split(','):
                raise ValueError('Music volume is mounted read-only: ' + volume)
            return fields[0]
    return None


class ManagerRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


class BoundHTTPSHandler(urllib.request.HTTPSHandler):
    def __init__(self, address, context, port=None):
        super().__init__(context=context)
        self.address = address
        self.port = port

    def https_open(self, request):
        address, port = self.address, self.port

        class LANConnection(http.client.HTTPSConnection):
            def connect(self):
                tcp = socket.create_connection((address, port or self.port), self.timeout)
                try:
                    self.sock = self._context.wrap_socket(tcp, server_hostname=self.host)
                except BaseException:
                    tcp.close()
                    raise

        # Keep the hostname for TLS and HTTP while dialing the known local address.
        return self.do_open(LANConnection, request, context=self._context)


def manager_endpoint(p, validate_tls=False):
    config = pathlib.Path(p['root']) / 'config'
    hostname = p.get('manager_lan_hostname')
    stem = 'manager-lan-tls' if hostname else 'manager-tls'
    cert, key, ca = (config / (stem + suffix) for suffix in ('-cert.pem', '-key.pem', '-ca.pem'))
    handlers = [urllib.request.ProxyHandler({}), ManagerRedirectHandler()]
    scheme = 'http'
    if hostname or cert.exists() or key.exists():
        if not cert.is_file() or not key.is_file():
            raise ValueError('Provide both ' + cert.name + ' and ' + key.name)
        if validate_tls:
            server_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            server_context.load_cert_chain(str(cert), str(key), password='')
        context = ssl.create_default_context(cafile=str(ca) if ca.is_file() else None)
        handlers.append(BoundHTTPSHandler(p['lan_ip'], context) if hostname else urllib.request.HTTPSHandler(context=context))
        scheme = 'https'
    return scheme + '://' + (hostname or p['lan_ip']) + ':' + str(p['web_port']), urllib.request.build_opener(*handlers)


def manager_health(body):
    state = json.loads(body.decode())
    if (not isinstance(state, dict) or state.get('service') != 'hifi-manager' or not isinstance(state.get('busy'), bool)
            or not isinstance(state.get('storage_ready'), bool)):
        raise ValueError('Invalid manager health response')
    return state


def unit(description, directory, executable, args='', memory='128MiB', dependencies=''):
    return ('[Unit]\nDescription=' + description + '\nAfter=network-online.target\n' + dependencies +
            '\n[Service]\nType=simple\nWorkingDirectory=' + str(directory) + '\nExecStart=' + str(executable) +
            (' ' + args if args else '') + '\nRestart=on-failure\nRestartSec=5\nTimeoutStopSec=90\n'
            'UMask=0077\nNoNewPrivileges=true\nEnvironment=GOMEMLIMIT=' + memory +
            '\nEnvironment=GOMAXPROCS=2\n\n[Install]\nWantedBy=default.target\n')


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_executable(data, name, expected, architecture):
    machine = int.from_bytes(data[18:20], 'little') if len(data) >= 20 else 0
    if not data.startswith(b'\x7fELF\x02\x01') or machine != {'arm64': 183, 'amd64': 62}[architecture] or sha(data) != expected:
        raise ValueError('Invalid executable/checksum: ' + name)


def payload(path, architecture):
    with zipfile.ZipFile(str(path)) as bundle:
        if sorted(bundle.namelist()) != sorted(list(BINARIES) + ['manifest.json']):
            raise ValueError('Bundle must contain only hifi-manager and manifest.json')
        if any(item.file_size > 80 * 1024 * 1024 for item in bundle.infolist()):
            raise ValueError('Oversized bundle member')
        manifest = json.loads(bundle.read('manifest.json').decode())
        if manifest['architecture'] != architecture or set(manifest['files']) != set(BINARIES):
            raise ValueError('Bundle architecture or file list does not match target')
        result = {}
        for name in BINARIES:
            data = bundle.read(name)
            validate_executable(data, name, manifest['files'][name], architecture)
            result[name] = data
        return result


def atomic(path, data, mode):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix='.' + path.name + '-', dir=str(path.parent))
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(temp, mode)
        os.replace(temp, str(path))
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


MANAGER = 'hifi-manager.service'
LEGACY_UNITS = ('hifi-bubble-relay.service', 'hifi-bubbleupnp.service',
                'hifi-music.service', 'hifi-minimserver.service')
XML_LIMIT = 2 * 1024 * 1024


def unit_directory():
    return pathlib.Path.home() / '.config/systemd/user'


def volume_id(mount):
    try:
        project = pathlib.Path(mount) / 'hifi-music-nano'
        path = project / 'library-id'
        if project.is_symlink() or path.is_symlink() or not path.is_file() or path.stat().st_size > 128:
            return None
        value = path.read_text().strip()
        return value if re.fullmatch('[0-9a-fA-F]{48}', value) else None
    except (OSError, ValueError):
        return None


def find_volume(root, configured):
    preference = root / 'config/data-disk.json'
    if preference.exists():
        selected = json.loads(preference.read_text())['volume_id']
        if not re.fullmatch('[0-9a-fA-F]{48}', selected):
            raise ValueError('Invalid saved data-disk identity')
        for line in pathlib.Path('/proc/self/mountinfo').read_text().splitlines():
            fields = line.split()
            if len(fields) > 6 and 'rw' in fields[5].split(','):
                mount = pathlib.Path(fields[4].replace('\\040', ' '))
                if volume_id(mount) == selected:
                    return mount
        return None  # An explicitly selected offline disk must not be replaced.
    return safe_path(configured) if configured else None


def resolve(profile):
    p = dict(profile)
    allowed = {'root', 'volume', 'lan_ip', 'lan_cidr', 'web_port', 'manager_public_domain',
               'manager_lan_hostname', 'webdav_public'}
    if set(p) - allowed:
        raise ValueError('Unknown device settings: ' + ', '.join(sorted(set(p) - allowed)))
    root = safe_path(p.get('root', '~/hifi'))
    if root.resolve() != root:
        raise ValueError('Use a real deployment directory rather than a symbolic link')
    p['root'] = str(root)
    if p.get('volume'):
        p['volume'] = str(safe_path(p['volume']))
    connection = os.environ.get('SSH_CONNECTION', '').split()
    ip = ipaddress.ip_address(p.get('lan_ip') or (connection[2] if len(connection) == 4 else ''))
    if ip.version != 4 or not any(ip in n for n in PRIVATE):
        raise ValueError('Set lan_ip to a literal private IPv4 address assigned to this device')
    p['lan_ip'] = str(ip)
    addresses = json.loads(command('ip', '-j', '-4', 'address', 'show'))
    prefix = next((a['prefixlen'] for interface in addresses for a in interface.get('addr_info', [])
                   if a.get('local') == str(ip)), None)
    if prefix is None:
        raise ValueError('lan_ip is not assigned to this device')
    p['lan_cidr'] = str(private_network(p.get('lan_cidr', str(ip) + '/' + str(prefix))))
    if ip not in ipaddress.ip_network(p['lan_cidr']):
        raise ValueError('lan_ip is outside lan_cidr')
    p.setdefault('web_port', 8787)
    if type(p['web_port']) is not int or not 1024 <= p['web_port'] <= 65535:
        raise ValueError('web_port must be an unprivileged TCP port')
    for field in ('manager_public_domain', 'manager_lan_hostname'):
        if field in p:
            p[field] = public_domain(p[field])
    if p.get('manager_lan_hostname') and not p['manager_lan_hostname'].endswith('.local'):
        raise ValueError('manager_lan_hostname must be a .local hostname')
    p.setdefault('webdav_public', False)
    if type(p['webdav_public']) is not bool or p['webdav_public'] and not p.get('manager_public_domain'):
        raise ValueError('webdav_public must be a boolean and requires manager_public_domain')
    return p


def public_endpoint(p, validate_tls=False):
    domain = p['manager_public_domain']
    config = pathlib.Path(p['root']) / 'config'
    if validate_tls:
        cert, key = (config / ('manager-public-tls-' + name + '.pem') for name in ('cert', 'key'))
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(cert), str(key), password='')
    context = ssl.create_default_context()

    return 'https://' + domain, urllib.request.build_opener(
        urllib.request.ProxyHandler({}), ManagerRedirectHandler(),
        BoundHTTPSHandler('127.0.0.1', context, MANAGER_TUNNEL_PORT))


def read_health(url, opener):
    with opener.open(url + '/healthz', timeout=4) as response:
        return manager_health(response.read(4096))


def unit_state(name, prop):
    try:
        return systemctl('show', '-p', prop, name).split('=', 1)[-1]
    except subprocess.SubprocessError:
        return ''


def installed_unit(name):
    return unit_state(name, 'LoadState') in ('loaded', 'masked')


def ensure_manager_idle(p):
    # Check the currently installed endpoint first, even if this deployment
    # changes its port, TLS identity or IP. The old unit remains intact here.
    if unit_state(MANAGER, 'ActiveState') == 'active':
        saved = pathlib.Path(p['root']) / 'deployment/state.json'
        previous = dict(p)
        if saved.is_file():
            old = json.loads(saved.read_text())
            for key in ('web_port', 'manager_lan_hostname'):
                previous.pop(key, None)
                if key in old:
                    previous[key] = old[key]
            previous.setdefault('web_port', 8787)
        old_url, old_opener = manager_endpoint(previous)
        if read_health(old_url, old_opener)['busy']:
            raise RuntimeError('Wait for the current upload or file task before deployment')
    else:
        # An unmanaged listener must never be replaced or mistaken for our unit.
        with socket.socket() as check:
            check.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            check.bind((p['lan_ip'], p['web_port']))


def preflight(p):
    root = pathlib.Path(p['root'])
    if not os.access(str(root if root.exists() else root.parent), os.W_OK):
        raise ValueError('Deployment directory is not writable')
    if not os.environ.get('XDG_RUNTIME_DIR') and not pathlib.Path('/run/user/' + str(os.getuid())).is_dir():
        raise ValueError('A systemd user session is required')
    if command('loginctl', 'show-user', str(os.getuid()), '-p', 'Linger') != 'Linger=yes':
        raise ValueError('Enable systemd user linger before installing')
    systemctl('list-unit-files', '--no-pager')
    url, _ = manager_endpoint(p, validate_tls=True)
    if not url.startswith('https://'):
        raise ValueError('LAN WebDAV and management require HTTPS certificates')
    if p.get('manager_public_domain'):
        public_endpoint(p, validate_tls=True)
    ensure_manager_idle(p)
    mount = find_volume(root, p.get('volume'))
    generation = mount_identity(str(mount)) if mount else None
    return (str(mount), generation, volume_id(mount)) if generation else (None, None, None)


def render(p):
    root = pathlib.Path(p['root'])
    units = unit_directory()
    launcher = root / 'deployment/launch-service.py'
    settings = {key: p[key] for key in ('lan_cidr', 'web_port', 'manager_public_domain',
                                       'manager_lan_hostname', 'webdav_public') if key in p}
    return {
        root / 'config/manager-network.json': ((json.dumps(settings, indent=2) + '\n').encode(), 0o600),
        launcher: (launcher_source().encode(), 0o600),
        units / MANAGER: (unit('HiFi music manager and WebDAV', root / 'manager-service',
                               '/usr/bin/python3', str(launcher) + ' manager ' + str(root), '256MiB').encode(), 0o644),
        units / LAN_REBIND: (unit('Refresh HiFi listener after a LAN address change', root,
                                  '/usr/bin/python3', str(launcher) + ' watch ' + str(root)).encode(), 0o644),
    }


def dav_request(url, opener, method, status, headers=None, data=None):
    request = urllib.request.Request(url + '/webdav/', data=data, method=method, headers=headers or {})
    try:
        response = opener.open(request, timeout=5)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        if response.code != status:
            raise ValueError('WebDAV ' + method + ' returned ' + str(response.code) + ', expected ' + str(status))
        if status == 401 and not response.headers.get('WWW-Authenticate', '').startswith('Basic '):
            raise ValueError('WebDAV did not issue a Basic challenge')
        if status == 207:
            body = response.read(XML_LIMIT + 1)
            if len(body) > XML_LIMIT:
                raise ValueError('WebDAV property response exceeds the health check limit')
            xml = ET.fromstring(body)
            if xml.tag != '{DAV:}multistatus' or xml.findtext('{DAV:}response/{DAV:}href') != '/webdav/':
                raise ValueError('WebDAV did not describe its root collection')


def health(p, selection, timeout=90):
    root = pathlib.Path(p['root'])
    url, opener = manager_endpoint(p)
    deadline = time.monotonic() + timeout
    expected_id = selection[2]
    last = None
    while True:
        try:
            state = read_health(url, opener)
            if expected_id and (not state['storage_ready'] or state.get('volume_id') != expected_id):
                raise ValueError('The expected music disk is not active')
            with opener.open(url + '/', timeout=5) as response:
                if b'<div id="root">' not in response.read(65536):
                    raise ValueError('Management Web shell not ready')
            key = (root / 'config/webdav.key').read_bytes()
            if len(key) != 32:
                raise ValueError('Invalid WebDAV key')
            password = base64.urlsafe_b64encode(key).rstrip(b'=')
            headers = {'Depth': '0', 'Authorization': 'Basic ' + base64.b64encode(b'hifi:' + password).decode()}
            status = 207 if state['storage_ready'] else 503
            dav_request(url, opener, 'PROPFIND', 401, {'Depth': '0'})
            dav_request(url, opener, 'PROPFIND', status, headers)
            if p.get('manager_public_domain'):
                pub_url, pub_opener = public_endpoint(p)
                with pub_opener.open(pub_url + '/api/auth/status', timeout=5) as response:
                    if json.loads(response.read(4096)) != {'authenticated': False}:
                        raise ValueError('Invalid public management response')
                dav_request(pub_url, pub_opener, 'PROPFIND', 401 if p['webdav_public'] else 404, {'Depth': '0'})
                if p['webdav_public']:
                    dav_request(pub_url, pub_opener, 'PROPFIND', status, headers)
            return state
        except (OSError, ValueError, ET.ParseError) as error:
            last = error
        if time.monotonic() >= deadline:
            raise RuntimeError('Management Web / WebDAV readiness failed: ' + str(last))
        time.sleep(0.5)


def legacy_files(root):
    units = unit_directory()
    return [*(units / name for name in LEGACY_UNITS),
            units / 'hifi-minimserver.service.d/usb-storage.conf',
            root / 'music-service/hifi-music-service']


def read_managed(path):
    # Never follow user-controlled symlinks when taking backups or replacing files.
    if path.resolve() != path:
        raise ValueError('Unexpected symlink in managed path: ' + str(path))
    try:
        with path.open('rb') as stream:
            return stream.read(), os.fstat(stream.fileno()).st_mode & 0o777
    except FileNotFoundError:
        return None


def apply_files(p, files, selection, reinstall=False):
    root = pathlib.Path(p['root'])
    url, _ = manager_endpoint(p)
    ensure_manager_idle(p)
    if selection[0] and mount_identity(selection[0]) != selection[1]:
        raise RuntimeError('Music mount changed after preflight')
    changes, previous = {}, {}
    for path, value in files.items():
        installed = read_managed(path)
        if reinstall or installed != value:
            changes[path], previous[path] = value, installed
    for path in legacy_files(root):
        installed = read_managed(path)
        if installed is not None:
            changes[path], previous[path] = None, installed
    legacy = [name for name in LEGACY_UNITS if installed_unit(name)]
    names = (LAN_REBIND, MANAGER) + tuple(legacy)
    active = {name for name in names if unit_state(name, 'ActiveState') == 'active'}
    enabled = {name for name in names if unit_state(name, 'UnitFileState') == 'enabled'}
    changed_units = bool(changes or legacy)
    if changes:
        backup = root / 'deployment/previous'
        if backup.resolve() != backup:
            raise ValueError('Unexpected symlink in the deployment backup path')
        manifest = {}
        for i, (path, value) in enumerate(previous.items()):
            if value is not None:
                atomic(backup / str(i), value[0], 0o600)
                manifest[str(i)] = {'path': str(path), 'mode': value[1]}
        atomic(backup / 'manifest.json', json.dumps(manifest).encode(), 0o600)
    if changed_units:
        ensure_manager_idle(p)
    try:
        if changed_units:
            for name in names:
                if name in active:
                    systemctl('stop', name)
            for name in legacy:
                if name in enabled:
                    systemctl('disable', name)
            for path, value in changes.items():
                if value is None:
                    path.unlink()
                else:
                    atomic(path, value[0], value[1])
            systemctl('daemon-reload')
        missing = [name for name in (MANAGER, LAN_REBIND) if name not in enabled]
        if missing:
            systemctl('enable', *missing)
        systemctl('start', MANAGER, LAN_REBIND)
        state = health(p, selection)
        for name in LEGACY_UNITS:
            if unit_state(name, 'ActiveState') == 'active':
                raise RuntimeError('Retired service is still active: ' + name)
        atomic(root / 'deployment/state.json', json.dumps(p, indent=2).encode(), 0o600)
    except BaseException:
        if changed_units:
            for name in (LAN_REBIND, MANAGER):
                try:
                    if installed_unit(name):
                        systemctl('stop', name)
                        if name not in enabled:
                            systemctl('disable', name)
                except subprocess.SubprocessError:
                    pass
            for path, value in previous.items():
                if value is None:
                    if path.exists():
                        path.unlink()
                else:
                    atomic(path, value[0], value[1])
            systemctl('daemon-reload')
            if enabled:
                systemctl('enable', *sorted(enabled))
            # Restore old services in dependency order.
            if active:
                systemctl('start', *sorted(active))
        raise
    result = {'changed_files': [str(path) for path in changes if changes[path] is not None],
              'retired_files': [str(path) for path in changes if changes[path] is None],
              'retired_services': legacy, 'storage_ready': state['storage_ready'],
              'volume_id': state.get('volume_id'), 'data_preserved': True, 'web_url': url, 'webdav_url': url + '/webdav/'}
    if p.get('manager_public_domain'):
        result['public_web_url'] = 'https://' + p['manager_public_domain']
        if p['webdav_public']:
            result['public_webdav_url'] = result['public_web_url'] + '/webdav/'
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--profile-json', required=True)
    parser.add_argument('--bundle')
    parser.add_argument('--reuse-manifest')
    parser.add_argument('--expect-volume')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--reinstall', action='store_true')
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    if args.reinstall and (not args.apply or not args.bundle or args.check):
        parser.error('--reinstall requires --apply and a verified --bundle')
    p = resolve(json.loads(args.profile_json))
    architecture = {'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine()]
    root = pathlib.Path(p['root'])
    if not args.apply:
        selection = preflight(p)
        print(json.dumps({'architecture': architecture, 'profile': p, 'prerequisites': 'ready',
                          'disk_mounted': selection[1] is not None, 'volume_id': selection[2],
                          'binary_sha256': {name: sha((root / relative).read_bytes())
                                            for name, relative in BINARIES.items() if (root / relative).is_file()}}))
        return
    if bool(args.bundle) == bool(args.reuse_manifest):
        parser.error('--apply requires exactly one of --bundle or --reuse-manifest')
    # Verify the complete bundle before stopping anything on the device.
    if args.bundle:
        executables = payload(args.bundle, architecture)
    else:
        manifest = json.loads(args.reuse_manifest)
        if manifest['architecture'] != architecture or set(manifest['files']) != set(BINARIES):
            raise ValueError('Invalid reuse manifest')
        executables = {name: (root / relative).read_bytes() for name, relative in BINARIES.items()}
        for name, data in executables.items():
            validate_executable(data, name, manifest['files'][name], architecture)
    files = render(p)
    for name, data in executables.items():
        files[root / BINARIES[name]] = (data, 0o755)
    import fcntl
    lock_path = pathlib.Path(os.environ.get('XDG_RUNTIME_DIR', '/run/user/' + str(os.getuid()))) / 'hifi-deploy.lock'
    with lock_path.open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        selection = preflight(p)
        if args.expect_volume and selection[2] != args.expect_volume:
            raise RuntimeError('The selected music disk changed after the initial preflight')
        print(json.dumps(apply_files(p, files, selection, reinstall=args.reinstall)))


if __name__ == '__main__':
    main()
