# NOTICE — third-party and proprietary content

This repository builds a firmware image (`Update.stu`) for Bose SoundTouch speakers.
It necessarily includes content this project does **not** own. This file records what
that content is and under what terms it is here. The GNU General Public License v3.0 in
`LICENSE` covers this project's own source in the repository root: the tools, docs, build
scripts and workflows. `app/LICENSE` (the same licence) covers the SoundTap application
source. Neither covers the items below. Every file in the repository says which licence it is
under and whose it is, in an SPDX header or in [`REUSE.toml`](REUSE.toml), and `LICENSES/`
holds the text of each licence named ([REUSE](https://reuse.software); `make lint-reuse`).

## Trademarks

SoundTap is an independent project, not affiliated with, endorsed by or sponsored by Bose
Corporation. *Bose* and *SoundTouch* are trademarks of Bose Corporation, used only to identify
the speakers SoundTap runs on. *Spotify* and *TuneIn* and their logos are trademarks of their
owners. SoundTap is not affiliated with either; the logos only mark where their content plays
([ADR 0123](docs/decisions/0123-service-logos-and-tunein.md)).

## Bose

The repository carries no Bose bytes. The images carry none either, with one exception by
design: the four WiLink `wl18xx-conf.bin*` files are generated with TI's own tool from TI's
defaults and come out byte-identical to the ones on a stock speaker (ADR 0047, ADR 0049).
Each speaker's AIC3256 codec script is copied from that speaker's own stock firmware at boot
(ADR 0012), never shipped.

## Third-party components in the image

Every package in the image, by its Buildroot name. The exact source of each, with its patches,
is in the `SoundTap-Core-X.Y.Z-sources.tar` published next to each release (`make legal-info`;
`legal-info/manifest.csv` there has the version and licence files). `make legal-info` fails
when a package in that manifest is missing from these tables.

Built by Buildroot from upstream recipes:

| Package | Licence |
|---|---|
| alsa-lib | LGPL-2.1+ (library), GPL-2.0+ (aserver) |
| alsa-utils | GPL-2.0 |
| avahi | LGPL-2.1+ |
| bluez5_utils | GPL-2.0+, LGPL-2.1+ |
| bluez-alsa | MIT |
| boost | BSL-1.0 |
| busybox | GPL-2.0, bzip2-1.0.6 |
| ca-certificates | GPL-2.0+ (script), MPL-2.0 (data) |
| dbus | AFL-2.1 or GPL-2.0+ |
| dropbear | MIT, BSD-2-Clause, public domain (alpha images only) |
| expat | MIT |
| flac | Xiph BSD-like (libFLAC, the only part installed), GPL-2.0+ (tools), LGPL-2.1+ (other libraries) |
| gcc-final | GCC runtime libraries: GPL-3.0+ with the GCC Runtime Library Exception |
| hostapd | BSD-3-Clause |
| ifupdown-scripts | GPL-2.0-or-later |
| initscripts | GPL-2.0-or-later |
| iw | ISC |
| libconfig | LGPL-2.1+ |
| libdaemon | LGPL-2.1+ |
| libffi | MIT |
| libgcrypt | LGPL-2.1+ |
| libglib2 | LGPL-2.1+ |
| libgpg-error | GPL-2.0+, LGPL-2.1+ |
| libmnl | LGPL-2.1+ |
| libnftnl | GPL-2.0+ |
| libnl | LGPL-2.1+ |
| libopenssl | Apache-2.0 |
| libplist | LGPL-2.1+ |
| libsodium | ISC |
| libsoxr | LGPL-2.1+ |
| libubootenv | LGPL-2.1+, MIT, CC0-1.0 |
| libyaml | MIT |
| libzlib | Zlib |
| linux | GPL-2.0 (the kernel, with this project's patches and ST10 audio driver) |
| linux-headers | GPL-2.0 (kernel headers the C library is built against) |
| linux-firmware | TI WiLink 8 (wl18xx) firmware, TI's redistribution licence (LICENCE.ti-connectivity) |
| mtd | GPL-2.0 |
| musl | MIT |
| ncurses | MIT with advertising clause |
| nftables | GPL-2.0 |
| pcre2 | BSD-3-Clause |
| popt | MIT |
| readline | GPL-3.0+ |
| sbc | LGPL-2.1+ |
| urandom-scripts | GPL-2.0-or-later |
| util-linux | GPL-2.0+, LGPL-2.1+, BSD and public-domain parts |
| wireless-regdb | ISC |
| wpa_supplicant | BSD-3-Clause |

Built from recipes in `os/package/` from upstream sources:

| Package | What | Licence |
|---|---|---|
| soundtap-ffmpeg | FFmpeg, audio-only build | LGPL-2.1+, libjpeg licence |
| soundtap-librespot | librespot, the Spotify Connect receiver | MIT |
| soundtap-shairport-sync | shairport-sync, the AirPlay receiver | MIT, BSD-3-Clause |
| soundtap-nqptp | nqptp, the AirPlay 2 clock | GPL-2.0 |
| soundtap-snapcast | Snapcast, multiroom | GPL-3.0-or-later, MIT (bundled headers) |
| soundtap-plistutil | libplist's plistutil | LGPL-2.1+ |
| soundtap-wl18xx-conf | TI 18xx-ti-utils, builds the WiLink `wl18xx-conf.bin` files | BSD-3-Clause |
| soundtap-bootloaders | U-Boot (SPL and U-Boot) with this project's patches | GPL-2.0+ |

Also built into the image:

- **TIInit_11.8.32.bts**, the WiLink Bluetooth init script, built into the kernel image; TI's
  licence, provenance in `os/upstream/ti-connectivity/`.
- **TweetNaCl** 20140427 (Bernstein, van Gastel, Janssen, Lange, Schwabe, Smetsers), the
  ed25519 check built into U-Boot for the USB-stick recovery; public domain,
  `os/bootloader/uboot/tweetnacl.c` and `tweetnacl.h` as fetched from tweetnacl.cr.yp.to, with
  their hashes in [ADR 0124](docs/decisions/0124-third-party-code.md).
- **Fonts.** The web UI and the browser installer use Montserrat (`@fontsource-variable/montserrat`; the installer's copy and its licence are in `docs/assets/fonts/`); the ST20's OLED
  atlas is rendered from Plus Jakarta Sans, Silkscreen, Manrope, Noto Sans Thai and Source Han
  Sans (Noto Sans CJK). All are under the SIL Open Font License 1.1; the atlas's notices are in
  `app/oled/screen/assets/OFL.txt`.
- **The Go runtime and standard library**, compiled into soundtapd by Buildroot's Go
  toolchain (the version `SOUNDTAP_GO_VERSION` in the `Makefile` pins): BSD-3-Clause, the
  `LICENSE` of the Go distribution, which the sources tar carries with the toolchain's source.
- **The Rust standard library**, compiled into librespot by Buildroot's Rust toolchain: MIT or
  Apache-2.0, with its source in the sources tar.
- **Web UI dependencies**, bundled into soundtapd: `app/frontend/THIRD_PARTY_LICENSES.txt`.
- **Go modules** in soundtapd: the table below.

Go modules compiled into soundtapd, pinned in `app/go.mod` and its `go.sum`
([ADR 0124](docs/decisions/0124-third-party-code.md)). Each one has a row with its SPDX licence
(several separated by commas); `make lint-go-licenses` fails when a module compiled in is missing
here, its licence file says otherwise, or the licence is not compatible with GPL-3.0-only.
Their source is vendored in `app/vendor/`, so it is in the sources tar with soundtapd's. Paho is
offered under the EPL-2.0 or the Eclipse Distribution License 1.0 (the BSD-3-Clause text);
soundtapd takes it under the latter.

| Go module | Licence |
|---|---|
| github.com/eclipse/paho.mqtt.golang | EPL-2.0 OR BSD-3-Clause |
| github.com/godbus/dbus/v5 | BSD-2-Clause |
| github.com/gorilla/websocket | BSD-2-Clause |
| golang.org/x/net | BSD-3-Clause |
| golang.org/x/sync | BSD-3-Clause |

## This project's own packages

soundtapd (`app/`), soundtap-runtime (`os/overlay/`), soundtap-aic3256-loader
(`os/drivers/`) and the ST20 BCO app (`bco/`) are this project's source under GPL-3.0-only
(the loader GPL-2.0-only), archived in the sources tar at their repository paths. What goes
into another project's build takes that project's licence: the ST10 audio driver and the
device trees GPL-2.0-only like Linux, and the U-Boot sources and patches in
`os/bootloader/uboot/` GPL-2.0-or-later like U-Boot.
