# The ST20's hardware contract

Code that drives the SoundTouch 20 (`spotty` / `scm`) keeps to these facts about its main CPU,
the SCM. They come from the stock device tree and drivers and from measurements on the speaker;
the reasons sit in the code named in each row. [The contract](../../../CONTRIBUTING.md#st20-spotty--scm)
points here and holds the BCO rules. Nothing here follows from the ST10: the ST10 has its own
[hardware contract](../st10/hardware-contract.md), and an ST10 measurement is no ST20 proof.

## Display

- The OLED is driven from userspace through `/dev/spidev1.0`, SPI mode 3 at 2 MHz, as a
  128x100 grayscale panel (`app/oled/spidev_linux.go`).
- A frame is 6400 bytes, packed two pixels a byte (the even pixel in the low nibble). Chip
  enable stays asserted for a whole transfer while it is split into writes of at most 4096
  bytes: a full frame is 4096 + 2304.
- A full frame took about 207 ms on the speaker, most of it the 10 ms line settling around
  each transfer. soundtapd sends only the rectangle that changed since the last frame: the
  column window (`0x15`, first and last column in units of two pixels, so the window starts at
  an even pixel and ends at an odd one) and the row window (`0x75`, first and last row) in one
  command transfer, then that window's bytes row by row. The first frame after the init sends
  the whole panel and is followed by display on (`0xaf`), which then stays on.
- A fade of the whole panel is its contrast (`0x81`, one level byte; the init sets `0x85`,
  full brightness), not a redraw (`app/oled/spidev.go`, `drawSteps`).
- U-Boot paints the first boot frame, soundtapd's `bootFrame(0)` (the empty bar outline),
  with the same init sequence ([ADR 0131](../../decisions/0131-st20-boot-frame.md)). The
  panel's reset (gpio2_18) and chip enable (gpio2_24) sit on GPIO2, which the device tree keeps
  out of ti-sysc's soft reset at boot (`ti,no-reset-on-init`): reset, the lines became inputs
  about 0.7 s into boot and the panel went dark until soundtapd opened it.
- Mainline ST20 has no `/dev/fb0`; the framebuffer experiments of the stock kernel are
  superseded.

## Codec and amplifier

- The AIC3256 sits on SPI0 chip select 1. On mainline McSPI leaves the codec's MISO stuck
  high, so the SPI0 pads run as GPIOs and `aic3256-bitbang` talks to the codec
  (`os/board/common/dts/ti/omap/am335x-soundtap-st20.dts`, `os/drivers/aic3256-bitbang.c`).
- Safe mute is gpio1_17 low. The amplifier is enabled with gpio1_23 high, and only once the
  codec is configured; its active-low fault line is gpio1_24 (`os/overlay/etc/init.d/soundtap`,
  `app/internal/hw/amp.go`). Stock numbers these 49, 55 and 56.
- The codec configuration is the speaker's own `spotty_scm_normal.cfg`, kept in
  `/mnt/nv/soundtap/codec/` ([ADR 0012](../../decisions/0012-single-dac-writer.md)). It is never
  shipped. The proven load is 10,534 register writes, about 5 seconds over the GPIO bit-bang.

## Keypad

- The keypad is scanned from userspace like the ST10's, on gpio0 throughout: strobes
  gpio0_28, gpio0_20 and gpio0_17, inputs gpio0_22, gpio0_23, gpio0_26 and gpio0_29. A strobe
  is asserted low and released to input (`app/internal/hw/keypad.go`).

## Network, as stock left it

These describe Bose's BCO firmware, which SoundTap does not run
([ADR 0031](../../decisions/0031-bco-network-card.md)); they matter when reading stock evidence or
during a stock restore.

- Stock BCO firmware owns LAN, Wi-Fi and NAT, with the SCM behind it on `192.168.1.101` and the
  BCO on `192.168.1.100`.
- It does not forward multicast; its `RemoteOnlyPorts` setting controls which inbound unicast
  ports reach the SCM. SoundTap's BCO app passes Ethernet frames without NAT or a port filter, so
  these limits do not apply under SoundTap; discovery still needs proof on the ST20 itself.
- The rear SETUP A micro-USB port is a separate USB network link: the SCM is `203.0.113.1/30`,
  the host `.2` ([installing over USB](../../owners/install.md)).

## Observations behind audio choices

Bench observations on one ST20 that [ADR 0012](../../decisions/0012-single-dac-writer.md) and
[ADR 0041](../../decisions/0041-radio-one-ffmpeg-one-url-rule.md) rest on. They are reports, not
release approval for a candidate.

- Master volume in the PCM: the ALSA ring alone held 14020 frames (299 ms at 46875 Hz), with
  the pipe and a 200 ms glide on top, before a volume step was heard. At the lowest steps the
  codec got a 24-bit word carrying a signal near -82 dBFS, and by ear the speaker sounded
  slightly crackly there. What the DSP does to a signal that small is not measurable on this
  board, because the codec's output is not readable.
- Internet radio on 2026-10-26, before the read-ahead: the Wi-Fi sometimes stalled for 15 s or
  more. ffmpeg logged `Will reconnect ... error=Operation timed out`, the Mixer underran and the
  radio went silent. On the reconnect, icecast sent its burst-on-connect (about 600 KB, roughly
  25 s of audio), which ffmpeg decoded from the start, so pieces already played were repeated.
  About 10 MB of RAM was free.
