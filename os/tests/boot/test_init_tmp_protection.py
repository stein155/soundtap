# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Root must not follow or write into what another user planted in /tmp.

soundtapd's Wi-Fi manager writes the hostapd, udhcpd and wpa_supplicant configs into /tmp as
root, and the media daemons' users can write there too. Runs the init script's set_kernel_params against a
temp /proc/sys.
"""
import tempfile
import unittest

from boot.init_script import InitScript

WANT = {"fs/protected_symlinks": "1", "fs/protected_hardlinks": "1",
        "fs/protected_regular": "2", "fs/protected_fifos": "2",
        "kernel/panic": "5", "vm/panic_on_oom": "2"}


class TmpProtection(unittest.TestCase):
    """set_kernel_params against a temp /proc/sys."""

    def test_init_sets_the_fs_protections(self) -> None:
        """Init sets the /tmp protections and the panic settings."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            for d in ("kernel", "vm", "fs"):
                (init.proc / "sys" / d).mkdir(parents=True)
            init.run("set_kernel_params")
            got = {k: (init.proc / "sys" / k).read_text().strip() for k in WANT}
        self.assertEqual(got, WANT)

    def test_a_key_the_kernel_lacks_does_not_stop_the_rest(self) -> None:
        """A key the kernel lacks does not stop the other writes."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            (init.proc / "sys/fs").mkdir(parents=True)  # no kernel/ or vm/: those writes fail
            done = init.run("set_kernel_params; echo done")
            self.assertEqual(done.stdout, "done\n")
            self.assertEqual((init.proc / "sys/fs/protected_fifos").read_text(), "2\n")


class Ipv6TemporaryAddresses(unittest.TestCase):
    """set_kernel_params prefers temporary IPv6 addresses for connections out (ADR 0134)."""

    def test_every_interface_and_the_default_prefer_temporary_addresses(self) -> None:
        """all, default and every interface already there get use_tempaddr 2."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            conf = init.proc / "sys/net/ipv6/conf"
            for name in ("all", "default", "lo", "wlan0"):
                (conf / name).mkdir(parents=True)
                (conf / name / "use_tempaddr").write_text("0\n")
            init.run("set_kernel_params")
            got = {name: (conf / name / "use_tempaddr").read_text().strip() for name in ("all", "default", "lo", "wlan0")}
        self.assertEqual(got, dict.fromkeys(("all", "default", "lo", "wlan0"), "2"))

    def test_a_kernel_without_ipv6_is_left_alone(self) -> None:
        """Without /proc/sys/net/ipv6 (a stock kernel) nothing is written and the rest goes on."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            (init.proc / "sys/fs").mkdir(parents=True)
            done = init.run("set_kernel_params; echo done")
            self.assertEqual(done.stdout, "done\n")
            self.assertFalse((init.proc / "sys/net").exists())


if __name__ == "__main__":
    unittest.main()
