# 0116. The ST300 shares its TV input by capturing the processor's return on McASP0

Status: accepted — 2026-11-08

## Context

On the SoundTouch 300 the TV (HDMI ARC, optical, HDMI inputs) goes straight into the soundbar's
processor, the LPM ([ADR 0109](0109-st300-soundbar-processor-link.md)). The module that runs
SoundTap never sees that audio on its playback path, so a multiroom group led by the bar would
play silence on the followers while the TV plays. Stock rebroadcasts the TV to a zone, so the
processor hands the audio back: McASP0's serializer 0 (AXR0) receives I2S from the processor on
its own bit and frame clocks while the bar is awake, two 32-bit slots with left-aligned samples
at 44100 Hz, without any LPM command ([measurement](../hardware/install-records.md#soundtouch-300)).

## Decision

While the bar leads a group and plays the TV, soundtapd captures the processor's return and puts
it into the Snapcast tap ([ADR 0013](0013-multiroom-through-snapcast.md)) in place of the Mixer's
output.

- Kernel patch 0011 lets davinci-mcasp in DIT mode also register a capture stream when the device
  tree sets `ti,dit-rx-i2s`. Capture runs on the external clocks: I2S framing, active-low frame
  sync, one bit of delay, two slots. Only the ST300's tree sets it.
- The machine driver declares that capture as stereo S32_LE at 44100 Hz.
- soundtapd runs `arecord -D plughw:0,0` at S16_LE 48000, the same tool and route as the AUX
  capture, and writes it into the tap. It does this only while the bar is on, its source is the
  processor (TV or HDMI), and the tap is on (the bar leads a group). The bar keeps playing the TV
  through the processor.

The bar is not delayed to match the followers, as in stock's SYNC_TO_ROOM. Stock's SYNC_TO_ZONE,
which delays the bar by the rebroadcast latency (LPM `0x89`), is not built, because nobody has
asked for lip-synced zones yet.

Alternatives that lost: none recorded. Why: to be confirmed by the maintainer (were other ways to
get the TV audio considered, such as an LPM command?).

## Consequences

- Followers play the TV in the group, slightly behind the bar.
- The 44100 Hz rate is a measurement. If the processor ever switches rate with its input, the
  followers would drift in pitch; it must be measured again then.
- Another external process (`arecord`) runs while sharing, as for the AUX capture.

## Code

- `os/board/rootfs/linux-patches/0011-asoc-davinci-mcasp-dit-rx-i2s.patch`: the capture stream
  on a DIT McASP.
- `os/board/common/dts/ti/omap/am335x-soundtap-st300.dts`: sets `ti,dit-rx-i2s`.
- `os/drivers/soundtap-st10-audio.c`: the machine driver's S32_LE 44100 Hz capture.
- `app/cmd/soundtapd/tv_share.go`: `runTVShare` and `shareTV`.
