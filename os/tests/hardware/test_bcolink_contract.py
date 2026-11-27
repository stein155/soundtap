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


def defines(text: str) -> dict[str, int]:
    """Return every numeric #define in `text` by name."""
    return {m[1]: int(m[2], 0) for m in re.finditer(r"#define\s+(\w+)\s+(0x[0-9a-fA-F]+|\d+)u?\b", text)}


class BcoLinkContract(unittest.TestCase):
    """The BCO app and the kernel driver agree on the link."""

    def setUp(self) -> None:
        """Read the defines of both sides and the driver patch."""
        self.app = defines((APP / "bcolink.h").read_text() + (APP / "mgmt.h").read_text())
        self.patch = DRIVER.read_text()
        self.drv = defines(self.patch)

    def test_framing(self) -> None:
        """Header size, maximum frame and channel numbers are the same on both sides."""
        for name in ("BCOLINK_HDR", "BCOLINK_MAX", "BCOLINK_CH_CTRL", "BCOLINK_CH_WIFI", "BCOLINK_CH_WIRED"):
            self.assertEqual(self.app[name], self.drv[name], name)
        self.assertEqual(self.app["MGMT_HDR"], self.drv["BCO_MGMT_HDR"])
        self.assertEqual(self.app["MGMT_MAX"], self.drv["BCOLINK_MAX"])

    def test_protocol_level(self) -> None:
        """Both sides speak the same protocol level."""
        self.assertEqual(self.app["BCOLINK_PROTO"], self.drv["BCO_LINK_PROTO"])

    def test_capabilities_and_layouts(self) -> None:
        """Both sides name the same capability bits and reply layouts.

        Flow control and the counters are capabilities, not a new level: both sides name the
        same bits and the same reply layouts (ADR 0031).
        """
        for app, drv in (("BCOLINK_CAP_FLOW", "BCOLINK_CAP_FLOW"), ("BCOLINK_CAP_STATS", "BCOLINK_CAP_STATS"),
                         ("MGMT_FLOW_LEN", "BCO_FLOW_LEN"), ("MGMT_STATS_N", "BCO_STATS_N")):
            self.assertEqual(self.app[app], self.drv[drv], app)

    def test_capabilities(self) -> None:
        """The access point bit is the same on both sides, and the driver offers AP mode only behind it.

        hostapd on an app without its own beacons would start an access point that never goes on air.
        """
        self.assertEqual(self.app["BCOLINK_CAP_AP"], self.drv["BCOLINK_CAP_AP"])
        gate = re.search(r"\n\+\tif \(b->caps & BCOLINK_CAP_AP\) \{(.*?)\n\+\t\}", self.patch, re.DOTALL)
        self.assertIsNotNone(gate)
        for line in ("BIT(NL80211_IFTYPE_AP)", "iface_combinations", "WIPHY_FLAG_HAVE_AP_SME"):
            self.assertIn(line, gate[1], line)
        self.assertIn("wiphy->interface_modes = BIT(NL80211_IFTYPE_STATION);", self.patch)

    def test_join_failure_steps(self) -> None:
        """STATUS's join failure steps are numbered the same on both sides.

        The driver turns the step into the status code wpa_supplicant (and so soundtapd) sees.
        """
        app = re.search(r"enum \{\s*(WIFI_FAIL_NONE[^}]*)\}", (APP / "wifi.h").read_text())[1]
        app = [n.split("_", 2)[2] for n in re.findall(r"WIFI_FAIL_\w+", app)]
        drv = re.search(r"enum \{ (F_NONE[^}]*)\}", self.patch)[1].replace("\n+", " ")
        drv = [n.strip()[2:] for n in drv.split(",") if n.strip()]
        self.assertEqual(app, drv)

    def test_standard_features(self) -> None:
        """The features of ADR 0135 have the same bits, layouts and numbers on both sides."""
        same = {f"BCOLINK_CAP_{n}": f"BCOLINK_CAP_{n}" for n in ("EVENTS", "ROAM", "REGDOM", "SCAN_SSID", "NVINFO")}
        same |= {"MGMT_EVENT_LEN": "BCO_EVENT_LEN", "MGMT_PROFILE_TAIL": "BCO_PROFILE_TAIL",
                 "MGMT_REGDOM_LEN": "BCO_REGDOM_LEN", "MGMT_NV_ENTRY": "BCO_NV_ENTRY"}
        same |= {f"MGMT_PROFILE_{n}": f"BCO_PROFILE_{n}" for n in ("BSSID_FIXED", "BSSID_HINT", "ROAM")}
        same |= {f"MGMT_EV_{n}": f"BCO_EV_{n}" for n in
                 ("DEAUTH", "DISASSOC", "BEACON_LOSS", "TX_WEDGE", "ROAMED", "ROAM_FAILED")}
        for app, drv in same.items():
            self.assertEqual(self.app[app], self.drv[drv], app)

    def test_new_requests_follow_hello(self) -> None:
        """The driver sends what ADR 0135 added only where the app announced it.

        An older app answers an unknown request with 0xff and refuses a longer SET_PROFILE, so
        each function that sends one also checks its capability bit (ADR 0031's rule).
        """
        funcs = dict(re.findall(r"\n\+static [^\n]*\b(\w+)\([^)]*\)\n\+\{(.*?)\n\+\}", self.patch, re.DOTALL))
        for name, body in funcs.items():
            for req, cap in (("bco_req(b, T_EVENTS", "EVENTS"), ("bco_req(b, T_SET_REGDOM", "REGDOM"),
                             ("bco_req(b, T_NV_INFO", "NVINFO"),
                             ("BCO_PROFILE_ROAM", "ROAM"), ("start[1] = req->ssids", "SCAN_SSID")):
                if req in body:
                    self.assertIn(f"BCOLINK_CAP_{cap}", body, f"{name} uses {req}")
        self.assertIn("wiphy->reg_notifier = bco_reg_notifier", self.patch)
        self.assertIn("mod_delayed_work(system_wq, &b->join_work, 0)", funcs["bco_ctrl_rx"])
        self.assertIn("cancel_work_sync(&b->reg_work)", funcs["bco_unbind"])
        # the antenna read is a bench read: root only, nothing in the driver asks on its own
        self.assertIn("DEVICE_ATTR_ADMIN_RO(nv_keys)", self.patch)
        self.assertEqual([n for n, b in funcs.items() if "bco_req(b, T_NV_INFO" in b], ["nv_keys_show"])

    def test_held_queue_is_watched(self) -> None:
        """Whatever stops a queue for want of credit also schedules the watch that counts afresh.

        A queue stopped for want of credit must not stay stopped when a report or a frame is lost.
        """
        funcs = re.findall(r"\n\+static [^\n]*\b(\w+)\([^)]*\)\n\+\{(.*?)\n\+\}", self.patch, re.DOTALL)
        stoppers = [name for name, body in funcs if "netif_stop_queue" in body]
        self.assertTrue(stoppers)
        for name, body in funcs:
            if name in stoppers:
                self.assertIn("schedule_delayed_work(&b->flow_work", body, name)
        self.assertIn("INIT_DELAYED_WORK(&b->flow_work, bco_flow_watch)", self.patch)

    def test_usb_match(self) -> None:
        """The driver matches the USB ids, subclass and protocol the app announces."""
        want = (f"USB_DEVICE_AND_INTERFACE_INFO(0x{self.app['BCOLINK_USB_VID']:04x}, 0x{self.app['BCOLINK_USB_PID']:04x}, "
                f"USB_CLASS_VENDOR_SPEC, 0x{self.app['BCOLINK_USB_SUBCLASS']:02x}, {self.app['BCOLINK_USB_PROTO']:d})")
        self.assertIn(want, self.patch)

    def test_message_types(self) -> None:
        """The driver's message type numbers are the ones the app's management code handles."""
        names = re.search(r"enum \{ (T_HELLO = 1[^}]*)\}", self.patch)[1].replace("\n+", " ")
        driver, nxt = {}, 1
        for n in (n.strip() for n in names.split(",") if n.strip()):
            name, _, val = n.partition("=")
            nxt = int(val) if val.strip() else nxt
            driver[nxt] = name.strip()[2:]
            nxt += 1
        mgmt = (APP / "mgmt.c").read_text()
        # dev-phase requests (WILD_OWN builds only) are sent raw by tools/wildload.c, not the driver
        mgmt = re.sub(r"#ifdef WILD_OWN\n    case .*?#endif", "", mgmt, flags=re.DOTALL)
        app = {int(m[1]): m[2] for m in re.finditer(r"case (\d+):(?: \{)? /\* (\w+)", mgmt)}
        self.assertEqual(driver, app)

    def test_hunk_count(self) -> None:
        """The driver patch's hunk header counts the lines the new file has."""
        lines = self.patch.split("\n")
        i = next(k for k, line in enumerate(lines) if line.startswith("+++ b/drivers/net/usb/soundtap_bco.c"))
        count = int(re.match(r"@@ -0,0 \+1,(\d+) @@", lines[i + 1])[1])
        body = [line for line in lines[i + 2 : i + 2 + count + 1] if line.startswith("+")]
        self.assertEqual(count, len(body))

    def test_eth_gets_carrier(self) -> None:
        """eth0's open (and the transport coming back) raises its carrier.

        Every port starts without carrier and rx drops frames for a port without it.
        """
        ops = re.search(r"bco_eth_ops = \{(.*?)\};", self.patch, re.DOTALL)[1]
        opener = re.search(r"\.ndo_open = (\w+)", ops)[1]
        body = re.search(rf"static int {opener}\(struct net_device \*dev\)\n\+\{{(.*?)\n\+\}}", self.patch, re.DOTALL)[1]
        self.assertIn("netif_carrier_on(dev)", body)
        self.assertRegex(self.patch, r"\.reset = bco_reset,")


if __name__ == "__main__":
    unittest.main()
