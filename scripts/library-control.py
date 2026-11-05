"""Local music-library controls. No root operations or credentials."""
import argparse
import os
import pathlib
import subprocess
import urllib.request

import upnp_client

parser = argparse.ArgumentParser()
parser.add_argument('action', choices=['status', 'pause', 'resume', 'rescan'])
parser.add_argument('--home', type=pathlib.Path, default=pathlib.Path.home() / 'hifi')
args = parser.parse_args()
root = args.home
marker = pathlib.Path(os.environ.get('XDG_RUNTIME_DIR', '/run/user/%d' % os.getuid())) / 'hifi-library.paused'
if args.action == 'pause':
    marker.touch()
    subprocess.check_call(['systemctl', '--user', 'stop', 'hifi-minimserver.service'])
    print('Paused; wait for the data disk to appear offline in Web, then safely unmount it before removal.')
elif args.action == 'resume':
    if marker.exists():
        marker.unlink()
    print('Resumed; the supervisor will select an initialized mounted data disk. Initialize new disks in Web settings.')
elif args.action == 'rescan':
    request = urllib.request.Request('http://127.0.0.1:9790/', data=b'rescan',
                                     headers={'Content-Type': 'text/plain'})
    opener = upnp_client.http_client()
    with opener.open(request, timeout=60) as response:
        data = response.read((1 << 20) + 1)
        if len(data) > 1 << 20:
            raise ValueError('MinimServer response exceeds 1 MiB')
        print(data.decode('utf-8'))
else:
    state = root / 'logs/library-state.json'
    print(state.read_text() if state.exists() else 'Library supervisor has not started.')
    subprocess.run(['systemctl', '--user', '--no-pager', 'status',
                    'hifi-minimserver.service', 'hifi-music.service'])
