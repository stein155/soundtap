# 0049. Bose-free: no Bose files in the repository or the package

Status: accepted — 2026-11-15

## Context

SoundTap runs its own SPL, U-Boot, kernel, drivers and userland on every model
([ADR 0009](0009-mainline-linux-own-bootloaders.md)) and its own BCO app on the ST20
([ADR 0031](0031-bco-network-card.md)), so nothing SoundTap builds needs Bose's binaries. Bose's software
is not the project's to redistribute, and the repository is public. Installing SoundTap starts from a
speaker on Bose's software, and owners must be able to go back to it.

## Decision

- **Bose-free means two things:** the repository and the STU hold no Bose files, and installing
  SoundTap needs no original Bose STU. It does not say who has to compile an open-source
  component; whether the build takes a prebuilt upstream binary is a question for the build
  pipeline ([ADR 0011](0011-one-buildroot-pipeline.md)) and the third-party rules
  ([ADR 0124](0124-third-party-code.md)).
- Stock bytes are never committed. Local copies stay outside git (`SOUNDTAP_STOCK`). Stock-image
  analysis happens outside the repository; its findings go into docs as facts, registered in the
  [source register](../../os/stock/PROVENANCE.md), not into scripts or copied code.
- A binary SoundTap ships but does not compile comes from upstream with its source recorded, never from a
  Bose image ([ADR 0047](0047-wilink8-firmware-and-conf.md)).
- Two things the speaker already carries are used where they are, and are neither in the
  repository nor in the STU: the speaker's own codec script on the ST10 and ST20
  ([ADR 0012](0012-single-dac-writer.md)) and the ST20's BCO boot loaders BFL and BSL
  ([ADR 0031](0031-bco-network-card.md)).
- Installing from stock firmware and the way back to stock stay. An original Bose STU that an
  owner uploads to go back is their input, not part of SoundTap.
- The public repository's history starts from one clean commit of the tree, so it publishes no
  Bose bytes either.

Alternatives that lost: keeping the stock files in the repository for tests and tooling (simplest,
but keeps Bose bytes in the repository and the old STU shape alive); a hash gate that scans every
build against an index of stock file hashes (it catches only a whole file from one stock version,
nothing embedded or from another version, and was never the goal); a wider bar that every byte on
the board be built by the project from source (prebuilt upstream packages are fine).

## Consequences

- No build step scans for stock bytes. Keeping them out rests on review, on the rules in
  [the contract](../../CONTRIBUTING.md#bose-free) and on the provenance check for binaries.
- The four WiLink `wl18xx-conf.bin*` files in the image are byte-identical to stock by design:
  they are regenerated from text with TI's tool ([ADR 0047](0047-wilink8-firmware-and-conf.md)).
- Tooling that works only against a stock image does not live in the repository.

## Code

- `os/support/check_provenance.py` with `os/upstream/MANIFEST.json`: fails on a shipped binary
  without an upstream, generated or original source.
- `os/support/buildroot_wla_nand_check.py`: fails when anything under `os/stock` is installed into
  the image.
- `os/stock/PROVENANCE.md`: the register of analysed stock sources, without their bytes.
