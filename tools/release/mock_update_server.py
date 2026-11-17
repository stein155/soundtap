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
"""
import argparse
import http.server
import os
import sys
import zlib
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
# points its swUpdateUrl at that same file. The ST20 has two ids, one per generation.
MODELS = [
    ("0x0939", "SoundTouch 10"),
    ("0x0923", "SoundTouch 20"),
    ("0x093B", "SoundTouch 20"),
    ("0x0949", "SoundTouch 300"),
    ("0x094A", "SoundTouch Wireless Link adapter"),
]


def build_index(package, devices, version, host, urlpath=URLPATH):
    """The index document in the schema the device parses, one DEVICE per (id, product)
    in devices; host is the HTTPHOST it fetches from."""
    with open(package, "rb") as f:
        crc = zlib.crc32(f.read())
    name = os.path.basename(package)
    release = (
        '    <HARDWARE REVISION="00.01.00">\n'
        f'      <RELEASE REVISION="{version}" HTTPHOST="{host}" URLPATH="{urlpath}"'
        f' USBPATH="/{urlpath}/Update.stu">\n'
        f'        <IMAGE SUBID="0" LENGTH="{os.path.getsize(package)}" CRC="0x{crc:08x}"'
        f' FILENAME="{name}"/>\n'
        '      </RELEASE>\n'
        '    </HARDWARE>\n'
    )
    body = "".join(f'  <DEVICE ID="{i}" PRODUCTNAME="{p}">\n{release}  </DEVICE>\n'
                   for i, p in devices)
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            f'<INDEX REVISION="02.11.00">\n{body}</INDEX>\n').encode()


def make_handler(options):
    package = options.serve
    served_name = os.path.basename(package) if package else None

    class H(http.server.BaseHTTPRequestHandler):
        def log_message(self, *a):  # quiet
            pass

        def _send(self, code, body, content_type):
            self.send_response(code)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            path = self.path.split("?", 1)[0]
            if package and path == f"/{URLPATH}/{served_name}":
                with open(package, "rb") as f:
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

        def do_POST(self):
            n = int(self.headers.get("Content-Length", 0))
            data = self.rfile.read(n)
            with open(options.out, "wb") as f:
                f.write(data)
            ok = accept(options.out, options.model)
            self.send_response(202 if ok else 400)
            self.end_headers()
            self.wfile.write(b"ACCEPTED\n" if ok else b"REJECTED\n")

    return H


def main(argv):
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
    options = ap.parse_args(argv)
    if options.write_index:
        if not (options.serve and options.host):
            raise SystemExit("--write-index needs --serve <package> and --host")
        with open(options.write_index, "wb") as f:
            f.write(build_index(options.serve, MODELS, options.version, options.host,
                                options.urlpath))
        return
    if options.serve and not os.path.isfile(options.serve):
        raise SystemExit(f"no package at {options.serve}")
    where = f"http://{options.bind}:{options.port}"
    print(f"mock stock updater on {where} for {options.model or 'the default chain'}")
    print(f"  upload:  POST /update -> {options.out}")
    if options.serve:
        print(f"  fetch:   GET  <anything> -> index offering {options.version}")
        print(f"           GET  /{URLPATH}/{os.path.basename(options.serve)} -> the package")
    http.server.HTTPServer((options.bind, options.port),
                           make_handler(options)).serve_forever()


if __name__ == "__main__":
    main(sys.argv[1:])
