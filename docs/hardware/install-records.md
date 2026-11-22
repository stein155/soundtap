# Install records

The dated runs, per-route notes and developer recovery tools behind the
[install guide](../owners/install.md). Owners follow the guide; this page says what has run on which
speaker, with which build and when. A record here is a historical report, not approval of a
later candidate: release approval is the per-model [hardware checklist](hardware-test.md).

## Contents

1. [Install records per model](#install-records-per-model)
2. [Route notes](#route-notes)
3. [SoundTouch 300](#soundtouch-300)
4. [Going back to Bose](#going-back-to-bose)
5. [Developer recovery tools](#developer-recovery-tools)

## Install records per model

Back to the guide: [Before you start](../owners/install.md#before-you-start).

Older Bose software works too: the oldest tested is 20.0.6 (Wireless Link adapter) and 22.0.3 (SoundTouch 10). You do not have to update to 27.0.6 first.

| Model | Update server | Upload to port 17008 |
| --- | --- | --- |
| **SoundTouch 20** | 2026-11-11, Bose 27.0.6 → SoundTap: API answered after 336 s; its BCO app was then replaced and Wi-Fi/Ethernet returned. No audio test. 2026-11-14, again from 27.0.6: after 534 s, Wi-Fi taken over from Bose's software | 2026-10-17: installed from Bose's software 27.0.6 over its setup network (`Bose SoundTouch Wi-Fi Network`, upload to `192.168.1.1:17008`, HTTP 200 after 196 s), then booted SoundTap with our bootloaders |
| **Wireless Link adapter** | 2026-11-06: installed from Bose's software 27.0.6, also from GitHub over HTTPS. 2026-11-11: again, started from the browser installer in Chrome, and with the Wi-Fi taken over from Bose's software. 2026-11-14, from Bose's software 20.0.6: Wi-Fi taken over 2026-11-17: put back on Bose's 27.0.6 from SoundTap's firmware page (up in 50 s), then back to SoundTap through the update feed in 233 s, with its name, presets, volume and Wi-Fi kept | 2026-10-23: installed from Bose's software 27.0.6 over its setup network (`Bose ST WLA (…)`, upload to `192.0.2.1:17008`, HTTP 200 after 202 s), then rebooted into SoundTap by itself 46 s later with its settings intact |
| **SoundTouch 10** | 2026-11-12, from Bose's software 27.0.6: SoundTap answered after 262 s, Wi-Fi taken over from Bose's profile. 2026-11-14, from Bose's software 22.0.3: after 259 s, Wi-Fi taken over. 2026-11-17, back to Bose 27.0.6 through the Updates page (unsigned Bose package) and returned through the feed: after 336 s, name, presets, volume and Wi-Fi kept | 2026-10-23: installed from Bose's software 27.0.6 over its setup network (`Bose ST 10 (…)`, upload to `192.0.2.1:17008`, HTTP 200 after 211 s), then rebooted into SoundTap by itself 44 s later with its settings intact |
| **SoundTouch 300** | 2026-11-15: installed from Bose's software, started from the browser installer, with the Wi-Fi taken over from Bose's software; the same day back to Bose through the Updates page and returned through the feed ([checklist](hardware-test.md#release-checklist-per-model)) | 2026-11-06, one bar: see [SoundTouch 300](#soundtouch-300) |

## Route notes

Back to the guide: [the three routes](../owners/install.md#route-1-the-installer).

- **Update server.** Recorded first installs took about four to six minutes to reach
  SoundTap. The ST20 then replaces its BCO network app and restarts again. The update server
  starts answering with the first public release. It has so far been proven on ST10, ST20,
  Wireless Link and ST300 with test feeds.
- **Packages.** The first public release (`os-v0.1.0`) is still going through its per-model
  hardware gates, including the ST300. Until it is out, packages are development candidates
  for testers.
- **Upload to port 17008.** A first request has sometimes returned HTTP 500. This can also
  indicate a real failure: wait for any ongoing install to finish before retrying; do not
  submit another package while the speaker is still writing or restarting. A cold start has
  recovered a known stall on the first restart after an install, but elapsed time alone does
  not establish that writes have finished. If it has not returned after ten minutes and you
  cannot tell whether it is still installing, keep it powered and use the support route.
- **The same over USB.** On the SoundTouch 10 the exact micro-USB service connector is still
  being confirmed. The route has not been tested on Windows yet; reports are welcome.
- **USB stick.** Only the gesture itself has been seen working (SoundTouch 10, 2026-11-08,
  without a stick); an install from a stick is unproven. This route has not yet been proven
  as a first install from Bose's software.
- **Wi-Fi takeover.** A first install attempts to take over the saved Wi-Fi. Historical runs
  on [ST10, ST20 and Wireless Link](hardware-test.md#open-hardware-checks) recorded this
  working; they are not a guarantee for every firmware version or network. ST10/WLA can import
  WPA or open profiles; the ST20's own BCO station supports WPA2-PSK only (2.4 GHz). The setup
  network is proven on the SoundTouch 10, 20 and Wireless Link (see the table above). The
  SoundTouch 20's restart onto your Wi-Fi was seen on 2026-11-14 after an install from Bose's
  software 27.0.6.
- **Updates and rollback.** After an update the new version has to run for a minute and then
  answer its local API check before the speaker confirms it. This does not test audio or
  Wi-Fi. If the boot remains unconfirmed, the speaker returns to the previous one after four
  unconfirmed restarts, so up to three power cuts during those first minutes do not undo a
  good update (proven on the SoundTouch 10, 20 and Wireless Link with the earlier limit of
  three restarts; four is not proven on a speaker yet, nor the SoundTouch 300 at all;
  [evidence](hardware-test.md#release-checklist-per-model)).
  Developer runs behind the design of [ADR 0010](../decisions/0010-ab-slots-boot-confirm-rollback.md):
  - Wireless Link, 2026-10-08, at the limit of that time, 3: the environment was set to a broken
    `bootcmd` (Ping kernel with `init=/bin/false`, `panic=10`), `altbootcmd=run nand_boot;`,
    `bootlimit=3`, SCRATCH2 cleared. Three broken boots followed; the fourth ran `altbootcmd` and
    came up with `init=/init` after 105 s, SCRATCH2 reading `0x040001B0` (count 4). soundtapd,
    hot-swapped in, logged "booted by fallback; selecting the running slot" and "boot confirmed"
    about 60 s after its start, cleared SCRATCH2 and armed `altbootcmd=run nand_boot2;` with
    `bootlimit=3` for the other slot. The environment was then restored byte for byte from the
    backup taken before the test.
  - Wireless Link, 2026-10-27: a cold reset or power cut clears RTC SCRATCH2, so U-Boot's RTC
    boot count alone starts over after each one.
  - Offline: Debian's libubootenv 0.3.5 against a copy of the WLA factory dump; a script set and
    deleted as intended, and a broken CRC made both tools exit 243 with the file untouched.
- **Buttons.** Browser pairing and its old Preset 6 chord have been removed.

## SoundTouch 300

Back to the guide: [SoundTouch 300](../owners/install.md#soundtouch-300).

The SoundTouch 300 takes the same package as the other three and installs the same way, from
Bose's software through the browser installer (2026-11-15). It is supported like them; what has
run on it:

- Recorded on the bar: the install, the way back to Bose and again to SoundTap, updates over
  the web UI, radio, Spotify, TV input, volume/mute, Wi-Fi lamp and presets, tone from the app,
  picking HDMI or TV from the app, playing as a DLNA renderer, multiroom follower and
  Bluetooth (2026-11-17), and by the owner's report ADAPTiQ (2026-11-20) and AirPlay
  (2026-11-22). Multiroom leader and rollback still need recorded runs.
- After the install the bar takes over the Wi-Fi Bose's software was on (2026-11-15).
- The soundbar processor link ([ADR 0109](../decisions/0109-st300-soundbar-processor-link.md)): a
  capture of stock on 2026-11-06 recorded the processor's health requests every 5 s (capture
  summary only). An earlier one-second estimate was never verified.
- The rear Ethernet: on 2026-11-16 `eth0` took a DHCP lease and carried traffic on the bar, and
  soundtapd sent the processor net 4. The lamp going off and the return to Wi-Fi after pulling
  the cable are not yet recorded.
- The TV input's return ([ADR 0116](../decisions/0116-st300-tv-input-share.md)): measured on the bar
  on 2026-11-08, with no processor opcode sent, McASP0's serializer 0 (AXR0) received I2S from
  the processor on its own bit and frame clocks while the bar was awake: two 32-bit slots,
  left-aligned samples, 44100 Hz (44239 frames/s against the speaker's uptime over 15 s),
  carrying the TV's audio at full scale and zeros when the TV was quiet.
- SoundTap's own bootloader replaces Bose's, as on the other models. Its unconfirmed-boot
  counter selects the other slot after the boot limit is exceeded (ADR 0010). This is an
  implemented fallback, not a recorded ST300 recovery pass. The other slot's contents and
  shared bootloaders determine what can boot; do not assume it will return to Bose.
- If the bar does not return, keep it powered while installation or automatic fallback may
  still be in progress. Open an
  [install or recovery issue](https://github.com/stein155/soundtap/issues/new?template=install.yml)
  with the last response and elapsed time before attempting another recovery route.

## Going back to Bose

Back to the guide: [Going back to Bose](../owners/install.md#going-back-to-bose).

- **From running SoundTap:** `POST /api/firmware?install=1` accepts a model-compatible original
  Bose `Update.stu` on ST10, ST20, Wireless Link and ST300. It writes the inactive kernel/rootfs
  and restores the **shared** SPL/U-Boot when different. Bootloaders are outside the two slots;
  a previous rootfs does not protect a failed bootloader restore. Returns are recorded on the
  WLA and ST10 (2026-11-17), ST20 (2026-11-11) and ST300 (2026-11-15)
  ([checklist](hardware-test.md#release-checklist-per-model)); they do not approve another
  candidate.
  On the Updates page: choose Bose's `Update.stu` on its own (no `.sig`), confirm the return to
  Bose and press a button on the speaker. From the command line this is
  `X-SoundTap-Confirm: stock` without `X-SoundTap-Signature`; the confirmation value `install` is
  refused for Bose's firmware ([ADR 0048](../decisions/0048-signed-updates.md)). A file that
  contains any SoundTap module is refused before anything is written. This is not a complete
  factory NAND restore.
- **ST20 additionally:** the return writes only the SCM, the main processor. The network
  processor (BCO) keeps SoundTap's app: `/api/firmware` does not flash BCO payloads, and
  whether Bose's software puts its own BCO app back by itself is unproven. The round
  trip of 2026-11-11 (to Bose and back through the update feed) does not settle that.
  Restoring Bose's BCO app by hand is a development step with the `bcoflash` tool; see
  [Developer recovery tools](#developer-recovery-tools).
- **ST300 additionally:** the installer accepts the stock package but skips its soundbar
  processor and bass-link payloads (an offline installer test covers that), so the processor
  keeps the firmware it has. The round trip ran on one bar (2026-11-15). Do not
  treat it as a full soundbar factory restore.
- **When SoundTap does not start but its SPL/U-Boot still work:** our bootloader implements
  USB-stick recovery. Prove it on the exact model and candidate before relying on it. Put an
  `Update.stu` and its `Update.stu.sig` (the release's `.sig`, signed for the installed
  image's profile) on a FAT USB stick, plug it into the micro-B port through an OTG adapter, and
  hold **Volume − and Preset 4** (ST10, ST20) or **both buttons** (Wireless Link) while
  powering on. ST300's remote-triggered code path has no completed recovery run and is not
  an approved recovery guarantee. This route reads a compatible SoundTap package's `soundtap-rootfs`, including
  its `/boot` files, into the other slot and selects it after verification. It writes nothing
  unless the `.sig` verifies against a release key and names the file's SHA-256 (ADR 0048),
  so it does not install a stock package, and it does not replace SPL/U-Boot. Stock ST10/ST20 USB recovery is a different
  implementation; stock Wireless Link has no USB-stick update gesture.
- **Slot limits:** a normal update preserves the running kernel/rootfs, but bootloaders and
  environment are shared. After another update both slots may hold SoundTap. A/B rollback
  alone does not restore the stock boot chain or the ST20's stock BCO app.

Proving each of these from the release candidate is part of the [road to 1.0](../owners/roadmap.md).

## Developer recovery tools

Back to the guide: [Going back to Bose](../owners/install.md#going-back-to-bose).

- **If SPL/U-Boot are damaged:** the gesture cannot run. Development recovery is the board's
  mask ROM over USB with `tools/recovery/rom-usb-restore.py`, below, and that board's verified
  NAND backup. It is not an owner-ready recovery guarantee for any candidate.
- **ST20 BCO app:** restoring Bose's BCO app by hand is a development step with the `bcoflash`
  tool, before the stock SCM boot. Plan and verify independent SCM access first: replacing our
  BCO app removes SoundTap's normal LAN path. See [BCO recovery limits](st20/bco-recovery.md).
- Every write, reset and power cycle follows the rules in [the contract](../../CONTRIBUTING.md#the-contract).

### Repairing the boot chain over ROM USB

`tools/recovery/rom-usb-restore.py` brings a board up from its mask ROM over USB and writes the
release's boot chain the way the installers do ([ADR 0009](../decisions/0009-mainline-linux-own-bootloaders.md#repairing-a-damaged-boot-chain-over-rom-usb)):
the spare U-Boot in `NAND.U-Boot` blocks 8-13, SPL copies 1, 2, 3 and 0, then the first U-Boot
copy in blocks 0-7. Block 14 (the redundant environment) and `NAND.U-Boot-Env` stay as they
are unless `--erase-env` is given. The release's images come from a built `output/os/images`
(`--images`, checked against its `SHA256SUMS`) or from the release's STU (`--stu`).

```sh
tools/recovery/rom-usb-restore.py --model st10 --images output/os/images --dry-run
tools/recovery/rom-usb-restore.py --model wla --stu SoundTap-Core-X.Y.Z.stu \
    --backup ~/nand-backups/wla-binky-20260903 --partition NAND.Kernel-Pong --dry-run
```

- `--dry-run` prints every erase and write with its flash range, the blob and the build it
  would make, and touches nothing; `--json` prints the same plan as JSON.
- `--backup DIR --partition NAME` also restores partitions from this board's own
  `nand-backup-ssh.sh` backup, in place, in sessions of at most 16 MiB. Anything stock comes from
  that backup and nowhere else ([ADR 0049](../decisions/0049-no-stock-bytes.md)). With `--backup-only`
  the release's chain is not written and the backup's own `NAND.SPL*` and `NAND.U-Boot` may go
  back, which together with the other partitions is the way back to the board's own NAND.
- Each session builds a RAM U-Boot bound to its blob (Docker), checks the loader's inputs
  without USB, asks for `write` to be typed, then runs the loader. `--power-cycle CMD` runs
  `CMD off` and `CMD on` around the loader start; without it, power the board on when told.
- The run stops before the first erase when a block it would program is marked bad, and after
  the first block that does not read back. It ends with `RU-complete` and a reset. A run whose
  loader did not see that marker is unconfirmed, not done: read the ranges back
  (`tools/recovery/rom-usb-read.sh`) before trusting them.
- `rom-usb-read.sh <name> <offset> <length> <new-dir>` reads one range of at most 16 MiB and
  checks it against the CRC the board prints; it never writes NAND. It serves the sm2 SPL, so it
  reads a WLA (and, unproven, an ST10), not an ST20. `ROM_USB_POWER_CYCLE` takes the same command
  as `--power-cycle`, and `ROM_USB_PROTECTED_BACKUP` names a directory, such as the board's
  backup, that it refuses to write into. Its header lists every setting.

Every model is open in the [hardware checklist](hardware-test.md#open-hardware-checks) row
"ROM-USB restore of the release's boot chain".

#### SoundTouch 10

Its ROM tries USB before NAND (SYSBOOT 01011b, measured 2026-09-19): a plain cable to the micro-B
port, no OTG adapter, and power on ([ST10 notes](st10/usb-recovery-and-rom.md)). The ROM gets
the RAM SPL of the tool's U-Boot build, the WLA's sm2 SPL. Unproven: anything past the ROM's
enumeration on an ST10.

#### SoundTouch 20

Its ROM boots NAND first; the busy pad held low at power-on sends it to USB
([ST20 ROM-USB recovery](st20/rom-usb-recovery.md#forcing-the-rom-to-usb0)). The ROM gets the
release's own ST20 SPL, which asks USB first and takes the tool's RAM U-Boot. Unproven: that
SPL served by this loader, and the writes.

#### Wireless Link adapter

Same straps as the ST10: the ROM takes USB first. The loader and the RAM U-Boot path were
developed on this board ([loader notes](../../tools/recovery/rom-usb-loader/README.md)); the restore
script itself is unproven.

#### SoundTouch 300

Takes the sm2 SPL like the ST10, but nobody has measured how its ROM reaches USB. Establish that,
and a way back, before using the tool on a bar.
