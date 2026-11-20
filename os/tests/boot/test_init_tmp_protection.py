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
    def test_init_sets_the_fs_protections(self):
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            for d in ("kernel", "vm", "fs"):
                (init.proc / "sys" / d).mkdir(parents=True)
            init.run("set_kernel_params")
            got = {k: (init.proc / "sys" / k).read_text().strip() for k in WANT}
        self.assertEqual(got, WANT)

    def test_a_key_the_kernel_lacks_does_not_stop_the_rest(self):
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            (init.proc / "sys/fs").mkdir(parents=True)  # no kernel/ or vm/: those writes fail
            done = init.run("set_kernel_params; echo done")
            self.assertEqual(done.stdout, "done\n")
            self.assertEqual((init.proc / "sys/fs/protected_fifos").read_text(), "2\n")


if __name__ == "__main__":
    unittest.main()
