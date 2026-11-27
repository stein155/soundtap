# Compatibility matrix

What works on which speaker, and how certain that is. One universal STU installs on the ST10, the
ST20, the Wireless Link and the SoundTouch 300 ([install records](../hardware/install-records.md)).
It also carries [previews](#previews) for other SoundTouch models, none of them ever run on hardware.
SoundTap Core 0.1.0 is out as an alpha; no candidate is release-approved yet. The API contract begins at public 0.1.0;
its stable/internal split, deprecation and maintenance rules are in
[ADR 0108](../decisions/0108-http-api-compatibility-for-1-0.md).

A feature's status describes evidence, not release approval:

- **Observed**: reported on this model's hardware. The note or evidence column names the
  observation; older entries without a linked run are historical reports, not proof for the
  current candidate.
- **Built**: implemented, without a complete hardware pass for the feature. A partial bench
  observation can be recorded in the note.
- **No**: the hardware is absent or the feature is intentionally unsupported; see the note.

Every model in this table is **Supported**: it is part of the first release, and a release is
published only after it has passed that model's column of the
[release checklist](../hardware/hardware-test.md#release-checklist-per-model). No candidate has passed it
yet, so no release of SoundTap is approved for any model; the alphas come before that test. An
open row there blocks a release, not the model.

## Models

|                         | SoundTouch 10 | SoundTouch 20                 | Wireless Link | SoundTouch 300                          |
| ----------------------- | ------------- | ----------------------------- | ------------- | --------------------------------------- |
| Board / variant         | `rhino_sm2`   | `spotty_scm`                  | `binky`       | `ginger`                                |
| CPUs                    | one (AM335x)  | two: AM335x + BCO front CPU   | one (AM335x)  | two: AM335x module + soundbar processor |
| LAN reaches SoundTap on | `:80`         | `:80` (own BCO app, ADR 0001) | `:80`         | `:80`                                   |
| Overall status          | Supported     | Supported                     | Supported     | Supported                               |

The Wireless Link runs the same features as the ST10 except where it has no hardware for them:
no loudspeaker, and two buttons instead of a keypad. It has a 3.5 mm AUX in and a 3.5 mm
[analog line out](../hardware/wla/bluetooth-and-analog-output.md); S/PDIF on that output jack is not built.

The hardware checklist has
[WLA](../hardware/hardware-test.md#wireless-link-candidate-validation) and
[ST300](../hardware/hardware-test.md#soundtouch-300-candidate-validation) sections, but a section
is not a completed run: WLA and ST300 features stay Built until a feature-specific observation
exists. What has run on the SoundTouch 300 is in its
[install records](../hardware/install-records.md#soundtouch-300).

## Previews

The package carries these boards, worked out from what the stock packages show
([ADR 0133](../decisions/0133-more-soundtouch-models-from-stock.md)). **None has been run on
hardware**: every feature is Built at most, and none is Supported until its column of the
checklist has passed. Their recovery gestures are implemented in SoundTap's U-Boot and unproven: an
implemented route is not a proven one. The installers offer a preview only when its owner picks
it by name and confirms it as one ([installing](install.md#1-which-speaker-you-have)); they do
not offer the Lifestyle 600/650 module, which has no way back, nor a Lifestyle's newer (SM2)
adapter, which they cannot tell apart from that module. A preview installs only from the
release's preview package, `SoundTap-Core-X.Y.Z-preview.stu`, which the installers fetch through
the preview feed once its owner confirmed it.

| Model | Variant | Runs as | Recovery | Page |
| --- | --- | --- | --- | --- |
| SoundTouch 30 Series I/II | `mojo` / scm | ST20 | implemented, unproven: Volume Down + Preset 4 (SoundTap's U-Boot) | [st20](../hardware/st20/README.md) |
| SoundTouch 20 Series III | `spotty3` / sm2 | ST10, with the panel and the rear Ethernet port | implemented, unproven: Volume Down + Preset 4 | [hardware](../hardware/README.md) |
| SoundTouch 30 Series III | `mojo3` / sm2 | as the ST20 Series III | implemented, unproven: Volume Down + Preset 4 | [hardware](../hardware/README.md) |
| SoundTouch Portable | `taigan` / scm | ST20, with its battery on `/api/health` | implemented, unproven: Volume Down + Preset 4 | [portable](../hardware/portable/README.md) |
| SoundTouch SA-5 | `burns` / sm2 | ST10, with its amplifier controller and Ethernet | implemented, unproven: its one button at power-on (SoundTap's U-Boot) | [sa5](../hardware/sa5/README.md) |
| Lifestyle 600/650 module | `bardeen` / sm2 | ST300 | none, and none in stock either: not offered by the installers | [ls650](../hardware/ls650/README.md) |
| SoundTouch adapter: SA-4, Stereo JC, Wave, CineMate, Lifestyle 135–535 and VideoWave (on a Lifestyle the installers offer the scm adapter only) | `lisa` / scm, `lisa2` / sm2 | ST20 (scm) or ST10 (sm2) board, master of the Bose link to the host | implemented, unproven: its one button at power-on (SoundTap's U-Boot) | [lisa](../hardware/lisa/README.md) |

## Features

Each Observed cell has an evidence entry for that model. **Missing run** marks a historical
report whose run is not in the repository's documentation; do not cite it as verified hardware
support. Linked summaries and excerpts say which build, hash or capture they lack. Evidence from
a stock path does not carry over to the current boot chain, and no hardware was retested to
fill this table. The [source register](../../os/stock/PROVENANCE.md#reverse-engineering-source-register)
tracks the inputs and gaps behind the model notes.

| Feature | ST10 | ST20 | WLA | ST300 | Note | Evidence by model |
| ------------------------------------------------ | ------- | ------- | ----- | ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | --- |
| Web UI + JSON API | Observed | Observed | Built | Built | ST20 remains experimental overall | ST10: Missing run; ST20: [LAN/API response, 2026-10-06](../hardware/st20/bco-network-card.md#lan-and-api-response) (historical summary; build/capture missing, not every route) |
| TuneIn playback | Observed | Observed | Built | Built | Historical ST20 report covers raw PCM/TuneIn on an earlier chain; current-candidate output needs a new recorded pass. | ST10: Missing run; ST20: [2026-08 RAM/USB observation](../hardware/hardware-test.md#st20-candidate-validation) (old boot chain; no current-candidate run) |
| Spotify Connect (librespot) | Observed | Built | Built | Built |  | ST10: Missing run |
| AirPlay 2 (shairport-sync) | Observed | Built | Built | Built | SoundTap's BCO app is a network card; stock BCO AirPlay is not a SoundTap service | ST10: Missing run |
| Bluetooth A2DP sink | Observed | Built | Built | Built | ST20 Series I/II have no Bluetooth; Series III is offline-covered only. ST300: picked over the processor link; the checklist records a pass on 2026-11-17 without a candidate hash. | ST10: Missing run |
| Bluetooth AVRCP skip (`NEXT_TRACK`/`PREV_TRACK`) | Built | Built | Built | Built | Needs a phone on hardware to confirm. One phone plays at a time, as on stock | — |
| Multiroom (Snapcast) | Observed | Built | Built | Built | ST300: the TV input can be shared with a group | ST10: [2026-10-10 follower measurement](../hardware/hardware-test.md#multiroom-measurements) (60 s with ST20 leader; build/hash missing); ST20: same linked run as leader, not a complete feature pass |
| UPnP/DLNA renderer | Observed | Built | Built | Built |  | ST10: [Music Assistant 2.6.3 playback checks](music-assistant.md#recorded-music-assistant-checks) (summary only; date/build/capture missing) |
| UPnP ContentDirectory browse | Observed | Built | Built | Built | Browse and play from external media servers; ST20 hardware unconfirmed | ST10: Missing run |
| UPnP presets | Observed | Built | Built | Built | Source/type/location persisted; survive reboot | ST10: Missing run |
| Recently played | Observed | Built | Built | Built | Up to 50 entries; TuneIn and known Spotify URIs can replay. Other identified playback may be listed without a replay location; Bluetooth is excluded. | ST10: Missing run |
| Apple Home via AirPlay 2 | Observed | Built | Built | Built | No fake HomeKit speaker implementation | ST10: Missing run |
| MQTT | Observed | Built | Built | Built |  | ST10: Missing run |
| Front-panel keypad | Observed | Built | No | No | WLA: two buttons, no keypad. ST300: the bar has no buttons; its remote's keys arrive over the processor link | ST10: Missing run |
| Status LEDs | Observed | Built | Built | Built | ST20 Wi-Fi PWM lamps are implemented alongside the OLED; ST300 sends network status to its lamp-owning processor. These are code paths, not new hardware passes. | ST10: Missing run; WLA: [2026-09-19, sysfs sample](../hardware/wla/led-behaviour.md#what-soundtap-does-with-it) (partial: colour labels unconfirmed) |
| OLED standby screen (clock) | No | Observed | No | No | Userspace `/dev/spidev1.0`; no `/dev/fb0`. Clock seen on the panel (2026-11-20, then still with a weather line, since removed); no recorded report yet. | ST20: Missing run |
| IR remote | Observed | Observed | No | Built | ST10 and ST20: rc-core `/dev/lirc0`; the linked run covers preset, power and volume. ST300: through the processor. | ST10: [2026-09-23, key log](../hardware/st10/remote-control.md#proven-on-hardware-2026-09-23) (excerpt only; candidate hash missing); ST20: [2026-10-10](roadmap.md#done), confirmed on the bench 2026-11-20 (build/capture missing) |
| Wired Ethernet | No | Built | No | Built | ST20: the BCO's port, DHCP at boot and after a BCO reset, seen on hardware 2026-11-10. ST20 Series III (preview): its rear port is enabled in the device tree as stock wires it, untested; nothing on that board has been seen. ST300: the CPSW rear port, with the wired state reported to the bar's processor (ADR 0109); a DHCP lease and traffic over the cable were seen on the bar 2026-11-16, cable pull and the lamp not yet | ST20: 2026-11-10 hardware report (DHCP at boot and after BCO reflash; exact build/hash missing, cable replug untested) |
| Wi-Fi setup AP | Observed | Built | Built | Built | ST20: the AP runs on SoundTap's own BCO app, driven by hostapd as on the ST10 (ADR 0031). With SoundTap's own BCO core it never went on air (the core has no beacon timer); the app now sends the beacons itself, not yet seen on a phone, and the earlier observation does not cover this core | ST10: Missing run; ST20: Missing run |
| Backup / restore / factory reset | Observed | Built | Built | Built |  | ST10: Missing run |
| Local password on admin routes | No | No | No | No | The LAN is trusted: no password or household token (ADR 0055) | — |
| Firmware update from the web UI | Observed | Observed | Built | Built | One universal STU through `/api/firmware`, written to the inactive slot and booted from it | ST10: Missing run; ST20: Missing run |
| SoundTap USB-stick recovery | Built | Built | Built | Built | SoundTap's U-Boot path; stock ST20 chord evidence does not prove it. Exact-candidate recovery remains a separate gate (ADR 0009). | — |

## SoundTouch 300 only

| Feature                                           | ST300 | Note                                                                        |
| ------------------------------------------------- | ----- | --------------------------------------------------------------------------- |
| TV input                                          | Built | Picked over the processor link, like HDMI and Bluetooth                     |
| Lip-sync, audio mode, CEC mode, HDMI input button | Built | Read from and written to the processor as stock does                        |
| Bass module and volume                            | Built | The processor applies the volume, as stock's S/PDIF path                    |
| ADAPTiQ, tone controls                            | Built | Built offline; never run on a bar (project rule)                            |
| Auto power down, screensaver                      | No    | The processor reports them; no setting yet                                  |

## Firmware versions

|                                           | Supported                                                            |
| ----------------------------------------- | -------------------------------------------------------------------- |
| Stock Bose ST10                           | First-install source and recovery target; SoundTap replaces its boot chain |
| Stock Bose ST20 (BCO `3.x-3_8_0-9423_ER`) | Starting point. SoundTap installs its own BCO app image (ADR 0031); BFL and BSL stay stock |
| SoundTap, current candidate                | One universal STU for the ST10, ST20, Wireless Link and ST300; exact-byte approval required |
| SoundTap, older release                    | Update forward; no downgrade path is tested                          |

## Rules this table is bound by

- New Observed entries need a linked run naming model, build, procedure and result. Static
  reverse engineering or an offline test is not a hardware observation.
- No release goes out while an ST20 check in the
  [hardware checklist](../hardware/hardware-test.md#st20-candidate-validation) is open.
- SPL/U-Boot, kernel and rootfs are in scope. Hardware writes still need explicit permission
  and a proven recovery plan; mtd0–mtd4 writes need fresh permission for each write on every model.
  Persistent NAND must never be formatted automatically.
