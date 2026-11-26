# The ST10's hardware contract

Code that drives the SoundTouch 10 (`rhino` / `sm2`) keeps to these facts. They were read from
the stock device tree and drivers and measured on the speaker; the reasons sit in the code
named in each row. [The contract](../../../CONTRIBUTING.md#st10-rhino--sm2) points here, so a change that
breaks one of them breaks the contract. Nothing here is ST20 proof: the ST20 has its own
[hardware contract](../st20/hardware-contract.md). The remote control's receiver is in
[the remote-control page](remote-control.md#the-infrared-contract).

## Wi-Fi

- The radio is a TI WiLink 8 (wl18xx, WL1835). Station and scans run on `wlan0`; the setup
  access point, named after the speaker (`SoundTap-Setup` when the name does not fit an SSID),
  runs on `wlan1`. soundtapd's Wi-Fi manager brings up both (`app/internal/wifi`).

## Audio

- Under stock Linux the audio link is raw S16_LE, 48 kHz stereo written to
  `/dev/snd/snd_shelby` (`app/internal/hw/model/model.go`). Under mainline it is the ALSA card
  of SoundTap's McASP machine driver (`os/drivers/soundtap-st10-audio.c`), S16 stereo at the one rate
  that driver's clock makes, 46875 Hz; the init script hands it to soundtapd as
  `AUDIO_DEV=alsa:plughw:0,0`. soundtapd is the only writer to the DAC
  ([ADR 0012](../../decisions/0012-single-dac-writer.md)).
- The codec configuration is the speaker's own `rhino_sm2_normal.cfg`, kept in
  `/mnt/nv/soundtap/codec/` (ADR 0012). It is never shipped in the repository or the STU.
- GPIO72 is the amplifier mute and GPIO75 the amplifier power, in Bose's flat GPIO numbering
  (bank × 32 + line: gpio2_8 and gpio2_11). On mainline the Linux number is the bank's chip
  base plus the line, looked up by the bank's address, never the flat number
  (`os/overlay/etc/init.d/soundtap`).

## Lamps and keypad

- Stock exposes the lamps as `/dev/shelby-leds/uledy` and `uledw` (and the `rled` channels)
  and the keypad as `/dev/shelby-keypad`. Mainline has neither: the lamps are `gpio-leds` in
  `/sys/class/leds` (`os/board/common/dts/ti/omap/am335x-soundtap-st10.dts`, whose labels stay
  in step with `ledChannels()` in `app/internal/hw/led.go`), and soundtapd scans the keypad
  itself.
- The mainline keypad is polled from userspace, never interrupt-driven
  (`app/internal/hw/keypad.go`). The matrix strobes are gpio1_28, gpio1_12 and
  gpio1_13; the inputs are gpio0_23, gpio0_26, gpio0_22 and gpio0_27, active low. The scancode
  is `strobe * 4 + input`; the directly wired key, read without a strobe, is scancode 12.
- GPIO0 shares its interrupt bank with the WL1835's own interrupt (gpio0_31). Interrupts on
  the floating switch-in lines stormed the radio's interrupt line and kept Wi-Fi down, which
  is why nothing requests button interrupts on bank 0.

## Console

- The recovery console is UART3 at 115200 baud: `ttyS3` under the mainline kernel, `ttyO3`
  under stock 3.14 (`console=ttyS3,115200n8` in the device tree's bootargs).
