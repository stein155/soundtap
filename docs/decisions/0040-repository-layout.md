# 0040. Repository layout: app/, os/, bco/, docs/ and a slim tools/; Buildroot sees only os/

Status: accepted — 2026-10-07

## Context

SoundTap ships on two release lanes, SoundTap Core and the SoundTap app
([ADR 0035](0035-releases.md)), and is built from one Buildroot profile
([ADR 0011](0011-one-buildroot-pipeline.md)). A reader has to be able to tell from the tree what
ships and what does not, and which lane a file belongs to. Buildroot needs an external tree of its
own, and host tooling (CI checks, release scripts, bench and rescue scripts) must never end up in
the image.

## Decision

Every file belongs to one of four parts, each with an owner and a lifecycle, to a slim `tools/`
of host tooling that ships nothing, or to `releases/`, the record of what each Core release was
tested with:

| Directory | What it is |
| --- | --- |
| `app/` | The SoundTap app: `soundtapd` (Go) and its React frontend. `app/VERSION`. `app/oled/design/` holds the ST20 panel's design and the tools that bake its atlas and goldens. |
| `os/` | SoundTap Core, and the Buildroot external tree (`BR2_EXTERNAL=os`). `os/VERSION`. |
| `bco/` | The ST20's BCO network processor: our app (`app/`), WiLD core (`wild/`) and image packer (`tools/`). |
| `docs/` | The documentation and its site; `docs/tools/` makes the README's images and holds the MkDocs hook. |
| `releases/` | Each Core release's hardware reports and their evidence files, `releases/os-vX.Y.Z/`, as tested; `sign.yml` signs a release only against them ([ADR 0035](0035-releases.md)). Nothing in it ships. |
| `tools/` | Host tooling that delivers nothing into the image: `ci/` (the CI entry points and helpers: repository lints, licence checks, `buildroot-reuse.sh`, the fake speaker), `release/` (notes, approval, signing, the feed check and mock update server), `hil/` (bench scripts against a speaker), `recovery/` (NAND backup, the ROM-USB restore, its loader and read-back script) and `dev/` (the developer loop behind `make dev`). |

Nothing in `tools/` goes into the image or the STU: Buildroot, `make os` and `make image-check`
never read it. One check compares against a recovery tool without shipping it: the RAM-boot
U-Boot check reads the SPL hash `tools/recovery/rom-usb-loader` approves. The U-Boot environment
formats and slot recipes the installer must match live in `os/support/` (`uboot_env.py`,
`uboot_recipes.py`), where `make stu`'s stock-route dry run
(`os/support/wla_stock_route_dryrun.py`) and the tests use them. [`tools/README.md`](../../tools/README.md)
lists every tool.

Inside `os/`:

| Path | Content |
| --- | --- |
| `configs/soundtap_defconfig` | The one profile |
| `models.json` | The model table: what tells the models apart ([ADR 0008](0008-one-universal-stu.md)) |
| `board/common` | Device trees and shared patches |
| `board/image` | Kernel fragment, post-build and post-image steps for the universal image |
| `board/rootfs` | Rootfs files, kernel patches, UBI config, module list |
| `package/` | Our Buildroot packages |
| `overlay/` | Shared rootfs overlay (init scripts, ALSA, IR map) |
| `bootloader/` | SPL and U-Boot sources and the scripts that build them |
| `drivers/` | Our C drivers (AIC3256 loader, ST10 audio) |
| `installer/` | STU packager and the on-box installer module |
| `stock/` | Register of analysed stock sources; no stock firmware bytes |
| `container/` | The pinned builder image |
| `upstream/` | Upstream firmware blobs with their provenance |
| `support/` | What the image build, `make stu` and `make image-check` call: STU, rootfs, kernel-config, provenance and bootloader checkers, the reproducibility check; and the model table's Go generator |
| `tests/` | Tests of the shell layer, build and docs, one directory per subject (`boot/`, `wifi/`, `playback/`, `installer/`, `build/`, `docs/`, `hardware/`), and recorded hardware evidence (`fixtures/`) |

The profile is `soundtap_defconfig`, the build is `make os` into `output/os`, and the package is
`make stu`. The profile is named after the product because there is no second profile for it to be
told apart from.

Alternatives that lost: the Buildroot external tree at the repository root, beside the app, with
the system spread over several top-level directories (the tree then shows neither the two lanes nor
the one profile); one `tools/` for CI checks and hardware rescue scripts alike (it mixes what checks
a build with what touches a speaker).

## Consequences

- A reader can tell from the top level what ships (`app/`, `os/`, `bco/`) and what does not
  (`tools/`, `docs/`, `releases/`).
- Buildroot sees only `os/`. Packages that build from the rest of the repository say so with
  `$(BR2_EXTERNAL_SOUNDTAP_PATH)/../app`, which keeps that dependency visible in the `.mk`.
- Python under `os/support/` and `os/tests/` is imported through `sys.path`, never as a package:
  `os.support` would name the standard library's `os`. `make test-python` discovers `os/tests`, so a
  test module imports another as `boot.init_script`.
- The docs' own tests live in `os/tests/docs/` with the others, not under `docs/`: one rule for
  where a test of the shell layer, build or docs goes, one discovery root, and a site directory that
  holds pages.
- The Go module is `github.com/stein155/soundtap/app`: the repository's own path plus `app`, so the
  module path names where the source lives.
- ADRs and docs follow a move in the same PR. `os/tests/docs/test_doc_links.py` and
  `os/tests/docs/test_adr_code_refs.py` fail on a path that no longer exists.

## Code

- `os/external.desc`, `os/external.mk`, `os/Config.in`: the Buildroot external tree.
- `os/configs/soundtap_defconfig`: the one profile.
- `os/package/soundtapd/soundtapd.mk`: builds `app/` from outside the external tree.
- `os/tests/`, `os/support/`: tests and build-time checkers, imported through `sys.path`.
- `tools/ci/`, `tools/release/`, `tools/hil/`, `tools/recovery/`, `tools/dev/`: host tooling.
- `os/support/wla_stock_route_dryrun.py`: replays the installer against `os/support/uboot_recipes.py`.
- `tools/README.md`: what each tool is for.
