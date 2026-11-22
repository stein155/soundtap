# Production hardware test

Mandatory per-model spare-device checklist. QEMU cannot approve a release. Keep the model's
recovery console attached through first boot and test the exact SHA-256 artifact intended
for publication. On ST10 the console is UART3, `ttyS3` on mainline (`ttyO3` on stock).

## Session Authorization

This document is a plan, not authorization. Start each session read-only. Before each category,
obtain explicit permission for that exact category and record it in the test notes:

1. Installing the named candidate STU through the specified route below, including its
   actual bootloader write set and, on ST20, the bundled BCO app installation.
2. Playing named audio sources or notification clips at the agreed volume.
3. Rebooting or power-cycling for the specified boot-count test.
4. Running the specified recovery route with its named SoundTap or stock input.
5. Changing the U-Boot environment for serial slot rollback.
6. Changing presets, settings, Wi-Fi credentials or other persistent state.

Permission for one category does not authorize another. Stop after any unexpected storage,
recovery, boot, temperature or audio result. Bootloader and BCO app writes need the explicit
permissions and proven recovery plan in [the contract](../../CONTRIBUTING.md#the-contract); on every model each mtd0–mtd4 write needs a
fresh explicit approval immediately before it. Never write BCO memory or BFL/BSL. A test on one model
cannot approve another.

## Select the install and recovery scenario

- **First install from stock:** submit the candidate to stock `:17008`. Record replacement
  of the shared SCM SPL/U-Boot as well as the inactive kernel/rootfs. On ST20 account for
  the automatic bundled BCO app install after boot and loss of the old BCO network path.
- **SoundTap update:** use the signed `/api/firmware` route. Kernel/rootfs go to the inactive
  slot; shared bootloaders are written only if different. Establish the actual write set
  before seeking permission; a version update is not necessarily a rootfs-only update.
- **SoundTap USB-stick recovery:** with working SPL/U-Boot, test the model's gesture and a
  compatible SoundTap `Update.stu` with its `Update.stu.sig` (the release's `.sig`, signed for
  the installed image's profile). The current U-Boot path verifies that statement and the
  file's SHA-256 before it erases anything, writes `soundtap-rootfs` (with its `/boot` files)
  and selects that slot. It does not restore Bose or replace bootloaders, and it refuses a
  stick without the `.sig`, with Bose's file or with an alpha package on a production image
  (ADR 0048).
- **Return to stock:** use the model-specific procedure and separately approved input in
  [Going back to Bose](install-records.md#going-back-to-bose). This changes shared bootloaders;
  ST20 needs its stock BCO app restored separately. Test subsequent stock boot explicitly.
- **Damaged boot chain:** ROM-USB and the board's verified NAND backup are development
  recovery tools. Record the model-specific entry and restore plan; USB enumeration alone
  does not prove NAND restoration. Never manufacture a fault without separate permission.

## Read-Only Preflight

Complete this before requesting any destructive permission:

1. Confirm the spare model and immutable variant/module type; for ST10, `rhino` / `sm2`.
2. Record firmware, active Ping/Pong slot, partition labels and U-Boot environment over serial.
3. Verify `Update.stu` with `stu_check.py accept` and `recovery-mainline`; record its SHA-256.
4. Verify the named recovery inputs, FAT/FAT32 stick where relevant, board backup and
   model-specific USB restore tools required by the selected scenario.
5. Attach and capture the model's recovery console (ST10 UART3, 115200 baud). If a serial session is already running, record it;
   do not reboot or power-cycle during preflight.
6. Back up persistent `mtd10` read-only and record its hash and restore procedure.
7. Compare candidate and installed bootloader bytes; record which shared copies would change.
   Confirm the kernel/rootfs write target is the inactive slot. Do not assume A/B protects
   the shared boot chain.

## A. Boot, storage and rollback

After explicit candidate-install permission, install through the selected route. Do not use direct
NAND writes. Obtain separate reboot/power-cycle permission before boot-count testing and separate
persistent-state permission before changing presets, settings or Wi-Fi. Prove U-Boot interruption
during the first authorized reboot, before relying on serial rollback.

1. Record the running slot immediately before installation and prove it is not the write target.
2. Confirm U-Boot loads the model's device tree (`/boot/rhino_sm2.dtb` on ST10; see
   [`flash/recipes.go`](../../app/internal/flash/recipes.go) for the other variants) and Linux
   executes `/init` without panic.
3. Confirm `mtd10` mounts as `ubi1:persistent_volume` on `/mnt/nv`; volatile fallback is a fail.
4. Save `dmesg`; no module vermagic, unknown symbol, firmware-load, UBI or UBIFS errors allowed.
   `/proc/sys/kernel/modules_disabled` reads `1` and the log says `module loading closed`
   ([ADR 0132](../decisions/0132-modules-locked-after-boot.md)); on the ST300 the CPSW modules are in
   `/proc/modules` and its wired port still takes a lease.
5. Cold boot three times and warm reboot three times.
6. Write presets/settings/Wi-Fi, boot each A/B slot, and confirm identical persistent state.
7. After separate recovery permission, exercise the selected USB-stick or stock-return
   scenario. Record the observed gesture, installed payload and subsequent boot; a stock
   chord measurement does not validate our U-Boot's implementation.
8. After separate U-Boot-environment permission, test serial slot selection with a known-good
   slot compatible with the installed boot chain. Do not assume the other slot is still
   stock or that stock will boot under our loaders.

## Safety rules (every session)

- **Never exceed the current authorization.** Outside an explicitly approved install or rollback
  step: no firmware flash, STU install, partition/bootloader/NAND write or destructive sysfs.
- **Back up first, restore after.** Snapshot and put back exactly as found: volume,
  source / now-playing, `/etc/asound.conf`, SSH on/off state, and any temp files or
  processes you started. Include any writes or restarts needed for restoration in the
  session authorization; record anything that could not be restored.
- **Low volume.** Keep the speaker at ~10% throughout — a remote tester cannot hear it.
- **SSH state.** (Alpha images only; production has no SSH.) If SSH is off, it stays off afterward. Prefer on-box diagnostics over a
  shell: `GET http://<speaker>/api/debug` returns version, audio-arbiter state
  (`AUDIO_MIX`, source/snapcast FIFOs, `/etc/asound.conf` mode, mute GPIO, current source) and
  tailed logs — paste that instead of poking sysfs by hand.

## B. Audio arbiter

Precondition: `/api/debug` shows `AUDIO_MIX: 1`, `source bus: … (fifo ok)`, `asound:` first
line pointing at the bus config.

1. **Single-writer sanity** — play a TuneIn preset. Confirm audio out and exclusive ownership
   of the physical playback device. On mainline, soundtapd's Mixer feeds its supervised
   `aplay`, which opens the model's ALSA card; source daemons and Snapclient must not open
   that card. On the legacy raw path, soundtapd itself writes `/dev/snd/snd_shelby`. Record
   the selected device, format, rate and owning process ([ADR 0012](../decisions/0012-single-dac-writer.md)).
2. **Duck + restore** — with TuneIn playing, `POST /api/speaker/notify` a short clip.
   Expect: source dips (~-14 dB), clip plays over the top, source ramps back to full. No
   click/cut, no stuck-ducked state after the clip ends.
3. **DAC pacing and rate** — during (1), check for stutter, wrong pitch or speed and record
   the requested sink format/rate and the card's actual `hw_params`. The current ALSA sink
   is paced by blocking writes to `aplay`; the legacy raw sink uses the Mixer timer
   ([`audio_sink.go`](../../app/cmd/soundtapd/audio_sink.go)). The source bus is S16_LE at
   48 kHz; the ST10/ST20/WLA mainline `plughw:0,0` sink path selects S32_LE at 46875 Hz
   (ST20 opens `hw:0,0`). A playback fault needs investigation; it does not by itself
   identify a pacing bug or justify a configuration change during the test.
4. **No permanent silence on a gap** — play TuneIn, switch to another source and back, or
   let a source stop. Audio must resume; the bus must not go silent forever (regression
   guard for the O_RDWR feeder fix).
5. **AUX latency** — select AUX with a loopback cable or a known signal on the AUX in.
   `aux capture started` logs the sizes and the worst-case `budget`, `DAC reopened` the ring
   aplay was asked for, `/proc/asound/card0/pcm0p/sub0/hw_params` the ring the card took. Over
   10 minutes `underruns_delta` stays 0 and the log has no `aplay` underrun line (`counters` in
   `GET /api/health`: `bufferUnderruns` and `alsaUnderruns` do not move); measure the
   delay end to end on the loopback recording, and check it for a transient where the
   source switches into and out of AUX from a playing source.

## C. Source lifecycle

1. **Release** — TuneIn station playing → start AirPlay → stop AirPlay. Expect: standby;
   an ended external session must not restart an interrupted stream unexpectedly.
2. **Bluetooth link loss** — Bluetooth playing, then take the phone out of range. Expect: the
   source stays BLUETOOTH, paused; playback resumes when the phone is back, and the idle
   auto-off ends the wait. Standby from Bluetooth then wakes on Bluetooth, paused.
3. **Stale stop** — TuneIn → AirPlay → Bluetooth (selected) → AirPlay session's late
   "stop" arrives. Expect: still on Bluetooth (the stale stop is ignored).
4. **Exclusivity** — only one music source drives the amp at a time; starting a new source
   silences the previous (no two streams mixed).

## D. Snapcast multi-room

1. Confirm Snapserver, soundtapd and the local Snapclient remain supervised after killing
   each process in turn; audio and the web UI must recover without rebooting. In standby
   with no group no Snapclient runs; it starts at an invite and stops a minute after the
   group is gone (ADR 0013).
2. Confirm the leader's Mixer feeds `/run/soundtap/snapcast.pcm`. A leader's anchor Snapclient
   renders to `/dev/null`; when following another speaker, Snapclient instead writes
   S16_LE 48 kHz PCM into the source bus FIFO. Neither opens the physical card
   ([`snapclientArgs`](../../app/cmd/soundtapd/procs.go)). Source daemons may use ALSA `default`:
   in arbiter mode it must resolve to that FIFO, not the physical card
   ([bus configuration](../../os/overlay/etc/asound.conf.bus)).
3. Connect a Docker laptop client (by the leader's address: snapserver announces no mDNS
   service) and a second physical room. Verify pitch, dropouts,
   synchronization, reconnect behavior and grouping/volume/mute in SoundTap Settings > Multiroom.
4. Capture RSS for Snapserver, local Snapclient and soundtapd during a one-hour stream.
   Hardware synchronization remains unvalidated until these checks pass.
5. Sync: on each follower `GET /api/multiroom/downstream` must stay well under 1000 ms
   (Snapcast cannot correct more), and the leader's `GET /api/multiroom` must show the same
   value as that client's `latency`. The leader logs `multiroom leader delay` when it moves
   its own delay. Underruns: `audio pcm health` lines in `/tmp/soundtap.log`, `underruns_delta`.

### Multiroom measurements

Recorded runs behind [ADR 0013](../decisions/0013-multiroom-through-snapcast.md). Each is a
historical report of the build it ran on, not approval of a later candidate.

- 2026-10-09, WLA leading the ST10 and ST20, with the fixed offsets that preceded the measured
  chain (the leader held its output back 340 ms, followers counted their chain as zero): the
  speakers were 50 to 400 ms apart, and 2.4 s for an ST10 follower whose Mixer had
  re-prebuffered after an underrun.
- 2026-10-10, at 3 % volume: an ST20 led an ST10 on TuneIn for 60 s; the follower received
  exactly 192000 B/s, and the ST20 announced `_snapcast._tcp` and AirPlay over mDNS (snapserver
  still announced itself then). Build and hash were not recorded.
- 2026-10-23, Snapcast buffer at 600 ms: about 250 ms was left after an ST20 follower's
  latency, and it hit "Not enough frames available" 9 times in 7 minutes, each a ~1.5 s hard
  resync of silence. The buffer went to 1000 ms.
- ST10 leading two followers on Spotify/AirPlay: 89-100 % CPU for the source work and the
  snapserver encode (date not recorded).
- 2026-11-20, Wireless Link in standby, before the anchor ran only on demand: the anchor
  snapclient alone cost about 0.8 % CPU and 35 wakeups a second, all day.

### Measuring CPU

`/proc/stat`'s busy share is not trustworthy on these boards: the ST10 counted 7911 ticks in
60 s of wall time (2026-10-09), all of the excess in user and system. Measure idle against
the wall clock instead, and per process from `/proc/PID/stat` read in one pass:

```sh
set -- $(head -1 /proc/stat); i1=$5; t1=$(cut -d' ' -f1 /proc/uptime); sleep 30
set -- $(head -1 /proc/stat); t2=$(cut -d' ' -f1 /proc/uptime)
awk -v di=$(($5 - i1)) -v a=$t1 -v b=$t2 'BEGIN { printf "busy %.0f%%\n", 100 - di / (b - a) }'
```

Do not count forks while sampling with a shell loop: the loop's own `cat` and `awk` are most
of what it sees. The fork rate is the last field of `/proc/loadavg` across a plain `sleep`.

Without a shell (the production profile has none), the same figures come from `/api/debug`:
its `cpu` line gives uptime, idle seconds, soundtapd's own CPU seconds and the fork count,
all since boot. Take two bundles a known time apart and subtract; for example busy share
over the interval is `100 - 100 * Δidle / Δuptime`.

Standby record behind [ADR 0056](../decisions/0056-state-flows-by-change.md), ST20, 2026-10:
soundtapd's utime+stime over 60 s (HZ 100) was 1143 ticks with polling loops, 1030 with the loops
waiting on a state change, and 847 (−26%) with the resampler also skipping all-zero blocks. A
CPU profile before the change put 58% of the samples in resampling the silence the mixer writes
through standby. Playback CPU was not measured in that run.

### Debugging without a shell

Everything below is HTTP on :80, with no token (ADR 0055). Debug, install and reboot routes
wait for a key held on the speaker, or take the one-shot `X-SoundTap-Device-Token` from
`/run/soundtap/confirm-token` when you have root.

| Need | Route |
| --- | --- |
| State, logs, kernel messages, installer and slot status, discovery table | `GET /api/debug` |
| The same as one redacted zip, with every log in full, the previous boot's ramoops records, version and health | `GET /api/debug?download=1` |
| Try a soundtapd build without an install (rolls back if it does not come up) | `POST /api/soundtapd` |
| Install a candidate STU into the inactive slot | `POST /api/firmware?install=1` |
| Reboot | `POST /api/reboot` with `{"confirm":true}` |
| What the speaker plays, its volume | `GET /api/now`, `GET /api/volume` |

mDNS can be checked from any Linux host on the LAN (`avahi-browse -rpt _soundtap._tcp`), and a
power cycle is the last resort when the HTTP server itself is gone.

## E. Hardware and network

- **Buttons** — press each; confirm the firmware-derived mapping and 1750 ms preset hold.
  Hold volume up and down: the level must ramp continuously, not step once, and must stop on
  release. On the stock kernel the events come from `/dev/shelby-keypad`; on mainline Linux
  soundtapd scans the matrix itself over GPIO, so also check that a key reaches only its own
  action — a transposed matrix swaps whole rows and still feels responsive. Watch
  `/tmp/soundtap.log`, which logs every key the scanner sees.
- **Buttons vs Wi-Fi (mainline only)** — after any change to the keypad path, confirm the
  speaker still associates. In-kernel keypad drivers used to break Wi-Fi boot because they
  put interrupts on switch-in lines that share gpio0 with the WL1835 IRQ; the userspace scan
  takes no interrupts, and this check is what keeps it that way.
- **IR remote (capture first)** — record the model, kernel, receiver path and map used.
  Mainline uses rc-core raw timings at `/dev/lirc0`; stock's legacy driver uses `/dev/ir`.
  SoundTap falls back from the legacy path to `lirc0` when present. The supplied Bose map
  is a starting point; a different remote needs measured codes in `/mnt/nv/ir.map`.
  With permission for the service restart, run soundtapd with `IR_LEARN=1`: it logs decoded
  `ir learn` codes without executing their actions. Record each key, then restore normal
  operation. Map entries are `0x1a = VOLUME_UP`, one per line; `#` comments are allowed.
  Two observations must accompany the codes:
  1. Hold one key for ~3 s. Confirm that repeats decode as the same key, the application
     maintains one hold and it releases when the key is let go. Mainline synthesises
     release after 200 ms without a frame; the application adds 150 ms release grace by
     default (`IR_RELEASE_GRACE_MS`). Record actual repeat and release timings. The stock
     driver's 14-jiffy timer is a separate legacy path, not a mainline timing guarantee.
  2. If the remote sends only one frame and no repeats, report that limitation: reliable
     volume ramp and preset hold-to-store cannot be inferred from a single short press.
- **IR remote (behaviour)** — with the map in place confirm: each mapped key acts once per
  press; holding volume ramps continuously and stops on release, exactly as on the front
  panel; holding a preset for 1750 ms stores the current source and does _not_ also play the
  preset; power from the remote enters and leaves standby through the same mute/source/LED
  path as the front-panel key. Check range and line of sight, then use the remote, the front
  panel and the web UI at the same time and confirm they do not fight over the arbiter.
  Finally confirm an unmapped key (any other IR remote in the room) does nothing and logs at
  most one line per code — `/tmp/soundtap.log` must not grow while a foreign remote is used.
- **LEDs** — verify all seven lamps start off, then that each state is distinguishable:
  Wi-Fi white solid when associated, white blinking while associating, amber solid while
  hosting the setup AP (including from standby), amber solid with no link; SoundTouch white for
  network sources, Shelby white blinking while buffering, Shelby amber for error/pause;
  AUX white whenever AUX is selected (there is no play status to wait for); BT blue blinking
  while Bluetooth is selected and waiting for a device, BT white once one is playing;
  everything except the setup-AP signal off in standby.
  Reading `/sys/class/leds/*/brightness` over SSH samples the lamp directly, which
  separates "the daemon never reports buffering" from "the write never lands". Note that a
  single missing gpio-leds channel takes all seven down: the daemon requires the whole set
  before it will use `/sys/class/leds` and otherwise falls back to the stock `/dev` path.
- **Bluetooth** — pair, play, walk out of range, reconnect; confirm source lifecycle B holds.
  Confirm `bluetoothctl show` reports a Class ending in `0414` (Audio/Video, Loudspeaker), and
  that a phone paired fresh, with its device type left as the phone picks it, plays at normal
  level at the speaker's usual volume: with the class unset iOS took the speaker for a
  headphone and sent the stream about 22 dB down (2026-11-16).
- **AirPlay** — start/stop several times; confirm reliable acquire/release, no orphaned amp.
  Confirm the track and artist appear in `/api/now` (this needs shairport-sync ≥ 5.2; 5.1
  dropped the DMAP `core` items we parse). Move the sender's volume slider and confirm the
  speaker's master volume follows and the web UI updates without a reload.
- **Apple Home** — in the Home app, Settings → _Allow Speaker & TV Access_ → **Anyone on the
  Same Network**, password off. Add Accessory → _More options…_ → pick the speaker under its
  SoundTap name, assign a room. Confirm: it survives a reboot without re-pairing, Siri can
  route playback to it, and the Home volume slider works. Known ceiling for a non-MFi
  receiver — the Home transport buttons and playback status will not work; do not file these.
  While testing, capture `shairport-sync` RSS during a session: v5 links ffmpeg and 128 MB is
  the tightest constraint on this box.
- **Spotify** — `librespot` connect / play / disconnect; confirm it obeys the same
  source-ownership rules (acquire on sink `running`, delayed release after `closed`).
  On each model, skip from both Spotify Connect and SoundTap, including preloaded tracks,
  rapid repeated skips and a slow next-track download. The old track must not reappear
  after the transition, and the new opening must not be clipped. Repeat in multiroom.
  Let a track end naturally: its complete tail must play. The upstream close/reopen mode
  does not promise gapless live-album transitions (ADR 0014). Record firmware/app versions,
  skip-to-cut latency and any gap; offline flush tests do not establish these on hardware.
- **Wi-Fi** — provision from clean, including spaces, quotes, `$`, backticks and UTF-8 in
  SSID/password. Confirm AP fallback, association, DHCP, credential replacement and that the setup
  AP disappears after success. During setup, confirm `iw dev` shows managed `wlan0` and AP
  `wlan1`; after joining, confirm `wlan1` is gone. Compare `iw reg get` with `/proc/country_code`.
  On the setup AP, `/tmp/hostapd.conf` holds the quietest of channels 1/6/11 and, except on the
  ST20, `country_code` with `ieee80211d=1`; a WLA without a country opens no AP.
  Test a 2.4 GHz channel 12/13 network. Repeat five times and confirm one network daemon per role.
  Since soundtapd manages Wi-Fi (ADR 0063, `app/internal/wifi`): `/tmp/wpa_supplicant.conf` holds
  only `ctrl_interface=`, no `wpa_cli` runs, and the log names each `wpa_state` of a join under
  `comp=wifi`. A wrong password on new settings returns to the last network that worked and the
  setup page says so once. `kill $(pidof soundtapd)` while joined, and again while the setup AP
  is up, keeps the network: wpa_supplicant (or hostapd) keeps its pid and the next soundtapd logs
  `wifi already joined; keeping it` (or `setup AP already up; keeping it`). After a join the log
  says `clock synchronized after the Wi-Fi join`. On the WiLink models `dmesg` shows the board
  configuration of this speaker's region loaded, without `falling back to default config`.
- **Reboot persistence** — presets, settings, Wi-Fi and source recovery survive a reboot.
- **Security** — port 22 is closed; blank-password root login is impossible from LAN and setup AP.
  Production: the web UI and AirPlay answer from the speaker's own subnet and on the setup AP,
  and `nft list set ip filter lan_nets` lists exactly the subnets of `ip -4 addr`; from another
  subnet routed to the speaker, port 80 does not answer.
- **Soak** — run 24 hours with network loss/recovery and source changes; `/tmp/soundtap.log` stays
  below 512 KiB and the daemon remains reachable.

## Codec script from the speaker (ST10/ST20, ADR 0012)

- First boot after the install: the boot log says `codec script <name> copied from mtdN`.
- `sha256sum /mnt/nv/soundtap/codec/*.cfg` matches stock: ST10 `cc4ad9f1…`, ST20 `3c65ec31…`.
- The codec still loads: `AIC3256_CONFIG_OK` (ST10) or `ST20 AIC3256 initialized over GPIO SPI`.

## ST20 Candidate Validation

The ST20 takes the same universal STU as the other models
(`docs/owners/install.md`): its own kernel and our SPL/U-Boot pair, the
`.spotty` modules, installed through stock's `:17008` like the WLA. Bose's MLO/U-Boot are
replaced, so the way back is the ST20's mask-ROM USB route (`docs/hardware/st20/rom-usb-recovery.md`)
with that board's NAND backup, and an install needs a separately authorised session.

Run the same install, reboot, persistent-state, recovery and rollback gates as above on the
ST20, with OLED and BCO networking in place of ST10 LEDs/SCM Wi-Fi. Record the device as
model `SoundTouch 20`, variant `spotty`, module type `scm` (what SoundTap reports), and put the
completed `hardware-st20.json` with the other two reports for `sign-release.sh`
(`RELEASING.md`).

On a cable, also check the wired port: `eth0` gets a DHCP lease at boot and again
after a BCO reset, and Wi-Fi keeps its own lease beside it.

History, not approval: on 2026-08-11/12 a one-shot RAM/USB boot of an earlier candidate,
built on the stock 13-module container with Bose's bootloaders, passed USB boot, the SoundTap
API, AIC3256 config, raw PCM/TuneIn and the OLED transport. That path is gone and those
passes approve no current STU.

## Wireless Link Candidate Validation

The Wireless Link (`binky`) takes the same universal STU and installs through stock's
`:17008`. Run the install, reboot, persistent-state, recovery and rollback gates of section A,
with these differences:

- It has no loudspeaker. Audio gates run on its analog output, at any volume.
- Two buttons replace the keypad, and there is no IR receiver; skip those rows of section E.
- Its recovery chord is both buttons at power-on, not Volume Down + Preset 4 (`docs/owners/install.md`).
- Bluetooth runs on the WL1835's UART2 through the kernel's serdev: confirm `hci0` and pairing.

Record model `SoundTouch Wireless Link Adapter`, variant `binky`.

## SoundTouch 300 Candidate Validation

The SoundTouch 300 (`ginger`) ships in the first release ([install.md](install-records.md#soundtouch-300)).
Every hardware action on it needs a fresh explicit approval, and audio stays at 5% or below.

- Install and reboot as in section A. ST300 recovery remains unproven; do not
  infer it from a stock remote gesture or an offline GPIO-trigger implementation. Resolve
  the recovery plan under [the contract](../../CONTRIBUTING.md#soundtouch-300-ginger--sm2) before any write.
- The soundbar processor link on UART0: the bar answers, takes the volume, and follows
  SoundTap's power and the TV input.
- On a cable: `eth0` gets a DHCP lease, the log shows `lpm radio status` with `net=4` and the
  Wi-Fi lamp goes off; pulling the cable brings `net=2` and the lamp back within about 5 s,
  and plugging it in again returns to `net=4` once the lease is back.
- ADAPTiQ and tone controls are never run on a bar.

Record model `SoundTouch 300`, variant `ginger`.

## Release checklist per model

One row per path, one column per model. A cell holds the tested artifact, as its version and the first eight hex digits of its SHA-256 (`0.1.0 sha256:1a2b3c4d`), and the date it last passed, or `open`. A cell with a date alone comes from a development build whose SHA-256 was not recorded. `n/a` means the model has no such hardware (ST300 has no AUX input). The ST20 in use here has no Bluetooth (Series I). The ST10 BCO cell is `open`.

Before a release, every cell must carry the release candidate's version and SHA-256; another artifact, a date alone or `open` blocks the release. To update a cell, run the path on the speaker in an approved session, record the artifact's version and SHA-256, the date and the result in the test report, and replace the cell with that version, hash and date. Never fill a cell without a recorded report. A cell covers only what its comment shows (for instance Bluetooth wake, or AirPlay through Music Assistant), and the rows under [Open hardware checks](#open-hardware-checks) keep the finer assertions.

| Path | WLA | ST10 | ST20 | ST300 |
|---|---|---|---|---|
| Install from stock (update feed) | 2026-11-13 | 2026-11-17 | 2026-11-14 | 2026-11-15 |
| Back to Bose (`/api/firmware`, unsigned Bose STU) and back through the update feed, name, presets and Wi-Fi kept | 2026-11-17 | 2026-11-17 | 2026-11-11 | 2026-11-15 |
| Update over the web UI (`/api/firmware`, 5 installs) | 2026-11-17 | 2026-11-17 | 2026-11-17 | 2026-11-13 |
| Newer Core from the Updates page (`/api/core-update`, press, reboot, confirmed) | open | open | open | open |
| Rollback after 4 unconfirmed boots (`bootlimit=4`, ADR 0010) | open | open | open | open |
| A new slot kept through 3 power cuts during its trial boots, each before the confirm; the fourth boot confirms it | open | open | open | open |
| Wi-Fi take-over from stock (earlier passes ran on `wifi.sh`; rerun on soundtapd's Wi-Fi manager, ADR 0063) | open | open | open | open |
| Wi-Fi through soundtapd's manager (ADR 0063): join, fallback to the last network that worked, setup AP and its retry, network kept across a soundtapd restart | open | open | open | open |
| 10 install+reboot cycles (`/api/firmware?install=1`, then `POST /api/reboot`): every new slot boots at U-Boot count 1 (init's `nand boot:` log line) and keeps `upgrade_available=1` until its confirm (ADR 0010, 0052) | pass 2026-11-21 (dev STU sha256 55fc3874: 10/10 at count 1, back in 36-48 s) | open | open | open |
| BCO flash (ST20 only) | n/a | n/a | 2026-11-11 | n/a |
| Setup network | open | open | open | open |
| Production filter answers its own subnet only (`lan_nets`) | open | open | open | open |
| Standby and wake | 2026-11-12 | 2026-11-12 | 2026-11-14 | 2026-11-07 |
| Reboot loop, 20 reboots + 5 installs | 2026-11-13 | 2026-11-13 | 2026-11-13 | 2026-11-13 |
| Multiroom leader | 2026-11-13 | 2026-11-14 | 2026-11-14 | open |
| Multiroom follower | 2026-11-14 | 2026-11-13 | 2026-11-14 | 2026-11-15 |
| TuneIn | 2026-11-13 | 2026-10-21 | 2026-10-21 | 2026-11-13 |
| Spotify | 2026-11-04 | 2026-10-21 | 2026-10-21 | open |
| AirPlay (via Music Assistant) | 2026-11-04 | open | open | open |
| Bluetooth | 2026-11-12 | 2026-11-17 | n/a (Series I) | 2026-11-17 |
| AUX | open | 2026-11-12 | 2026-11-12 | n/a |
| DLNA | 2026-11-13 | 2026-11-14 | 2026-11-14 | 2026-11-15 |
| Confirmation on the speaker: volume held 5 s does not confirm, and the volume changes | open | open | open | open |
| Confirmation on the speaker: a short press (under 2 s) of another key does not confirm and does nothing else | open | open | open | open |
| Confirmation on the speaker: a key held 2 s confirms, and no preset plays or stores (ST300: a bar button and a remote key both confirm) | open | open | open | open |
| Confirmation on the speaker: the IR remote does not confirm | n/a (no IR) | open | open | n/a (no buttons: the remote confirms) |
| Confirmation on the speaker: read the `producer` soundtapd logs for a remote key held to confirm | n/a | n/a | n/a | pass 2026-11-20 (bench bar, dev STU sha256 b202cb87: Preset 1 held 2.0 s confirmed GET /api/debug, producer=2; the bar has no buttons) |
| Confirmation on the speaker: a second request replaces the first (409 `confirm_superseded`), and a hold confirms the second | open | open | open | open |

## Open hardware checks

This is a model-specific work list. An `open` row still needs the stated speaker test.
A historical pass records the tested build, date and evidence; it does not approve a
new candidate. Release reports still require the exact artifact SHA-256 and captures.
A merged change alone never establishes a pass. Hardware permissions remain
those of [the contract](../../CONTRIBUTING.md#the-contract); earlier approvals are not renewed here.

### Evidence reconciliation, 2026-11-13

Reviewed the hardware reports available on 2026-11-13. The evidence below is limited to those
reports and the current implementation; an absent report means none was located.

| Finding | Treatment and remaining work |
| --- | --- |
| WLA Wi-Fi takeover, 2026-11-11 | Historical pass for the Wi-Fi takeover on WLA. The [roadmap](../owners/roadmap.md#install-and-recovery) also records ST10 and ST20 takeover runs. ST20 uses the separate BCO-profile path of the ST20 install. These summaries lack attached artifact hashes/full captures, so their candidate checks stay open. ST300 remains open. |
| ST20 rollback, 2026-11-12 | Historical pass at `bootlimit=3`: three unconfirmed boots changed slot, retained after reboot. The limit is 4 since 2026-11-20 (ADR 0010), so the rollback rows of the release checklist are open again on every model; the earlier passes at 3 were WLA and ST10 2026-10-30, ST20 2026-11-12. Keep candidate-specific regression testing in section A. |
| WLA AirPlay pause/resume using Music Assistant 2.6.3 | Partial observation: pause/resume/seek were observed; build/date are not identified in the report and no iPhone was used. Keep the sender/model remainder open. |
| Bluetooth runs, ST10 and WLA | Pairing and a 10-minute silent stream were measured. Absolute volume was not negotiated, so that check stays open; WLA pairing reliability, two-phone behaviour and the later clear-stream fix also remain. |
| ST20 Bluetooth test scope | The bench ST20 is Series I and has no Bluetooth. Keep support built for Series III, offline-covered only; its phone tests are unavailable on this board, not passed. ST300 needs a nearby phone. |
| ST300 tracker outcome | Installation and the LPM link are historically reported working and can leave the open install list. Build/hash and individual ping/volume/speaker-list checks are absent from that summary, so they remain open. |
| Bluetooth source selection | Replaces the earlier automatic source claim: a connected/streaming phone must not take another source; selection enables Bluetooth. This is current `bt.sh` / `bt_follow.go` behaviour. |
| [Go media supervision / ADR 0121](../decisions/0121-one-supervisor-per-process.md) | Replaces the deleted shell loop. The current check covers bounded backoff, deliberate restarts and parent/child lifetime under soundtapd. |
| Standby CPU, 2026-11-14 | Partial observation: ST10 and a grouped WLA were measured; no ST20 measurement and no every-key/hold/wake-latency run. The old keypad check is part of the keypad row; do not close it from CPU figures. |
| Dependency batches | One current-candidate boot/play smoke run per model can exercise both; keep their individual dependency/build checks in CI. |
| Stock/Stockholm batches | The merge and green CI are not a hardware run. The first batch overlaps individual feature rows; retain its batch-specific retry/standby-clock remainder. The second keeps its panel-language, failure-notice and UPnP-data remainder. |

An open row can be fulfilled in the same approved session as another row, provided its
specific assertions and model are recorded. Counts below cover `open` rows only; unavailable,
prohibited and historical results are not silently counted as pending executable tests.

### ST10 (44 open)

| Prove on the speaker | Status |
|---|---|
| soundtapd runs as `soundtapd` (ADR 0066): `ps` shows soundtapd as that user and `soundtap-root serve` as root, `/proc/$(pidof soundtapd)/status` has `CapEff`, `CapPrm` and `CapBnd` `00000000000034e0` and `CapAmb` 0, the media daemons run as their users with `CapEff` 0 (nqptp: `CAP_NET_BIND_SERVICE` only), and the log has `soundtap-root up` and every `soundtap-root` request it answered; on the first boot after the update from a build that ran it as root, `/mnt/nv/soundtap` and `wifi.conf*` turn soundtapd's and presets, settings, Wi-Fi and Spotify survive; then, as soundtapd: a signed STU install (the log shows each `flash_erase`, `nandwrite`, `ubiformat` and `fw_setenv` via soundtap-root and the readback passes), the boot confirm after the reboot (boot count cleared), `POST /api/reboot`, a Wi-Fi change and the setup AP, a factory reset (Spotify and pairings gone, the speaker comes back in setup), an app upload through `/api/soundtapd`, the keypad, the IR remote, both lamps and the hardware watchdog (no reset in an hour); audio at the agreed volume | open: passed 2026-11-22 (dev STU sha256 b1394fa8): user, capabilities and the media daemons' users as listed; state migrated (presets, settings, Wi-Fi, Spotify kept); an unsigned dev install through soundtap-root with readback, the boot confirm (count cleared) and `POST /api/reboot`; keypad, IR, lamps and the watchdog up as soundtapd (not pressed); audio at volume 2. Still to do: a signed install, a Wi-Fi change, the setup AP, a factory reset, `/api/soundtapd`, presses and an hour without reset |
| owner key (ADR 0048): set one on the Updates page with a hold and read `soundtap_owner_key` back from both environment copies (`fw_printenv`, `nanddump` of `mtd5` and of `mtd4`+0x1c0000); a production STU signed with it installs through `/api/firmware` and from a USB stick at power-on (Volume Down + Preset 4), the stick's console saying `the owner key is trusted as well`; after removing it on the Updates page, and again after a factory reset, `/api/firmware` refuses it with 403 and the stick writes nothing (`soundtap.recovery=-129`); a release-signed STU still installs with a key set | open |
| signed USB stick at power-on: `Update.stu` with its `Update.stu.sig` installs and boots the other slot; without the `.sig`, with a changed `Update.stu`, or with an alpha package on a production image nothing is written, the Wi-Fi lamp blinks slowly three times, the old slot boots and the kernel command line says `soundtap.recovery=-126` or `-129`; record the time from power-on to the new slot (ADR 0009) | open |
| radio plays through a Wi-Fi stall (read-ahead) | open |
| Spotify artist context playback against a live session | open |
| install writes the full chain; a second install leaves mtd0-mtd4 identical | historical pass, second install identical: 2026-11-15 |
| custom radio URL plays (Custom Station page) | open |
| Bluetooth hint disappears after the pairing window | open |
| join a hidden network; visible networks still join | open |
| phone volume slider follows speaker volume (AVRCP) | open |
| reconnect of trusted phones when Bluetooth is selected | open |
| pairings read and cleared over D-Bus | open |
| stock radio presets kept on the first boot after an install (hot-swap only on ST300) | open |
| AirPlay pause from the sender shows in now playing | open |
| Bluetooth connectable-only outside the pairing window (measured on WLA with the Pi, no phone) | open |
| six small speaker scripts print their errors | open |
| Bluetooth selection gates stream, track and volume; phone connect/play cannot take another source; leaving pauses/disconnects | open |
| Wi-Fi returns within a minute after a router power cut | open |
| Wi-Fi returns within 30 minutes after a router power cut with a phone left on the setup network | open |
| Wi-Fi through soundtapd's manager (ADR 0063, section E): clean provision, wrong password back to the last network, setup AP channel and country, network kept across `kill $(pidof soundtapd)` with the station and with the setup AP, clock synced after the join, region's wl18xx board configuration in `dmesg` | open |
| play a media-server folder as a queue (real UPnP server) | open |
| radio station song name in now playing | open |
| batch-only fast-retry ordering and walking standby clock; source/queue/metadata checks remain in their own rows | open |
| third batch: panel language, failure notices, UPnP track data (no sound) | open |
| AirPlay time bar | open |
| Spotify Liked Songs button (read endpoint unproven) | open |
| Go media supervisor: bounded backoff after crashes, immediate deliberate restart, no orphan children after soundtapd restart (ADR 0121) | open |
| one phone plays at a time (two phones) | open |
| stock's Wi-Fi carried over by decrypting its profile | open |
| network diagnostics read the log file | open |
| multiroom leader address and id file; reboot of a follower (section D.3) | open |
| liveness kills count toward the app override rollback | open |
| current candidate with kernel/ffmpeg and dependency fixes from the dependency updates boots and plays | open |
| front-panel reset/setup chords held during an install (BCO flash applies only to ST20) | open |
| cancelled or replaced sleep timer does not reach standby | open |
| leave a multiroom group through the web server; regroup | open |
| clearing pairings ends a phone's stream | open |
| standby CPU and keypad/display wake latency; every key and 1750 ms preset hold; partial CPU measurements above | open |
| multiroom follower starts in a local source's order | open |
| probe upload does not hold the install lock | open |
| notify clips read one at a time (audible on the WLA only) | open |
| station logos in the UI (images only) | open |
| proxy forwards only to an address that answers as that speaker (multiroom) | open |
| a new slot confirms with the router switched off: the boot confirm checks no network (ADR 0010) | open |
| ROM-USB restore of the release's boot chain (`tools/recovery/rom-usb-restore.py`, [recovery tools](install-records.md#repairing-the-boot-chain-over-rom-usb)) | open |

### ST20 (39 open)

| Prove on the speaker | Status |
|---|---|
| soundtapd runs as `soundtapd` (ADR 0066): `ps` shows soundtapd as that user and `soundtap-root serve` as root, `/proc/$(pidof soundtapd)/status` has `CapEff`, `CapPrm` and `CapBnd` `00000000000034e0` and `CapAmb` 0, the media daemons run as their users with `CapEff` 0 (nqptp: `CAP_NET_BIND_SERVICE` only), and the log has `soundtap-root up` and every `soundtap-root` request it answered; on the first boot after the update from a build that ran it as root, `/mnt/nv/soundtap` and `wifi.conf*` turn soundtapd's and presets, settings, Wi-Fi and Spotify survive; then, as soundtapd: a signed STU install (the log shows each `flash_erase`, `nandwrite`, `ubiformat` and `fw_setenv` via soundtap-root and the readback passes), the boot confirm after the reboot (boot count cleared), `POST /api/reboot`, a Wi-Fi change and the setup AP, a factory reset (Spotify and pairings gone, the speaker comes back in setup), an app upload through `/api/soundtapd`, the keypad, the IR remote, the panel (OLED over spidev), the amplifier fault line and the hardware watchdog; the BCO install (power switched through soundtap-root, `bco-image` ` ok`), its LAN watch; audio at the agreed volume | open: passed 2026-11-22 (dev STU sha256 b1394fa8): user, capabilities and the media daemons' users as listed; state migrated (presets, settings, Wi-Fi, Spotify kept); an unsigned dev install through soundtap-root with readback, the boot confirm (count cleared) and `POST /api/reboot`; the OLED, keypad, IR and amplifier lines exported through soundtap-root, the BCO link up, volume in the codec DSP; audio at volume 2. Still to do: a signed install, a BCO image install through soundtap-root, a Wi-Fi change, the setup AP, a factory reset, `/api/soundtapd`, presses and an hour without reset |
| owner key (ADR 0048): set one on the Updates page with a hold and read `soundtap_owner_key` back from both environment copies (`fw_printenv`, `nanddump` of `mtd5` and of `mtd4`+0x1c0000); a production STU signed with it installs through `/api/firmware` and from a USB stick at power-on (Volume Down + Preset 4), the stick's console saying `the owner key is trusted as well`; after removing it on the Updates page, and again after a factory reset, `/api/firmware` refuses it with 403 and the stick writes nothing (`soundtap.recovery=-129`); a release-signed STU still installs with a key set | open |
| signed USB stick at power-on: `Update.stu` with its `Update.stu.sig` installs and boots the other slot; without the `.sig`, with a changed `Update.stu`, or with an alpha package on a production image nothing is written, the speaker shows nothing (no lamp, no display), the old slot boots and the kernel command line says `soundtap.recovery=-126` or `-129`; record the time from power-on to the new slot (ADR 0009) | open |
| Historical stock-BCO Wi-Fi join path | not applicable: stock BCO networking is unsupported (ADR 0031) |
| TODO Series III (spotty3, mojo3): which model our U-Boot takes the board for without a stored environment. Read only, on a Series III under stock: the product name at 0x170 of `NAND.Manufacturing-Data` (stock calls the board `variant=spotty` or `mojo`) and the `variant_raw` U-Boot samples. `rt_board_setup` (`uboot-2026.07-scm-nand-wp.patch`) knows only rhino, binky and ginger there and falls back to `variant_raw`: rhino (`rhino_sm2.dtb`, `uImage-rhino`) from 0x3100, binky (the WLA's tree and kernel, no preset chord) below. That is the default `bootcmd`, the recipes the USB-stick recovery saves into a torn environment, and the variant soundtapd repairs with afterwards; an installed environment keeps the installer's spotty3 recipes. Once the value is measured, the board code maps it to rt_v spotty3 or mojo3, and `rt_set_recipes` and the default `bootcmd` take `spotty3_sm2.dtb` with `uImage-rhino` (as `flash/recipes.go` and `installer.c` do), not `uImage-spotty3` | unavailable: no Series III board; nothing in the repository records either value |
| radio plays through a Wi-Fi stall (read-ahead) | open |
| Spotify artist context playback against a live session | open |
| install writes the full chain; a second install leaves mtd0-mtd4 identical | historical pass, second install identical: 2026-11-15 |
| custom radio URL plays (Custom Station page) | open |
| Bluetooth hint disappears after the pairing window | unavailable: Series I; Series III has offline coverage only (scope above) |
| join a hidden network; visible networks still join | open |
| phone volume slider follows speaker volume (AVRCP) | unavailable: Series I; Series III has offline coverage only (scope above) |
| reconnect of trusted phones when Bluetooth is selected | unavailable: Series I; Series III has offline coverage only (scope above) |
| pairings read and cleared over D-Bus | unavailable: Series I; Series III has offline coverage only (scope above) |
| stock radio presets kept on the first boot after an install (hot-swap only on ST300) | open |
| AirPlay pause from the sender shows in now playing | open |
| Bluetooth connectable-only outside the pairing window (measured on WLA with the Pi, no phone) | unavailable: Series I; Series III has offline coverage only (scope above) |
| six small speaker scripts print their errors | open |
| reboot after the first install of the own BCO app | open |
| Bluetooth selection gates stream, track and volume; phone connect/play cannot take another source; leaving pauses/disconnects | unavailable: Series I; Series III has offline coverage only (scope above) |
| Wi-Fi returns within a minute after a router power cut | open |
| Wi-Fi returns within 30 minutes after a router power cut with a phone left on the setup network | open |
| Wi-Fi through soundtapd's manager (ADR 0063, section E): clean provision, wrong password back to the last network, setup AP channel and country, network kept across `kill $(pidof soundtapd)` with the station and with the setup AP, clock synced after the join; the BCO's `wlan0` and `wlan1` (ADR 0031), channel 6 without a country element | open |
| play a media-server folder as a queue (real UPnP server) | open |
| radio station song name in now playing | open |
| batch-only fast-retry ordering and walking standby clock; source/queue/metadata checks remain in their own rows | open |
| third batch: panel language, failure notices, UPnP track data (no sound) | open |
| AirPlay time bar | open |
| Spotify Liked Songs button (read endpoint unproven) | open |
| Go media supervisor: bounded backoff after crashes, immediate deliberate restart, no orphan children after soundtapd restart (ADR 0121) | open |
| one phone plays at a time (two phones) | unavailable: Series I; Series III has offline coverage only (scope above) |
| stock's Wi-Fi carried over by decrypting its profile | open |
| network diagnostics read the log file | open |
| unconfirmed-boot recovery: a crash loop falls back to the other slot, the warm reset keeps the count (tools/hil/rollback-check.sh) | historical pass at `bootlimit=3`: 2026-11-12 (run); candidate hash/capture not attached |
| multiroom leader address and id file; reboot of a follower (section D.3) | open |
| press routes allow the press window plus their own work | open |
| liveness kills count toward the app override rollback | open |
| current candidate with kernel/ffmpeg and dependency fixes from the dependency updates boots and plays | open |
| reset and setup chords held during an install or BCO flash | open |
| cancelled or replaced sleep timer does not reach standby | open |
| leave a multiroom group through the web server; regroup | open |
| standby CPU and keypad/display wake latency; every key and 1750 ms preset hold; partial CPU measurements above | open |
| multiroom follower starts in a local source's order | open |
| probe upload does not hold the install lock | open |
| notify clips read one at a time (audible on the WLA only) | open |
| station logos in the UI (images only) | open |
| proxy forwards only to an address that answers as that speaker (multiroom) | open |
| a new slot confirms with the router switched off: the boot confirm checks no network (ADR 0010) | open |
| boot confirm waits for the BCO install and `bcolink0`: a trial boot with a new BCO image confirms after the flash, and a BCO cut off mid-flash is flashed again on the next boot, which then confirms (ADR 0010, 0031) | open |
| ROM-USB restore of the release's boot chain (`tools/recovery/rom-usb-restore.py`, [recovery tools](install-records.md#repairing-the-boot-chain-over-rom-usb)): busy pad held, the release's ST20 SPL from the ROM | open |

### WLA (42 open)

| Prove on the speaker | Status |
|---|---|
| soundtapd runs as `soundtapd` (ADR 0066): `ps` shows soundtapd as that user and `soundtap-root serve` as root, `/proc/$(pidof soundtapd)/status` has `CapEff`, `CapPrm` and `CapBnd` `00000000000034e0` and `CapAmb` 0, the media daemons run as their users with `CapEff` 0 (nqptp: `CAP_NET_BIND_SERVICE` only), and the log has `soundtap-root up` and every `soundtap-root` request it answered; on the first boot after the update from a build that ran it as root, `/mnt/nv/soundtap` and `wifi.conf*` turn soundtapd's and presets, settings, Wi-Fi and Spotify survive; then, as soundtapd: a signed STU install (the log shows each `flash_erase`, `nandwrite`, `ubiformat` and `fw_setenv` via soundtap-root and the readback passes), the boot confirm after the reboot (boot count cleared), `POST /api/reboot`, a Wi-Fi change and the setup AP, a factory reset (Spotify and pairings gone, the speaker comes back in setup), an app upload through `/api/soundtapd`, both buttons, both lamps and the hardware watchdog; line out at the agreed volume | open: passed 2026-11-22 (dev STU sha256 b1394fa8): user, capabilities and the media daemons' users as listed; state migrated (presets, settings, Wi-Fi, Spotify kept); an unsigned dev install through soundtap-root with readback, the boot confirm (count cleared) and `POST /api/reboot`; buttons, lamps and the watchdog up as soundtapd (not pressed); line out at volume 2. Still to do: a signed install, a Wi-Fi change, the setup AP, a factory reset, `/api/soundtapd`, presses and an hour without reset |
| owner key (ADR 0048): set one on the Updates page with a hold and read `soundtap_owner_key` back from both environment copies (`fw_printenv`, `nanddump` of `mtd5` and of `mtd4`+0x1c0000); a production STU signed with it installs through `/api/firmware` and from a USB stick at power-on (both buttons), the stick's console saying `the owner key is trusted as well`; after removing it on the Updates page, and again after a factory reset, `/api/firmware` refuses it with 403 and the stick writes nothing (`soundtap.recovery=-129`); a release-signed STU still installs with a key set | open |
| signed USB stick at power-on: `Update.stu` with its `Update.stu.sig` installs and boots the other slot; without the `.sig`, with a changed `Update.stu`, or with an alpha package on a production image nothing is written, the lamp blinks slowly three times, the old slot boots and the kernel command line says `soundtap.recovery=-126` or `-129`; record the time from power-on to the new slot (ADR 0009) | open |
| both buttons at power-on install Update.stu from USB | open |
| radio plays through a Wi-Fi stall (read-ahead) | open |
| Spotify artist context playback against a live session | open |
| install writes the full chain; a second install leaves mtd0-mtd4 identical | historical pass, second install identical: 2026-11-15 |
| custom radio URL plays (Custom Station page) | open |
| Bluetooth hint disappears after the pairing window | open |
| join a hidden network; visible networks still join | open |
| Bluetooth button: press-and-hold time | open |
| phone volume slider follows speaker volume (AVRCP) | open |
| reconnect of trusted phones when Bluetooth is selected | open |
| pairings read and cleared over D-Bus | open |
| stock radio presets kept on the first boot after an install (hot-swap only on ST300) | open |
| AirPlay pause/resume from iPhone or other AirPlay 2 sender; Music Assistant partial run linked above | open |
| Bluetooth connectable-only outside the pairing window (measured on WLA with the Pi, no phone) | open |
| six small speaker scripts print their errors | open |
| Bluetooth selection gates stream, track and volume; phone connect/play cannot take another source; leaving pauses/disconnects | open |
| Wi-Fi returns within a minute after a router power cut | open |
| Wi-Fi returns within 30 minutes after a router power cut with a phone left on the setup network | open |
| Wi-Fi through soundtapd's manager (ADR 0063, section E): clean provision, wrong password back to the last network, setup AP channel and country, network kept across `kill $(pidof soundtapd)` with the station and with the setup AP, clock synced after the join, region's wl18xx board configuration in `dmesg`; no setup AP without a regulatory country | open |
| play a media-server folder as a queue (real UPnP server) | open |
| radio station song name in now playing | historical pass: 2026-11-15 |
| AirPlay time bar | open |
| Spotify Liked Songs button (read endpoint unproven) | open |
| Go media supervisor: bounded backoff after crashes, immediate deliberate restart, no orphan children after soundtapd restart (ADR 0121) | open |
| one phone plays at a time (two phones) | open |
| stock's Wi-Fi carried over by decrypting its profile | open: historical pass 2026-11-11 (run, candidate hash not attached) was on `wifi.sh`; rerun on soundtapd's Wi-Fi manager |
| network diagnostics read the log file | open |
| multiroom leader address and id file; reboot of a follower (section D.3) | open |
| liveness kills count toward the app override rollback | open |
| current candidate with kernel/ffmpeg and dependency fixes from the dependency updates boots and plays | open |
| front-panel reset/setup chords held during an install (BCO flash applies only to ST20) | open |
| cancelled or replaced sleep timer does not reach standby | open |
| leave a multiroom group through the web server; regroup | open |
| clearing pairings ends a phone's stream | open |
| multiroom follower starts in a local source's order | open |
| probe upload does not hold the install lock | open |
| notify clips read one at a time (audible on the WLA only) | open |
| station logos in the UI (images only) | open |
| proxy forwards only to an address that answers as that speaker (multiroom) | open |
| a new slot confirms with the router switched off: the boot confirm checks no network (ADR 0010) | open |
| ROM-USB restore of the release's boot chain (`tools/recovery/rom-usb-restore.py`, [recovery tools](install-records.md#repairing-the-boot-chain-over-rom-usb)) | open |

### ST300 (20 open)

| Prove on the speaker | Status |
|---|---|
| soundtapd runs as `soundtapd` (ADR 0066): `ps` shows soundtapd as that user and `soundtap-root serve` as root, `/proc/$(pidof soundtapd)/status` has `CapEff`, `CapPrm` and `CapBnd` `00000000000034e0` and `CapAmb` 0, the media daemons run as their users with `CapEff` 0 (nqptp: `CAP_NET_BIND_SERVICE` only), and the log has `soundtap-root up` and every `soundtap-root` request it answered; on the first boot after the update from a build that ran it as root, `/mnt/nv/soundtap` and `wifi.conf*` turn soundtapd's and presets, settings, Wi-Fi and Spotify survive; then, as soundtapd: a signed STU install (the log shows each `flash_erase`, `nandwrite`, `ubiformat` and `fw_setenv` via soundtap-root and the readback passes), the boot confirm after the reboot (boot count cleared), `POST /api/reboot`, a Wi-Fi change and the setup AP, a factory reset (Spotify and pairings gone, the speaker comes back in setup), an app upload through `/api/soundtapd`, the LPM serial link to the soundbar processor (no op `0x08`) and the hardware watchdog; audio at the agreed volume | open |
| owner key (ADR 0048): set one on the Updates page with a hold and read `soundtap_owner_key` back from both environment copies (`fw_printenv`, `nanddump` of `mtd5` and of `mtd4`+0x1c0000); a production STU signed with it installs through `/api/firmware` and from a USB stick at power-on (the remote's recovery combo), the stick's console saying `the owner key is trusted as well`; after removing it on the Updates page, and again after a factory reset, `/api/firmware` refuses it with 403 and the stick writes nothing (`soundtap.recovery=-129`); a release-signed STU still installs with a key set | open |
| signed USB stick at power-on: `Update.stu` with its `Update.stu.sig` installs and boots the other slot; without the `.sig`, with a changed `Update.stu`, or with an alpha package on a production image nothing is written and the old slot boots (what the bar shows is not known; record it) and the kernel command line says `soundtap.recovery=-126` or `-129`; record the time from power-on to the new slot (ADR 0009) | open |
| Bluetooth pairing/playback with a nearby phone; Pi range prevented the prior run (scope) | open: pairing with the Pi passed 2026-11-15; playback needs a phone |
| install writes the full chain; a second install leaves mtd0-mtd4 identical | open |
| join a hidden network; visible networks still join | open |
| remote's mute shows in SoundTap | open |
| bar's input remembered across a restart | open |
| bar answers ping, follows volume, speakers read | 2026-11-15: volume 3 and 19 read back, bass module paired and connected |
| six small speaker scripts print their errors | open |
| Wi-Fi returns within a minute after a router power cut | open |
| Wi-Fi returns within 30 minutes after a router power cut with a phone left on the setup network | open |
| Wi-Fi through soundtapd's manager (ADR 0063, section E): clean provision, wrong password back to the last network, setup AP channel and country, network kept across `kill $(pidof soundtapd)` with the station and with the setup AP, clock synced after the join, region's wl18xx board configuration in `dmesg`; Wi-Fi beside the wired port | open |
| Go media supervisor: bounded backoff after crashes, immediate deliberate restart, no orphan children after soundtapd restart (ADR 0121) | open |
| pick the soundbar's HDMI input (no audio needed) | 2026-11-15: HDMI activated bar source 1, TV source 8 |
| stock's Wi-Fi carried over by decrypting its profile | open |
| network diagnostics read the log file | open |
| liveness kills count toward the app override rollback | open |
| current candidate with kernel/ffmpeg and dependency fixes from the dependency updates boots and plays | open |
| probe upload does not hold the install lock | open |
| a new slot confirms with the router switched off: the boot confirm checks no network (ADR 0010) | open |
| ROM-USB restore of the release's boot chain (`tools/recovery/rom-usb-restore.py`, [recovery tools](install-records.md#repairing-the-boot-chain-over-rom-usb)): first find how its ROM reaches USB | open |


### Prohibited and historical checks

| Reference | Model | Outcome |
| --- | --- | --- |
| ST300 | ADAPTiQ start is prohibited on the bench bar. Keep offline coverage; do not schedule a speaker run. |
| ST300 | Preview installation has a later tracker summary. Historical report only; exact candidate approval remains separate. |

## Audio start order on a speaker

A pop at a source start is read from the order of a few log lines in `/tmp/soundtap.log`,
not by ear: `source switch` (with `held=true` for a multiroom follower), `amp power`,
`amp mute`, `multiroom follower in sync` and `source sound reaches the DAC`. With
`SOUNDTAP_LOG_LEVEL=debug` each codec DSP volume write adds a `codec volume` line. A follower
joining from a playing source shows `source switch held=true` and no `amp mute` line at
all. From standby, `amp power on=true` comes first and `amp mute muted=false` at least 3 s
later. Either way the follower's `source sound reaches the DAC` comes after
`multiroom follower in sync`.

From mute, `amp mute muted=false` follows `source sound reaches the DAC` by about 300 ms of
sound without an ALSA underrun: an `underrun!!!` line from aplay right after
`amp mute muted=false` is a pop. An install first puts a playing speaker in standby, so the
log shows `source switch source=STANDBY` and `amp mute muted=true` before the install's
first flash line. Passed on the ST20 on 2026-11-22 (dev STU sha256 f0d64cca): a preset
from standby, by ear.

## Report back

For each item record pass/fail, artifact SHA-256, serial log and failure details. Any failed item
blocks production release.

The report records evidence after a session; it never grants permission to perform a hardware
action.

A release candidate is a production image and has no SSH
([ADR 0119](../decisions/0119-no-ssh-in-production.md)). Rows that read the speaker over SSH need
the alpha build of the same commit; never approve or publish that alpha artifact. The scripts
in `tools/hil/` run on the production candidate itself, through the HTTP API, and prompt for
the key held on the speaker that each install and reboot needs; the
[install gate's layer 3](../contributors/testing/install-gate.md#layer-3-hardware-in-the-loop-by-hand) says
what an owner runs for each step. Only `rollback-check.sh`'s watchdog route (soundtapd
frozen) needs the alpha build.
