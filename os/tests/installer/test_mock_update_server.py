#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The index the mock serves has to be the shape the device actually parses.

A stock Wireless Link on 27.0.6 took this schema off a LAN feed and installed SoundTap
(2026-11-05). This holds the generated document to that schema and to the values
that are load-bearing: the device ID, the package's length and CRC32, and where the device
goes to fetch it.
"""
import contextlib
import io
import sys
import tempfile
import threading
import unittest
import urllib.request
import xml.etree.ElementTree as ET
import zlib
from http.server import HTTPServer
from pathlib import Path
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tools/release"))
from mock_update_server import MODELS, STOCK_REVISION, build_index, main as mock_main, make_handler

def check_index():
    with tempfile.TemporaryDirectory() as tmp:
        package = Path(tmp) / "Update-wla.stu"
        package.write_bytes(b"x" * 4242)
        text = build_index(str(package), [("0x094A", "SoundTouch Wireless Link adapter")],
                           "99.0.0.1.1", "http://192.168.1.9:8300").decode()

    root = ET.fromstring(text)
    assert root.tag == "INDEX"
    device = root.find("DEVICE")
    assert device.get("ID") == "0x094A", text
    release = device.find("HARDWARE/RELEASE")
    assert release.get("HTTPHOST") == "http://192.168.1.9:8300"
    assert release.get("URLPATH") == "soundtap"
    image = release.find("IMAGE")
    assert image.get("FILENAME") == "Update-wla.stu"
    assert image.get("LENGTH") == "4242"
    assert image.get("CRC") == f"0x{zlib.crc32(b'x' * 4242):08x}"
    # An offer the device would not take is not worth serving.
    assert release.get("REVISION") > STOCK_REVISION


def check_default_device_id_is_binky():
    # Started without --device-id, the server offers the package to the binky WLA's id,
    # not the legacy Ginger 0x1c00 of the stock GIN example.
    import mock_update_server

    captured = {}

    class Unbound:
        def __init__(self, address, handler):
            captured["handler"] = handler

        def serve_forever(self):
            pass

    with tempfile.TemporaryDirectory() as tmp:
        package = Path(tmp) / "Update-wla.stu"
        package.write_bytes(b"z" * 8)
        real = mock_update_server.http.server.HTTPServer
        mock_update_server.http.server.HTTPServer = Unbound
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                mock_main(["--serve", str(package)])
        finally:
            mock_update_server.http.server.HTTPServer = real
        server = HTTPServer(("127.0.0.1", 0), captured["handler"])
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            url = f"http://127.0.0.1:{server.server_port}/updates/soundtouch?serialnumber=1"
            with urllib.request.urlopen(url, timeout=5) as r:
                ids = [d.get("ID") for d in ET.fromstring(r.read()).findall("DEVICE")]
        finally:
            server.shutdown()
            thread.join()
            server.server_close()
    assert ids == ["0x094A"], ids


def check_static_feed_lists_every_model():
    # One file for every model, as Bose's own index is.
    with tempfile.TemporaryDirectory() as tmp:
        package = Path(tmp) / "SoundTap-Core-0.1.0.stu"
        package.write_bytes(b"y" * 10)
        out = Path(tmp) / "index.xml"
        mock_main(["--serve", str(package), "--write-index", str(out),
                   "--host", "https://github.com", "--urlpath", "o/r/releases/download/t"])
        root = ET.fromstring(out.read_bytes())
    ids = [d.get("ID") for d in root.findall("DEVICE")]
    assert ids == [i for i, _ in MODELS], ids
    for d in root.findall("DEVICE"):
        release = d.find("HARDWARE/RELEASE")
        assert release.get("HTTPHOST") == "https://github.com"
        assert release.get("URLPATH") == "o/r/releases/download/t"
        assert release.find("IMAGE").get("FILENAME") == "SoundTap-Core-0.1.0.stu"


def check_fetch_route():
    with tempfile.TemporaryDirectory() as tmp:
        package = Path(tmp) / "Update-wla.stu"
        payload = b"binky package"
        package.write_bytes(payload)
        options = SimpleNamespace(serve=str(package), product="SoundTouch Wireless Link adapter",
                                  device_id="0x094A", version="99.0.0.1.1", all_models=False,
                                  out=str(Path(tmp) / "out"), model="wla")
        server = HTTPServer(("127.0.0.1", 0), make_handler(options))
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            base = f"http://127.0.0.1:{server.server_port}"
            # What the device asks for: swUpdateUrl with its serial number.
            with urllib.request.urlopen(base + "/updates/soundtouch?serialnumber=1", timeout=5) as r:
                release = ET.fromstring(r.read()).find("DEVICE/HARDWARE/RELEASE")
            fetch = f"{release.get('HTTPHOST')}/{release.get('URLPATH')}/Update-wla.stu"
            assert fetch == base + "/soundtap/Update-wla.stu"
            with urllib.request.urlopen(fetch, timeout=5) as response:
                assert response.read() == payload
        finally:
            server.shutdown()
            thread.join()
            server.server_close()


class MockUpdateServer(unittest.TestCase):
    def test_index(self):
        check_index()

    def test_default_device_id_is_binky(self):
        check_default_device_id_is_binky()

    def test_static_feed_lists_every_model(self):
        check_static_feed_lists_every_model()

    def test_fetch_route(self):
        check_fetch_route()


if __name__ == "__main__":
    unittest.main()
