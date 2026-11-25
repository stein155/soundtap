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
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tools/release"))
import mock_update_server
from mock_update_server import MODELS, STOCK_REVISION, build_index, make_handler
from mock_update_server import main as mock_main


class MockUpdateServer(unittest.TestCase):
    """The mock update server's index, its default device and its fetch route."""

    def test_index(self) -> None:
        """The generated index carries the device ID, the package's length and CRC, and where to fetch it."""
        with tempfile.TemporaryDirectory() as tmp:
            package = Path(tmp) / "Update-wla.stu"
            package.write_bytes(b"x" * 4242)
            text = build_index(str(package), [("0x094A", "SoundTouch Wireless Link adapter")],
                               "99.0.0.1.1", "http://192.168.1.9:8300").decode()

        root = ET.fromstring(text)
        self.assertEqual(root.tag, "INDEX")
        device = root.find("DEVICE")
        self.assertEqual(device.get("ID"), "0x094A", text)
        release = device.find("HARDWARE/RELEASE")
        self.assertEqual(release.get("HTTPHOST"), "http://192.168.1.9:8300")
        self.assertEqual(release.get("URLPATH"), "soundtap")
        image = release.find("IMAGE")
        self.assertEqual(image.get("FILENAME"), "Update-wla.stu")
        self.assertEqual(image.get("LENGTH"), "4242")
        self.assertEqual(image.get("CRC"), f"0x{zlib.crc32(b'x' * 4242):08x}")
        # An offer the device would not take is not worth serving.
        self.assertGreater(release.get("REVISION"), STOCK_REVISION)

    def test_default_device_id_is_binky(self) -> None:
        """Started without --device-id, the server offers the package to the binky WLA's id.

        Not the legacy Ginger 0x1c00 of the stock GIN example.
        """
        captured = {}

        class Unbound:
            """An HTTPServer stand-in that keeps the handler and never binds."""

            def __init__(self, _address: tuple[str, int], handler: type[BaseHTTPRequestHandler]) -> None:
                """Keep the request handler the server would have used."""
                captured["handler"] = handler

            def serve_forever(self) -> None:
                """Return at once: nothing is served."""

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
                with urllib.request.urlopen(
                        f"http://127.0.0.1:{server.server_port}/updates/soundtouch?serialnumber=1", timeout=5) as r:
                    ids = [d.get("ID") for d in ET.fromstring(r.read()).findall("DEVICE")]
            finally:
                server.shutdown()
                thread.join()
                server.server_close()
        self.assertEqual(ids, ["0x094A"], ids)

    def test_static_feed_lists_every_model(self) -> None:
        """One static index lists every model, as Bose's own index is one file for all."""
        with tempfile.TemporaryDirectory() as tmp:
            package = Path(tmp) / "SoundTap-Core-0.1.0.stu"
            package.write_bytes(b"y" * 10)
            out = Path(tmp) / "index.xml"
            mock_main(["--serve", str(package), "--write-index", str(out),
                       "--host", "https://github.com", "--urlpath", "o/r/releases/download/t"])
            root = ET.fromstring(out.read_bytes())
        ids = [d.get("ID") for d in root.findall("DEVICE")]
        self.assertEqual(ids, [i for i, _ in MODELS], ids)
        for d in root.findall("DEVICE"):
            release = d.find("HARDWARE/RELEASE")
            self.assertEqual(release.get("HTTPHOST"), "https://github.com")
            self.assertEqual(release.get("URLPATH"), "o/r/releases/download/t")
            self.assertEqual(release.find("IMAGE").get("FILENAME"), "SoundTap-Core-0.1.0.stu")

    def test_fetch_route(self) -> None:
        """The device's feed request names a URL the server answers with the package itself."""
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
                with urllib.request.urlopen(
                        f"http://127.0.0.1:{server.server_port}/updates/soundtouch?serialnumber=1", timeout=5) as r:
                    release = ET.fromstring(r.read()).find("DEVICE/HARDWARE/RELEASE")
                fetch = f"{release.get('HTTPHOST')}/{release.get('URLPATH')}/Update-wla.stu"
                self.assertEqual(fetch, base + "/soundtap/Update-wla.stu")
                # The URL just checked equal to the index's, spelled out for the opener.
                with urllib.request.urlopen(
                        f"http://127.0.0.1:{server.server_port}/soundtap/Update-wla.stu", timeout=5) as response:
                    self.assertEqual(response.read(), payload)
            finally:
                server.shutdown()
                thread.join()
                server.server_close()


if __name__ == "__main__":
    unittest.main()
