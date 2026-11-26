# The SoundTouch adapter (`lisa` / scm, `lisa2` / sm2)

Preview: SoundTap boots it from what the stock packages show, and nothing on this page was seen
on an adapter ([ADR 0133](../../decisions/0133-more-soundtouch-models-from-stock.md)). One board
serves many host products. It takes the product's name from the host it is cabled to:

| Host class | Products | How the adapter learns it |
| --- | --- | --- |
| ABLColin | SA-4 | Answers speaker-type query 0x10 with 3 |
| ABLLuke | SoundTouch Stereo JC | Answers query 0x10 with 0x11 |
| ABLNelson | Wave SoundTouch | Discovery id 0x30C0 / 0x30A0 |
| ABLTriode | CineMate, SoundTouch 120/130/220/520 | Discovery id 0x1C00 |
| ABLMarconi | Lifestyle 135/235/525/535, VideoWave (`corey`, `otto`) | Discovery id 0x1401 / 0x1801 |

**Names.** Stock calls the adapter `lisa` on both modules. Under SoundTap the SCM one stays
`lisa` (the ST20's board) and the SM2 one is `lisa2` (the ST10's module), as the Series III
are `spotty3` and `mojo3`.
- SoundTap's U-Boot knows the SCM adapter by the variant resistor (100–1024) and the SM2 adapter by the
  manufacturing name `lisa`.
- The installer maps stock's `lisa` on an SM2 to `lisa2`.

The stock `lisa_scm` tree is `spotty_scm` with five changes:
- UART0 is the plain host UART, not the IR receiver.
- The codec script is `lisa_scm_normal.cfg`.
- USB0 is peripheral only.
- `spidev@0` loses its status line but stays enabled.
- There is no sync-out.

So on the SCM the adapter has the ST20's BCO network, codec and panel nodes, no Bluetooth,
and the same host link. Its rootfs shares every binary with `lisa.sm2`; only the product XMLs
(`moduleType`, `hasBcoProcessor`, `hasAIC`) and the firmware files differ. Porting it is the
ST20 port plus the ABL stack.

## The stock packages

Seven packages carry the adapter, all at 27.0.6. Each has the common kernel, MLO and U-Boot.

| Package | Module | Rootfs | Host payload (never written by SoundTap) |
| --- | --- | --- | --- |
| SA-4 / Stereo JC, AirPlay | scm | AirPlay scm build | none |
| Wave SoundTouch, AirPlay | scm | the same AirPlay scm build | `ABL_Update`, `SEN_FW_update.bos` |
| CineMate, non-AirPlay | scm | non-AirPlay scm build | `ABL_Update`, `TRI_shelby_update.tri` |
| Lifestyle / VideoWave, non-AirPlay | scm | the same non-AirPlay scm build | `MarconiUpdate`, `DEF_CON_*.bos` |
| SA-4 / Stereo JC | sm2 | one sm2 build | none |
| CineMate | sm2 | the same sm2 build | `ABL_Update`, `TRI_shelby_update.tri` |
| Wave SoundTouch IV | sm2 | the same sm2 build | `ABL_Update`, `SEN_FW_update.bos` |
| Lifestyle / VideoWave | sm2 | the same sm2 build | `MarconiUpdate`, `DEF_CON_*.bos` |

- **SM2:** every adapter package carries the same rootfs, byte for byte, with every host
  profile in it. The product is decided at runtime by the host.
- **SCM:** two rootfs builds. The AirPlay and non-AirPlay ones differ only in:
  - the BCO firmware (`_NoAP` without AirPlay in the BCO);
  - `avahi-daemon` in Shepherd-core on the non-AirPlay build;
  - the update manifests.
- **The payloads update the host product, not the adapter:**
  - `ABL_Update` and `MarconiUpdate` are ARM updaters that run on the adapter.
  - The `.tri` is CineMate console firmware (STM32F1 with FreeRTOS, plus DSP images).
  - `DEF_CON_*.bos` is a SuperH ELF wrapping a squashfs, for the Lifestyle/VideoWave console.
  - `SEN_FW_update.bos` is a container of its own (magic `48 37 26 15`) for the Wave; its CPU
    is not identified.
  - Identical payloads are identical across packages.

## Hardware (stock `lisa_sm2`)

- No codec (`aic3256` disabled, `hasAIC=false`).
- Audio leaves as I2S at 46875 Hz to a converter on the adapter board: `dac_adc_reset` is
  gpio 74, `mute_out` gpio 72.
- UART0 is a plain UART to the host.
- USB is device only.
- Ethernet is on.

## The Bose link (stock abl.ko, ABLServer)

**The bus.** One wire, half duplex. Line discipline 29 on `/dev/ttyO0`, 19200 baud 8N2. The
bus echoes every byte.

- **Speed:** it moves to 230400 or 460800 when the host's capability flags have 0x800 or 0x400.
- **Framing:** frames are told apart by idle gaps. At least 1.4 ms of silence is a sync. The
  master slot opens 10 ms after sync, and the client answers 0.4 ms after a master frame.
- **Checksum:** a final byte makes the XOR of the frame zero.

**Roles.** The adapter is the active master in room A. It polls with `[00][F0|room][xor]`; the
host answers with `[80][zone<<4|room][mute<<7|vol][xor]`. The zone nibble says what the host
plays: 0 or 2 the adapter, 0xE its own source, 0xF off.

**Master commands:**

| Command | Meaning |
| --- | --- |
| 0x01 | On/off: 0x01 selects the adapter as input, 0x80 off |
| 0x02 | Attenuation |
| 0x0B | Query: 0x10 speaker type, 0x12 version, 0x13 serial |
| 0x0D | Key (0x4C power) |
| 0x10 | Power off after a delay |

**Volume, per host:**
- Nelson, Triode and Marconi: the volume 0–99.
- SA-4: 100 − max(vol, 6), with volume blocked for 8 s after it turns on.
- Stereo JC: 100 − vol.

**Datagrams.** A 6-byte header with a 16-bit Internet checksum, segments of up to 244 bytes.
Ports: 19 discovery, 18 remote control, 16 repository, 9 CLI. P2P frames must never be a
multiple of 14 bytes long; a host firmware crashes on them.

## What SoundTap does

**Trees.**
- `am335x-soundtap-lisa.dts` (`lisa2`, SM2): the ST10's tree with UART0 as the link and no IR.
  The converter is held enabled (stock's `DacAdcReset`, high), `mute_out` low as stock leaves
  it. It also has the rear Ethernet port and the Series III lamps.
- `am335x-soundtap-lisa-scm.dts` (`lisa`, SCM): the ST20's tree with UART0 as the link and no
  IR. It keeps the codec, with `lisa_scm_normal.cfg`.

**The link** (`app/internal/abl`, `cmd/soundtapd/adapter.go`). soundtapd is the bus master in
room A on `/dev/ttyS0` (`/dev/ttyS1` on the SCM adapter, where `ttyS0` is the BCO) at 19200 8N2, as stock's abl.ko is:
- It polls every room that answers, plus one silent room in turn.
- It reads its own echo back; a different echo is a collision.
- It sends its commands and its own datagrams.

**Finding the host.** soundtapd broadcasts discovery queries and its own announcement (device id
0x5000, `SOUNDTOUCH`).
- A Wave, CineMate or Lifestyle host is classified by its announcement.
- An SA-4 or Stereo JC is classified by its answer to the speaker-type query.
- SoundTap's announcement leaves out the speed flags (0x0E00), so a host keeps the link at
  19200, the speed this master is timed for. Stock moves to 230400 or 460800.

**Control.**
- **Playing:** while SoundTap plays, it selects the adapter on the host (command 0x01, 0x01).
  An SA-4 or Stereo JC that is off is first woken with its power key (0x4C).
- **Host turns away:** when the host goes to its own source or off while the adapter played,
  SoundTap goes to standby.
- **Volume and mute:** they go to the host (command 0x02, mapped per host class; mute as user
  mute 0xFD/0xFE). The host's own volume changes come back from its poll replies, with stock's
  600 ms echo guard. The SA-4's 8 s volume block after it turns on is kept. While a host is
  known, the PCM, and on the SCM the codec, stay at full scale.

**Remote control (Wave, CineMate).** Once such a host is found, SoundTap sends stock's
registration on port 18:
- the boot delay (13);
- the one state registration;
- the keys, then "registration done";
- on the SM2 also Bluetooth pairing (held 0.75 s) and clearing the pairings (held 10 s).

The keys are presets 1–6 (a press plays, held 1.5 s stores), next and previous, play,
pause, stop and play/pause. The host's remote keys then come back as commands and do the same
on SoundTap. When the host selects the adapter itself, SoundTap acknowledges as stock does and
plays the preset it names or wakes into the last source; a deactivation puts it in standby.
The SA-4, Stereo JC and Lifestyle/VideoWave get no registration, as on stock.

**Recovery.** Its one button held at power-on runs the USB-stick update in SoundTap's U-Boot, as stock's
does: gpio0_27 on the SM2, gpio0_29 on the SCM.

## Open on hardware

- The whole bus.
  - Timing from userspace: poll window, echo, reply windows.
  - That hosts answer the announcement without the speed flags.
  - That the host keeps 19200.
- That the converter plays with `mute_out` low.
- That a Wave and a CineMate accept the registration and send their keys.
- Not built:
  - preset names on the host display (RC type 0x0F);
  - the Wave's clock and alarm;
  - the CineMate's language setting;
  - the rating keys.

## Never

- Port 16, the repository: its staging messages update the host.
- `abl stage`.
- Master command 0x0F (ETAP test mode) or 0x21 (pedestal comms).
- Any host firmware: `ABL_Update`, `SEN_FW_update.bos`, `TRI_shelby_update.tri`, the Marconi
  update files.

