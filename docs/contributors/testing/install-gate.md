# The install gate

The install gate checks what an install writes and the userspace it ships before a speaker
is involved. Only a hardware run on each model shows that the installed boot chain and
hardware paths work, so the gate has three layers, each proving something the one below it
cannot. This page says what each layer proves, which of the ten gaps of the old harness it
closes, and what is still open.

Layers 1 and 2 run in every Build of `main`, layer 1 on all four models. Layer 3 is a
separately authorised session by hand (ADR 0117), not a CI task. Its gaps close only with
recorded evidence for that model and candidate; automation is not the criterion for a
hardware pass.

| Layer | When | What it is | State |
| --- | --- | --- | --- |
| 1. Simulation | every Build | the real `POST /api/firmware` install path writing into a simulated NAND, per model | built |
| 2. Run | every Build | the shipped armv7 `soundtapd`, run from the package's own rootfs under qemu-user, up to `/api/version` and the web UI | built (userspace only) |
| 3. Hardware in the loop | release candidates | the candidate installed on a bench speaker, rebooted, rolled back | by hand |

## Layer 1: simulation

Script: [`os/tests/installer/nandsim-install-test.sh`](../../../os/tests/installer/nandsim-install-test.sh), run by
`tools/ci/run-build.sh gate` in the `install-gate` jobs of
[`build.yml`](../../../.github/workflows/build.yml). It also runs on a laptop with Docker, from any
directory; its scratch files go to `output/nandsim.*` in the checkout:

```sh
tools/ci/run-build.sh gate                      # all eight cells below
MODELS=wla tools/ci/run-build.sh gate           # one model's two cells
SCENARIOS="st10/8/none wla/9/some/cut" os/tests/installer/nandsim-install-test.sh dist/SoundTap-Core-*.stu
```

**How it works.** `soundtapd` (built for the runner's architecture, with the production
web UI) runs inside a QEMU guest with a stock Ubuntu kernel. The kernel's `nandsim` module
provides an MTD device with the speakers' NAND geometry and the stock partition table;
`mtd-utils` and `libubootenv` are the same tools the speaker image carries. The test
uploads the package over HTTP, exactly as the web UI does, and then looks at the flash.
One guest boots once and runs every scenario in turn, reloading `nandsim` between them.

**What it proves, per scenario** (`model/slot/badblocks[/cut]`):

1. A truncated upload is refused, the alternate kernel is erased (not bootable), the
   environment and every other partition are unchanged.
2. Optionally (`cut`): the rootfs write is stopped half way and `soundtapd` and the flash
   tools are SIGKILLed. The slot is not bootable, the environment does not select it,
   nothing else moved. A good install afterwards repairs the slot.
3. The install reports the right target and running slot.
4. The U-Boot environment, read with `fw_printenv` through the same
   `fw_env.config` line `soundtapd` writes (`/dev/mtd5 0x0 0x2000 0x20000 1`), has `bootcmd`
   on the installed slot and the SoundTap markers on that slot's `nand_args*` entry, and
   `bootlimit=4` and `altbootcmd` boot the slot that was running, which is the rollback of
   [ADR 0010](../../decisions/0010-ab-slots-boot-confirm-rollback.md). The same on all four
   models (ADR 0009). Boot recipes may change too (item 7); the scenario rejects changes
   outside its explicit expected set, including ST300's `bootdelay=-2`.
5. The kernel partition holds the image's kernel, byte for byte, and the new UBI volume
   is byte-identical to the one in the image (`/dev/ubiN_0` against a volume rebuilt from
   the package by [`stu_expect.py`](../../../os/support/stu_expect.py), which does not use the
   installer's code). The volume mounts and holds `/init`, `/boot/uImage-<variant>` and the
   device tree `slot_boot` loads.
6. The running slot, `NAND.Manufacturing-Data` and the persistent and staging partitions are
   byte-identical before and after. Before the install they are filled with random data so
   that a stray write or erase shows.
7. The bootloader partitions start out different from the package, as on a board still
   running stock's chain. The four SPL copies and U-Boot are rewritten (all four copies
   identical), the slot recipes in the environment are ours for this model
   (`internal/flash/recipes.go`), no entry outside the slot switch and those recipes
   changed, and a second install over the new chain leaves mtd0-mtd4 byte-identical.

**Matrix.** Slot 8 (install from PING into PONG) and slot 9 (the mirror image); no bad
blocks and "some": a fixed set in the target kernel and rootfs, in the running slot's first
kernel block and in the last block of U-Boot next to the environment; the power cut in two
cells; the four models. The full matrix is eight cells (one guest boot, the scenarios in turn):

| Cell | Running slot | Bad blocks | Power cut |
| --- | --- | --- | --- |
| `st10/8/none` | PING | none | no |
| `st10/9/some/cut` | PONG | some | yes |
| `wla/8/some` | PING | some | no |
| `wla/9/none` | PONG | none | no |
| `st20/8/none` | PING | none | no |
| `st20/9/some/cut` | PONG | some | yes |
| `st300/8/none` | PING | none | no |
| `st300/9/some` | PONG | some | no |

Every model sees both slots, and every slot sees both a clean and a worn chip. Run any
other cell locally with `SCENARIOS="wla/8/none/cut" os/tests/installer/nandsim-install-test.sh <package.stu>`.

**What CI runs.** Every Build on `main` runs the full matrix as four jobs, one per model with
its two cells, after the package job; nothing decides per change whether it runs. The hosted
x64 runner has `/dev/kvm` (ADR 0011), and the script uses it when it is there. Without KVM, as
on the self-hosted runner the Build used before, a cell takes 8 to 13 minutes (measured
2026-10-26). A pull request builds the image and the package but runs no layer 1; run the
cells of the models a change is about locally before merging a change to the installer, the
environment handling or the flash code.

**Cost.** With KVM a scenario takes 10 s (17 s with the power cut), so the eight cells are
about 100 s of guest time. The rest is setup that does not grow with the matrix: building
the web UI and soundtapd, and installing the guest's packages from the snapshot (minutes on
a cold runner).

**Geometry per model.** The four scenario models share one 256 MiB chip layout and one partition
table: 2048-byte pages, 128 KiB eraseblocks, 64 bytes OOB, partitions as in
`os/board/common/dts/ti/omap/am335x-soundtap-nand.dtsi` (the ST10 device tree carries the
same table). What differs is the chip ID.

| Model | Variant | Chip ID given to nandsim | Source |
| --- | --- | --- | --- |
| ST10 | `rhino`, sm2 | `2c da 90 95` (3.3 V part) | the ID the harness has always used; the repo records no READID for the ST10 |
| WLA | `binky`, sm2 | `2c aa 90 15` (1.8 V part) | READID `2c aa 90 15 06` read on the board |
| ST20 | `spotty`, scm | `2c da 90 95` (same as ST10) | the repo documents the same 256 MiB chip and table as the ST10 (`os/tests/fixtures/st20-stock/hardware-facts.md`) but holds no READID; the ID is borrowed, the geometry is not invented |
| ST300 | `ginger`, sm2 | `2c aa 90 15` (same as WLA) | `2c aa` from the ST300's dmesg (2026-11-07); the other two bytes follow the WLA and are not an independently captured READID |

nandsim takes four ID bytes, so the fifth (`06` on the WLA) is not modelled. If a READID
for the ST10, ST20 or ST300 is recorded, update `model_setup` and its source evidence together.

**Reproducible.** The images are pinned by digest. The guest's packages (kernel, QEMU,
`mtd-utils`, `libubootenv-tool`, BusyBox) come from `snapshot.ubuntu.com` at one fixed
timestamp (`APT_SNAPSHOT` in the script); the run prints the exact versions. Move the date
deliberately, in a commit of its own.

**What it cannot show.** The flash is simulated, the CPU is not an AM335x, nothing is
booted, and the RTC is missing. See the gaps below. The "some" cells set factory bad blocks
only: a block that fails during the install, which the installers then mark bad
([ADR 0128](../../decisions/0128-bad-block-marking.md)),
is tested on the host instead, in the offline installer gate and the U-Boot recovery probe.

## Layer 2: run the shipped binary

Script: [`os/tests/installer/rootfs-run-test.sh`](../../../os/tests/installer/rootfs-run-test.sh), run by the
"Run the shipped soundtapd from the package's rootfs" step of
[`build.yml`](../../../.github/workflows/build.yml), about a minute once its image is cached:

```sh
os/tests/installer/rootfs-run-test.sh dist/SoundTap-Core-*.stu
```

Layer 1 runs a `soundtapd` built for the runner's architecture. This layer runs the one
that ships: it takes the rootfs volume out of the package (with the reader
`stu_expect.py` uses), unpacks the UBIFS with a pinned `ubi_reader`, and starts
`/usr/bin/soundtapd` chrooted into that tree under `qemu-arm`, so it reads the image's
own `/etc`. It proves the armv7 binary starts, `/api/version` reports the OS version in
the rootfs's `/etc/soundtap/os-version`, and the embedded web UI loads with every asset
`index.html` names.

**What it does not prove.** QEMU has no AM335x machine, so the kernel, the device tree
and the init scripts are not run here. A kernel that does not boot on the board is caught
only by layer 3.

## Layer 3: hardware in the loop (by hand)

The scripts in [`tools/hil/`](../../../tools/hil/) run from a laptop on the speaker's network.
They work on a production image, the release candidate itself, through the HTTP API alone
([ADR 0119](../../decisions/0119-no-ssh-in-production.md)):

- `GET /api/health` says which boot runs: `boot.id` (the kernel's boot id, new at every
  boot), `boot.slot` (`PING` or `PONG`), `boot.confirmed` (soundtapd has reset U-Boot's boot
  count, a minute after it started) and `boot.previous` (how the boot before it ended:
  `reboot`, `power loss`, a watchdog reset, a panic). `GET /api/version` names the build.
  Both are read without a press.
- An install, an app upload and a reboot wait for a key held on the speaker for two seconds
  ([ADR 0055](../../decisions/0055-household-token-and-windows.md)). Before each one the script
  prints `>>> Hold a button on the speaker now (not volume) for two seconds to confirm: …`;
  hold any key on the speaker's panel other than volume while its lamps blink (ST10 white,
  Wireless Link amber; the ST20 display names the action). The remote does not confirm. The
  speaker waits 60 s, then the request fails with 403 and the script stops.
- On an alpha image the scripts find root SSH and send the one-time token from
  `/run/soundtap/confirm-token` instead, so a run needs nobody at the speaker. `SOUNDTAP_HIL_SSH=0`
  makes an alpha image behave as a production one.
- `SOUNDTAP_HIL_SERIAL=/dev/ttyUSB0` (any serial device on the speaker's console, the ST10's
  UART3 at 115200 baud) keeps everything the console prints in `hil-console-<time>.log`
  (`SOUNDTAP_HIL_CONSOLE_LOG` names another file), and a failure shows its last 20 lines.
  SPL, U-Boot and the kernel print there on a production image too, which has no login on
  it, so a boot that stalls before the network says where.
- Every wait is bounded: 300 s for a boot and for its confirm (`SOUNDTAP_HIL_TIMEOUT`), a
  look every 5 s (`SOUNDTAP_HIL_POLL`). [`tools/hil/lib.sh`](../../../tools/hil/lib.sh) holds
  what the scripts share. They need `curl` and `python3`.

What an owner runs, step by step, on a production candidate:

| Step | Command | At the speaker |
| --- | --- | --- |
| Install the candidate, prove it boots from the other slot and confirms | `tools/hil/install-candidate.sh <ip> SoundTap-Core-X.Y.Z.stu` | two holds: the install, the reboot |
| Try a soundtapd build without a Core install | `tools/hil/install-app.sh <ip> output/app/soundtapd` | one hold |
| Soak: installs, warm reboots, cold boots | `tools/hil/boot-soak.sh <ip> SoundTap-Core-X.Y.Z.stu 5 3 3` | two holds per install, one per warm reboot, a power cut per cold boot |
| Rollback by boot count | `tools/hil/rollback-check.sh <ip>` | five holds, each at once |

`install-candidate.sh` installs through `POST /api/firmware?install=1` (shared bootloaders
written when different), reboots through `POST /api/reboot` like the web UI, and with
bounded waits checks a new boot, the other slot, soundtapd's boot confirm and
`/api/version`, then puts the volume back. A warm reboot that is not back in 300 s fails:
waiting longer would let a power cycle by hand rescue a stalled reboot and still pass.
With the candidate of 2026-10-26 it passed on the WLA and caught a stalled warm reboot
on the ST20. `SOUNDTAP_CONFIRM=downgrade` installs a bench build over a newer release.

```sh
tools/hil/install-candidate.sh 192.0.2.126 SoundTap-Core-0.1.0.stu
```

When only soundtapd changed, a Core build is not needed to try it on a speaker. `make app`
builds it with the UI embedded in about a minute, and
[`tools/hil/install-app.sh`](../../../tools/hil/install-app.sh) installs it, signed the same
way, through `POST /api/soundtapd`, and waits for `/api/version` to report the new build.
The override is retained under `/mnt/nv` and runs from RAM at startup while its Core
base hash matches; trial failure or a changed base drops it (ADR 0035). It does not replace
production-candidate validation:

```sh
make app && tools/release/sign.sh output/app/soundtapd
tools/hil/install-app.sh 192.0.2.126 output/app/soundtapd
```

[`tools/hil/boot-soak.sh`](../../../tools/hil/boot-soak.sh) repeats the install and the boot,
without sound:

```sh
tools/hil/boot-soak.sh <ip> <file.stu> [installs, default 5] [warm reboots, default 3] [cold boots, default 0]
```

It installs the candidate `installs` times through `install-candidate.sh`, which times the
first warm reboot after each install (the case that once stalled before init on the ST10
and the Wireless Link), then reboots warm through `/api/reboot`, then asks for the cold
boots: `>>> Cold boot n of N: cut the speaker's power and wait 10 s. Press Enter as you
switch it on again.` A cold boot needs a hand on the plug or a switched socket; the speaker
cannot be asked for one. Per boot it prints the seconds until the API answers from the new
boot and until the boot confirm, the slot, and `previous=` as `/api/health` reports it,
which tells a clean reboot from a watchdog reset or a power loss:

```text
install 1: rebooted after 58 s total 131 s, after: PONG, {"version": ...}
warm reboot 1: up 52s confirmed 113s PONG previous=reboot
cold boot 1: up 49s confirmed 110s PONG previous=power loss
PASS
```

With 0 installs any placeholder stands for the package. On an alpha image the installs and
the warm reboots run unattended; the cold boots always need someone.

The rollback by boot count has its own script,
[`tools/hil/rollback-check.sh`](../../../tools/hil/rollback-check.sh), for models running our
boot-counting U-Boot, including the SCM ST20 (ADR 0009/0010):

```sh
tools/hil/rollback-check.sh 192.0.2.126
```

It keeps every boot of the running slot from confirming, then checks that U-Boot falls back
to the other slot after `bootlimit` (4), that `soundtapd` there confirms the boot, and that
the next reboot stays on that slot, which shows soundtapd selected it. The speaker ends on
the other slot. How a boot is kept from confirming depends on the image:

- **Production:** as soon as a boot of the slot under test answers, the script asks for a
  reboot through `/api/reboot` and prompts for the hold. Hold at once: soundtapd confirms a
  minute after it starts, and a boot that confirms first resets the count, which the script
  reports as a failure. Init keeps the reset warm while U-Boot counts, so the count
  survives. This proves the count and the fallback, not the watchdog.
- **Alpha:** it freezes `soundtapd` (SIGSTOP over SSH) before its confirm, so the kernel
  watchdog resets the board unconfirmed, as a hang would, and it also checks that soundtapd
  logged the fallback. With the candidate of 2026-10-30 this passed on the WLA and the
  ST10, falling back after three unconfirmed boots (`bootlimit=3` then; 4 since 2026-11-20).

A power cut cannot stand in for an unconfirmed boot: the slot under test is a confirmed one,
and on a confirmed slot a cold reset clears the count in the RTC, so U-Boot counts the boot
after it as the first. (A slot an install selected keeps its count across power cuts until
its confirm, ADR 0010.) Power cuts belong to the soak's cold
boots, which the script asks a person for.

The scripts run against a fake speaker in `make test-shell`
([`test_hil_scripts.py`](../../../os/tests/hardware/test_hil_scripts.py) with
[`fake_speaker.py`](../../../tools/ci/fake_speaker.py), which follows U-Boot's count and
soundtapd's confirm). That proves the scripts, not the speaker.

Keep the authorised manual run and its evidence with the candidate's hardware report.
Do not schedule these scripts from CI: ADR 0117 forbids unattended speaker installs.
Layer 3 uses a real mask ROM, BCH8 ECC, U-Boot and RTC; the offline layers cannot approve
behaviour that depends on them.

## The ten gaps

| # | Gap in the old harness | Status |
| --- | --- | --- |
| 1 | ECC: nandsim uses Linux software ECC, the AM335x uses BCH8 in hardware. The test shows Linux can read the slot, not that the ROM or U-Boot can boot it. | **Open, owned by layer 3** (real ROM and BCH8), or by an AM335x ROM/BCH8 model if one is ever built. Not built. |
| 2 | Bootloaders are never written. | **Closed in layer 1; booting it is layer 3.** Every cell writes the chain and the mainline recipes and checks them, and a second install leaves them byte-identical (item 7). nandsim has no BCH8, so whether ROM boots the result stays a board question. |
| 3 | The slot switch is checked only through the JSON answer, never the environment. | **Closed by layer 1:** `fw_printenv` after the install, expected entries and nothing else changed. The boot-count value itself lives in the RTC (`SCRATCH2`), which nandsim lacks: its reset after a confirmed boot, and the rollback it triggers, are **layer 3**. |
| 4 | `BADBLOCKS` exists but CI never sets it. | **Closed by layer 1:** the "some" cells set bad blocks in the target slot, the running slot and beside the environment. A bad block in the one-block environment partition is not covered: the speaker cannot write its environment then either. |
| 5 | `SLOT=9` never runs in CI. | **Closed by layer 1:** both slots run in every Build. |
| 6 | Only ST10 geometry. | **Closed by layer 1** for the partition table and chip geometry of ST10, WLA and ST20 (one shared table). Chip ID bytes are modelled for the WLA only; the ST10 and ST20 use the ID the harness has always used because the repo records no READID for them (table above). |
| 7 | `soundtapd` is built for the runner's architecture, not the armv7 binary that ships. | **Closed by layer 2** for the binary: the shipped armv7 `soundtapd` runs from the package's rootfs in every Build. The shipped kernel and init stay **layer 3**. |
| 8 | apt pulls the kernel and QEMU unpinned. | **Closed by layer 1:** Ubuntu snapshot at a fixed timestamp, versions printed in the log. |
| 9 | The new slot is only mounted, its content is not compared with the image. | **Closed by layer 1:** kernel bytes and the whole UBI volume are compared with values derived from the package; running slot and Manufacturing-Data are compared before and after. |
| 10 | One truncation point, no power cut. | **Closed by layer 1** for a software cut: the truncation case stays, and the `cut` cells SIGKILL `soundtapd` and the flash tools mid-rootfs. nandsim has no torn page, so a cut in the middle of a page program is not modelled; the real thing is **layer 3**. |

## Changing the gate

- A new model or a different chip: `model_setup` and the matrix in `build.yml`; record the
  source of every ID byte in the table above.
- Anything that changes what an install writes (the environment, slot selection, the
  rootfs format) changes the expectations in the script or in `stu_expect.py`. They are
  deliberately written down twice, in the installer and in the test.
- The gate is not hardware release approval: the [hardware checklist](../../hardware/hardware-test.md)
  still decides that.
