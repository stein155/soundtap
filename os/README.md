# SoundTap Core

Everything that boots: SPL and U-Boot, Linux 6.12, drivers and the root filesystem, for every
supported speaker in one image. This directory is a Buildroot external tree
(`BR2_EXTERNAL=os`); the Buildroot source is the `buildroot/` submodule at the repository root.

```sh
make os         # build it in Docker: output/os/images
make stu        # package it as the universal STU, with the install dry runs
```

`VERSION` is the SoundTap Core version (ADR 0035). The app the image carries comes from `../app`.

| Directory | What |
| --- | --- |
| `configs/soundtap_defconfig` | the one profile (ADR 0011) |
| `board/image` | the kernel fragment, every model's kernel and device tree in `/boot`, image checks |
| `board/rootfs` | busybox fragment, device table, kernel patches, UBI layout, kept modules |
| `board/common` | device trees and global patches |
| `package` | SoundTap's Buildroot packages: soundtapd, web UI, audio, bootloaders, firmware blobs |
| `overlay` | the root filesystem overlay: init script, Wi-Fi, Bluetooth, ALSA |
| `bootloader` | U-Boot 2026.07 patches, the WLA device tree, the reference builds and the ROM-USB loader |
| `drivers` | codec loaders and the McASP machine driver source |
| `installer` | the STU packager, the installer module that runs on the speaker, and their checks |
| `upstream` | upstream firmware blobs, with provenance |
| `stock` | reverse-engineering provenance: the register of analysed stock sources; original stock inputs stay outside git |
| `container` | the pinned builder image |

## Boot

U-Boot starts the model's kernel from the selected slot (`/boot/uImage-<variant>`), then
BusyBox runs `/etc/inittab` and `/etc/init.d/soundtap`. The init script mounts the persistent
partition at `/mnt/nv` (a tmpfs in degraded mode if it cannot), brings up codec, amplifier and
Wi-Fi or the setup AP, and starts the required system services and soundtapd. The init
script supervises soundtapd; soundtapd supervises shairport-sync, librespot, nqptp, snapserver
and snapclient (ADR 0121). Bluetooth keeps its own bounded boot-script lifecycle.

soundtapd is the sole DAC writer. Sources enter its mixer through the source bus; the mixer
writes the local output and copies its pre-volume mix to Snapcast when followers exist.
A grouped follower receives the leader's stream through snapclient into that same bus.
Local playback does not depend on routing every sample through snapserver (ADR 0012/0013).

## Gates

`build-stu.yml` runs lint, the app tests and `os/tests` (`tools/ci/run-ci.sh`), and builds the
image on a pull request that touches `os/`; `build.yml` builds the image and the STU and runs
the install gate on every model (`tools/ci/run-build.sh`). QEMU cannot prove module probing,
radio, audio, keys, LEDs or A/B activation: those are the hardware checks in [`docs/hardware/hardware-test.md`](../docs/hardware/hardware-test.md).
