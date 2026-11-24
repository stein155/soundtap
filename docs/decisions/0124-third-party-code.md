# 0124. Third-party code: libraries are welcome, Bose files never

Status: accepted — 2026-11-20

## Context

The one hard line on where code comes from is [ADR 0049](0049-no-stock-bytes.md): no Bose files
in the repository or the package. Everything else the image runs comes from upstream projects,
pinned by Buildroot ([ADR 0011](0011-one-buildroot-pipeline.md)), and the frontend bundles npm
packages. Holding soundtapd to the Go standard library made the project write and maintain
protocol code of its own (an MQTT client, a D-Bus client) that maintained libraries already do,
and it was a habit, never a project requirement. A GPL firmware image cannot leave licences and
pinning to chance, and the speakers are small 32-bit ARM boards with a fixed NAND rootfs slot.

## Decision

### Bose files never; other third-party code under five rules

A library, module or binary that contains Bose bytes or needs them is refused, whatever its
licence. Other third-party code is welcome in every part of the project (the Go daemon, the
frontend, the image, the tools) when it meets all of these:

- **Licence.** Compatible with GPL-3.0-only, soundtapd's licence, and recorded in
  [`NOTICE.md`](../../NOTICE.md) in the same PR: permissive licences (MIT, BSD, ISC, Apache-2.0,
  Zlib, BSL-1.0), MPL-2.0, LGPL, and GPL-2.0-or-later or GPL-3.0. GPL-2.0-only is not compatible.
  A row may be an SPDX choice (`EPL-2.0 OR BSD-3-Clause`); it passes when one alternative is
  compatible, and soundtapd takes that one.
- **Pinned.** An exact version with a hash: `app/go.mod` and `go.sum`, `package-lock.json`
  integrities, version and hash in the Buildroot package. Updates are deliberate PRs, not floating
  ranges.
- **Maintained.** Recent releases, issues answered, no open security advisory without a fix. A
  small library that is clearly finished can pass without recent activity.
- **Reasonable size.** What it adds fits the speakers' binary and rootfs. A large framework for
  one function loses.
- **It fixes a real problem.** A maintained library beats in-house code when it fixes something
  ours does wrong or would have to grow into: a protocol we only partly speak, a format we parse by
  hand. A library for what a few lines of the standard library already do is not that.

soundtapd stays a static binary built with `CGO_ENABLED=0`, so a Go module must be pure Go;
linking a C library into soundtapd would be a build-pipeline change with its own ADR. The image
builds soundtapd offline with `-mod=vendor`, so modules are vendored in `app/vendor/`, byte for
byte as `go.mod` and `go.sum` say.

### Every file says whose it is and under what licence

The repository follows the [REUSE specification](https://reuse.software) 3.3. Our source files
carry `SPDX-License-Identifier` and `SPDX-FileCopyrightText` tags (the form is in
[`CONTRIBUTING.md`](../../CONTRIBUTING.md)): GPL-3.0-only, except code built into another project,
which takes that project's licence (the kernel driver and device trees GPL-2.0-only, U-Boot
GPL-2.0-or-later). Files that cannot carry a comment, generated files and third-party files are
annotated by path in `REUSE.toml`; third-party files keep their bytes and their own licence, and
a patch takes the licence of the project it patches. Everything SPL and U-Boot are built from
stays byte-identical, because the bootloaders are written to a speaker only when they differ
([ADR 0008](0008-one-universal-stu.md)) and a licence header must not make every speaker rewrite
its boot chain. `LICENSES/` holds the text of every licence named. What ships is listed in
`NOTICE.md`, and `make legal-info` collects the image's licence texts and sources.

### Vendored files are recorded and kept byte-identical

Code copied into the repository rather than fetched by a package manager is listed here with its
source, version, licence and the SHA-256 of each file as taken. It stays byte-identical; our
changes go around it, not into it.

**TweetNaCl, the ed25519 check written for U-Boot's USB-stick recovery.** U-Boot verified
`Update.stu.sig` with it until 2026-11-24; the stick now checks no signature
([ADR 0130](0130-usb-stick-recovery.md)), U-Boot no longer compiles it, and the files remain
for the host-built verifier test (`app/internal/release/uboot_test.go`). Mainline U-Boot has
no ed25519.

- **Source:** TweetNaCl by Bernstein, van Gastel, Janssen, Lange, Schwabe and Smetsers,
  <https://tweetnacl.cr.yp.to/20140427/tweetnacl.c> and `.../tweetnacl.h`, version 20140427.
- **Files**, in `os/bootloader/uboot/`:
  - `tweetnacl.c` sha256 `02e65bc3013ff2168983365e55906bc783c4c7e0a60d8100f17bb303a17175c4`
  - `tweetnacl.h` sha256 `43f29ad721d9927b747b0100ab4160c119e7bb180c7c98a66e4bf79d31244287`
- **Licence:** public domain (the authors' statement on tweetnacl.cr.yp.to), which fits U-Boot's
  GPL-2.0+ and this project's licences.
- **What is used:** `crypto_sign_open` and the SHA-512 it calls; the linker drops the rest. The
  build copies it next to `board.c` with our `soundtap-sig.c`, which parses the statement.
- **Why this one:** about 800 lines with its own SHA-512, no allocation, no tables, no
  dependencies, and finished, which the maintained rule allows for a small library that is done;
  its speed does not matter for one statement at power-on.
- **Where it differs from Go:** TweetNaCl accepts a signature whose S is not reduced below the
  group order, and `crypto/ed25519` refuses it. `soundtap-sig.c` refuses it first, so both accept
  exactly the same signatures.
- **Compiler:** it shifts negative limbs left, which C leaves undefined and GCC, which builds
  U-Boot, defines as the arithmetic shift the code expects.

Alternatives that lost: the Go standard library only (a small supply chain, but protocol code of
our own that maintained libraries already do); no rule at all (licence compatibility and pinning
are not optional for a GPL firmware image); Monocypher for the U-Boot check (faster, but ten times
the code for a check that runs once).

## Consequences

- soundtapd may import Go modules. Each one compiled into it has a row in `NOTICE.md`'s Go module
  table, checked against its licence file.
- The MQTT bridge runs on Eclipse Paho ([ADR 0110](0110-mqtt-bridge.md)), with gorilla/websocket,
  golang.org/x/net/proxy and golang.org/x/sync/semaphore, which Paho imports unconditionally.
  D-Bus goes through godbus ([ADR 0114](0114-bluetooth-agent-in-soundtapd.md),
  [ADR 0043](0043-mdns-through-avahi.md)); it requires golang.org/x/sys, so `app/vendor/` keeps its
  `unix` package although nothing of it is compiled for the speakers, and the repository lints skip
  `app/vendor/`.
- Every dependency is supply chain we trust. Pinning and review keep that in check, and
  [ADR 0113](0113-threat-model.md) says so instead of claiming a standard-library-only daemon.
- A new source file without a licence header, or a licence without its text, fails the build's
  lint.

## Code

- `tools/ci/go_licenses.py` (`make lint-go-licenses`): each vendored Go module's licence against
  the allowed list and its `NOTICE.md` row; `tools/ci/npm_licenses.py` (`make lint-npm-licenses`)
  for the frontend.
- `make lint-go-vendor` holds `app/vendor/` to `app/go.mod` and `app/go.sum`;
  `os/package/soundtapd/soundtapd.mk` builds with `CGO_ENABLED=0` and `-mod=vendor` and lists the
  modules' licence files for `make legal-info`.
- `REUSE.toml`, `LICENSES/`; `make lint-reuse` runs `reuse lint` from a pinned image.
- `os/bootloader/uboot/tweetnacl.c`, `os/bootloader/uboot/soundtap-sig.c`;
  `os/tests/boot/test_uboot_usb_recovery.py` holds TweetNaCl to the hashes above, and
  `app/internal/release/uboot_test.go` requires the same verdict as `release.Verify` on every
  statement it signs and every statement one edit away.
