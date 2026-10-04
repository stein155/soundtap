# 0109. The ST300: the Wireless Link's module, and soundtapd speaks to the soundbar processor

Status: accepted — 2026-11-07

## Context

The SoundTouch 300 is a soundbar. Its network module is the same sm2 board as the Wireless Link
(variant `ginger`). Everything else belongs to a second processor in the bar, the LPM: the DSP,
HDMI and TV control, the bass module and rear speakers, ADAPTiQ, the buttons, the remote's IR and
the lamps. The module talks to the LPM over UART0. On stock that is the job of `A4VServer`, which
is Bose code and cannot ship ([ADR 0049](0049-no-stock-bytes.md)).

The requirement: the universal STU installs on the ST300, and the bar, the bass module and the TV
keep working as on stock. What the link does is read from stock `A4VServer`'s behaviour and from
the running bar, never copied from its code: 921600 baud raw, fixed 128-byte frames with an XOR
checksum.

## Decision

1. **The ST300 takes the Wireless Link's kernel and boot chain** with its own device tree
   (`ginger_sm2.dtb`). The installer and soundtapd map `ginger` to the model `st300` and boot it
   with the `ginger` recipes.
2. **U-Boot does not listen on UART0.** On the ST300 that UART is the LPM link, and a byte from
   the processor during `bootdelay` would stop autoboot. The environment carries `bootdelay=-2`,
   written by both the on-box installer and soundtapd.
3. **soundtapd speaks the LPM protocol itself** (`app/internal/lpm`, on `/dev/ttyS0`):
   - It answers the processor's echo and health requests, as stock does.
   - The processor owns the volume. The remote and the TV change it there and soundtapd follows
     the reports; soundtapd sends its own changes as stock does (0..100, 0 while muted).
   - A source picked on the bar, its remote or the TV is followed and acknowledged as stock does.
     soundtapd selects the module's input when it starts playing.
   - Keys and preset requests from the bar act like the other models' buttons and presets.
   - It reads the speaker list (bass module, rears) and the tone levels, and can set the tone.
   - soundtapd remembers the last source itself, because the processor reports a source only
     when it changes, and keeps playing while the module restarts.
   - The radio status (op `0x15`) carries the network value the bar's Wi-Fi lamp shows: 0 none,
     1 setup network, 2 connected, 3 connecting, as stock reports them, and 4 while the bar runs
     on its rear Ethernet, which turns the lamp off as stock does on a cable. Wired means `eth0`
     has carrier and an IPv4 address; a cable still waiting for its lease, or one pulled out,
     reports the Wi-Fi state. The status goes out when it changes, when the processor restarts,
     and once a minute unchanged.
4. **Standby is a source**, as on stock: SoundTap's standby selects source 0, waking selects the
   module's input again. Going idle does not, because that must not switch off a bar that plays
   the TV.

Alternatives that lost:

- **Running stock `A4VServer`.** Bose code; not allowed in the image.
- **Treating the ST300 as a Wireless Link and leaving the link alone.** The bar shows its error
  lights after a minute, and the remote, TV volume and source changes never reach SoundTap.
- **A kernel driver for the link.** The protocol is small and stateful in ways soundtapd already
  models (volume, source, presets); a driver would move that logic into C for no gain.

## Consequences

- The bar keeps its own audio path: TV, HDMI, the bass module and the rears work without
  SoundTap in the signal path.
- `app/internal/lpm` is the only ST300-specific runtime code. The rest of soundtapd treats the
  ST300 as a model with an external volume owner.
- The rear Ethernet is the AM335x CPSW switch (`ti_cpsw_new`, `davinci_mdio`, `phy-gmii-sel`),
  built as modules that the init script loads on the ST300 only.
- A serial console on the ST300 is not available from U-Boot. Recovery goes through the same
  routes as the other models.
- CEC and HDMI selection have code paths; auto power-down and screensaver reports are read, with
  no setting exposed. ADAPTiQ and tone frames are implemented but never run on the bench bar, and
  implementation is not evidence that calibration works. op `0x08` is never sent: it reboots the
  processor.
- Do not derive a fixed link timeout from an estimate; the processor's health cadence is recorded
  in the [install records](../hardware/install-records.md#soundtouch-300). Tests on the bar follow
  [the ST300 candidate validation](../hardware/hardware-test.md#soundtouch-300-candidate-validation).

## Code

- `app/internal/lpm/lpm.go`: the frames and checksum, `lpm.Link.Run` and the session that answers
  health requests and follows volume, source and keys.
- `app/internal/lpm/frames.go`: `lpmRadioStatusFrame` and the radio status loop (op `0x15`).
- `app/internal/lpm/state.go`: `lpm.New`, `lpm.Link`, the saved input, tone, HDMI/TV choice and
  standby as a source (`lpmPowerSource`).
- `app/cmd/soundtapd/home.go`: starts the link on the ST300.
- `app/internal/flash/recipes.go:InstallMainlineRecipes` and `os/installer/module/installer.c`:
  `bootdelay=-2` for `ginger`.
- `os/board/common/dts/ti/omap/am335x-soundtap-st300.dts`: the ST300's device tree.
- `os/overlay/etc/init.d/soundtap`: loads the rear Ethernet modules on the ST300.
