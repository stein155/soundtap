# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every CVE os/ignore-cves.mk ignores has a reason that ignore_cves.py can read, and the
checkable reasons hold: on the real tree, and on small fake trees that break one rule each."""

import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "os/support"))
import ignore_cves  # noqa: E402

DEFCONFIG = 'BR2_arm=y\nBR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="6.12.117"\n'
FRAGMENT = "# CONFIG_IP_SCTP is not set\nCONFIG_NF_TABLES=y\n"


class IgnoreCvesTest(unittest.TestCase):
    def check(self, mk, fragment=FRAGMENT, defconfig=DEFCONFIG, kernel_config=None, files=None):
        root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        for name, text in {"os/ignore-cves.mk": mk, "os/configs/soundtap_defconfig": defconfig,
                           "os/board/image/linux.fragment": fragment, **(files or {})}.items():
            (root / name).parent.mkdir(parents=True, exist_ok=True)
            (root / name).write_text(text)
        return ignore_cves.problems(root, kernel_config)

    def test_the_repository_gives_every_entry_a_reason_that_holds(self):
        self.assertEqual(ignore_cves.problems(ROOT), [])

    def test_the_repository_ignores_only_cves_with_one_per_line(self):
        entries = [line for line in (ROOT / "os/ignore-cves.mk").read_text().splitlines()
                   if "_IGNORE_CVES +=" in line]
        self.assertGreater(len(entries), 100)
        for line in entries:
            self.assertRegex(line, r"^\w+_IGNORE_CVES \+= CVE-\d{4}-\d+ # \S")

    def test_an_entry_without_a_reason_fails(self):
        self.assertEqual(self.check("LINUX_IGNORE_CVES += CVE-2026-1000\n"),
                         ["os/ignore-cves.mk:1: CVE-2026-1000 has no reason"])

    def test_an_unknown_reason_or_two_cves_on_a_line_fail(self):
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # not relevant\n"
                           "LIBPLIST_IGNORE_CVES += CVE-2017-5835 CVE-2017-5836 # fixed before 2.0.0\n")
        self.assertEqual(len(found), 2, found)
        self.assertIn("none of the forms", found[0])
        self.assertIn("one CVE per line", found[1])

    def test_a_6_12_fix_must_be_in_the_pinned_kernel(self):
        self.assertEqual(self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # fixed in 6.12.117\n"), [])
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # fixed in 6.12.118\n")
        self.assertEqual(len(found), 1)
        self.assertIn("after the pinned 6.12.117", found[0])

    def test_a_kernel_off_6_12_wants_every_entry_checked_again(self):
        found = self.check("", defconfig='BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="6.18.3"\n')
        self.assertEqual(len(found), 1)
        self.assertIn("not 6.12", found[0])

    def test_a_bug_must_come_after_6_12(self):
        self.assertEqual(self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # bug came in 6.14\n"), [])
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # bug came in 6.6\n")
        self.assertIn("which the pinned kernel includes", found[0])

    def test_a_symbol_said_off_must_be_off(self):
        mk = "LINUX_IGNORE_CVES += CVE-2026-1000 # net/sctp: CONFIG_IP_SCTP off\n"
        self.assertEqual(self.check(mk), [])
        found = self.check(mk, fragment="CONFIG_IP_SCTP=m\n")
        self.assertEqual(found, ["os/ignore-cves.mk:1: CVE-2026-1000: linux.fragment sets CONFIG_IP_SCTP=m"])
        found = self.check(mk, kernel_config="CONFIG_IP_SCTP=y\n")
        self.assertEqual(found, ["os/ignore-cves.mk:1: CVE-2026-1000: the built kernel has CONFIG_IP_SCTP=y"])

    def test_a_module_must_not_be_built_in(self):
        mk = "LINUX_IGNORE_CVES += CVE-2026-1000 # x_tables: CONFIG_NETFILTER_XTABLES=m\n"
        self.assertEqual(self.check(mk, kernel_config="CONFIG_NETFILTER_XTABLES=m\n"), [])
        found = self.check(mk, kernel_config="CONFIG_NETFILTER_XTABLES=y\n")
        self.assertIn("the built kernel has CONFIG_NETFILTER_XTABLES=y", found[0])

    def test_our_patch_must_name_the_cve(self):
        mk = "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-107696 # fixed by our patch\n"
        patch = {"os/package/soundtap-ffmpeg/0001-fix.patch": "[for CVE-2026-107696]\n"}
        self.assertEqual(self.check(mk, files=patch), [])
        found = self.check(mk, files={"os/package/soundtap-ffmpeg/0001-fix.patch": "other\n"})
        self.assertIn("no patch of ours for soundtap_ffmpeg names it", found[0])

    def test_code_not_built_for_linux_is_for_packages_other_than_the_kernel(self):
        mk = "LIBGLIB2_IGNORE_CVES += CVE-2025-4056 # glib/gspawn-win32.c, Windows only: not built for Linux\n"
        self.assertEqual(self.check(mk), [])
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # arch/x86: not built for Linux\n")
        self.assertEqual(found, ["os/ignore-cves.mk:1: CVE-2026-1000: code the kernel does not build "
                                 "names its symbol, 'CONFIG_X off'"])

    def test_a_duplicate_fails(self):
        line = "LINUX_IGNORE_CVES += CVE-2026-1000 # fixed in 6.12.1\n"
        self.assertIn("listed twice", self.check(line * 2)[0])


if __name__ == "__main__":
    unittest.main()
