# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every CVE os/ignore-cves.mk ignores has a reason that ignore_cves.py can read.

The checkable reasons hold: on the real tree, and on small fake trees that break one rule each.
"""

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
    """ignore_cves.py reads every reason and holds the checkable ones."""

    def check(self, mk: str, fragment: str = FRAGMENT, defconfig: str = DEFCONFIG,
              kernel_config: str | None = None, files: dict[str, str] | None = None) -> list[str]:
        """Write a fake tree with mk as os/ignore-cves.mk and return the problems found in it."""
        root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        for name, text in {"os/ignore-cves.mk": mk, "os/configs/soundtap_defconfig": defconfig,
                           "os/board/image/linux.fragment": fragment, **(files or {})}.items():
            (root / name).parent.mkdir(parents=True, exist_ok=True)
            (root / name).write_text(text)
        return ignore_cves.problems(root, kernel_config)

    def test_the_repository_gives_every_entry_a_reason_that_holds(self) -> None:
        """Every entry in the repository has a reason that holds."""
        self.assertEqual(ignore_cves.problems(ROOT), [])

    def test_the_repository_ignores_only_cves_with_one_per_line(self) -> None:
        """The repository's entries name one CVE per line, each with a reason."""
        entries = [line for line in (ROOT / "os/ignore-cves.mk").read_text().splitlines()
                   if "_IGNORE_CVES +=" in line]
        self.assertGreater(len(entries), 100)
        for line in entries:
            self.assertRegex(line, r"^\w+_IGNORE_CVES \+= CVE-\d{4}-\d+ # \S")

    def test_an_entry_without_a_reason_fails(self) -> None:
        """An entry without a reason fails."""
        self.assertEqual(self.check("LINUX_IGNORE_CVES += CVE-2026-1000\n"),
                         ["os/ignore-cves.mk:1: CVE-2026-1000 has no reason"])

    def test_an_unknown_reason_or_two_cves_on_a_line_fail(self) -> None:
        """A reason of no known form and two CVEs on one line both fail."""
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # not relevant\n"
                           "LIBPLIST_IGNORE_CVES += CVE-2017-5835 CVE-2017-5836 # fixed before 2.0.0\n")
        self.assertEqual(len(found), 2, found)
        self.assertIn("none of the forms", found[0])
        self.assertIn("one CVE per line", found[1])

    def test_an_ffmpeg_filter_reason_must_name_a_filter_we_leave_out(self) -> None:
        """An FFmpeg filter reason must name a filter our FFmpeg leaves out, for FFmpeg only."""
        mk = ("SOUNDTAP_FFMPEG_FILTERS = aresample volume\n"
              "SOUNDTAP_FFMPEG_CONF_OPTS = --disable-everything\n")
        files = {"os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk": mk}
        self.assertEqual(self.check("SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1001 # ffmpeg filters zmq, azmq not built\n",
                                    files=files), [])
        found = self.check("SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1002 # ffmpeg filter volume not built\n"
                           "LIBGLIB2_IGNORE_CVES += CVE-2026-1003 # ffmpeg filter zmq not built\n", files=files)
        self.assertEqual(len(found), 2, found)
        self.assertIn("builds the volume filter", found[0])
        self.assertIn("soundtap-ffmpeg only", found[1])

    def test_an_ffmpeg_encoder_or_decoder_reason_reads_that_kind_s_list(self) -> None:
        """An FFmpeg encoder or decoder reason is checked against that kind's list."""
        mk = ("SOUNDTAP_FFMPEG_ENCODERS = pcm_s16le\n"
              "SOUNDTAP_FFMPEG_DECODERS = aac mp3\n"
              "SOUNDTAP_FFMPEG_CONF_OPTS = --disable-everything\n")
        files = {"os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk": mk}
        self.assertEqual(self.check("SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1001 # ffmpeg encoder exr not built\n"
                                    "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1002 # ffmpeg decoders h264, hevc not built\n",
                                    files=files), [])
        found = self.check("SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1003 # ffmpeg decoder mp3 not built\n"
                           "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1004 # ffmpeg encoder pcm_s16le not built\n",
                           files=files)
        self.assertEqual(len(found), 2, found)
        self.assertIn("builds the mp3 decoder", found[0])
        self.assertIn("builds the pcm_s16le encoder", found[1])

    def test_an_ffmpeg_muxer_demuxer_or_protocol_reason_reads_that_kind_s_list(self) -> None:
        """An FFmpeg muxer, demuxer or protocol reason is checked against that kind's list."""
        mk = ("SOUNDTAP_FFMPEG_MUXERS = pcm_s16le\n"
              "SOUNDTAP_FFMPEG_DEMUXERS = wav pcm_s16le\n"
              "SOUNDTAP_FFMPEG_PROTOCOLS = http tcp\n"
              "SOUNDTAP_FFMPEG_CONF_OPTS = --disable-everything\n")
        files = {"os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk": mk}
        self.assertEqual(self.check("SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1001 # ffmpeg muxers wav, w64 not built\n"
                                    "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1002 # ffmpeg demuxer imf not built\n"
                                    "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1003 # ffmpeg protocol rtmp not built\n",
                                    files=files), [])
        found = self.check("SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1004 # ffmpeg demuxer wav not built\n"
                           "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1005 # ffmpeg protocol tcp not built\n"
                           "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-1006 # ffmpeg filter zmq not built\n",
                           files=files)
        self.assertEqual(len(found), 3, found)
        self.assertIn("builds the wav demuxer", found[0])
        self.assertIn("builds the tcp protocol", found[1])
        self.assertIn("lists no filters", found[2])

    def test_a_6_12_fix_must_be_in_the_pinned_kernel(self) -> None:
        """A 6.12 fix must be in the pinned kernel version."""
        self.assertEqual(self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # fixed in 6.12.117\n"), [])
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # fixed in 6.12.118\n")
        self.assertEqual(len(found), 1)
        self.assertIn("after the pinned 6.12.117", found[0])

    def test_a_kernel_off_6_12_wants_every_entry_checked_again(self) -> None:
        """A kernel that is not 6.12 is reported so every entry gets checked again."""
        found = self.check("", defconfig='BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="6.18.3"\n')
        self.assertEqual(len(found), 1)
        self.assertIn("not 6.12", found[0])

    def test_a_bug_must_come_after_6_12(self) -> None:
        """A bug said to come in later must come in after 6.12."""
        self.assertEqual(self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # bug came in 6.14\n"), [])
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # bug came in 6.6\n")
        self.assertIn("which the pinned kernel includes", found[0])

    def test_a_symbol_said_off_must_be_off(self) -> None:
        """A symbol said off must be off in the fragment and in the built kernel."""
        mk = "LINUX_IGNORE_CVES += CVE-2026-1000 # net/sctp: CONFIG_IP_SCTP off\n"
        self.assertEqual(self.check(mk), [])
        found = self.check(mk, fragment="CONFIG_IP_SCTP=m\n")
        self.assertEqual(found, ["os/ignore-cves.mk:1: CVE-2026-1000: linux.fragment sets CONFIG_IP_SCTP=m"])
        found = self.check(mk, kernel_config="CONFIG_IP_SCTP=y\n")
        self.assertEqual(found, ["os/ignore-cves.mk:1: CVE-2026-1000: the built kernel has CONFIG_IP_SCTP=y"])

    def test_a_module_must_not_be_built_in(self) -> None:
        """A symbol said to be a module must not be built in."""
        mk = "LINUX_IGNORE_CVES += CVE-2026-1000 # x_tables: CONFIG_NETFILTER_XTABLES=m\n"
        self.assertEqual(self.check(mk, kernel_config="CONFIG_NETFILTER_XTABLES=m\n"), [])
        found = self.check(mk, kernel_config="CONFIG_NETFILTER_XTABLES=y\n")
        self.assertIn("the built kernel has CONFIG_NETFILTER_XTABLES=y", found[0])

    def test_our_patch_must_name_the_cve(self) -> None:
        """A fixed-by-our-patch reason needs a patch of ours that names the CVE."""
        mk = "SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-107696 # fixed by our patch\n"
        patch = {"os/package/soundtap-ffmpeg/0001-fix.patch": "[for CVE-2026-107696]\n"}
        self.assertEqual(self.check(mk, files=patch), [])
        found = self.check(mk, files={"os/package/soundtap-ffmpeg/0001-fix.patch": "other\n"})
        self.assertIn("no patch of ours for soundtap_ffmpeg names it", found[0])

    def test_code_not_built_for_linux_is_for_packages_other_than_the_kernel(self) -> None:
        """A not-built-for-Linux reason holds for other packages but not for the kernel."""
        mk = "LIBGLIB2_IGNORE_CVES += CVE-2025-4056 # glib/gspawn-win32.c, Windows only: not built for Linux\n"
        self.assertEqual(self.check(mk), [])
        found = self.check("LINUX_IGNORE_CVES += CVE-2026-1000 # arch/x86: not built for Linux\n")
        self.assertEqual(found, ["os/ignore-cves.mk:1: CVE-2026-1000: code the kernel does not build "
                                 "names its symbol, 'CONFIG_X off'"])

    def test_a_duplicate_fails(self) -> None:
        """A CVE listed twice fails."""
        line = "LINUX_IGNORE_CVES += CVE-2026-1000 # fixed in 6.12.1\n"
        self.assertIn("listed twice", self.check(line * 2)[0])


if __name__ == "__main__":
    unittest.main()
