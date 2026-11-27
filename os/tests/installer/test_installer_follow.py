# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""After the install starts, install.sh and install.ps1 find a speaker that came back at another address.

They look for it by the deviceID stock reported. The scripts' own functions run against a
stubbed curl or HTTP client; no LAN access.
"""
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
INSTALLER = ROOT / "docs/installer"

# What SoundTap's /api/info answers, at the address the speaker moved to.
MOVED_TO = "192.168.7.42"
INFO = '{"deviceId":"020000A1B230","id":"SERIAL0000000000A","ip":"192.168.7.42","model":"SoundTouch 20","name":"Kitchen"}'
OTHER = '{"deviceId":"020000C3D450","id":"020000C3D450","ip":"192.168.7.9","model":"SoundTouch 10","name":"Hall"}'

STUB_CURL = f"""#!/bin/sh
# The last argument is the URL; only two addresses run SoundTap.
for url; do :; done
case "$url" in
  http://{MOVED_TO}/api/info) printf '%s' '{INFO}' ;;
  http://192.168.7.9/api/info) printf '%s' '{OTHER}' ;;
  *) exit 7 ;;
esac
"""


class InstallShFollowTest(unittest.TestCase):
    """install.sh's find_moved."""

    @staticmethod
    def find_moved(shell: str, device_id: str) -> str:
        """Run find_moved under ``shell`` with the stub curl first on PATH; return what it prints."""
        with tempfile.TemporaryDirectory() as tmp:
            curl = Path(tmp) / "curl"
            curl.write_text(STUB_CURL)
            curl.chmod(curl.stat().st_mode | stat.S_IEXEC)
            script = f'. docs/installer/install.sh\nfind_moved "{device_id}" "192.168.7"\n'
            out = subprocess.run([shell, "-c", script], cwd=ROOT, text=True, capture_output=True,
                                 check=True, timeout=120,
                                 env={**os.environ, "PATH": tmp + os.pathsep + os.environ["PATH"],
                                      "SOUNDTAP_INSTALLER_FUNCTIONS": "1"})
            return out.stdout.strip()

    def test_finds_the_speaker_by_its_device_id(self) -> None:
        """The speaker with the remembered deviceID is found at its new address, and no other."""
        for shell in ("sh", "dash"):  # macOS sh is bash; Debian sh is dash
            if not shutil.which(shell):
                continue
            with self.subTest(shell=shell):
                self.assertEqual(self.find_moved(shell, "020000A1B230"), MOVED_TO)
                # Stock and SoundTap may write the MAC in another case.
                self.assertEqual(self.find_moved(shell, "020000a1b230"), MOVED_TO)
                # Another SoundTap speaker on the network is not the one installed.
                self.assertEqual(self.find_moved(shell, "020000FFFFFF"), "")


@unittest.skipUnless(shutil.which("pwsh"), "install.ps1 runs under PowerShell")
class InstallPs1FollowTest(unittest.TestCase):
    """install.ps1's Find-Moved."""

    def test_finds_the_speaker_by_its_device_id(self) -> None:
        """The speaker with the remembered deviceID is found at its new address, and no other."""
        # The script runs inside & { }; dot-sourcing the same block keeps its functions.
        source = (INSTALLER / "install.ps1").read_text().replace("\n& {\n", "\n. {\n", 1)
        info, other = INFO.replace("'", "''"), OTHER.replace("'", "''")
        program = source + f"""
# A stand-in for HttpClient: GetStringAsync answers from a table, or fails as a dead address.
$fake = New-Object psobject
$fake | Add-Member ScriptMethod GetStringAsync {{
    param($url)
    $answers = @{{ 'http://{MOVED_TO}/api/info' = '{info}'; 'http://192.168.7.9/api/info' = '{other}' }}
    if ($answers.ContainsKey($url)) {{ return [System.Threading.Tasks.Task]::FromResult([string]$answers[$url]) }}
    [System.Threading.Tasks.Task]::FromException[string]([System.Net.Http.HttpRequestException]::new('no answer'))
}}
foreach ($id in '020000A1B230', '020000a1b230', '020000FFFFFF') {{
    Write-Output ("" + (Find-Moved $fake $id @('192.168.7')))
}}
"""
        out = subprocess.run(["pwsh", "-NoProfile", "-NonInteractive", "-Command", program],
                             cwd=ROOT, text=True, capture_output=True, check=False, timeout=120,
                             env={**os.environ, "SOUNDTAP_INSTALLER_FUNCTIONS": "1"})
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.assertEqual(out.stdout.splitlines(), [MOVED_TO, MOVED_TO, ""])


if __name__ == "__main__":
    unittest.main()
