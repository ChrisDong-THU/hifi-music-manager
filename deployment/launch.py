"""Start HiFi services on the trusted LAN and reconcile address changes."""
import argparse
import ipaddress
import json
import os
import pathlib
import socket
import subprocess
import tempfile
import time


PRIVATE = tuple(ipaddress.ip_network(n) for n in ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16'))
LAN_SERVICES = {'manager': 'hifi-manager.service'}


def trusted_network(value):
    network = ipaddress.ip_network(value, strict=False)
    if network.version != 4 or not any(network.network_address in n and network.broadcast_address in n for n in PRIVATE):
        raise ValueError('Startup network must stay inside a private IPv4 subnet')
    return network


def current_address(network):
    addresses = json.loads(subprocess.check_output(['ip', '-j', '-4', 'address', 'show'], timeout=5).decode())
    candidates = []
    for interface in addresses:
        flags = interface.get('flags', [])
        if 'UP' not in flags or 'LOOPBACK' in flags or interface.get('operstate') in ('DOWN', 'LOWERLAYERDOWN'):
            continue
        for address in interface.get('addr_info', []):
            if address.get('scope') != 'global' or address.get('preferred_life_time') == 0:
                continue
            ip = ipaddress.ip_address(address['local'])
            if ip in network:
                candidates.append((interface['ifname'], interface.get('ifindex', 0), ip))
    if not candidates:
        return None
    if len({name for name, _, _ in candidates}) == 1:
        return str(min(ip for _, _, ip in candidates))

    # Older iproute2 on the Nano supports JSON addresses but not JSON routes.
    routes = subprocess.check_output(['ip', '-4', 'route', 'show', 'default'], timeout=5).decode()
    metrics = {}
    for line in routes.splitlines():
        fields = line.split()
        if not fields or fields[0] != 'default' or 'dev' not in fields:
            continue
        device = fields[fields.index('dev') + 1]
        metric = int(fields[fields.index('metric') + 1]) if 'metric' in fields else 0
        metrics[device] = min(metrics.get(device, metric), metric)
    return str(min(candidates, key=lambda entry: (metrics.get(entry[0], 1 << 32), entry[1], entry[2]))[2])


def publish(path, config):
    if path.is_symlink():
        raise ValueError('LAN binding record cannot be a symlink')
    fd, pending = tempfile.mkstemp(prefix='.network-', dir=str(path.parent))
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write((json.dumps(config, indent=2) + '\n').encode())
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(pending, 0o600)
        os.replace(pending, str(path))
    finally:
        if os.path.exists(pending):
            os.unlink(pending)


def runtime_dir():
    return pathlib.Path(os.environ.get('XDG_RUNTIME_DIR') or '/run/user/' + str(os.getuid()))


def record_binding(service, address):
    directory = runtime_dir() / 'hifi-lan'
    directory.mkdir(mode=0o700, exist_ok=True)
    publish(directory / service, {'pid': os.getpid(), 'address': address})


def reconcile(home):
    import fcntl

    runtime = runtime_dir()
    # Keep a DHCP-triggered restart out of an installer transaction.
    with (runtime / 'hifi-deploy.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        config = json.loads((pathlib.Path(home) / 'config/manager-network.json').read_text())
        address = current_address(trusted_network(config['lan_cidr']))
        if not address:
            return
        old_bindings = {}
        for service, unit in LAN_SERVICES.items():
            try:
                binding = json.loads((runtime / 'hifi-lan' / service).read_text())
            except FileNotFoundError:
                continue
            if binding['address'] != address:
                old_bindings[unit] = str(binding['pid'])
        if not old_bindings:
            return
        status = subprocess.check_output(['systemctl', '--user', 'show', '-p', 'Id', '-p', 'MainPID',
                                          *old_bindings], timeout=5).decode()
        stale = []
        for block in status.strip().split('\n\n'):
            properties = dict(line.split('=', 1) for line in block.splitlines() if '=' in line)
            unit = properties.get('Id')
            if unit in old_bindings and properties.get('MainPID') == old_bindings[unit]:
                stale.append(unit)
        if stale:
            print('LAN address changed to ' + address + '; restarting ' + ', '.join(stale), flush=True)
            subprocess.run(['systemctl', '--user', 'try-restart', *stale], check=True)


def watch(home):
    # rtnetlink groups for link, IPv4 address and IPv4 route changes.
    groups = 0x01 | 0x10 | 0x40
    with socket.socket(socket.AF_NETLINK, socket.SOCK_RAW, socket.NETLINK_ROUTE) as events:
        events.bind((0, groups))
        events.settimeout(10)
        while True:
            reconcile(home)
            try:
                events.recv(65536)
            except socket.timeout:
                pass  # Retry after an installer transaction or a missed event.


def start(home, timeout=120):
    root = pathlib.Path(home)
    config = json.loads((root / 'config/manager-network.json').read_text())
    network = trusted_network(config['lan_cidr'])
    web_port = config['web_port']
    if type(web_port) is not int or not 1024 <= web_port <= 65535:
        raise ValueError('Invalid Web port')
    deadline = time.monotonic() + timeout
    print('Waiting for a LAN address inside ' + str(network), flush=True)
    while True:
        try:
            address = current_address(network)
        except (OSError, subprocess.SubprocessError, ValueError):
            address = None
        if address:
            break
        if time.monotonic() >= deadline:
            raise RuntimeError('No active LAN address inside ' + str(network))
        time.sleep(1)
    executable = str(root / 'manager-service/hifi-manager')
    args = [executable, '-library-home', str(root), '-listen', address + ':' + str(web_port),
            '-allow-lan', str(network)]
    lan_hostname = config.get('manager_lan_hostname')
    stem = 'manager-lan-tls' if lan_hostname else 'manager-tls'
    cert, key = (root / 'config' / (stem + suffix) for suffix in ('-cert.pem', '-key.pem'))
    args += ['-tls-cert', str(cert), '-tls-key', str(key)]
    if lan_hostname:
        args += ['-lan-hostname', lan_hostname]
    if config.get('manager_public_domain'):
        cert, key = (root / 'config' / ('manager-public-tls-' + n + '.pem') for n in ('cert', 'key'))
        args += ['-public-domain', config['manager_public_domain'],
                 '-public-tls-cert', str(cert), '-public-tls-key', str(key)]
    if config.get('webdav_public'):
        args.append('-public-webdav')
    print('Starting management and WebDAV on ' + address, flush=True)
    record_binding('manager', address)
    os.execv(executable, args)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=('manager', 'watch'))
    parser.add_argument('home')
    args = parser.parse_args()
    (start if args.action == 'manager' else watch)(args.home)
