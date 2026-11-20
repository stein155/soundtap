# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The ROM-USB read script, run offline against stub commands. The restore is
tools/recovery/rom-usb-restore.py (test_rom_usb_restore.py)."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
RECOVERY = ROOT / "tools/recovery"

DUMP_DOCKER = r"""#!/bin/sh
printf '%s\n' "$@" > "$ARGV"
for arg; do case "$arg" in *:/dump) dir=${arg%:/dump};; esac; done
[ -z "${dir:-}" ] || {
  printf abc > "$dir/mtd7"
  crc=352441c2
  [ "${BAD_CRC:-}" != 1 ] || crc=352441c3
  c1=${crc%??????}
  rest=${crc#??}
  c2=${rest%????}
  rest=${rest#??}
  c3=${rest%??}
  c4=${rest#??}
  printf 'uboot: crc32 for 82000000 ... 82000002 ==> \nuboot: %.2s\nuboot: %.2s\nuboot: %.2s\nuboot: %.2s\nuboot: DP-mtd7-crc\n' \
    "$c1" "$c2" "$c3" "$c4"
}
"""

def stubs(directory, **scripts):
    directory.mkdir(parents=True, exist_ok=True)
    for name, body in scripts.items():
        path = directory / name
        path.write_text(body if body.startswith("#!") else "#!/bin/sh\n" + body + "\n")
        path.chmod(0o755)


class WlaRecoveryScripts(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(subprocess.run, ["rm", "-rf", str(self.tmp)])

    def run_sh(self, args, **env):
        full = {**os.environ, "PATH": f"{self.tmp}/bin:{os.environ['PATH']}", **env}
        return subprocess.run(args, env=full, capture_output=True, text=True, timeout=60)

    def test_fifo_read_write_fd(self):
        # The loader keeps a FIFO open read-write so a reader started first still gets the stream.
        script = """set -eu
fifo=$1/in; data=stream-before-writer
mkfifo "$fifo"
cat "$fifo" >"$1/out" & reader=$!
exec 3<>"$fifo"
sleep 1
printf %s "$data" >"$fifo"
exec 3>&-
wait "$reader"
test "$(cat "$1/out")" = "$data"
"""
        subprocess.run(["sh", "-c", script, "sh", str(self.tmp)], check=True, timeout=30)

    def dump_setup(self):
        t = self.tmp
        stubs(t / "bin", power="printf '%s\\n' \"$1\" >> \"$POWER_LOG\"",
              lsusb="printf '0451:6141\\n'", sleep=":", docker=DUMP_DOCKER,
              timeout='shift; exec "$@"')
        stubs(t / "repo/os/bootloader", **{"build-wla-uboot.sh": 'mkdir -p "$WLA_UBOOT_OUT"; '
              'printf mlo > "$WLA_UBOOT_OUT/MLO"; printf uboot > "$WLA_UBOOT_OUT/u-boot.img"'})
        stubs(t / "repo/tools/recovery/rom-usb-loader", **{"build.sh": "exit 0"})
        (t / "parent").mkdir()
        # No maintainer paths: HOME points at an empty directory.
        (t / "home").mkdir()
        return {"ARGV": str(t / "argv"), "ROM_USB_REPO": str(t / "repo"), "HOME": str(t / "home"),
                "POWER_LOG": str(t / "power.log")}

    def test_dump_one(self):
        t = self.tmp
        env = self.dump_setup()
        dump = RECOVERY / "rom-usb-read.sh"
        r = self.run_sh([str(dump), "mtd7", "0x780000", "0x3", str(t / "parent/dump")],
                        ROM_USB_POWER_CYCLE=f"{t}/bin/power", **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue((t / "parent/dump/mtd7").is_file())
        self.assertEqual((t / "power.log").read_text(), "off\non\n")
        r = self.run_sh([str(dump), "mtd7", "0x780000", "0x3", str(t / "parent/dump-bad")],
                        BAD_CRC="1", **env)
        self.assertNotEqual(r.returncode, 0, "wrong board CRC accepted")
        self.assertFalse((t / "parent/dump-bad").exists())

    def test_dump_one_without_power_command_asks_for_power(self):
        t = self.tmp
        env = self.dump_setup()
        r = self.run_sh([str(RECOVERY / "rom-usb-read.sh"), "mtd7", "0x780000", "0x3",
                         str(t / "parent/dump")], **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("Power the board on into ROM USB now", r.stderr)
        self.assertFalse((t / "power.log").exists())

    def test_dump_one_refuses_the_protected_backup(self):
        t = self.tmp
        env = self.dump_setup()
        backup = t / "parent/backup"
        backup.mkdir()
        dump = RECOVERY / "rom-usb-read.sh"
        r = self.run_sh([str(dump), "mtd7", "0x780000", "0x3", str(backup / "again")],
                        ROM_USB_PROTECTED_BACKUP=str(t / "parent/../parent/backup"), **env)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("refusing protected backup directory", r.stderr)
        self.assertFalse((backup / "again").exists())
        r = self.run_sh([str(dump), "mtd7", "0x780000", "0x3", str(t / "parent/next")],
                        ROM_USB_PROTECTED_BACKUP=str(backup), **env)
        self.assertEqual(r.returncode, 0, r.stderr)

if __name__ == "__main__":
    unittest.main()
