# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The BCO link is defined twice: bco/app/src (the BCO side) and the soundtap_bco kernel driver patch.

Neither build sees the other, so a framing value changed on one side only would build, flash and then
fail on the speaker. This holds the two to the same numbers (ADR 0031).
"""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
APP = ROOT / "bco/app/src"
DRIVER = ROOT / "os/board/rootfs/linux-patches/0007-net-usb-add-soundtap-bco-link-driver.patch"


def defines(text):
    return {m[1]: int(m[2], 0) for m in re.finditer(r"#define\s+(\w+)\s+(0x[0-9a-fA-F]+|\d+)u?\b", text)}


class BcoLinkContract(unittest.TestCase):
    def setUp(self):
        self.app = defines((APP / "bcolink.h").read_text() + (APP / "mgmt.h").read_text())
        self.patch = DRIVER.read_text()
        self.drv = defines(self.patch)

    def test_framing(self):
        for name in ("BCOLINK_HDR", "BCOLINK_MAX", "BCOLINK_CH_CTRL", "BCOLINK_CH_WIFI", "BCOLINK_CH_WIRED"):
            self.assertEqual(self.app[name], self.drv[name], name)
        self.assertEqual(self.app["MGMT_HDR"], self.drv["BCO_MGMT_HDR"])
        self.assertEqual(self.app["MGMT_MAX"], self.drv["BCOLINK_MAX"])

    def test_protocol_level(self):
        self.assertEqual(self.app["BCOLINK_PROTO"], self.drv["BCO_LINK_PROTO"])

    def test_usb_match(self):
        want = "USB_DEVICE_AND_INTERFACE_INFO(0x%04x, 0x%04x, USB_CLASS_VENDOR_SPEC, 0x%02x, %d)" % (
            self.app["BCOLINK_USB_VID"], self.app["BCOLINK_USB_PID"],
            self.app["BCOLINK_USB_SUBCLASS"], self.app["BCOLINK_USB_PROTO"])
        self.assertIn(want, self.patch)

    def test_message_types(self):
        names = re.search(r"enum \{ (T_HELLO = 1[^}]*)\}", self.patch)[1].replace("\n+", " ")
        driver, nxt = {}, 1
        for n in (n.strip() for n in names.split(",") if n.strip()):
            name, _, val = n.partition("=")
            nxt = int(val) if val.strip() else nxt
            driver[nxt] = name.strip()[2:]
            nxt += 1
        mgmt = (APP / "mgmt.c").read_text()
        # dev-phase requests (WILD_OWN builds only) are sent raw by tools/wildload.c, not the driver
        mgmt = re.sub(r"#ifdef WILD_OWN\n    case .*?#endif", "", mgmt, flags=re.S)
        app = {int(m[1]): m[2] for m in re.finditer(r"case (\d+):(?: \{)? /\* (\w+)", mgmt)}
        self.assertEqual(driver, app)

    def test_hunk_count(self):
        lines = self.patch.split("\n")
        i = next(k for k, line in enumerate(lines) if line.startswith("+++ b/drivers/net/usb/soundtap_bco.c"))
        count = int(re.match(r"@@ -0,0 \+1,(\d+) @@", lines[i + 1])[1])
        body = [line for line in lines[i + 2 : i + 2 + count + 1] if line.startswith("+")]
        self.assertEqual(count, len(body))

    def test_eth_gets_carrier(self):
        # Every port starts without carrier and rx drops frames for a port without it,
        # so eth0's open (and the transport coming back) must raise it.
        ops = re.search(r"bco_eth_ops = \{(.*?)\};", self.patch, re.S)[1]
        opener = re.search(r"\.ndo_open = (\w+)", ops)[1]
        body = re.search(rf"static int {opener}\(struct net_device \*dev\)\n\+\{{(.*?)\n\+\}}", self.patch, re.S)[1]
        self.assertIn("netif_carrier_on(dev)", body)
        self.assertRegex(self.patch, r"\.reset = bco_reset,")


if __name__ == "__main__":
    unittest.main()
