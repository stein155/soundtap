# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Tests for kernel_config_check, the hardening check of the kernel configuration."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "support"))
from kernel_config_check import DENY, NONZERO, REQUIRE, problems

ROOT = Path(__file__).resolve().parents[3]


class KernelConfigCheckTest(unittest.TestCase):
    """The kernel fragment and a built configuration meet the hardening rules."""

    def test_fragment_is_hardened(self) -> None:
        """The image's kernel fragment passes the check."""
        text = (ROOT / "os/board/image/linux.fragment").read_text()
        self.assertEqual(problems(text, fragment=True), [])

    def test_built_config_catches_reenabled_and_missing(self) -> None:
        """A built configuration with a denied option on or a required one missing fails."""
        good = "\n".join(
            [f"# CONFIG_{n} is not set" for n in DENY]
            + [f"CONFIG_{n}=y" for n in REQUIRE]
            + [f"CONFIG_{n}=5" for n in NONZERO],
        )
        self.assertEqual(problems(good, fragment=False), [])
        bad = good.replace("# CONFIG_KPROBES is not set", "CONFIG_KPROBES=y").replace("CONFIG_STRICT_DEVMEM=y", "")
        self.assertEqual(problems(bad, fragment=False), ["CONFIG_KPROBES must be off", "CONFIG_STRICT_DEVMEM must be y"])

    def test_unused_usb_driver_back_as_module_fails(self) -> None:
        """An unused USB driver built as a module fails, and a fragment must turn it off explicitly."""
        good = "\n".join([f"CONFIG_{n}=y" for n in REQUIRE] + [f"CONFIG_{n}=5" for n in NONZERO])
        self.assertEqual(problems(good, fragment=False), [])
        self.assertEqual(
            problems(good + "\nCONFIG_USB_NET_AX8817X=m", fragment=False),
            ["CONFIG_USB_NET_AX8817X must be off"],
        )
        # A fragment has to say so: a missing line leaves omap2plus's or Kconfig's default.
        self.assertIn("CONFIG_USB_HID must be off", problems(good, fragment=True))

    def test_ipv6_comes_with_its_filter_and_no_tunnels(self) -> None:
        """IPv6 needs the inet table that filters it, and a SIT tunnel fails (ADR 0134)."""
        good = "\n".join([f"CONFIG_{n}=y" for n in REQUIRE] + [f"CONFIG_{n}=5" for n in NONZERO])
        self.assertEqual(
            problems(good.replace("CONFIG_NF_TABLES_INET=y", ""), fragment=False),
            ["CONFIG_NF_TABLES_INET must be y"],
        )
        self.assertEqual(
            problems(good.replace("CONFIG_IPV6=y", "# CONFIG_IPV6 is not set"), fragment=False),
            ["CONFIG_IPV6 must be y"],
        )
        self.assertEqual(problems(good + "\nCONFIG_IPV6_SIT=y", fragment=False), ["CONFIG_IPV6_SIT must be off"])

    def test_oops_panics_and_a_panic_resets(self) -> None:
        """An oops must panic and a panic must reset after a nonzero timeout."""
        base = [f"CONFIG_{n}=y" for n in REQUIRE if n != "PANIC_ON_OOPS"]
        self.assertEqual(
            problems("\n".join([*base, "CONFIG_PANIC_TIMEOUT=5"]), fragment=False),
            ["CONFIG_PANIC_ON_OOPS must be y"],
        )
        base.append("CONFIG_PANIC_ON_OOPS=y")
        for timeout in ("CONFIG_PANIC_TIMEOUT=0", "CONFIG_PANIC_TIMEOUT=", ""):
            with self.subTest(timeout=timeout):
                self.assertEqual(
                    problems("\n".join([*base, timeout]), fragment=False),
                    ["CONFIG_PANIC_TIMEOUT must be set and not 0"],
                )
        # Below 0 resets at once, which is a reset too.
        self.assertEqual(problems("\n".join([*base, "CONFIG_PANIC_TIMEOUT=-1"]), fragment=False), [])


if __name__ == "__main__":
    unittest.main()
