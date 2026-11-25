#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Mock of the two ways a package reaches Bose's stock updater.

We cannot boot the proprietary stock firmware in CI (board-specific closed OMAP3 drivers +
BoseApp), so this stands in for the one thing that actually gates a stock->soundtap install:
the updater accepting the package.

**Upload.** GET / serves the upload page; POST /update takes the raw STU body, runs the real
acceptance check (header + per-module CRC32, the stock gate), and returns 202 (accepted,
saved to OUT) or 400 (rejected).

The model matters. accept() without one holds the upload to the default module chain, so
a WLA package -- eight modules including MLO and u-boot.img -- was rejected by a mock that
was standing in for the very updater that accepts it.

**Fetch.** The other route, and the one the app uses: the device GETs the URL
configured as `swUpdateUrl` (`<url>?serialnumber=<serial>`), expects an index document, and
downloads the package that index names from `<HTTPHOST>/<URLPATH>/<FILENAME>`. With --serve,
every GET that is not the package gets that index.

The schema below is the one a stock Wireless Link on 27.0.6 accepted on 2026-11-05: it went
from stock to SoundTap off a LAN feed in about 200 s. A flat `<IMAGE><PRODUCTNAME>...` index,
which this mock served before, earns `4002 SW_UPDATE_CHECK_UNSUPPORTED_DEVICE`. CRC is the
CRC32 of the whole package. The device takes the offer only when RELEASE REVISION is newer
than what it runs.

    <INDEX REVISION="02.11.00">
      <DEVICE ID="0x094A" PRODUCTNAME="SoundTouch Wireless Link adapter">
        <HARDWARE REVISION="00.01.00">
          <RELEASE REVISION="99.0.0.1.1" HTTPHOST="http://host:port" URLPATH="soundtap" ...>
            <IMAGE SUBID="0" LENGTH="..." CRC="0x..." FILENAME="package.stu"/>

  mock_update_server.py <port> <out.stu> [model] [--serve package.stu] [--bind ADDR]
                        [--product NAME] [--device-id ID] [--version V] [--all-models]
  mock_update_server.py --serve package.stu --write-index index.xml --host URL [--urlpath P]
                        [--preview]
"""
import argparse
import http.server
import sys
import zlib
from collections.abc import Iterable
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "os/support"))
from stu_check import accept

# The running revision on the Wireless Link this was taken from (etc/FS_VERSION). An offer
# has to beat it, or the caller has to force the install.
STOCK_REVISION = "27.0.6.46330.5043500"
# Where the index tells the device to fetch the package: <HTTPHOST>/<URLPATH>/<FILENAME>.
URLPATH = "soundtap"

INDEX_PAGE = (b"<!doctype html><title>SoundTouch Software Update</title>"
              b"<h1>Software Update</h1>"
              b"<form method=post action=/update enctype=multipart/form-data>"
              b"<input type=file name=stu><button>Upload</button></form>")


# Every model SoundTap supports, as Bose's own index names them (downloads.bose.com
# .../index.xml, archived 2023): one index holds a DEVICE per product and every product
# points its swUpdateUrl at that same file. updates/soundtouch lists these.
MODELS = [
    ("0x0939", "SoundTouch 10"),
    ("0x0923", "SoundTouch 20"),
    ("0x0949", "SoundTouch 300"),
    ("0x094A", "SoundTouch Wireless Link adapter"),
]

# The preview models (ADR 0133), with the ids and names of the same Bose index:
# updates/soundtouch-preview lists these, offering the preview package, the only one they
# install from. The ST20 Series III (0x093B, sm2) is here, not above. Not listed: the
# Lifestyle ids on the sm2 (0x093F, 0x0947), which the installers refuse, the Lifestyle
# 600/650 module (0x0948) and 0x094B.
PREVIEW_MODELS = [
    ("0x0924", "SoundTouch 30"),            # Series I/II, scm
    ("0x093B", "SoundTouch 20"),            # Series III, sm2
    ("0x093C", "SoundTouch 30"),            # Series III, sm2
    ("0x0925", "SoundTouch Portable"),
    ("0x093A", "SoundTouch SA-5"),
    # The SoundTouch adapter on the scm
    ("0x0932", "Wave SoundTouch"),
    ("0x0933", "VideoWave"),
    ("0x0944", "VideoWave"),
    ("0x0934", "Lifestyle"),
    ("0x0945", "Lifestyle"),
    ("0x0935", "SoundTouch Stereo JC"),
    ("0x0936", "SoundTouch SA-4"),
    ("0x0938", "Cinemate"),
    # and on the sm2
    ("0x093D", "Wave SoundTouch"),
    ("0x093E", "VideoWave"),
    ("0x0946", "VideoWave"),
    ("0x0940", "SoundTouch Stereo JC"),
    ("0x0941", "SoundTouch SA-4"),
    ("0x0942", "Cinemate"),
]


def build_index(package: str | Path, devices: Iterable[tuple[str, str]], version: str, host: str,
                urlpath: str = URLPATH) -> bytes:
    """Build the index document in the schema the device parses.

    One DEVICE per (id, product) in devices; host is the HTTPHOST it fetches from.
    """
    with Path(package).open("rb") as f:
        crc = zlib.crc32(f.read())
    name = Path(package).name
    release = (
        '    <HARDWARE REVISION="00.01.00">\n'
        f'      <RELEASE REVISION="{version}" HTTPHOST="{host}" URLPATH="{urlpath}"'
        f' USBPATH="/{urlpath}/Update.stu">\n'
        f'        <IMAGE SUBID="0" LENGTH="{Path(package).stat().st_size}" CRC="0x{crc:08x}"'
        f' FILENAME="{name}"/>\n'
        '      </RELEASE>\n'
        '    </HARDWARE>\n'
    )
    body = "".join(f'  <DEVICE ID="{i}" PRODUCTNAME="{p}">\n{release}  </DEVICE>\n'
                   for i, p in devices)
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            f'<INDEX REVISION="02.11.00">\n{body}</INDEX>\n').encode()


def make_handler(options: argparse.Namespace) -> type[http.server.BaseHTTPRequestHandler]:
    """Return a request handler for the upload and fetch routes the options configure."""
    package = options.serve
    served_name = Path(package).name if package else None

    class H(http.server.BaseHTTPRequestHandler):
        def log_message(self, *a: object) -> None:  # quiet
            pass

        def _send(self, code: int, body: bytes, content_type: str) -> None:
            self.send_response(code)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self) -> None:
            path = self.path.split("?", 1)[0]
            if package and path == f"/{URLPATH}/{served_name}":
                with Path(package).open("rb") as f:
                    self._send(200, f.read(), "application/octet-stream")
                return
            if package:
                host = f"http://{self.headers.get('Host', 'localhost')}"
                devices = MODELS if options.all_models else [(options.device_id,
                                                              options.product)]
                self._send(200, build_index(package, devices, options.version, host),
                           "text/xml")
                return
            self._send(200, INDEX_PAGE, "text/html")

        def do_POST(self) -> None:
            n = int(self.headers.get("Content-Length", 0))
            data = self.rfile.read(n)
            with Path(options.out).open("wb") as f:
                f.write(data)
            ok = accept(options.out, options.model)
            self.send_response(202 if ok else 400)
            self.end_headers()
            self.wfile.write(b"ACCEPTED\n" if ok else b"REJECTED\n")

    return H


def main(argv: list[str]) -> None:
    """Write a static index, or serve the upload and fetch routes, as argv asks."""
    ap = argparse.ArgumentParser(description="stand-in for the stock updater")
    ap.add_argument("port", nargs="?", type=int, default=8080)
    ap.add_argument("out", nargs="?", default="/tmp/uploaded.stu")
    ap.add_argument("model", nargs="?", default=None)
    ap.add_argument("--serve", help="package to advertise and hand out on the fetch route")
    # Loopback by default: a mock that accepts anything should not be on the network
    # unless someone asks for it. A real board needs 0.0.0.0.
    ap.add_argument("--bind", default="127.0.0.1")
    ap.add_argument("--product", default="SoundTouch Wireless Link adapter",
                    help="PRODUCTNAME in the index")
    # The binky WLA's own swUpdateDeviceId, cross-checked in the stock rootfs:
    # BoseApp-binky.xml <swUpdateDeviceId>0x094A</swUpdateDeviceId> and
    # bonjour.xml <DEVICE ID="0x094A">. The updater's SW_UPDATE_CHECK_UNSUPPORTED_DEVICE
    # (4002) gate compares the index DEVICE ID against this; a real WLA accepted 0x094A
    #. The 0x1c00 in the shipped GIN example is a legacy Ginger-classic id.
    ap.add_argument("--device-id", default="0x094A", help="DEVICEID in the index")
    ap.add_argument("--version", default="99.0.0.1.1",
                    help=f"VERSION in the index; the device runs {STOCK_REVISION}")
    ap.add_argument("--all-models", action="store_true",
                    help="offer the package to every supported model, not just --device-id")
    # A static feed: write the index for every model and exit. --host and
    # --urlpath say where the package is published, e.g. https://github.com and
    # <owner>/<repo>/releases/download/<tag>.
    ap.add_argument("--write-index", metavar="FILE", help="write a static all-model index")
    ap.add_argument("--host", help="HTTPHOST for --write-index")
    ap.add_argument("--urlpath", default=URLPATH, help="URLPATH for --write-index")
    ap.add_argument("--preview", action="store_true",
                    help="--write-index for the preview models (updates/soundtouch-preview)")
    options = ap.parse_args(argv)
    if options.write_index:
        if not (options.serve and options.host):
            msg = "--write-index needs --serve <package> and --host"
            raise SystemExit(msg)
        with Path(options.write_index).open("wb") as f:
            f.write(build_index(options.serve, PREVIEW_MODELS if options.preview else MODELS,
                                options.version, options.host, options.urlpath))
        return
    if options.preview:
        msg = "--preview goes with --write-index"
        raise SystemExit(msg)
    if options.serve and not Path(options.serve).is_file():
        msg = f"no package at {options.serve}"
        raise SystemExit(msg)
    where = f"http://{options.bind}:{options.port}"
    print(f"mock stock updater on {where} for {options.model or 'the default chain'}")
    print(f"  upload:  POST /update -> {options.out}")
    if options.serve:
        print(f"  fetch:   GET  <anything> -> index offering {options.version}")
        print(f"           GET  /{URLPATH}/{Path(options.serve).name} -> the package")
    http.server.HTTPServer((options.bind, options.port),
                           make_handler(options)).serve_forever()


if __name__ == "__main__":
    main(sys.argv[1:])
