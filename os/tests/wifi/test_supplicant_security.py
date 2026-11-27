# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""wpa_supplicant is built for every security SoundTap joins (ADR 0136).

soundtapd's Wi-Fi manager writes key_mgmt "WPA-PSK WPA-PSK-SHA256 SAE" for a saved password on
every model, which a wpa_supplicant without CONFIG_SAE refuses: the runtime selects Buildroot's
WPA3 option. hostapd needs none: the setup network is open.

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

    def test_wpa3_is_built(self) -> None:
        """The runtime selects wpa_supplicant's WPA3 option, and not hostapd's."""
        cfg = (ROOT / "os/package/soundtap-runtime/Config.in").read_text()
        self.assertRegex(cfg, r"\n\tselect BR2_PACKAGE_WPA_SUPPLICANT_WPA3\n")
        self.assertNotIn("BR2_PACKAGE_HOSTAPD_WPA3", cfg)


if __name__ == "__main__":
    unittest.main()
