# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The speaker tells phones it is a loudspeaker.

With the class left at 0x040000 (Audio/Video, no minor class) iOS took the speaker for a
headphone and sent the stream about 22 dB down, so Bluetooth sounded silent on every model
(2026-11-16). bluetoothd 5.79 reads Class from [General] in main.conf with strtol base 0 and
keeps only the major and minor bits: major = (class & 0x1f00) >> 8, minor = (class & 0xfc) >> 2
(src/main.c parse_config_hex, src/adapter.c).
"""
import configparser
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
MAIN_CONF = ROOT / "os/overlay/etc/bluetooth/main.conf"

MAJOR_AUDIO_VIDEO = 0x04
MINOR_LOUDSPEAKER = 0x05


class DeviceClassTest(unittest.TestCase):
    def test_class_is_audio_video_loudspeaker(self):
        conf = configparser.ConfigParser()
        conf.read_string(MAIN_CONF.read_text())
        cls = int(conf["General"]["Class"], 0)
        self.assertEqual((cls & 0x1F00) >> 8, MAJOR_AUDIO_VIDEO, hex(cls))
        self.assertEqual((cls & 0xFC) >> 2, MINOR_LOUDSPEAKER, hex(cls))


if __name__ == "__main__":
    unittest.main()
