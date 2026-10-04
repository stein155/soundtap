# 0011. One Buildroot pipeline, with alpha and production profiles

Status: accepted — 2026-11-20

## Context

SoundTap ships as one fixed, read-only image for one product: 128 MiB of RAM, a root filesystem
slot of 96 MiB of flash, and four speaker models on the same AM335x chip family. An update replaces
the whole image, never a single package. The build has to be reproducible from pinned sources, must
carry no stock bytes ([ADR 0049](0049-no-stock-bytes.md)), and must yield the sources and licences
a public GPL release owes.

## Decision

Buildroot is the only way the image is built, with `os/` as its `BR2_EXTERNAL` tree.

### One profile, two release profiles

`os/configs/soundtap_defconfig` is the one profile: `make os` builds it into `output/os` and
`make stu` packages it. `make SOUNDTAP_PROFILE=alpha|production os` picks the release profile;
alpha is the default. Production merges `os/configs/production.fragment` over the defconfig (no
dropbear, no root login), post-build removes the rest of alpha's access and records the profile in
`/etc/soundtap/profile`, and `rootfs_check.py` fails a build that carries the other profile's
access ([ADR 0066](0066-production-hardening.md), [ADR 0119](0119-no-ssh-in-production.md)).

### Pins

`BUILDROOT_COMMIT` is the newest point release of Buildroot's LTS branch. Where a package needs a
newer release than the LTS carries, the `Makefile` overrides its version and keeps the hash under
`os/board/common/patches/<package>/`, with the reason next to it. Every action is pinned by full
commit SHA and every container image by digest; `make lint` holds both (`zizmor` next to
`actionlint`, and `make lint-image-pins`).

### The toolchain is built once and kept

`os/configs/soundtap_toolchain_defconfig` holds only the target and the toolchain (gcc, binutils,
musl, kernel headers). `make toolchain-build` builds it with the same Buildroot pin into
`.cache/toolchain/<key>` together with its legal-info; the key covers the Buildroot commit, that
defconfig and the builder image. `soundtap_defconfig` uses that tree as a custom external
toolchain, and `make legal-info` copies the toolchain's sources into `legal-info/toolchain/`. The
build host and CI keep `.cache/` between runs, so an image build starts with its packages instead
of with gcc. It is the way other firmware projects do it: Buildroot's manual describes the route
("Buildroot toolchain as external toolchain"), and SDK-based projects such as Nerves work the same
way.

### What is built outside Buildroot's downloads

- **The frontend.** `make web` runs `npm ci` and `npm run build` in a pinned Node image, and
  `soundtapd` embeds the result. `package-lock.json` pins the dependencies, and
  `app/frontend/THIRD_PARTY_LICENSES.txt` carries them into `make legal-info`.
- **Go modules are vendored** ([ADR 0124](0124-third-party-code.md)). `soundtapd` is a local
  package: Buildroot rsyncs `app/`, has no download step that could fetch modules, and builds with
  `-mod=vendor` and `GOPROXY=off`. `app/vendor/` is committed as `go mod vendor` writes it, so the
  build stays offline; `make lint-go-vendor` runs `go mod verify` and regenerates the directory,
  failing on any byte that differs, so `go.sum`'s hashes pin what is compiled. The vendored source
  sits under `app/`, so `make legal-info` archives it with soundtapd's.

### Reproducible

Two builds of one tree, each from an empty `output/os`, with the same `SOURCE_DATE_EPOCH` and
`SOUNDTAP_COMMIT`, give the same `output/os/images/SHA256SUMS` and the same STU. Both values
default to `HEAD` (commit date and hash), can be set on the command line, and
`make require-build-ids` refuses a build without them. What holds the bytes still:

- `BR2_REPRODUCIBLE`: `TZ`, `LANG`, `KBUILD_BUILD_TIMESTAMP/USER/HOST` and the file times of the
  root tree come from `SOURCE_DATE_EPOCH`; Go packages build with `-trimpath -buildvcs=false`;
  packages build with `GIT_DIR=.`, so no build reads the SoundTap checkout's git state.
- The kernel signs no modules: `CONFIG_MODULE_SIG` makes a new key in every build, and a fixed key
  in a public tree signs for anyone. The modules are covered as every other file of the root
  filesystem is, and init closes module loading once the boot has loaded them
  ([ADR 0048](0048-signed-updates.md)).
- `mkfs.ubifs` takes a fixed UUID and `--timestamp $(SOURCE_DATE_EPOCH)` for every inode, and
  writes directories and hard links in sorted order (our patches in
  `os/board/common/patches/mtd/`); `ubinize` takes a fixed image sequence number (`-Q`).
- `mkimage` stamps the uImages with `SOURCE_DATE_EPOCH`; the post scripts refuse to run without
  it. The bootloaders use their own fixed epoch (`os/bootloader/source-date-epoch`).
- librespot's build ID and build date come from `SOURCE_DATE_EPOCH`.
- The STU's installer is built in an image from one dated Debian snapshot, tagged with its
  Dockerfile's digest.

`make repro-check` builds twice and compares with `os/support/repro_compare.py`, which names each
image that differs and, for `rootfs.ubi`, each path of the packed tree.

### Where builds run

All CI runs on GitHub-hosted runners; there is no self-hosted runner.

| Build | Where | Why |
|---|---|---|
| Candidates and releases, with the install gate on every model | `build.yml` on a hosted x64 runner, on every push to `main` | An empty machine every run: a build depends on its commit and two caches, and the run records which commit gave which bytes |
| CI checks, including fork pull requests | `build-stu.yml` on hosted `ubuntu-24.04`, one job per suite | Isolated runners with read-only repository permissions |
| The OS build of a pull request that touches `os/`, `bco/`, the build or the installer | the `os` job of `build-stu.yml` | A broken image is red before the merge |
| Draft releases, both lanes | `release.yml` on hosted `ubuntu-24.04` | Publish only a commit CI passed on; signing stays offline ([ADR 0035](0035-releases.md)) |
| Weekly reproducibility check of `main` | `reproducible.yml` on hosted `ubuntu-24.04`; its second build starts without the compiler cache | Two full builds, too long for every merge |
| Bootloader and BCO app package checks, tool builds, the version watch | their own workflows on `ubuntu-24.04`; the bootloader package also on `ubuntu-24.04-arm` | The pinned bootloader sums were measured on arm64 and the package ships from x64; both must give them |
| Development candidates | `tools/ci/run-build.sh --reuse` on a build host or a laptop | A warm tree |

Every workflow calls the repository's own entry points, and so does a build host:
`tools/ci/run-ci.sh <step>` for the checks (each step one `make` target: `lint`, `web`, `go`,
`go-race`, `python`, `firewall`, `bco`, `vuln`; without arguments all of them side by side) and
`tools/ci/run-build.sh` for `make os`, `make stu`, the candidate in `dist/` and both layers of the
install gate. Nothing a run needs lives outside the repository.

A hosted runner starts without a Buildroot tree, so CI never calls `tools/ci/buildroot-reuse.sh`
(`run-build.sh --reuse` is for a build host). What carries over between runs is the download
directory, the toolchain and the compiler cache, through the composite action
`.github/actions/buildroot-cache`, which every Buildroot job shares. Only `main` saves them, right
after the build so a failure in a later step does not lose them; pull requests read main's caches.
To stay inside the repository's 10 GB cache budget, the action deletes every older entry of the
same kind after saving, and ccache is capped at 3 GB, dropping objects no build of the last week
used. The check jobs run in pinned containers and cache nothing. The runner is x64 because that has
`/dev/kvm` for the install gate; the prebuilt Go and the bootloaders' compiler are pinned for both
amd64 and arm64. Each run writes the minutes per package, the cache hit rate and the disk use into
its summary.

Builds do not run on the Pi: it has 16 KiB pages and limited disk, and cannot run 32-bit ARM
binaries.

Alternatives that lost:

- **Alpine.** Prebuilt packages look simpler to maintain, and size does not rule it out (all
  packages stock, without librespot and nqptp, which it does not carry for armv7, came to 145 MB;
  with our trimmed ffmpeg about 82 MB against 76 MB). It would save an estimated 25 to 30 of the
  files in `os/`, while the kernel, bootloaders, installer, drivers, ffmpeg, librespot, Snapcast and
  AirPlay stay ours to build. We would lose exact rebuilds, since Alpine keeps little beyond each
  package's newest version, and `make legal-info`. Rejected: little gain, and it reopens boot,
  Wi-Fi and audio on every model.
- **Yocto.** Heavier, aimed at many products and teams, no advantage for one product.
- **Debian.** Its base system alone is larger than this flash and RAM leave room for.

Reopen this if most of a cold build proves to sit in the base system, if the speaker has to update
single packages, or if a board with a very different chip joins.

## Consequences

- The toolchain is built only when its key changes; a hosted runner keeps it in `actions/cache`
  under the same key. A toolchain change (a new Buildroot pin, or an edit to
  `soundtap_toolchain_defconfig`) builds it once and then every package. On a hosted runner the
  toolchain and the compiler cache are what shorten a build. Build times and their targets are
  tracked in [`../owners/roadmap.md`](../owners/roadmap.md).
- Updates to the base system mean moving the Buildroot submodule, which stays on a stable line.
- One profile, one reuse key and one set of checks.
- A Go module change is `go get`, `go mod tidy` and `go mod vendor` in one commit.
- Anyone can rebuild a release from its commit and compare it with the published STU. Only the
  signature is not reproducible; it is added offline after the build ([ADR 0048](0048-signed-updates.md)).
- The reproducibility check costs two full builds a week. The toolchain is shared through its
  cache, so a toolchain that differs between builds is outside what the check sees.

## Code

- `os/configs/soundtap_defconfig`, `os/configs/production.fragment`,
  `os/configs/soundtap_toolchain_defconfig`: the profile, the production overlay, the toolchain.
- `os/board/rootfs/post-build.sh`: the profile file and alpha's access removed from production.
- `os/support/rootfs_check.py`: refuses a rootfs that carries the other profile's access.
- `os/board/common/patches/`: version overrides' hashes and our patches (`os/board/common/patches/mtd/` for the reproducible UBIFS).
- `os/board/image/post-image.sh`, `os/bootloader/source-date-epoch`: timestamps from `SOURCE_DATE_EPOCH`.
- `os/support/repro-check.sh`, `os/support/repro_compare.py`, `.github/workflows/reproducible.yml`: the reproducibility check.
- `app/vendor/`: the vendored Go modules; `os/package/soundtapd/`: the offline Go build.
- `os/container/`: the pinned builder image.
- `tools/ci/run-ci.sh`, `tools/ci/run-build.sh`, `tools/ci/buildroot-reuse.sh`: the entry points.
- `.github/workflows/build.yml`, `.github/workflows/build-stu.yml`, `.github/workflows/release.yml`,
  `.github/actions/buildroot-cache`: where builds run and what they cache.
