"""Read-only acceptance from another LAN computer; supply this device's service config."""
import argparse
import hashlib
import ipaddress
import json
import pathlib
import urllib.error
import urllib.parse
import urllib.request

import upnp_client as upnp


def verify(config, expected_tracks=None, sample_tracks=3, full_audio=False):
    base = 'http://' + config['browse_listen']
    host = urllib.parse.urlsplit(base).hostname
    ip = ipaddress.ip_address(host)
    if not any(ip in network for network in upnp.PRIVATE):
        raise ValueError('The device must have a literal private LAN address')
    client = upnp.http_client()
    description = base + '/device.xml'
    backend = urllib.parse.urlsplit(config['backend_description'])
    locations = upnp.discover()
    if description not in locations:
        raise ValueError('Configured music service was not discovered through SSDP')
    if any(urllib.parse.urlsplit(url).hostname == host and
           urllib.parse.urlsplit(url).path == backend.path for url in locations):
        raise ValueError('This device still advertises its native MinimServer')
    with client.open(description, timeout=5) as response:
        device = upnp.document(response.read(1024 * 1024 + 1))
    namespace = '{urn:schemas-upnp-org:device-1-0}'
    name = device.findtext('.//' + namespace + 'friendlyName')
    if name != config['name'] or device.findtext('.//' + namespace + 'UDN') != config['browse_udn']:
        raise ValueError('Discovered the wrong device identity')
    native = urllib.parse.urlunsplit(('http', host + ':' + str(backend.port), backend.path, backend.query, ''))
    try:
        client.open(native, timeout=3).close()
    except urllib.error.HTTPError:
        raise ValueError('Native control port is reachable outside loopback')
    except (urllib.error.URLError, TimeoutError):
        pass
    else:
        raise ValueError('Native control endpoint is exposed')
    control = base + '/upnp/content/control'
    root = upnp.browse(control, '0', client)
    titles = [node.findtext('{http://purl.org/dc/elements/1.1/}title') for node in root]
    if titles != ['专辑', '曲目', '歌手', '我的播放列表']:
        raise ValueError('Unexpected public library categories')
    tracks = upnp.browse(control, 'tracks', client)
    if expected_tracks is not None and len(tracks) != expected_tracks:
        raise ValueError('Track count differs from the expected library')
    selected = list(tracks) if full_audio else list(tracks)[:sample_tracks]
    checked = []
    for track in selected:
        media = track.find('{' + upnp.DIDL + '}res')
        url = urllib.parse.urlsplit(media.text if media is not None else '')
        if url.scheme != 'http' or url.hostname != host or url.username or url.password or url.fragment:
            raise ValueError('Track does not use this device LAN media endpoint')
        digest, size, first = hashlib.sha256(), 0, b''
        with client.open(url.geturl(), timeout=60) as response:
            for block in iter(lambda: response.read(1024 * 1024), b''):
                if not first:
                    first = block[:4096]
                size += len(block)
                digest.update(block)
        if not first:
            raise ValueError('Empty audio response')
        request = urllib.request.Request(url.geturl(), headers={'Range': 'bytes=0-' + str(len(first) - 1)})
        with client.open(request, timeout=10) as response:
            expected_range = 'bytes 0-' + str(len(first) - 1) + '/' + str(size)
            if response.status != 206 or response.headers.get('Content-Range') != expected_range or response.read(len(first) + 1) != first:
                raise ValueError('Audio range response differs from the full stream')
        checked.append({'id': track.get('id'), 'bytes': size, 'sha256': digest.hexdigest()})
    return {'passed': True, 'library_name': name, 'categories': titles,
            'total_tracks': len(tracks), 'checked_audio_tracks': len(checked),
            'all_audio_checked': len(checked) == len(tracks), 'audio': checked,
            'native_control_isolated': True, 'management_web_required': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=pathlib.Path, required=True,
                        help='Copy of the target music-service/config.json; no credentials needed')
    parser.add_argument('--expected-tracks', type=int)
    parser.add_argument('--sample-tracks', type=int, default=3,
                        help='Full audio/hash/range checks for this many tracks (default: 3)')
    parser.add_argument('--full-audio', action='store_true', help='Read every complete audio file; may take a long time')
    parser.add_argument('--report', type=pathlib.Path)
    args = parser.parse_args()
    if args.sample_tracks < 0 or args.expected_tracks is not None and args.expected_tracks < 0:
        parser.error('Track counts must be non-negative')
    result = verify(json.loads(args.config.read_text(encoding='utf-8-sig')),
                    args.expected_tracks, args.sample_tracks, args.full_audio)
    text = json.dumps(result, ensure_ascii=False, indent=2)
    if args.report:
        args.report.write_text(text, encoding='utf-8')
    print(text)


if __name__ == '__main__':
    main()
