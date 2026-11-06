"""Resolve this boot's trusted LAN address, then replace this process with Go."""
import ipaddress
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time


PRIVATE = tuple(ipaddress.ip_network(n) for n in ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16'))


def trusted_network(value):
    network = ipaddress.ip_network(value, strict=False)
    if network.version != 4 or not any(network.network_address in n and network.broadcast_address in n for n in PRIVATE):
        raise ValueError('Startup network must stay inside a private IPv4 subnet')
    return network


def current_address(network):
    addresses = json.loads(subprocess.check_output(['ip', '-j', '-4', 'address', 'show'], timeout=5).decode())
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
    candidates = []
    for interface in addresses:
        if 'UP' not in interface.get('flags', []) or 'LOOPBACK' in interface.get('flags', []) or interface.get('operstate') == 'DOWN':
            continue
        for address in interface.get('addr_info', []):
            if address.get('scope') != 'global' or address.get('preferred_life_time') == 0:
                continue
            ip = ipaddress.ip_address(address['local'])
            if ip.version == 4 and ip in network:
                candidates.append((metrics.get(interface['ifname'], 1 << 32),
                                   interface.get('ifindex', 0), int(ip), str(ip)))
    return min(candidates)[-1] if candidates else None


def publish(path, config):
    if path.is_symlink():
        raise ValueError('Music configuration cannot be a symlink')
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


def start(service, home, web_port, timeout=120, public_domain=''):
    if (service not in ('music', 'manager', 'bubble', 'bubble-relay') or not 1024 <= int(web_port) <= 65535 or
            public_domain and service == 'music' or not public_domain and service in ('bubble', 'bubble-relay')):
        raise ValueError('Invalid service or Web port')
    root = pathlib.Path(home)
    path = root / 'music-service/config.json'
    config = json.loads(path.read_text())
    network = trusted_network(config['lan_cidr'])
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
    if service == 'music':
        _, port = config['browse_listen'].rsplit(':', 1)
        if not 1024 <= int(port) <= 65535:
            raise ValueError('Invalid browse port')
        listen = address + ':' + port
        if listen != config['browse_listen']:
            config['browse_listen'] = listen
            publish(path, config)
        executable = str(root / 'music-service/hifi-music-service')
        args = [executable, '-home', str(root)]
    elif service == 'manager':
        executable = str(root / 'manager-service/hifi-manager')
        args = [executable, '-library-home', str(root), '-listen', address + ':' + str(web_port),
                '-allow-lan', str(network), '-rescan-url', 'http://127.0.0.1:9790/']
        cert, key = (root / 'config' / name for name in ('manager-tls-cert.pem', 'manager-tls-key.pem'))
        if cert.exists() or key.exists():
            args += ['-tls-cert', str(cert), '-tls-key', str(key)]
        if public_domain:
            cert, key = (root / 'config' / name for name in ('manager-public-tls-cert.pem', 'manager-public-tls-key.pem'))
            args += ['-public-domain', public_domain, '-public-tls-cert', str(cert), '-public-tls-key', str(key)]
    elif service == 'bubble':
        executable = '/usr/bin/authbind'
        args = [executable, '--deep', '/bin/sh', str(root / 'bubbleupnp/launch.sh'), '-disableUPnPPortForwarding',
                '-disableCloudFfprobe', '-lanIp', address, '-httpPort', '80', '-wanHostname', public_domain]
    else:
        executable = str(root / 'music-service/hifi-music-service')
        args = [executable, '-home', str(root), '-bubble-relay-domain', public_domain, '-bubble-relay-upstream', address]
    print('Starting ' + service + ' on ' + address, flush=True)
    os.execv(executable, args)


if __name__ == '__main__':
    if len(sys.argv) not in (4, 5):
        raise SystemExit('Usage: launch.py music|manager|bubble|bubble-relay HOME WEB_PORT [PUBLIC_DOMAIN]')
    start(*sys.argv[1:4], public_domain=sys.argv[4] if len(sys.argv) == 5 else '')
