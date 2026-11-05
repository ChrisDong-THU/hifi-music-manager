"""Bounded, read-only UPnP helpers for deployment acceptance (standard library only)."""
import ipaddress
import socket
import time
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

SERVICE = 'urn:schemas-upnp-org:service:ContentDirectory:1'
SOAP = 'http://schemas.xmlsoap.org/soap/envelope/'
DIDL = 'urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/'
PRIVATE = tuple(ipaddress.ip_network(value) for value in ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16'))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, target):
        return None


def http_client():
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def document(data):
    if len(data) > 8 * 1024 * 1024 or b'<!DOCTYPE' in data.upper():
        raise ValueError('Unbounded or unsupported XML document')
    return ET.fromstring(data)


def discovery_location(value, peer):
    try:
        url = urllib.parse.urlsplit(value)
        ip = ipaddress.ip_address(url.hostname)
        if (url.scheme == 'http' and str(ip) == peer and
                any(ip in network for network in PRIVATE) and
                not url.username and not url.password and not url.fragment and
                0 < (url.port or 80) <= 65535):
            return value
    except ValueError:
        pass
    return None


def discover(timeout=4):
    message = ('M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\n'
               'MAN: "ssdp:discover"\r\nMX: 2\r\n'
               'ST: urn:schemas-upnp-org:device:MediaServer:1\r\n\r\n')
    found = set()
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.sendto(message.encode('ascii'), ('239.255.255.250', 1900))
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            sock.settimeout(min(.5, max(.001, deadline - time.monotonic())))
            try:
                raw, peer = sock.recvfrom(8192)
            except socket.timeout:
                continue
            for line in raw.decode('utf-8', errors='replace').splitlines():
                key, separator, value = line.partition(':')
                if separator and key.lower() == 'location':
                    location = discovery_location(value.strip(), peer[0])
                    if location:
                        found.add(location)
    return sorted(found)


def browse_page(control, object_id, start=0, count=256, client=None):
    envelope = ET.Element('{' + SOAP + '}Envelope')
    body = ET.SubElement(envelope, '{' + SOAP + '}Body')
    action = ET.SubElement(body, '{' + SERVICE + '}Browse')
    for key, value in [('ObjectID', object_id), ('BrowseFlag', 'BrowseDirectChildren'),
                       ('Filter', '*'), ('StartingIndex', str(start)),
                       ('RequestedCount', str(count)), ('SortCriteria', '')]:
        ET.SubElement(action, key).text = value
    request = urllib.request.Request(control, data=ET.tostring(envelope), headers={
        'Content-Type': 'text/xml; charset=utf-8', 'SOAPAction': '"' + SERVICE + '#Browse"'})
    with (client or http_client()).open(request, timeout=30) as response:
        root = document(response.read(8 * 1024 * 1024 + 1))
    result = root.find('./{' + SOAP + '}Body/{' + SERVICE + '}BrowseResponse')
    if result is None:
        raise ValueError('Missing BrowseResponse')
    fields = {child.tag.split('}')[-1]: child.text or '' for child in result}
    tree = document(fields['Result'].encode('utf-8'))
    returned, total = int(fields['NumberReturned']), int(fields['TotalMatches'])
    if (tree.tag != '{' + DIDL + '}DIDL-Lite' or returned != len(tree) or
            returned < 0 or returned > count or total < 0 or total > 100000 or start + returned > total):
        raise ValueError('Invalid UPnP page counts')
    return tree, total, fields['UpdateID'], len(fields['Result'].encode('utf-8'))


def browse(control, object_id, client=None):
    result = ET.Element('{' + DIDL + '}DIDL-Lite')
    seen, version, expected = set(), None, None
    metadata_bytes = 0
    while True:
        page, total, update, size = browse_page(control, object_id, len(result), client=client)
        metadata_bytes += size
        if metadata_bytes > 32 * 1024 * 1024:
            raise ValueError('Acceptance metadata exceeds 32 MiB')
        if expected is not None and (total != expected or update != version):
            raise ValueError('Library changed during acceptance; retry when idle')
        expected, version = total, update
        for item in page:
            identity = item.get('id')
            if not identity or identity in seen:
                raise ValueError('Missing or repeated UPnP object identity')
            seen.add(identity)
        result.extend(page)
        if len(result) == total:
            return result
        if not len(page):
            raise ValueError('Incomplete UPnP pagination')
