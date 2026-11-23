# Contributing to SoundTap

Thanks for wanting to help. SoundTap is new firmware for speakers people already own and
love, so the bar is simple: **never leave someone with a dead speaker**. Everything below
follows from that.

## Ways to help

| You have… | You can… |
| --- | --- |
| a SoundTouch running SoundTap | report what works and what does not ([bug form](../../issues/new?template=bug.yml)); the speaker's own *Settings → Debug mode → Report a bug* opens a pre-filled issue with the diagnostics |
| a SoundTouch still on Bose software | read-only evidence from a model we do not support yet ([device form](../../issues/new?template=device.yml)) |
| a translator's eye | improve a language in [`app/frontend/src/lib/i18n/messages/`](app/frontend/src/lib/i18n/messages/) |
| Go or React | pick an issue labelled [`good first issue`](../../issues?q=is%3Aopen+label%3A%22good+first+issue%22) |
| Linux, U-Boot or embedded experience | the boot chain, drivers and the ST20's network processor; start at [`docs/contributors/design.md`](docs/contributors/design.md) |

## Ground rules

1. **Read [the contract](#the-contract) below before touching anything that runs on hardware.**
   It binds every contributor: what may be flashed, what may never be written, and how each
   model gets back to stock.
2. **No Bose files in the repository or the package** ([ADR 0049](docs/decisions/0049-no-stock-bytes.md)).
3. **Every architectural choice gets an ADR** in [`docs/decisions/`](docs/decisions/README.md),
   in the same pull request. Change the picture in [`docs/contributors/design.md`](docs/contributors/design.md) too.
   Where stock had a feature, SoundTap behaves as stock does; that needs no ADR, and a
   difference from stock without one is a bug.
4. **Evidence over claims.** "Works on my ST10" comes with the commit, the package hash and the
   log. A green QEMU run is a gate, not hardware approval.
5. **Third-party libraries are welcome; Bose files never** ([ADR 0124](docs/decisions/0124-third-party-code.md)).
   A dependency needs a licence compatible with GPL-3.0-only, recorded in
   [`NOTICE.md`](NOTICE.md); an exact pinned version (`go.sum`, `package-lock.json`, Buildroot
   hash); active maintenance; a reasonable size; and a real problem it fixes. A Go module is
   added with `go get`, `go mod tidy` and `go mod vendor` in `app/`, all three in one commit:
   the image builds from `app/vendor/` offline, and `make lint-go-vendor` checks it.
6. **A new source file starts with its licence and copyright** as SPDX tags in a comment, after
   the shebang line if there is one (in a Buildroot `.mk`, after the package banner):

   <!-- REUSE-IgnoreStart -->
   ```go
   // SPDX-License-Identifier: GPL-3.0-only
   // SPDX-FileCopyrightText: 2026 SoundTap contributors
   ```
   <!-- REUSE-IgnoreEnd -->

   GPL-3.0-only is the project's licence; code that goes into another project's build takes
   that project's (the kernel driver and device trees GPL-2.0-only, U-Boot GPL-2.0-or-later).
   Go, TypeScript, JavaScript, Python, shell, C and assembly, Makefiles, Dockerfiles, YAML and
   CSS take the header. A file that cannot carry one (images, JSON, Markdown, generated files,
   patches, configs) is covered by a path in [`REUSE.toml`](REUSE.toml); third-party files keep
   their own licence there, and patches take the licence of the project they patch. Every
   licence named has its text in `LICENSES/`. `make lint-reuse` (part of `make lint`) fails on a
   file without both.

## Develop without a speaker

You do not need a speaker to work on the app. On macOS or Linux, from a fresh clone:

```sh
make dev                 # a simulated ST10; make dev MODEL=st20 (st10, st20, st300, wla)
# open http://localhost:5173 and edit app/frontend/src: the page reloads as you save
```

`make dev` builds `soundtapd` and runs it with `-dev` at `127.0.0.1:8080`, its home in
`output/dev-home/<model>` (delete it to start over), and the Vite dev server at
`localhost:5173`, which proxies `/api` and the live updates to it. Ctrl-C stops both. A
change to the Go code needs a restart.

It uses the host's Go (1.26 or newer, `app/go.mod`) and Node (22 or newer,
`app/frontend/package.json`) when both are there: a few seconds after the first `npm ci`.
Otherwise it builds and runs both in the pinned Go and Node images, which needs only Docker
(`DEV_CONTAINERS=1` forces that, `DEV_CONTAINERS=0` forbids it). `DEV_PORT` and
`DEV_UI_PORT` move the ports.

What `-dev` is: the in-process speaker (`app/internal/speakerstate`) behind the real web API,
with `MODEL`'s profile: its name, sources, capabilities and volume limits. What the API keeps
in that speaker and in the home (volume, presets, settings, source, standby) behaves as on a
speaker, live updates included; a selected station goes from buffering to playing after a
moment, silently. What it is not: nothing is decoded or
heard, and there are no buttons, lamps, panel, IR, Bluetooth, Wi-Fi changes, firmware
installs, boot confirmation or BCO. Nothing is announced on the LAN (no mDNS name, no
`_soundtap._tcp`), so other speakers never see it and multiroom has no peers; both servers
listen on 127.0.0.1 only. Browsing TuneIn and the weather still go out to the internet, as
the UI asks. soundtapd refuses `-dev` on a flashed speaker.

To work against a real speaker instead, run `SOUNDTAP_URL=http://<speaker-ip> npm run dev` in
`app/frontend`.

Building the firmware itself needs Docker and a Linux host:

```sh
make os      # Buildroot: bootloaders, kernel, root filesystem (long, cached after the first run)
make stu     # package the universal .stu
```

`tools/ci/run-build.sh` does the same in one go: `make os`, `make stu`, the candidate in
`dist/` and, with `run-build.sh gate`, the install gate. `--reuse` keeps what it can of an
earlier Buildroot tree, minutes instead of an hour. The [Build workflow](.github/workflows/build.yml)
runs that script on GitHub-hosted runners
([ADR 0011](docs/decisions/0011-one-buildroot-pipeline.md)), so a build on your machine and
one in CI take the same steps, and none of them needs a private machine.

## Checks before you open a pull request

```sh
# From the repository root, on Linux or macOS; Docker supplies the pinned tools.
tools/ci/run-ci.sh            # everything CI checks on a pull request, side by side
tools/ci/run-ci.sh go web     # or some of it
```

CI runs the same script on GitHub-hosted runners, one job per step; a pull request from a
fork gets read-only permissions and no secrets. The steps: `lint` (`make lint`), `web`, `go`,
`go-race`, `python`, `firewall` and `bco` (together `make test`) and `vuln` (`make vuln`:
`govulncheck` and `npm audit`). The Go steps check both build architectures, vet, the tests and the tests under
the race detector; the frontend step includes lint, format, types, tests and build. On the host
it needs only Docker, Python 3, git and make (macOS's make 3.81 will do); the linters, and
`bash -n`, run in pinned containers, and `lint` needs the Buildroot submodule
(`git submodule update --init`). `lint` includes `actionlint` and `zizmor` on the workflows:
every action is pinned by full commit SHA, every container image by digest.
A pull request that touches `os/`, `bco/`, the build or the installer also builds the image
and the package (`tools/ci/run-build.sh`); the Build workflow on `main` then runs the install
gate on every model. See the [test map](docs/contributors/testing/README.md).
For documentation-only changes, the focused suite is
`python3 -m unittest discover -s os/tests/docs` from the repository root.

## Pull requests

- One topic per pull request, branched from `main`.
- Say what changed and **why**, and how you tested it: unit tests, QEMU, or which speaker
  with which build.
- Anything that changes an install or update route, the flash layout, the boot chain or a
  safety boundary needs an ADR and a hardware-test plan before it can merge.
- Keep commits readable; we squash on merge.

## Reporting security problems

Not in a public issue, please: see [SECURITY.md](SECURITY.md).

## Conduct

Be kind, assume good intent, and remember there is a person with a speaker on the other end.
See the [Code of Conduct](CODE_OF_CONDUCT.md).

## The contract

SoundTap replaces the Bose SoundTouch software with one Go daemon (`soundtapd`) and an OS we build
ourselves: our SPL, U-Boot, mainline kernel and rootfs. One universal STU installs on the
SoundTouch 10 (ST10), the Wireless Link Adapter (WLA), the SoundTouch 20 (ST20) and the SoundTouch
300 (ST300) ([ADR 0008](docs/decisions/0008-one-universal-stu.md)).

These are the rules every contributor follows. They are a contract, not documentation: the
reasons and details are in the linked ADRs and docs. The design starts at
[`docs/contributors/design.md`](docs/contributors/design.md); setup and pull requests are above.

**Speaker owner** below means the person whose speaker it is; only their own permission counts.

### Absolute safety rules

- Hardware investigation is read-only by default. Reading never includes an erase, not even for a
  bad-block audit.
- Never play audio, radio, test tones, announcements, notification sounds or ALSA output on a
  speaker without its owner's explicit permission for that exact test, at the volume they agree;
  [`docs/hardware/hardware-test.md`](docs/hardware/hardware-test.md) sets the limits. Builds, tests and QEMU never use
  host audio.
- Never install an STU, write NAND, change the U-Boot environment, flash the BCO, reboot or
  power-cycle a speaker without its owner's explicit permission for that action and a recovery
  plan proven for that model (below). Permission for one action does not cover another
  ([session authorization](docs/hardware/hardware-test.md#session-authorization)).
- A write to `mtd0`-`mtd4` (SPL or U-Boot), by any route, needs the owner's OK given right before
  it, with the actual write set, test results and open risks shown first. An install writes the
  bootloaders that differ from the package, so compare the candidate's SPL and U-Boot with the
  speaker's before asking ([ADR 0008](docs/decisions/0008-one-universal-stu.md)).
- Never use `SMSC_BSL_Update`, `SMSCControlApp bsl`, raw BCO memory writes or guessed BCO commands.
  The BCO loaders BFL and BSL are never written. Keep read-only BCO inspection separate from
  updater code.
- Never format or erase persistent NAND (`mtd10`) or manufacturing data (`mtd12`) from code.
- QEMU never approves hardware. Never infer one model's safety from another model, from QEMU,
  from the simulated-NAND install gate or from offline STU validation. ST20 safety is never
  inferred from the ST10.
- No automated job installs on a speaker ([ADR 0117](docs/decisions/0117-no-automated-speaker-install.md)).

### Recovery invariant

Every hardware action keeps a way back. This is about the way back, not about keeping Bose code.

- **USB stick (the product's recovery).** Our U-Boot, before Linux and without soundtapd,
  networking or `/mnt/nv`: a FAT/FAT32 stick with `Update.stu` and `Update.stu.sig` at its root
  (a release's `.stu` and `.sig` renamed, or a build signed with the speaker's owner key), power
  off, hold **Volume Down + Preset 4** (ST10, ST20) or **both buttons** (WLA), apply power. It
  verifies the signature and the SHA-256 before anything is erased, writes only `soundtap-rootfs`
  into the slot that is not booting, and selects it only after a second matching hash. It refuses
  unsigned packages, Bose's file, a `stock` statement and an alpha package on a production image,
  and never writes SPL or U-Boot ([ADR 0009](docs/decisions/0009-mainline-linux-own-bootloaders.md),
  [ADR 0048](docs/decisions/0048-signed-updates.md)). The ST300 has no proven gesture.
- An implemented route is not a proven one: prove it on the exact candidate and model before
  relying on it. Stock's chord on stock bootloaders proves nothing about ours.
- Do not change the physical keypad or button mapping U-Boot reads, and never claim recovery from
  an IR remote.
- **Back to Bose.** Only through the Updates page (`POST /api/firmware`, below). Not a full NAND
  restore: the other slot and `/mnt/nv` remain. On the ST20 the BCO keeps our app; restoring the
  stock BCO app is a development step with `bcoflash` that needs independent SCM access first
  ([BCO recovery](docs/hardware/st20/bco-recovery.md)). On the ST300 the soundbar processor keeps its firmware.
- **Damaged SPL or U-Boot.** Mask-ROM USB with `tools/recovery/rom-usb-restore.py` and that
  board's own NAND backup ([ADR 0009](docs/decisions/0009-mainline-linux-own-bootloaders.md#repairing-a-damaged-boot-chain-over-rom-usb),
  [developer recovery tools](docs/hardware/install-records.md#developer-recovery-tools)). This is
  development tooling, not owner recovery:
  - ST10 and WLA: the ROM tries USB first (same sm2 module and straps;
    [ST10 notes](docs/hardware/st10/usb-recovery-and-rom.md), [`tools/recovery/rom-usb-loader/`](tools/recovery/rom-usb-loader/README.md)).
  - ST20: the ROM boots NAND first; USB needs the busy pad held, inside the speaker. Our ST20 SPL
    asks the micro-B port first ([ST20 ROM-USB recovery](docs/hardware/st20/rom-usb-recovery.md)).
  - ST300: how its ROM reaches USB is unknown. No bootloader work on an ST300 until that and a way
    back are established.
- **Before bootloader work on a board** (anything that may write `mtd0`-`mtd4`, an install
  included): a full per-partition NAND backup of that board including OOB
  (`tools/recovery/nand-backup-ssh.sh`, which needs root SSH and so an alpha image), and that
  model's ROM-USB route checked with `rom-usb-restore.py --dry-run`. Every bootloader change is
  RAM-booted first. One exception: the very first install from Bose's software, through Bose's
  own updater, may run without a backup, because a speaker on stock firmware has no route to
  take one. From then on, back up before every bootloader change.

### Bose-free

[ADR 0049](docs/decisions/0049-no-stock-bytes.md): the repository and the STU hold no Bose files,
and installing SoundTap needs no original Bose STU.

- Never commit stock bytes: firmware, STU containers, dumps, BCO images, codec scripts, NAND
  backups. Keep local copies outside the working tree. Do not redistribute stock firmware or BCO
  artifacts.
- Stock analysis happens outside the repository; its findings go into docs as facts, registered in
  [`os/stock/PROVENANCE.md`](os/stock/PROVENANCE.md), never as copied code.
- A binary we ship but do not compile comes from upstream with its source recorded
  (`os/support/check_provenance.py`), never from a Bose image.
- Two things stay on the speaker and are used in place: its own codec script (ST10, ST20;
  [ADR 0012](docs/decisions/0012-single-dac-writer.md)) and the ST20's BFL/BSL
  ([ADR 0031](docs/decisions/0031-bco-network-card.md)).
- The STU header's `BOSE` magic is the only borrowed thing; every module in the STU is ours.
- Third-party libraries are welcome under the rules of [ADR 0124](docs/decisions/0124-third-party-code.md).

### NAND and boot contract

| mtd | Content | An update writes it |
| --- | --- | --- |
| 0-3 | SPL copies | only when different from the package (or matching only after many ECC corrections) |
| 4 | U-Boot, its spare, the redundant environment copy (last block) | the U-Boot copies, as for 0-3 |
| 5 | U-Boot environment, primary copy | slot selection and boot recipes |
| 6 / 7 | kernel Ping / Pong | the inactive one |
| 8 / 9 | rootfs Ping / Pong (UBI, 128 KiB PEB, 2 KiB minimum I/O) | the inactive one |
| 10 | `ubi1:persistent_volume`, mounted at `/mnt/nv` | never |
| 11 | update staging | never |
| 12 | manufacturing data | never |

- Discover partitions by label from `/proc/mtd`; do not assume numbers on unknown firmware.
- An update writes the inactive kernel/rootfs pair and never the running slot. Bootloader copies
  are written in the order of [ADR 0009](docs/decisions/0009-mainline-linux-own-bootloaders.md)
  (spare U-Boot, SPL 1, 2, 3, 0, first U-Boot), each read back.
- Before erasing the inactive slot, point the environment at the running slot if it selects the
  inactive one. Select the new slot only after its rootfs reads back whole and its kernel, device
  tree and `/init` check out. A power loss at any step must leave a bootable slot
  ([ADR 0010](docs/decisions/0010-ab-slots-boot-confirm-rollback.md)).
- `bootcmd` in the environment selects the slot on every model; U-Boot's boot count falls back to
  the other slot for a boot soundtapd never confirms. soundtapd writes the environment with
  libubootenv's `fw_setenv` and reads every write back; it rebuilds the environment only when no
  copy checks out, and writes the copy left itself when the other sits on a bad block (ADR 0010).
- U-Boot loads the model's device tree from `/boot` (recipes in `app/internal/flash/recipes.go`)
  and Linux runs `/init`.

### Updates and signing

- Every package a running speaker installs is signed: a `.sig` statement with kind, profile,
  version and SHA-256, checked before anything is written, under the per-route policy of
  [ADR 0048](docs/decisions/0048-signed-updates.md). That table is the contract; do not add a route
  around the signature or the press on the speaker ([ADR 0055](docs/decisions/0055-household-token-and-windows.md)).
- Unsigned packages install only on an alpha image with `/mnt/nv/soundtap/allow-unsigned`.
- An owner may set their own key on their speaker and sign their own builds
  ([signing your own builds](RELEASING.md#signing-your-own-builds)). It is set and cleared only
  behind the press; a restore never carries it.
- The way back to Bose: `POST /api/firmware` takes a model-compatible original Bose STU without a
  signature, only with `X-SoundTap-Confirm: stock` and the press. Only Bose's modules reach NAND; a
  `soundtap-` or native module is refused before it is written. It writes the inactive slot and the
  stock bootloaders when different.
- SoundTap has no port-17008 listener. Stock's updater on `:17008` is bootstrap transport for the
  first install only.
- An ST20 install plan accounts for the BCO app write as well as the SCM boot chain.

### Per-model contracts

Per-model evidence lives in [`docs/owners/compatibility.md`](docs/owners/compatibility.md) and
[`docs/hardware/hardware-test.md`](docs/hardware/hardware-test.md). What is not recorded there stays disabled until
read-only hardware evidence for that model exists.

#### ST10 (`rhino` / sm2)

- Respect the hardware contract in [`docs/hardware/st10/hardware-contract.md`](docs/hardware/st10/hardware-contract.md):
  Wi-Fi interfaces, audio link, codec and amplifier GPIOs, lamps, the userspace-polled keypad and
  the console.

#### Infrared (ST10, ST20)

- Respect the infrared contract in [the remote-control page](docs/hardware/st10/remote-control.md#the-infrared-contract):
  receive only, never add transmission, and a failed IR probe never blocks boot.

#### ST20 (`spotty` / scm)

- Dual CPU. The BCO (DM870) runs our own BCO app as the SCM's network card: the SCM's
  `soundtap_bco` driver shows `wlan0` (WPA2 station, PMK offload), `eth0` (wired) and on request
  `wlan1` (open setup AP for hostapd), as on the ST10 ([ADR 0031](docs/decisions/0031-bco-network-card.md)).
  SoundTap does not support stock BCO firmware.
- The rootfs carries our BCO app at `/usr/share/soundtap/bco-app.bcd`; soundtapd installs it when
  its hash differs and keeps retrying while the BCO runs no app of ours. CC2540 is not included.
- BCO app updates use the existing loaders' app TFTP/XMODEM path. Entering the BSL app-update menu
  is not replacing BSL. Stock BCO firmware must always stay restorable.
- Never add a network watchdog that reboots the SCM or restarts its networking because the gateway
  does not answer. The one recovery is the BCO power cycle of ADR 0031, on evidence that the radio
  itself is dead.
- Boot chain: its own build of the shared SPL source (`soundtap-spl.spotty`) in all four copies and
  the shared U-Boot.
- Respect the hardware contract in [`docs/hardware/st20/hardware-contract.md`](docs/hardware/st20/hardware-contract.md):
  display, codec, amplifier and keypad.

#### Wireless Link Adapter (`binky` / sm2, model `wla`)

- A separate model, not an ST10, although it shares the module type and STU module names.
- Line out and AUX in, Bluetooth on the WL1835's UART side (uart2), the two buttons as
  gpio-keys-polled and the two lamps rest on [`docs/hardware/wla/`](docs/hardware/wla/). No AIC3256 or amp GPIO
  control.
- Recovery gesture: both buttons at power-on. Stock WLA has no USB-stick gesture.

#### SoundTouch 300 (`ginger` / sm2)

- The WLA's module, kernel and boot chain with its own device tree; soundtapd speaks the soundbar
  processor (LPM) protocol on UART0 ([ADR 0109](docs/decisions/0109-st300-soundbar-processor-link.md)).
- U-Boot never listens on UART0 (`bootdelay=-2`).
- Never send LPM op `0x08`: it reboots the processor. The processor's firmware is never written.
- ADAPTiQ and tone-control frames are never sent to a bar without its owner's explicit permission
  for that test.
- Recovery is unproven (no stick gesture, no known ROM-USB entry): resolve a way back before any write.

### Release and security

- Two profiles ([ADR 0011](docs/decisions/0011-one-buildroot-pipeline.md),
  [ADR 0119](docs/decisions/0119-no-ssh-in-production.md)): alpha, the default, has passwordless
  root Dropbear for bench work, and CI enforces it; production (`make SOUNDTAP_PROFILE=production os`)
  has no SSH, no blank root password and no port 22, and nothing replaces SSH. Never mix them.
  Owner documentation never asks for SSH.
- Releases follow [`RELEASING.md`](RELEASING.md) and [`docs/hardware/hardware-test.md`](docs/hardware/hardware-test.md):
  publish only the exact bytes tested. Only the maintainer signs and publishes. Private keys are
  never in the repository or on a build host. The release key exists in CI only as an encrypted
  secret of the protected `release` environment, which only `sign.yml` on `main` reads
  ([ADR 0035](docs/decisions/0035-releases.md)); it is never a repository secret, and no other
  workflow names it.
- Credentials are inert data: never source user-controlled files. Credentials stay on `/mnt/nv`
  (0600) and never reach the browser, logs or diagnostics.
- From 0.1.0 on-box state survives every update ([ADR 0120](docs/decisions/0120-on-box-state-migrates.md)):
  a format change to a file on `/mnt/nv` ships, in the same PR, its migration and a test loading the
  previous release's fixture in `app/testdata/nv-<release>/`; [`docs/contributors/nv-state.md`](docs/contributors/nv-state.md)
  lists every file. The HTTP API ([ADR 0108](docs/decisions/0108-http-api-compatibility-for-1-0.md))
  and the `.sig` format hold from 0.1.0 too.
- Pins: Buildroot packages by version and hash, container images by digest, workflow actions by
  commit SHA.
- `NOTICE.md` and `os/stock/PROVENANCE.md` are release inputs.
- Security problems go through [`SECURITY.md`](SECURITY.md), not a public issue.

### Decisions, tests and checks

- Every architectural choice gets an ADR in [`docs/decisions/`](docs/decisions/README.md) in the
  same PR: a change to what runs where, an install or update route, the flash layout, a build
  pipeline, an external protocol or a safety boundary without one is incomplete. Stock behaviour is
  the baseline and needs none ([decision records](docs/decisions/README.md)).
- An ADR describes what holds now: update it when the decision changes, delete it when we no longer
  do it. Update [`docs/contributors/design.md`](docs/contributors/design.md), Mermaid diagrams included, in the same PR.
- A new test goes next to what it tests: Go tests beside the package, frontend tests beside the
  component, shell/build/docs tests in `os/tests/<subject>/` as [the test map](docs/contributors/testing/README.md)
  lists. A PR that adds, moves or removes tests updates the map.
- Prefer a test that runs the code (a function sourced with stub commands, a script against a fake
  tree) over one that searches a file for a string.
- A change to a hardware path (boot, flash, audio, Wi-Fi, buttons, lamps) names the row of
  [the hardware checklist](docs/hardware/hardware-test.md) that proves it on a speaker. Bench runs use
  [`tools/hil/`](tools/hil/) through the HTTP API
  ([install gate, layer 3](docs/contributors/testing/install-gate.md#layer-3-hardware-in-the-loop-by-hand)),
  with the speaker owner at the speaker for each press.
- `os/tests/fixtures/` is recorded hardware evidence and immutable. Redact credentials, account
  identifiers, serials and MACs (placeholders: `SERIAL0000000000A`, `02:00:00:xx:xx:xx`, `192.0.2.x`).

Required checks:

```sh
tools/ci/run-ci.sh                       # make lint, make test, make vuln, as CI runs them
# a change to os/, bco/, the installer or the boot chain also (Linux, Docker):
tools/ci/run-build.sh os stu run gate    # make os, make stu (runs stu_check.py accept), install gate
make image-check
```

Never trust a stale `output/`: rebuild kernel, DTB and rootfs together. Read hashes from the
generated `SHA256SUMS`, never from Markdown. The install gate is not hardware approval.

### Where things are

[ADR 0040](docs/decisions/0040-repository-layout.md): `app/` (soundtapd and the web UI, `app/VERSION`),
`os/` (the Buildroot external tree, `os/VERSION`; `os/support/` checkers, `os/tests/` tests),
`bco/` (the ST20's BCO app and WiLD core), `docs/`, and `tools/` (CI entry points in `tools/ci/`,
`tools/release/`, bench scripts in `tools/hil/`, `tools/recovery/`), which ships nothing.
Production work lands on `main`.

### Accepted scope decisions

- Go is the daemon language; one daemon on :80 ([ADR 0001](docs/decisions/0001-soundtapd-one-go-daemon.md)).
- Apple Home through AirPlay 2; no Google Cast, Google Home, Sonos or Sendspin
  ([ADR 0003](docs/decisions/0003-apple-home-via-airplay-no-cast-no-sonos.md)).
- One universal STU (ADR 0008); mainline Linux and our own bootloaders (ADR 0009); A/B slots with boot
  confirmation (ADR 0010); Buildroot as the only pipeline (ADR 0011).
- soundtapd is the only DAC writer (ADR 0012); multiroom through Snapcast, stereo pairs as groups
  (ADR 0013, [ADR 0107](docs/decisions/0107-stereo-pair-acts-as-one-speaker.md)); Spotify only through
  librespot (ADR 0014).
- Shell runs the boot and recovery paths; decisions live in Go
  ([ADR 0115](docs/decisions/0115-shell-only-for-boot.md)); soundtapd supervises the media daemons
  ([ADR 0121](docs/decisions/0121-one-supervisor-per-process.md)).
- Updates between SoundTap versions are signed (ADR 0048); releases run on two lanes
  ([ADR 0035](docs/decisions/0035-releases.md)).
- AUX and Bluetooth routing remain unproven work, not production features.
