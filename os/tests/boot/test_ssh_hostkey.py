# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""ssh.sh must not keep a host key that dropbearkey never finished writing."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SSH = (ROOT / "os/overlay/opt/soundtap/ssh.sh").read_text()


def boot(nv: str, keygen: str) -> Path:
    """Run ssh.sh once against a temporary /mnt/nv with a stub dropbearkey."""
    bins = Path(nv, "bin")
    bins.mkdir(exist_ok=True)
    stubs = {
        "dropbearkey": f"#!/bin/sh\nf=$4\n{keygen}\n",
        "dropbear": '#!/bin/sh\n[ -s "$3" ] || { echo bad key >&2; exit 1; }\n',
        "mount": "#!/bin/sh\n",
    }
    for name, body in stubs.items():
        (bins / name).write_text(body)
        (bins / name).chmod(0o755)
    script = SSH.replace("/mnt/nv", nv).replace("/dev/pts", f"{nv}/pts")
    script = script.replace("LOG=/run/soundtap/log.fifo", f"LOG={nv}/log")
    env = dict(os.environ, PATH=f"{bins}:{os.environ['PATH']}")
    subprocess.run(["sh", "-c", script], env=env, capture_output=True, check=True)
    return Path(nv, "dropbear/host_ed25519")


class HostKey(unittest.TestCase):
    """ssh.sh's handling of the Dropbear host key."""

    def test_failed_keygen_is_retried_on_the_next_boot(self) -> None:
        """A key file dropbearkey left empty is made again on the next boot."""
        with tempfile.TemporaryDirectory() as nv:
            boot(nv, ': >"$f"; exit 1')  # dropbearkey dies after creating the file
            key = boot(nv, 'echo KEY >"$f"')  # next boot, it works
            self.assertEqual(key.read_text(), "KEY\n")

    def test_good_key_is_kept(self) -> None:
        """A good key is kept across boots."""
        with tempfile.TemporaryDirectory() as nv:
            boot(nv, 'echo FIRST >"$f"')
            key = boot(nv, 'echo SECOND >"$f"')
            self.assertEqual(key.read_text(), "FIRST\n")


if __name__ == "__main__":
    unittest.main()
