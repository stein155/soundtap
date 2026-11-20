#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import contextlib
import importlib.util
import io
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "os_cves", Path(__file__).parents[3] / "tools/release/os_cves.py")
os_cves = importlib.util.module_from_spec(spec)
spec.loader.exec_module(os_cves)

AVAHI = "cpe:2.3:a:avahi:avahi:0.8:-:*:*:*:*:*:*"
BUSYBOX = "cpe:2.3:a:busybox:busybox:1.38.0:-:*:*:*:*:*:*"
UBOOT = "cpe:2.3:a:denx:u-boot:2026.07:-:*:*:*:*:*:*"

# Shaped like `make legal-info`'s sbom.cdx.json from a real build, trimmed.
SBOM = {
    "components": [
        {"bom-ref": "avahi", "name": "avahi", "version": "0.8", "cpe": AVAHI,
         "properties": [{"name": "BR_TYPE", "value": "target"}]},
        {"bom-ref": "busybox", "name": "busybox", "version": "1.38.0", "cpe": BUSYBOX,
         "properties": [{"name": "BR_TYPE", "value": "target"}]},
        {"bom-ref": "host-flex", "name": "flex", "version": "2.6.4", "cpe": "cpe:2.3:a:westes:flex:2.6.4:-:*:*:*:*:*:*",
         "properties": [{"name": "BR_TYPE", "value": "host"}]},
        {"bom-ref": "soundtapd", "name": "soundtapd", "version": "1",
         "properties": [{"name": "BR_TYPE", "value": "target"}]},
        {"bom-ref": "host-soundtap-bootloaders", "name": "soundtap-bootloaders", "version": "2026.07", "cpe": UBOOT,
         "properties": [{"name": "BR_TYPE", "value": "host"}]},
        {"bom-ref": "host-soundtap-bco-app", "name": "soundtap-bco-app", "version": "1",
         "properties": [{"name": "BR_TYPE", "value": "host"}]},
    ],
    "vulnerabilities": [
        {"id": "CVE-2021-26720", "analysis": {"state": "in_triage"}, "affects": [{"ref": "avahi"}]},
        {"id": "CVE-2021-3468", "analysis": {"state": "resolved_with_pedigree"}, "affects": [{"ref": "avahi"}]},
    ],
}


def cve(id_, base, version="cvssMetricV31", status="Analyzed"):
    return {"cve": {"id": id_, "vulnStatus": status, "metrics": {version: [{"cvssData": {"baseScore": base}}]}}}


# NVD API 2.0 answers to cpeName=...&isVulnerable on 2026-11-12, trimmed to the fields read.
NVD = {
    AVAHI: {"totalResults": 4, "vulnerabilities": [
        cve("CVE-2021-26720", 7.8), cve("CVE-2021-3468", 5.5), cve("CVE-2025-59529", 5.5),
        cve("CVE-2025-00000", 9.8, status="Rejected")]},
    BUSYBOX: {"totalResults": 1, "vulnerabilities": [cve("CVE-2026-38754", 5.1)]},
    UBOOT: {"totalResults": 0, "vulnerabilities": []},
}
# A synthetic finding, to see whether the checker asks about the bootloaders at all.
UBOOT_HIT = {"totalResults": 1, "vulnerabilities": [cve("CVE-2099-0001", 9.8)]}


def with_uboot_hit(cpe, start):
    return UBOOT_HIT if cpe == UBOOT else NVD[cpe]


class OsCves(unittest.TestCase):
    def test_shipped_packages_with_a_cpe(self):
        # flex only builds the image; the bootloaders are a host package whose output ships.
        self.assertEqual(os_cves.packages(SBOM), {
            AVAHI: ("avahi", "0.8", "avahi"), BUSYBOX: ("busybox", "1.38.0", "busybox"),
            UBOOT: ("soundtap-bootloaders", "2026.07", "host-soundtap-bootloaders")})
        self.assertEqual(os_cves.uncovered(SBOM), ["soundtap-bco-app", "soundtapd"])

    def test_shipped_host_package_is_asked_about(self):
        out = io.StringIO()
        self.assertEqual(os_cves.check(dict(SBOM, vulnerabilities=[]), get=with_uboot_hit, out=out), 2)
        text = out.getvalue()
        self.assertIn("soundtap-bootloaders 2026.07: 1 open\n  CVE-2099-0001   9.8", text)
        self.assertIn("3 package(s) checked: avahi, busybox, soundtap-bootloaders\n", text)
        self.assertIn("No CPE, not checked: soundtap-bco-app, soundtapd\n", text)

    def test_host_package_cves_are_cleared_by_bom_ref(self):
        sbom = dict(SBOM, vulnerabilities=[
            {"id": "CVE-2099-0001", "affects": [{"ref": "host-soundtap-bootloaders"}]}])
        out = io.StringIO()
        self.assertEqual(os_cves.check(sbom, get=with_uboot_hit, out=out), 1)  # avahi's 7.8 stays open
        self.assertIn("  soundtap-bootloaders CVE-2099-0001", out.getvalue())

    def test_empty_or_partial_inventory_is_refused(self):
        comps = SBOM["components"]
        hosts_only = [c for c in comps if c["properties"][0]["value"] == "host"]
        no_bootloaders = [c for c in comps if c["name"] != "soundtap-bootloaders"]
        for bad in ({}, [], {"components": {}}, {"components": []},
                    {"components": hosts_only}, {"components": no_bootloaders}):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                os_cves.check(bad, get=lambda cpe, start: self.fail("NVD asked"), out=io.StringIO())

    def test_main_exits_2_on_empty_or_broken_json(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "sbom.json"
            for text in ("{}", "not json"):
                path.write_text(text)
                with self.subTest(text=text), contextlib.redirect_stderr(io.StringIO()):
                    self.assertEqual(os_cves.main(["os_cves.py", str(path)]), 2)

    def test_score_prefers_newest_cvss(self):
        both = {"metrics": {"cvssMetricV2": [{"cvssData": {"baseScore": 4.3}}],
                            "cvssMetricV31": [{"cvssData": {"baseScore": 7.5}}]}}
        self.assertEqual(os_cves.score(both), 7.5)
        self.assertIsNone(os_cves.score({"metrics": {}}))

    def test_unscored_open_cve_counts(self):
        sbom = dict(SBOM, vulnerabilities=[])
        nvd = {k: {"totalResults": 1, "vulnerabilities": [{"cve": {"id": "CVE-2026-1", "metrics": {}}}]} for k in NVD}
        out = io.StringIO()
        self.assertEqual(os_cves.check(sbom, get=lambda cpe, start: nvd[cpe], out=out), len(nvd))
        self.assertIn("CVE-2026-1  unscored", out.getvalue())

    def test_cpe_unknown_to_nvd_is_not_checked(self):
        out = io.StringIO()
        self.assertEqual(os_cves.check(SBOM, get=lambda cpe, start: None, out=out), 0)
        self.assertRegex(out.getvalue(), r"CPE unknown to NVD, not checked: \S")

    def test_ignored_cves_are_cleared_not_open(self):
        out = io.StringIO()
        serious = os_cves.check(SBOM, get=lambda cpe, start: NVD[cpe], out=out)
        text = out.getvalue()
        self.assertEqual(serious, 0)
        self.assertIn("avahi 0.8: 1 open\n  CVE-2025-59529   5.5", text)
        self.assertIn("Cleared by Buildroot:\n  avahi CVE-2021-26720\n  avahi CVE-2021-3468", text)
        self.assertNotIn("CVE-2025-00000", text)

    def test_open_high_score_counts(self):
        sbom = dict(SBOM, vulnerabilities=[])
        self.assertEqual(os_cves.check(sbom, get=lambda cpe, start: NVD[cpe], out=io.StringIO()), 1)

    def test_pages_until_total(self):
        starts = []

        def get(cpe, start):
            starts.append(start)
            return {"totalResults": 2 * os_cves.PAGE + 1, "vulnerabilities": [cve(f"CVE-2026-{start}", 1.0)]}

        self.assertEqual(len(os_cves.cves(AVAHI, get)), 3)
        self.assertEqual(starts, [0, os_cves.PAGE, 2 * os_cves.PAGE])


if __name__ == "__main__":
    unittest.main()
