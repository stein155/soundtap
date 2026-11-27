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

    def test_join_timeout_covers_the_app(self) -> None:
        """The driver waits longer for a join than the BCO app can take for one.

        The app scans once, then makes JOIN_TRIES attempts, each step with its own deadline
        (bco/app/src/wifi.c join_attempt and join_step). A driver that gave up earlier reported a
        failure while the BCO still joined, and wpa_supplicant's next connect raced that attempt.
        """
        wifi = (APP / "wifi.c").read_text()
        app = defines(wifi)
        steps = dict(re.findall(r"jstep = J_(SETUP|AUTH|ASSOC|4WAY), deadline = now \+ (\d+);", wifi))
        self.assertEqual(set(steps), {"SETUP", "AUTH", "ASSOC", "4WAY"})
        attempt_ms = sum(int(v) for v in steps.values())
        # the scan's last command (0xe4) is followed by one more wait before the scan ends
        tail_ms = int(re.search(r"0xe4, 0x13,.*?scan_next = now \+ (\d+);", wifi, re.DOTALL)[1])
        scan_ms = app["SCAN_PASSES"] * app["NCHAN"] * app["DWELL_MS"] + tail_ms
        budget_ms = scan_ms + app["JOIN_TRIES"] * attempt_ms
        drv = int(re.search(r"#define BCO_JOIN_TIMEOUT\s+\((\d+) \* HZ\)", self.patch)[1])
        self.assertGreater(drv * 1000, budget_ms + 10_000, f"app budget {budget_ms} ms, driver {drv} s")

    def test_join_timeout_stops_the_bco(self) -> None:
        """A join the driver gives up on is ended on the BCO too, and reported as a timeout."""
        funcs = dict(re.findall(r"\n\+static [^\n]*\b(\w+)\([^)]*\)\n\+\{(.*?)\n\+\}", self.patch, re.DOTALL))
        branch = re.search(r"\} else if \(timeout\) \{(.*?)\} else \{", funcs["bco_join_poll"], re.DOTALL)
        self.assertIsNotNone(branch)
        self.assertIn("bco_req(b, T_FORGET, NULL, 0)", branch[1])
        self.assertIn("cr.status = -1", branch[1])

    def test_status_group_cipher(self) -> None:
        """The driver reads STATUS's group cipher byte where the app writes it, and only when present.

        The app writes it right after the join failure's step and le16 code; an older app does not.
        """
        mgmt = (APP / "mgmt.c").read_text()
        status = re.search(r"case 5: \{ /\* STATUS(.*?)break;", mgmt, re.DOTALL)[1]
        self.assertRegex(status, r"\*o\+\+ = step;\s*\*o\+\+ = code, \*o\+\+ = code >> 8;\s*\*o\+\+ = b\.grp;")
        funcs = dict(re.findall(r"\n\+static [^\n]*\b(\w+)\([^)]*\)\n\+\{(.*?)\n\+\}", self.patch, re.DOTALL))
        body = funcs["bco_group_cipher"]
        self.assertIn("at = 3 + r[2] + 8 + 3;", body)
        self.assertIn("if (b->rsp_len < at + 1)\n+\t\treturn NULL;", body)
        self.assertIn("bco_group_cipher(b)", funcs["bco_join_result"])

    def test_security(self) -> None:
        """ADR 0136's profile and scan security values are numbered the same on both sides.

        The driver sends an open network only to an app that announces BCOLINK_CAP_SECURITY, and
        reads STATUS's security byte right after the group cipher, only when present.
        """
        self.assertEqual(self.app["BCOLINK_CAP_SECURITY"], self.drv["BCOLINK_CAP_SECURITY"])
        wifi = (APP / "wifi.h").read_text()
        for app, drv in (("WIFI_SEC_", "BCO_SEC_"), ("WIFI_BSS_", "BCO_BSS_")):
            names = re.search(rf"enum \{{ ({app}[^}}]*)\}}", wifi)[1]
            names = [n.strip()[len(app):] for n in names.split(",") if n.strip()]
            mirror = re.search(rf"enum \{{ ({drv}[^}}]*)\}}", self.patch)[1].replace("\n+", " ")
            mirror = [n.strip()[len(drv):] for n in mirror.split(",") if n.strip()]
            self.assertEqual(names, mirror, app)
        funcs = dict(re.findall(r"\n\+static [^\n]*\b(\w+)\([^)]*\)\n\+\{(.*?)\n\+\}", self.patch, re.DOTALL))
        self.assertRegex(funcs["bco_connect"], r"\(open \|\| wep\) && !\(caps & BCOLINK_CAP_SECURITY\)")
        for app, drv in (("WIFI_WEP_", "BCO_WEP_"),):
            names = re.search(rf"enum \{{ ({app}[^}}]*)\}}", wifi)[1]
            names = [n.strip()[len(app):] for n in names.split(",") if n.strip()]
            mirror = re.search(rf"enum \{{ ({drv}[^}}]*)\}}", self.patch)[1]
            mirror = [n.strip()[len(drv):] for n in mirror.split(",") if n.strip()]
            self.assertEqual(names, mirror, app)
        # cfg80211 uploads a connect's WEP keys after it: add_key needs del_key and set_default_key
        for op in ("add_key", "del_key", "set_default_key"):
            self.assertIn(f".{op} = bco_{op},", self.patch)
        mgmt = (APP / "mgmt.c").read_text()
        status = re.search(r"case 5: \{ /\* STATUS(.*?)break;", mgmt, re.DOTALL)[1]
        self.assertRegex(status, r"\*o\+\+ = b\.grp;\s*\*o\+\+ = b\.sec;")
        self.assertIn("at = 3 + r[2] + 8 + 3 + 1;", funcs["bco_status_sec"])
        self.assertIn("b->rsp_len >= at + 1", funcs["bco_status_sec"])
        # SCAN's security detail: the same flags, sets and length, asked for only behind the bit,
        # and STATUS's copy right after its security byte
        for app, drv in (("MGMT_SCAN_START", "BCO_SCAN_START"), ("MGMT_SCAN_DETAIL", "BCO_SCAN_DETAIL"),
                         ("MGMT_SCAN_DETAIL_LEN", "BCO_SCAN_DETAIL_LEN")):
            self.assertEqual(self.app[app], self.drv[drv], app)
        wifi_sets = defines(wifi)
        for name in ("CCMP", "TKIP", "PSK"):
            self.assertEqual(wifi_sets[f"WIFI_SET_{name}"], self.drv[f"BCO_SET_{name}"], name)
        self.assertIn("READ_ONCE(b->caps) & BCOLINK_CAP_SECURITY", funcs["bco_scan_work"])
        self.assertRegex(status, r"\*o\+\+ = b\.sec;\s*o = put_detail\(o, &b\);")
        self.assertIn("at = 3 + r[2] + 8 + 3 + 1 + 1;", funcs["bco_status_detail"])

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
