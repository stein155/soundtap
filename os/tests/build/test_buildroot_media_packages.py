# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Check audio-only FFmpeg (AirPlay and radio) and AirPlay 2 package wiring without a build."""

import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
FFMPEG = {
    "SOUNDTAP_FFMPEG_ENCODERS = pcm_s16le",
    "SOUNDTAP_FFMPEG_DECODERS = aac aac_latm aac_fixed alac mp3 flac vorbis opus pcm_s16le pcm_s16be pcm_s24le wmav1 wmav2",
    "SOUNDTAP_FFMPEG_MUXERS = pcm_s16le",
    "SOUNDTAP_FFMPEG_DEMUXERS = aac asf mov mp3 flac ogg wav pcm_s16le pcm_s16be hls mpegts",
    "SOUNDTAP_FFMPEG_PARSERS = aac aac_latm mpegaudio flac vorbis opus",
    "SOUNDTAP_FFMPEG_PROTOCOLS = file http https pipe tcp tls udp crypto",
    "SOUNDTAP_FFMPEG_FILTERS = aresample volume bass aformat afade",
    "\t--disable-ffprobe \\",
    "\t--disable-avdevice \\",
    "\t--disable-swscale \\",
    "\t--enable-small \\",
    "\t--disable-everything \\",
}


def require(path: Path, entries: set[str]) -> None:
    """Exit with the lines of entries that path does not contain."""
    missing = entries - set(path.read_text().splitlines())
    if missing:
        msg = f"{path.relative_to(ROOT)} missing: {', '.join(sorted(missing))}"
        raise SystemExit(msg)


def main() -> None:
    """Check the FFmpeg, shairport-sync and nqptp package wiring."""
    require(ROOT / "os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk", FFMPEG)
    shairport = ROOT / "os/package/soundtap-shairport-sync"
    require(shairport / "Config.in", {"\tselect BR2_PACKAGE_SOUNDTAP_NQPTP"})
    require(shairport / "soundtap-shairport-sync.mk", {
        "\tsoundtap-nqptp \\",
        "\t--with-metadata-pipe \\",
    })
    if "\t--with-pipe \\" in (shairport / "soundtap-shairport-sync.mk").read_text().splitlines():
        msg = "soundtap-shairport-sync.mk uses unsupported --with-pipe"
        raise SystemExit(msg)


class Check(unittest.TestCase):
    """The media package wiring check passes on this tree."""

    def test_check(self) -> None:
        """FFmpeg is audio-only and shairport-sync builds against nqptp with a metadata pipe."""
        main()


if __name__ == "__main__":
    unittest.main()
