# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""wpa_supplicant is built for every security SoundTap joins (ADR 0136).

wpa_supplicant 2.12 leaves WEP out unless its configure step is asked to uncomment CONFIG_WEP,
which Buildroot does for each entry of WPA_SUPPLICANT_CONFIG_ENABLE. Without it soundtapd's
wep_key0 for a WEP network is refused on every model, the ST20 included (its driver gets the key
from wpa_supplicant's connect).
"""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


class SupplicantSecurity(unittest.TestCase):
    """The external tree asks Buildroot's wpa_supplicant for WEP, with the runtime."""

    def test_wep_is_built(self) -> None:
        """external.mk adds CONFIG_WEP to the entries the configure step enables."""
        ext = (ROOT / "os/external.mk").read_text()
        blocks = re.findall(r"ifeq \(\$\(BR2_PACKAGE_SOUNDTAP_RUNTIME\),y\)\n(.*?)\nendif", ext, re.DOTALL)
        self.assertTrue(any("WPA_SUPPLICANT_CONFIG_ENABLE += CONFIG_WEP" in b for b in blocks))


if __name__ == "__main__":
    unittest.main()
