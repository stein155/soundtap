# 0123. Names and brands: the product is SoundTap; the Spotify and TuneIn logos and TuneIn's open endpoint stay

Status: accepted — 2026-11-21

## Context

The product's name runs through everything that names it: prose, identifiers, paths, host names,
HTTP headers, package and module names, and the names the bootloaders read or pass. A name that
changes later breaks bookmarks, paired apps and the boot ABI between bootloader and kernel.

The web UI draws the Spotify and TuneIn logos to mark the Spotify page, Spotify presets and TuneIn
stations, and the README screenshots show them. Radio search and every TuneIn preset use TuneIn's
OPML endpoint (`opml.radiotime.com`) without a partner id. Neither is a licence matter, and both
needed a maintainer decision before the repository went public.

## Decision

### The product is called SoundTap

Everything that names the product says SoundTap: "SoundTap" in prose, `soundtap` in identifiers,
paths and host names, `SOUNDTAP` in constants. Why: to be confirmed by the maintainer (why this
name, beyond the owner's ruling that it stays).

- The daemon is `soundtapd` (`app/cmd/soundtapd`, `/api/soundtapd`, `/usr/bin/soundtapd`).
- The host is `soundtap.local`, host names are `soundtap-<id>`, headers are `X-SoundTap-*`, and
  the mDNS service is `_soundtap._tcp`.
- State and runtime paths are `/mnt/nv/soundtap`, `/opt/soundtap`, `/run/soundtap`,
  `/etc/soundtap`, `/etc/init.d/soundtap` and `/tmp/soundtap*.log`.
- Buildroot packages, configs and symbols are `soundtap*` (`BR2_PACKAGE_SOUNDTAP*`), and STU
  module names are `soundtap-*`.
- The repository is `stein155/soundtap`, where development happens and releases are published,
  and the Go module path is `github.com/stein155/soundtap/app`. The release lanes, SoundTap Core
  and the SoundTap app, are named in [ADR 0035](0035-releases.md).
- **Boot ABI.** The kernel command-line keys `soundtap.nandroot`, `soundtap.watchdog` and
  `soundtap.ram`, the U-Boot environment entry `soundtap_args`, the serial markers
  `SOUNDTAP-UB:nn` and `SOUNDTAP-SPL:nn`, the TFTP names `soundtap-ram-nand.itb` and
  `soundtap-ram-capture.itb`, the U-Boot device tree `am335x-soundtap-wla`, and the STU module the
  USB recovery looks for (`soundtap-rootfs`). UBI volume, MTD partition and `NAND.*` names, and the
  files the bootloader loads from the rootfs (`/boot/uImage-*`, `*.dtb`), carry no product name.

No other name is accepted anywhere: there is no compatibility with an earlier working name.

### The services keep their own logos, and TuneIn is the one radio directory

The UI and the README show the Spotify and TuneIn logos, because they tell a listener at a glance
where a preset or playlist plays from. `NOTICE.md` names Spotify and TuneIn as trademarks of their
owners and says SoundTap is not affiliated with either. Radio search and TuneIn presets use
TuneIn's open OPML endpoint; when TuneIn refuses it, the last stream URLs that played are kept,
and standby asks again once a day. TuneIn stays the only radio directory, so there is one
directory to maintain.

Alternatives that lost: neutral icons instead of the logos (they no longer say where a station
plays from, and no owner of either mark has asked); a TuneIn partner id (not requested for the
first release); a second directory such as Radio Browser (two directories to maintain).

## Consequences

- New host tools and firmware ship together: a host tool speaks only the current headers and API
  paths.
- A new identifier, path, package or boot-ABI name uses `soundtap`; renaming any boot-ABI name is
  a bootloader change ([ADR 0009](0009-mainline-linux-own-bootloaders.md)).
- If Spotify or TuneIn objects to the logos, an app release replaces them with neutral icons;
  nothing on the speaker depends on them.
- If TuneIn blocks the endpoint, radio search and TuneIn presets stop until an app release changes
  the route. Presets keep playing their last stream URLs, and presets with the owner's own URL are
  not affected.

## Code

- `app/go.mod`: the module path; `app/cmd/soundtapd/identity.go:publishHostname`: the host name.
- `app/internal/mdns/mdns.go`: the mDNS name and service.
- `os/installer/build-stu.py` and `stu.Expected` (`app/internal/stu/profiles.go`): the
  `soundtap-*` STU modules.
- `os/bootloader/build-wla-uboot.sh`, `os/bootloader/uboot/uboot-2026.07-usb-recovery.patch`: the
  boot-ABI names on the U-Boot side.
- `app/frontend/src/components/atoms/Icon/sources.tsx`: `spotifyMark` and `tuneinMark`.
- `app/internal/tunein/tunein.go`: the OPML endpoint.
