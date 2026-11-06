"""Target-side installer. Python 3.6+, standard library, no destructive reset path."""
import argparse
import hashlib
import ipaddress
import json
import os
import pathlib
import platform
import re
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.request
import urllib.error
import uuid
import zipfile
import xml.etree.ElementTree as ET


BINARIES = {"hifi-manager": "manager-service/hifi-manager",
            "hifi-music-service": "music-service/hifi-music-service"}
PRIVATE = [ipaddress.ip_network(n) for n in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")]

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


def discover_backend(ip):
    """Find this device's initialized MinimServer; never copy another server's UDN."""
    from urllib.parse import urlsplit, urlunsplit
    request = ('M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: "ssdp:discover"\r\n'
               'MX: 1\r\nST: urn:schemas-upnp-org:device:MediaServer:1\r\n\r\n').encode()
    locations = set()
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.bind((ip, 0))
        sock.settimeout(0.5)
        sock.sendto(request, ('239.255.255.250', 1900))
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            try:
                response, peer = sock.recvfrom(8192)
            except socket.timeout:
                continue
            if peer[0] != ip:
                continue
            for line in response.decode('utf-8', errors='replace').splitlines():
                key, separator, value = line.partition(':')
                if separator and key.lower() == 'location':
                    url = urlsplit(value.strip())
                    if url.scheme == 'http' and url.hostname == ip and url.port in (9790, 9791) and not url.username:
                        locations.add(urlunsplit(('http', '127.0.0.1:' + str(url.port), url.path, url.query, '')))
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    candidates = []
    for url in sorted(locations):
        try:
            with opener.open(url, timeout=3) as response:
                document = ET.fromstring(response.read(1024 * 1024))
            if any('minimserver' in (node.text or '').lower() for node in document.iter()
                   if node.tag.endswith(('manufacturer', 'modelName'))):
                candidates.append(url)
        except (OSError, ET.ParseError):
            continue
    if len(candidates) != 1:
        raise ValueError('Cannot identify exactly one local MinimServer; initialize it first or set backend_description explicitly')
    return candidates[0]


def wait_backend(ip, timeout=45):
    """Native service activation returns before Java has advertised its device."""
    deadline = time.monotonic() + timeout
    while True:
        try:
            return discover_backend(ip)
        except ValueError:
            if time.monotonic() >= deadline:
                raise
            time.sleep(0.5)


def resolve(profile):
    p = dict(profile)
    allowed = {'root', 'volume', 'lan_ip', 'lan_cidr', 'web_port', 'browse_port', 'name',
               'manage_minim', 'manage_bubble', 'backend_description', 'browse_udn', 'java_home'}
    if set(p) - allowed:
        raise ValueError('Unknown device settings: ' + ', '.join(sorted(set(p) - allowed)))
    p["root"] = str(safe_path(p.get("root", "~/hifi")))
    root = pathlib.Path(p['root'])
    selected = active_library(root)
    if selected:
        # volume is an initial provisioning hint. Desktop automount names can
        # change; a verified supervisor selection is authoritative thereafter.
        p['volume'] = str(pathlib.Path(selected).parents[1])
    if not p.get('volume'):
        selection = root / 'logs/library-state.json'
        current = json.loads(selection.read_text()) if selection.exists() else {}
        if not current.get('selected'):
            raise ValueError('Connect a music disk first, or set volume for initial installation')
        p['volume'] = current.get('mount') or str(pathlib.Path(current['selected']).parent)
    p['volume'] = str(safe_path(p['volume']))
    if "lan_ip" not in p:
        connection = os.environ.get('SSH_CONNECTION', '').split()
        if len(connection) != 4:
            raise ValueError("Set lan_ip when running outside SSH")
        p['lan_ip'] = connection[2]
    ip = ipaddress.ip_address(p['lan_ip'])
    if ip.version != 4 or not any(ip in n for n in PRIVATE):
        raise ValueError("lan_ip must be a literal private IPv4 address")
    addresses = json.loads(command('ip', '-j', '-4', 'address', 'show'))
    prefix = next((a['prefixlen'] for interface in addresses for a in interface.get('addr_info', [])
                   if a.get('local') == str(ip)), None)
    if prefix is None:
        raise ValueError("lan_ip is not assigned to this device")
    p['lan_cidr'] = str(private_network(p.get('lan_cidr', str(ip) + '/' + str(prefix))))
    if ip not in ipaddress.ip_network(p['lan_cidr']):
        raise ValueError("lan_ip is outside lan_cidr")
    for key, default in [('web_port', 8787), ('browse_port', 9792)]:
        p[key] = p.get(key, default)
        if type(p[key]) is not int or not 1024 <= p[key] <= 65535:
            raise ValueError(key + ' must be an unprivileged TCP port')
    if p['web_port'] == p['browse_port']:
        raise ValueError('Web and music ports must differ')
    p.setdefault('name', 'hifi-music')
    if not isinstance(p['name'], str) or not p['name'].strip() or len(p['name']) > 100:
        raise ValueError('Invalid device name')
    p.setdefault('manage_minim', True)
    p.setdefault('manage_bubble', False)
    for key in ('manage_minim', 'manage_bubble'):
        if type(p[key]) is not bool:
            raise ValueError(key + ' must be a boolean')
    existing_path = root / 'music-service/config.json'
    existing = json.loads(existing_path.read_text()) if existing_path.exists() else {}
    for key in ('browse_udn', 'backend_description'):
        if key in existing and key in p and p[key] != existing[key]:
            raise ValueError('Refusing to change existing playback identity: ' + key)
        if key in existing:
            p[key] = existing[key]
    if 'backend_description' not in p:
        p['backend_description'] = discover_backend(str(ip))
    from urllib.parse import urlsplit
    backend = urlsplit(p['backend_description'])
    if backend.scheme != 'http' or backend.hostname != '127.0.0.1' or backend.port not in (9790, 9791) or backend.username or backend.password or backend.fragment:
        raise ValueError('backend_description must be a loopback MinimServer URL on port 9790/9791')
    if 'browse_udn' in p:
        if 'uuid:' + str(uuid.UUID(p['browse_udn'][5:])) != p['browse_udn']:
            raise ValueError('Invalid browse_udn')
    if p.get('java_home'):
        p['java_home'] = str(safe_path(p['java_home']))
    state = root / 'deployment/state.json'
    if state.exists():
        previous = json.loads(state.read_text())
        if previous['root'] != p['root']:
            raise ValueError('Deployment root changed; refusing to relocate existing installation')
    return p


def mount_identity(volume):
    """Return the live generation, or None while the configured disk is absent."""
    for line in pathlib.Path('/proc/self/mountinfo').read_text().splitlines():
        fields = line.split()
        if len(fields) > 6 and fields[4].replace('\\040', ' ') == volume:
            if 'rw' not in fields[5].split(','):
                raise ValueError('Music volume is mounted read-only: ' + volume)
            return fields[0]
    return None


def manager_busy(state):
    return bool(state.get('active_mutations', 0) or any(
        job.get('state') in ('queued', 'running') and job.get('kind') != 'sync'
        for job in state.get('jobs', [])))


class ManagerRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


def manager_endpoint(p, validate_tls=False):
    config = pathlib.Path(p['root']) / 'config'
    cert, key = (config / name for name in ('manager-tls-cert.pem', 'manager-tls-key.pem'))
    handlers = [urllib.request.ProxyHandler({}), ManagerRedirectHandler()]
    scheme = 'http'
    if cert.exists() or key.exists():
        if not cert.is_file() or not key.is_file():
            raise ValueError('Provide both manager-tls-cert.pem and manager-tls-key.pem')
        if validate_tls:
            server_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            server_context.load_cert_chain(str(cert), str(key), password='')
        ca = config / 'manager-tls-ca.pem'
        context = ssl.create_default_context(cafile=str(ca) if ca.is_file() else None)
        handlers.append(urllib.request.HTTPSHandler(context=context))
        scheme = 'https'
    return scheme + '://' + p['lan_ip'] + ':' + str(p['web_port']), urllib.request.build_opener(*handlers)


def manager_health(body):
    state = json.loads(body.decode())
    if (not isinstance(state, dict) or state.get('service') != 'hifi-manager' or not isinstance(state.get('busy'), bool)
            or not isinstance(state.get('storage_ready'), bool)):
        raise ValueError('Invalid manager health response')
    return state


def manager_port_busy(url, opener):
    def read_busy(endpoint, client):
        try:
            with client.open(endpoint + '/healthz', timeout=3) as response:
                return manager_health(response.read(4096))['busy']
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise
            error.close()
        with client.open(endpoint + '/api/state', timeout=3) as response:
            state = json.loads(response.read(2 * 1024 * 1024).decode())
        if not isinstance(state, dict) or 'music_path' not in state or 'stats' not in state:
            raise ValueError('Port is occupied by another service: ' + endpoint)
        return manager_busy(state)

    try:
        return read_busy(url, opener)
    except urllib.error.URLError as error:
        # The process already on the port may still speak HTTP during a TLS upgrade.
        # Certificate errors must never trigger this read-only compatibility probe.
        if (not url.startswith('https://') or not isinstance(error.reason, ssl.SSLError)
                or getattr(error.reason, 'reason', None) != 'WRONG_VERSION_NUMBER'):
            raise
    plain_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), ManagerRedirectHandler())
    return read_busy(url.replace('https://', 'http://', 1), plain_opener)


def preflight(p):
    if platform.system() != 'Linux' or platform.machine() not in ('aarch64', 'x86_64'):
        raise ValueError('Target must be Linux ARM64 or x86_64')
    if os.getuid() == 0:
        raise ValueError('Run as the normal service owner, not root')
    root, volume = pathlib.Path(p['root']), pathlib.Path(p['volume'])
    manager_url, manager_opener = manager_endpoint(p, validate_tls=True)
    generation = mount_identity(str(volume))
    inventory = json.loads(command('lsblk', '-J', '-p', '-o', 'NAME,TYPE,TRAN,MOUNTPOINT'))
    def usb(nodes, bus=''):
        return any((n.get('tran') or bus) == 'usb' and n.get('mountpoint') == str(volume)
                   or usb(n.get('children', []), n.get('tran') or bus) for n in nodes)
    if generation is not None and not usb(inventory['blockdevices']):
        raise ValueError('Music volume must be a mounted USB device')
    for path in (root, volume, root / 'music-service', root / 'manager-service',
                 volume / 'hifi-music-nano', volume / 'hifi-music-nano/music', volume / 'hifi-music-nano/manager'):
        if path.is_symlink() or any(parent.is_symlink() for parent in path.parents):
            raise ValueError('Deployment/storage directories cannot contain symlinks: ' + str(path))
    music_link = root / 'music'
    if music_link.exists() or music_link.is_symlink():
        if not music_link.is_symlink() or music_link.resolve() not in (volume/'hifi-music-nano/music',):
            raise ValueError('Existing music entry points to a different library')
    systemctl('show-environment')
    if command('loginctl', 'show-user', str(os.getuid()), '-p', 'Linger', '--value') != 'yes':
        raise ValueError('Administrator prerequisite: sudo loginctl enable-linger ' + command('id', '-un'))
    if not (root / 'minimserver/bin/startd').is_file():
        raise ValueError('Install and initialize official MinimServer first; see deployment/README.md')
    minim_profile = root / 'minimserver/data/default.profile'
    settings = dict(line.split('=', 1) for line in minim_profile.read_text().splitlines()
                    if '=' in line and not line.lstrip().startswith('#')) if minim_profile.exists() else {}
    content = next((value.strip() for key, value in settings.items() if key.strip() == 'contentDir'), '')
    if content not in (str(root / 'music'), str(volume/'hifi-music-nano/music')):
        raise ValueError('Initialize MinimServer contentDir to ' + str(root / 'music'))
    units = pathlib.Path.home() / '.config/systemd/user'
    if not p['manage_minim'] and not (units / 'hifi-minimserver.service').is_file():
        raise ValueError('External hifi-minimserver.service is missing')
    if p['manage_bubble'] and not (root / 'bubbleupnp/launch.sh').is_file():
        raise ValueError('Install official BubbleUPnP distribution first')
    java = pathlib.Path(p['java_home']) / 'bin/java' if p.get('java_home') else shutil.which('java')
    if (p['manage_minim'] or p['manage_bubble']) and (not java or not os.access(str(java), os.X_OK)):
        raise ValueError('A Java runtime is required; set java_home or install Java on PATH')
    base = root if root.exists() else root.parent
    if not os.access(str(base), os.W_OK) or generation is not None and not os.access(str(volume), os.W_OK):
        raise ValueError('Service owner cannot write deployment or music volume')
    if shutil.disk_usage(str(base)).free < 150 * 1024 * 1024:
        raise ValueError('Need at least 150 MiB free for staging and rollback')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    for port, path, expected in ((p['web_port'], '/healthz', b''),
                                 (p['browse_port'], '/device.xml', p.get('browse_udn', 'new-device').encode())):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            try:
                sock.bind((p['lan_ip'], port))
                continue
            except OSError:
                pass
        try:
            if path == '/healthz':
                if manager_port_busy(manager_url, manager_opener):
                    raise ValueError('Manager has uploads or active file jobs; retry deployment after they finish')
            else:
                with opener.open('http://' + p['lan_ip'] + ':' + str(port) + path, timeout=3) as response:
                    body = response.read(2 * 1024 * 1024)
                if expected not in body:
                    raise ValueError('Port is occupied by another service: ' + str(port))
        except OSError as error:
            raise ValueError('Port {} is occupied but the existing service is not healthy: {}'.format(port, error)) from error
    return generation


def unit(description, directory, executable, args='', memory='128MiB', dependencies=''):
    return ('[Unit]\nDescription=' + description + '\nAfter=network-online.target\n' + dependencies +
            '\n[Service]\nType=simple\nWorkingDirectory=' + str(directory) + '\nExecStart=' + str(executable) +
            (' ' + args if args else '') + '\nRestart=on-failure\nRestartSec=5\nTimeoutStopSec=90\n'
            'UMask=0077\nNoNewPrivileges=true\nEnvironment=GOMEMLIMIT=' + memory +
            '\nEnvironment=GOMAXPROCS=2\n\n[Install]\nWantedBy=default.target\n')


def render(p):
    root, volume = pathlib.Path(p['root']), pathlib.Path(p['volume'])
    units = pathlib.Path.home() / '.config/systemd/user'
    config = dict(browse_udn=p['browse_udn'], browse_listen=p['lan_ip'] + ':' + str(p['browse_port']),
                  backend_description=p['backend_description'], lan_cidr=p['lan_cidr'], name=p['name'])
    launcher = root / 'deployment/launch-service.py'
    files = {root / 'music-service/config.json': (json.dumps(config, indent=2) + '\n', 0o600),
             units / 'hifi-music.service': (unit('HiFi music USB supervision and UPnP presentation', root / 'music-service',
                 '/usr/bin/python3', str(launcher) + ' music ' + str(root) + ' ' + str(p['web_port'])), 0o644),
             launcher: (launcher_source(), 0o600)}
    args = str(launcher) + ' manager ' + str(root) + ' ' + str(p['web_port'])
    files[units / 'hifi-manager.service'] = (unit('HiFi Web music manager', root / 'manager-service',
        '/usr/bin/python3', args, '256MiB',
        'After=hifi-music.service\nRequires=hifi-music.service\nPartOf=hifi-music.service\n'), 0o644)
    java_env = ('Environment=JAVA_HOME=' + p['java_home'] + '\nEnvironment=PATH=' +
                p['java_home'] + '/bin:/usr/local/bin:/usr/bin:/bin\n') if p.get('java_home') else ''
    if p['manage_minim']:
        # USB supervisor starts/stops this static unit. Do not independently enable it.
        files[units / 'hifi-minimserver.service'] = ('[Unit]\nDescription=HiFi MinimServer music library\n\n'
            '[Service]\nType=simple\nWorkingDirectory=' + str(root / 'minimserver') + '\n' + java_env +
            'ExecStart=' + str(root / 'minimserver/bin/startd') + ' wait\nRestart=on-failure\nRestartSec=10\n'
            'TimeoutStopSec=30\nUMask=0022\n', 0o644)
    if not p.get('native_bootstrap'):
        files[units / 'hifi-minimserver.service.d/usb-storage.conf'] = (
            '[Service]\nExecStart=\nExecStart=' + str(root / BINARIES['hifi-music-service']) +
            ' -home ' + str(root) + ' -minim-runtime\nTimeoutStopSec=45\n', 0o644)
    if p['manage_bubble']:
        text = unit('HiFi BubbleUPnP remote media gateway', root / 'bubbleupnp', '/bin/sh',
                    str(root / 'bubbleupnp/launch.sh') + ' -disableUPnPPortForwarding -disableCloudFfprobe -lanIp ' + p['lan_ip'])
        files[units / 'hifi-bubbleupnp.service'] = (text.replace('[Service]\n', '[Service]\n' + java_env +
                                                   'Environment=USER_JVM_ARGS=-Xmx256m\n'), 0o644)
    return {path: (data.encode(), mode) for path, (data, mode) in files.items()}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_executable(data, name, expected, architecture):
    machine = int.from_bytes(data[18:20], 'little') if len(data) >= 20 else 0
    if not data.startswith(b'\x7fELF\x02\x01') or machine != {'arm64': 183, 'amd64': 62}[architecture] or sha(data) != expected:
        raise ValueError('Invalid executable/checksum: ' + name)


def payload(path, architecture):
    with zipfile.ZipFile(str(path)) as bundle:
        if sorted(bundle.namelist()) != sorted(list(BINARIES) + ['manifest.json']):
            raise ValueError('Bundle must contain only the two executables and manifest.json')
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


def wait_http(url, predicate=lambda body: bool(body), timeout=90, opener=None):
    deadline = time.monotonic() + timeout
    if opener is None:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    while True:
        try:
            with opener.open(url, timeout=3) as response:
                if response.status == 200 and predicate(response.read(2 * 1024 * 1024)):
                    return
        except (OSError, ValueError):
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError('Readiness timeout: ' + url)
        time.sleep(0.5)


def active_library(root):
    """Use the supervisor's live mount, not the initial provisioning pathname."""
    try:
        state = json.loads((root / 'logs/library-state.json').read_text())
        mount = pathlib.Path(state['mount'])
        selected = str(mount / 'hifi-music-nano/music')
        generation = mount_identity(str(mount))
        if (state.get('paused') or not mount.is_absolute() or '..' in mount.parts or not generation or state.get('selected') != selected or
                state.get('data_root') != str(mount / 'hifi-music-nano') or
                not re.fullmatch('[0-9a-fA-F]{48}', state.get('volume', '')) or
                generation != state.get('mount_id')):
            return None
        identity = mount / 'hifi-music-nano/library-id'
        if identity.stat().st_size > 128 or identity.read_text().strip() != state['volume']:
            return None
        return selected
    except (OSError, ValueError, KeyError, TypeError):
        return None


def health(p):
    root = pathlib.Path(p['root'])
    origin, web_opener = manager_endpoint(p)
    web = origin + '/healthz'
    wait_http(web, lambda b: bool(manager_health(b)), opener=web_opener)
    music = active_library(root)
    if music is None and mount_identity(p['volume']) is None:
        selected = [None]
        def waiting_for_disk(body):
            ready = manager_health(body)['storage_ready']
            live = active_library(root)
            if live:
                if ready:
                    selected[0] = live
                return ready
            return not ready
        wait_http(web, waiting_for_disk, opener=web_opener)
        music = selected[0]
        if music is None:
            return
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    # Settings remain available before the first disk is initialized.
    if music is None and not (pathlib.Path(p['volume'])/'hifi-music-nano/library-id').exists():
        return
    if music is None:
        music = p['volume'] + '/hifi-music-nano/music'
    wait_http(p['backend_description'], lambda b: b'ContentDirectory' in b)
    browse = 'http://' + p['lan_ip'] + ':' + str(p['browse_port'])
    wait_http(browse + '/device.xml', lambda b: p['browse_udn'].encode() in b)
    wait_http(browse + '/upnp/content.xml', lambda b: b'Browse' in b)
    wait_http(browse + '/upnp/connection.xml', lambda b: b'GetProtocolInfo' in b)
    wait_http(web, lambda b: manager_health(b)['storage_ready'], opener=web_opener)
    # Empty and populated libraries must both support a real ContentDirectory request.
    action = 'urn:schemas-upnp-org:service:ContentDirectory:1'
    soap = ('<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" '
            's:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Browse xmlns:u="' + action +
            '"><ObjectID>0</ObjectID><BrowseFlag>BrowseDirectChildren</BrowseFlag><Filter>*</Filter>'
            '<StartingIndex>0</StartingIndex><RequestedCount>0</RequestedCount><SortCriteria/>'
            '</u:Browse></s:Body></s:Envelope>').encode()
    request = urllib.request.Request(browse + '/upnp/content/control', data=soap,
                                     headers={'Content-Type': 'text/xml; charset=utf-8', 'SOAPAction': '"' + action + '#Browse"'})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=15) as response:
        document = ET.fromstring(response.read(2 * 1024 * 1024))
    if not any(node.tag.endswith('TotalMatches') and node.text == '4' for node in document.iter()):
        raise RuntimeError('UPnP library categories are not ready')
    if active_library(root) != music:
        raise RuntimeError('USB supervisor selected a different library')
    if p['manage_bubble']:
        wait_http('http://' + p['lan_ip'] + ':58050/')


def restart_set(changed):
    services = set()
    for path in changed:
        text = str(path)
        if path.name == 'launch-service.py':
            services.update(('hifi-music.service', 'hifi-manager.service'))
        if 'hifi-manager' in path.name:
            services.add('hifi-manager.service')
        if path.name == 'config.json' or 'hifi-music' in path.name:
            services.update(('hifi-music.service', 'hifi-manager.service'))
        if 'hifi-minimserver' in text:
            services.update(('hifi-minimserver.service', 'hifi-music.service', 'hifi-manager.service'))
        if 'hifi-bubbleupnp' in text:
            services.add('hifi-bubbleupnp.service')
    return services


def activate(p, services):
    units = pathlib.Path.home() / '.config/systemd/user'
    if 'hifi-minimserver.service' in services:
        try:
            systemctl('is-active', 'hifi-minimserver.service')
        except subprocess.SubprocessError:
            pass  # The USB supervisor starts it after preparing the music link.
        else:
            systemctl('restart', 'hifi-minimserver.service')
    for name in ('hifi-music.service', 'hifi-manager.service', 'hifi-bubbleupnp.service'):
        if not (units / name).exists():
            continue
        if name == 'hifi-bubbleupnp.service' and not p['manage_bubble']:
            continue
        if name in services:
            systemctl('restart', name)
        else:
            systemctl('start', name)
        if name == 'hifi-bubbleupnp.service':
            wait_http('http://' + p['lan_ip'] + ':58050/')


def apply_files(p, files, generation):
    if mount_identity(p['volume']) != generation:
        raise RuntimeError('Music mount changed after preflight')
    root = pathlib.Path(p['root'])
    changed = {path: value for path, value in files.items()
               if not path.is_file() or path.read_bytes() != value[0] or (path.stat().st_mode & 0o777) != value[1]}
    previous = {path: (path.read_bytes(), path.stat().st_mode & 0o777) if path.exists() else None for path in changed}
    enabled = ['hifi-music.service', 'hifi-manager.service']
    if p['manage_bubble']:
        enabled.append('hifi-bubbleupnp.service')
    was_enabled = {}
    for name in enabled:
        try:
            was_enabled[name] = systemctl('is-enabled', name) == 'enabled'
        except subprocess.SubprocessError:
            was_enabled[name] = False
    # Back up only the files this installer owns, never music, DBs or vendor credentials.
    backup = root / 'deployment/previous'
    if changed:
        backup.mkdir(parents=True, exist_ok=True)
        for index, (path, value) in enumerate(previous.items()):
            if value is not None:
                atomic(backup / str(index), value[0], 0o600)
        atomic(backup / 'manifest.json', json.dumps({str(i): str(path) for i, path in enumerate(previous)}).encode(), 0o600)
    services = restart_set(changed)
    try:
        for path, (data, mode) in changed.items():
            atomic(path, data, mode)
        command(str(root / BINARIES['hifi-music-service']), '-home', str(root), '-check')
        systemctl('daemon-reload') if changed else None
        missing = [name for name in enabled if not was_enabled[name]]
        if missing:
            systemctl('enable', *missing)
        activate(p, services)
        health(p)
        atomic(root / 'deployment/state.json', json.dumps(dict(p,storage_mode='usb'), indent=2).encode(), 0o600)
    except BaseException:
        # Stop new code before restoring old executables/units. Persistent data is never replaced.
        for name in ('hifi-manager.service', 'hifi-music.service', 'hifi-bubbleupnp.service', 'hifi-minimserver.service'):
            if name in services:
                try:
                    systemctl('stop', name)
                except subprocess.SubprocessError:
                    pass
        for path, value in previous.items():
            if value is None:
                if path.exists():
                    path.unlink()
            else:
                atomic(path, value[0], value[1])
        for name in enabled:
            if not was_enabled[name]:
                try:
                    systemctl('disable', name)
                except subprocess.SubprocessError:
                    pass
        systemctl('daemon-reload')
        # Existing installation's configuration is authoritative during rollback.
        config = root / 'music-service/config.json'
        if config.exists():
            old = dict(p)
            saved = root / 'deployment/state.json'
            if saved.exists():
                old.update(json.loads(saved.read_text()))
            oldcfg = json.loads(config.read_text())
            old['lan_ip'], port = oldcfg['browse_listen'].rsplit(':', 1)
            old['browse_port'] = int(port)
            try:
                activate(old, services)
            except Exception:
                print('Rollback restored files; service recovery needs attention. Inspect journalctl --user.', file=sys.stderr)
        raise
    return {'changed_files': [str(path) for path in changed], 'restarted_services': sorted(services),
            'data_preserved': True, 'web_url': 'http://' + p['lan_ip'] + ':' + str(p['web_port'])}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--profile-json', required=True)
    parser.add_argument('--bundle')
    parser.add_argument('--reuse-manifest', help='expected hashes when the same binaries are already installed')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--prepare-minim', action='store_true', help='first device only: register the initialized vendor daemon with systemd')
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    profile = json.loads(args.profile_json)
    if args.prepare_minim:
        root = safe_path(profile.get('root', '~/hifi'))
        units = pathlib.Path.home() / '.config/systemd/user'
        if (root / 'music-service/config.json').exists() or (units / 'hifi-minimserver.service').exists():
            raise ValueError('Minim preparation is for a new installation only; existing services are preserved')
        # Discovery is possible only after this initialized daemon starts.
        profile.setdefault('backend_description', 'http://127.0.0.1:9791/pending/Upnp/device.xml')
        profile['manage_minim'] = True
    p = resolve(profile)
    generation = preflight(p)
    if args.prepare_minim:
        p['browse_udn'] = 'uuid:00000000-0000-0000-0000-000000000000'
        target = units / 'hifi-minimserver.service'
        data, mode = render(dict(p, manage_minim=True,native_bootstrap=True))[target]
        try:
            atomic(target, data, mode)
            systemctl('daemon-reload')
            systemctl('start', target.name)
            backend = wait_backend(p['lan_ip'])
        except Exception:
            systemctl('stop', target.name)
            target.unlink()
            systemctl('daemon-reload')
            raise
        print(json.dumps({'minim_service': 'started', 'backend_description': backend}))
        return
    architecture = {'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine()]
    if not args.apply:
        print(json.dumps({'architecture': architecture, 'profile': p, 'prerequisites': 'ready',
                          'disk_mounted': generation is not None,
                          'binary_sha256': {name: sha((pathlib.Path(p['root']) / relative).read_bytes())
                                            for name, relative in BINARIES.items() if (pathlib.Path(p['root']) / relative).is_file()},
                          'installed': (pathlib.Path(p['root']) / 'manager-service/hifi-manager').is_file()}))
        return
    if bool(args.bundle) == bool(args.reuse_manifest):
        parser.error('--apply requires exactly one of --bundle or --reuse-manifest')
    p.setdefault('browse_udn', 'uuid:' + str(uuid.uuid4()))
    files = render(p)
    if args.bundle:
        executables = payload(args.bundle, architecture)
    else:
        manifest = json.loads(args.reuse_manifest)
        if manifest['architecture'] != architecture or set(manifest['files']) != set(BINARIES):
            raise ValueError('Invalid reuse manifest')
        executables = {name: (pathlib.Path(p['root']) / relative).read_bytes() for name, relative in BINARIES.items()}
        for name, data in executables.items():
            validate_executable(data, name, manifest['files'][name], architecture)
    for name, data in executables.items():
        files[pathlib.Path(p['root']) / BINARIES[name]] = (data, 0o755)
    # Serialize deployments on a device, including first installation.
    import fcntl
    lock_path = pathlib.Path(os.environ.get('XDG_RUNTIME_DIR', '/run/user/' + str(os.getuid()))) / 'hifi-deploy.lock'
    with lock_path.open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        print(json.dumps(apply_files(p, files, generation)))


if __name__ == '__main__':
    main()
