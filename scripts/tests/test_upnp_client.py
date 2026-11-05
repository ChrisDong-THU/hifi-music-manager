"""Acceptance tool tests: fake HTTP only, no device or music disk is contacted."""
import importlib.util
import io
import pathlib
import sys
import unittest
from unittest.mock import patch
import urllib.error
import urllib.parse
import xml.etree.ElementTree as ET

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
import upnp_client as upnp

spec = importlib.util.spec_from_file_location('acceptance', pathlib.Path(__file__).resolve().parents[1] / 'verify-single-library.py')
acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acceptance)


class Response(io.BytesIO):
    def __init__(self, data, status=200, headers=None):
        super().__init__(data)
        self.status = status
        self.headers = headers or {}


class Pages:
    def __init__(self, mode='stable'):
        self.mode, self.starts = mode, []

    def open(self, request, **kwargs):
        action = ET.fromstring(request.data).find('{' + upnp.SOAP + '}Body')[0]
        start = int(action.findtext('StartingIndex'))
        self.starts.append(start)
        result = ET.Element('{' + upnp.DIDL + '}DIDL-Lite')
        total, update = 300, '27'
        for i in range(start, min(start + 256, total)):
            item = ET.SubElement(result, '{' + upnp.DIDL + '}item', {'id': 'track-' + str(i)})
            ET.SubElement(item, '{http://purl.org/dc/elements/1.1/}title').text = 'Same title'
            ET.SubElement(item, '{' + upnp.DIDL + '}res').text = 'http://10.42.7.8:9790/audio-' + str(i)
        if start:
            if self.mode == 'changed':
                update = '28'
            elif self.mode == 'empty':
                result.clear()
            elif self.mode == 'duplicate':
                result[0].set('id', 'track-0')
        if self.mode == 'negative':
            total = -1
        envelope = ET.Element('{' + upnp.SOAP + '}Envelope')
        response = ET.SubElement(ET.SubElement(envelope, '{' + upnp.SOAP + '}Body'), '{' + upnp.SERVICE + '}BrowseResponse')
        for key, value in [('Result', ET.tostring(result, encoding='unicode')),
                           ('TotalMatches', str(total)), ('NumberReturned', str(len(result))), ('UpdateID', update)]:
            ET.SubElement(response, key).text = value
        return Response(ET.tostring(envelope))


class ClientTests(unittest.TestCase):
    def test_all_pages_and_duplicate_titles_keep_distinct_ids(self):
        pages = Pages()
        tracks = upnp.browse('http://10.42.7.8:9792/control', 'tracks', pages)
        self.assertEqual(len(tracks), 300)
        self.assertEqual(pages.starts, [0, 256])
        self.assertEqual(tracks[-1].get('id'), 'track-299')

    def test_changed_incomplete_repeated_and_invalid_pages_fail(self):
        for mode in ('changed', 'empty', 'duplicate', 'negative'):
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                upnp.browse('http://10.42.7.8/control', 'tracks', Pages(mode))

    def test_discovery_accepts_only_literal_private_response_peer(self):
        good = 'http://10.42.7.8:9792/device.xml'
        self.assertEqual(upnp.discovery_location(good, '10.42.7.8'), good)
        for value in ('http://10.42.7.9/device.xml', 'http://example.com/device.xml',
                      'http://user:pass@10.42.7.8/device.xml', good + '#fragment',
                      'http://203.0.113.2/device.xml', 'file:///device.xml'):
            self.assertIsNone(upnp.discovery_location(value, '10.42.7.8'))

    def test_xml_entities_and_oversized_documents_are_rejected(self):
        for value in (b'<!DOCTYPE a><a/>', b' ' * (8 * 1024 * 1024 + 1)):
            with self.assertRaises(ValueError):
                upnp.document(value)


class AcceptanceClient:
    def __init__(self, bad_range=False, native_http=False):
        self.pages, self.urls = Pages(), []
        self.bad_range, self.native_http = bad_range, native_http

    def open(self, request, **kwargs):
        url = request if isinstance(request, str) else request.full_url
        self.urls.append(url)
        if ':9791/' in url:
            if self.native_http:
                raise urllib.error.HTTPError(url, 404, 'Not found', {}, None)
            raise urllib.error.URLError('Connection refused')
        if url.endswith('/device.xml'):
            return Response(b'<root xmlns="urn:schemas-upnp-org:device-1-0"><device><friendlyName>Living room</friendlyName><UDN>uuid:device</UDN></device></root>')
        if ':9790/' in url:
            data = b'audio' * 2000
            if not isinstance(request, str) and request.has_header('Range'):
                return Response(b'bad' if self.bad_range else data[:4096], 206, {'Content-Range': 'bytes 0-4095/10000'})
            return Response(data)
        if not isinstance(request, str) and request.data:
            object_id = ET.fromstring(request.data).find('{' + upnp.SOAP + '}Body')[0].findtext('ObjectID')
            if object_id == '0':
                root = ET.Element('{' + upnp.DIDL + '}DIDL-Lite')
                for i, title in enumerate(('专辑', '曲目', '歌手', '我的播放列表')):
                    ET.SubElement(ET.SubElement(root, '{' + upnp.DIDL + '}container', {'id': str(i)}), '{http://purl.org/dc/elements/1.1/}title').text = title
                envelope = ET.Element('{' + upnp.SOAP + '}Envelope')
                response = ET.SubElement(ET.SubElement(envelope, '{' + upnp.SOAP + '}Body'), '{' + upnp.SERVICE + '}BrowseResponse')
                for key, value in [('Result', ET.tostring(root, encoding='unicode')), ('TotalMatches', '4'), ('NumberReturned', '4'), ('UpdateID', '27')]:
                    ET.SubElement(response, key).text = value
                return Response(ET.tostring(envelope))
        return self.pages.open(request, **kwargs)


class AcceptanceTests(unittest.TestCase):
    config = {'browse_listen': '10.42.7.8:9792', 'backend_description': 'http://127.0.0.1:9791/native/device.xml',
              'name': 'Living room', 'browse_udn': 'uuid:device'}

    def run_verify(self, client):
        with patch.object(upnp, 'http_client', return_value=client), patch.object(upnp, 'discover', return_value=['http://10.42.7.8:9792/device.xml']):
            return acceptance.verify(self.config, expected_tracks=300, sample_tracks=1)

    def test_generic_device_uses_complete_index_and_explicit_audio_sample(self):
        client = AcceptanceClient()
        result = self.run_verify(client)
        self.assertEqual(result['total_tracks'], 300)
        self.assertEqual(result['checked_audio_tracks'], 1)
        self.assertFalse(result['all_audio_checked'])
        self.assertFalse(result['management_web_required'])
        self.assertTrue(all(':8787/' not in url for url in client.urls))

    def test_range_mismatch_cannot_pass(self):
        with self.assertRaisesRegex(ValueError, 'range response'):
            self.run_verify(AcceptanceClient(bad_range=True))

    def test_http_404_does_not_prove_native_control_isolation(self):
        with self.assertRaisesRegex(ValueError, 'reachable'):
            self.run_verify(AcceptanceClient(native_http=True))


if __name__ == '__main__':
    unittest.main()
