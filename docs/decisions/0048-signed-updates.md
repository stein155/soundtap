# 0048. A release the speaker fetches installs signed without a press; every upload waits for one

Status: accepted — 2026-11-25

## Context

A speaker takes new code on several routes: the firmware upload (`/api/firmware`), the app
upload (`/api/soundtapd`), the releases it fetches itself ([ADR 0118](0118-update-button-without-press.md))
and the USB stick its U-Boot reads at power-on ([ADR 0009](0009-mainline-linux-own-bootloaders.md)).
The STU's structure and CRCs, and an upload's ELF and build metadata, say nothing about who
built the code. Anyone who can reach a speaker on the network can reach these routes.

The STU route streams the root filesystem into the spare slot, and a 30 MB package must not
sit in RAM twice on a 128 MB board, so the decision about a package has to be taken before
its first byte is written. Its version lives inside that root filesystem, out of reach until
it is written.

The owner of a speaker is the root of trust for that speaker: an owner who builds SoundTap
from source (reproducibly, [ADR 0011](0011-one-buildroot-pipeline.md)) should be able to run
that build without the maintainer's key and without weakening anyone else's speaker. An owner
who wants Bose's firmware back has Bose's file but no SoundTap signature over it. What anyone
on the network cannot do is hold a key on the speaker ([ADR 0055](0055-household-token-and-windows.md)).

## Decision

One rule, the same for an alpha and a release, on an alpha and a production image:

- **Fetched by the speaker**: a release the speaker downloads itself installs **without a
  press**, and only when its `.sig` verifies against a release key: `POST /api/update` with or
  without a tag (an older release included), `POST /api/core-update`, Home Assistant and MQTT.
  Once a release is out, updating takes one confirmation in the web interface. After a successful
  `POST /api/core-update` (the slot written and selected, the report saved) the speaker
  restarts into the new slot on its own; it never restarts after a failure, and the boot
  confirmation of [ADR 0010](0010-ab-slots-boot-confirm-rollback.md) keeps the way back.
- **Uploaded**: every upload (`POST /api/firmware?install=1`, `POST /api/soundtapd`, and
  `DELETE /api/soundtapd`) waits for **one press** on the speaker
  ([ADR 0055](0055-household-token-and-windows.md)), signed or not. A `.sig` that comes with
  it is still verified, and the version rules below still hold. An unsigned package (an own
  build, Bose's file through the `stock` route) installs on that press, or from the USB stick,
  where holding the keys at power-on is the authorization ([ADR 0130](0130-usb-stick-recovery.md)).
  Nothing automatic, fetched or started from the network alone installs one.
- A `.sig` that does **not** verify is refused (403), never taken as unsigned.

Why an upload waits even with a valid `.sig`: the `.sig` of any public release can be sent
with another body. The STU route streams the body into the spare slot, the one the speaker
falls back to, and can compare the digest only once the stream ends; it then erases what it
wrote. Without the press anyone on the LAN could wipe the fallback slot again and again.

Every other check holds on both: the STU's structure and CRCs, the model, the slot that is not
running, the Bose-module guard. Each route's policy below is the contract; there is no route
around the signature or, for an unsigned package, the press on the speaker.

### Release keys

Two ed25519 release keys: the active one (`release.PublicKey`), which signs every release, and
a reserve (`release.ReservePublicKey`), which signs nothing until it replaces the active key.
The active private half, under its passphrase, is a secret of the GitHub Actions environment
`release`, limited to `main`, where `sign.yml` runs `tools/release/sign-release.sh` to sign and
publish ([ADR 0035](0035-releases.md)); the release workflow builds the draft assets without a
key. The reserve's private half stays offline with the maintainer, apart from the active one. Both public halves are compiled into
soundtapd, which accepts a statement signed by either and skips an empty one. U-Boot checks
no statement ([ADR 0130](0130-usb-stick-recovery.md)); the C verifier and key header in
`os/bootloader/uboot/` are no longer compiled into it and remain only for the tests that hold
them to `release.PublicKeys`. A speaker trusts no other key.

### The signed statement

`tools/release/sign.sh` publishes `<asset>.sig` beside each asset, one line of text:

```text
soundtap-package v1 kind=core profile=production version=0.1.0 sha256=<64 hex> sig=<base64>
```

- `kind` is `core` (a SoundTap Core STU) or `app` (a soundtapd binary).
- `profile` is `production` (a release, an alpha release included; `sign-release.sh` signs
  only these) or `alpha` (a bench build, `sign.sh`'s default).
- `version` is `MAJOR.MINOR.PATCH` in decimal digits, `MAJOR.MINOR.PATCH-alpha.N` for an alpha
  ([ADR 0035](0035-releases.md); an alpha is older than the release of its number), or `stock`
  for a Bose package signed for the way back (core only). `sign.sh` takes it from
  `SOUNDTAP_PACKAGE_VERSION`, by default `os/VERSION` or `app/VERSION`; `sign-release.sh` takes
  it from the tag.
- `sha256` is the asset's SHA-256 in lowercase hex.
- `sig` is the ed25519 signature, padded standard base64, over the exact bytes before ` sig=`.

The fields come in that order, separated by single spaces, with at most one trailing LF or
CRLF. `release.Verify` parses it that strictly, and U-Boot's C reader
(`os/bootloader/uboot/soundtap-sig.c`, with TweetNaCl's ed25519,
[ADR 0124](0124-third-party-code.md)) is held to the same verdict on every statement Go signs
and every statement one edit away from one. A `.sig` that is not such a statement is refused
on every image. Signing the digest inside the statement keeps the check streaming: the
handler hashes the body as it reads it once. Kind, profile and version share the one
signature with the digest, so none of them can be lifted onto another file or cut off.

### Policy per route

Uploads receive the statement in `X-SoundTap-Signature`; the UI fetches a release's `.sig`
or takes it alongside a manual upload, and sends none when the owner picks a `.stu` alone.
Three rules hold for every signed statement:

- **Kind.** A route takes only its own kind.
- **Profile.** A statement that verifies is a signature on every image, whichever its profile:
  an upload takes `alpha` and `production` alike. A route that fetches a release from GitHub
  takes only `production`, which every release, alphas included, is signed as.
- **Version.** An older signed version installs only when the caller names it: the upload with
  `X-SoundTap-Confirm: downgrade` (and the press), `POST /api/update` with its tag (no press).
  The tagless update and Home Assistant never go back, and `POST /api/core-update` takes only
  the newest release.

An unsigned upload has no kind, profile or version to hold it to: the press is the whole
check of where it came from, and everything else still holds (the STU's structure and CRCs,
the model, the slot that is not running, Bose's modules only for `stock`).

Before the first write soundtapd compares the signed version with `/etc/soundtap/os-version`
and requires the matching `X-SoundTap-Confirm`: `install` for the same or a newer version,
`downgrade` for an older one, `stock` for a package signed as `stock`. Any other value is
refused with 428 before the body is read. The check of a package (`POST /api/firmware`
without `install=1`) reports `version` and `confirm`, so the Updates page shows the matching
warning and sends that confirmation.

| Route | Kind | Profile | Newer | Same | Older | `stock` | Unsigned |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `POST /api/firmware?install=1` (upload; always the press) | core | any | `install` | `install` | `downgrade` | `stock` | yes, any image: `install`, or `stock` for Bose's file |
| `POST /api/core-update` (no press, restarts itself after a successful install, [ADR 0118](0118-update-button-without-press.md)) | core | production | newest release only, with `install` | no | no | no | no |
| `POST /api/soundtapd` (upload; always the press) | app | any | `soundtapd` | `soundtapd` | `downgrade` | no | yes, any image: `soundtapd` |
| `POST /api/update` without a tag, Home Assistant (no press, [ADR 0118](0118-update-button-without-press.md)) | app | production | yes | no | no | no | no |
| `POST /api/update` with a tag (no press, [ADR 0118](0118-update-button-without-press.md)) | app | production | yes | no (current) | yes | no | no |
| USB stick, `Update.stu` (gesture at power-on, U-Boot; no statement, [ADR 0130](0130-usb-stick-recovery.md)) | any | any | yes | yes | yes | yes (Bose's file for this model) | yes |

"Press" is the confirmation on the speaker of [ADR 0055](0055-household-token-and-windows.md).
Every upload waits for it, whether or not its `.sig` verifies (`needsPress` in
`internal/web`); the handler then verifies the statement, if any, and holds the file's digest
to it. The routes where the speaker fetches the release itself never wait for it.
The release routes also require the statement's version to be the tag's (`os-vX.Y.Z` or
`app-vX.Y.Z` signed as `X.Y.Z`).

The STU route verifies the signed digest's statement before it reads the body, and the
streamed digest must match before the new slot is selected. The signature authenticates the
candidate, not every chunk: a replayed statement with other bytes can still overwrite the
spare slot before the mismatch shows, and the slot is then not selected. That is why an
upload waits for the press even when its statement verifies: only someone at the speaker can
start one. A fetched release comes from the project's releases, not from the caller.

**The USB stick** needs no statement ([ADR 0130](0130-usb-stick-recovery.md)). Signatures guard
what can be started automatically or from the network, where anyone on it can reach the web UI;
the stick is started by someone holding the speaker's keys at power-on, as stock's was. It takes
any SoundTap package and Bose's own file for the board, and checks only that the file arrived
whole: every header and module CRC, and the same SHA-256 over two reads of the stick, the second
from the very buffers written. The slot is selected only when both match.

What U-Boot shows when it refuses a stick is in [ADR 0009](0009-mainline-linux-own-bootloaders.md).

### Unsigned packages: an upload and the press

An own build, a `build.yml` run artifact or any other `.stu` without a `.sig` installs through
`POST /api/firmware?install=1` on every image, alpha and production; an unsigned soundtapd
through `POST /api/soundtapd`. Both routes wait for the press before the handler reads a byte,
signed or not (`needsPress`), so nothing automatic and nothing started from the network alone
installs one.
An unsigned upload has no version to judge and needs the plain confirmation (`install`, or
`soundtapd` for the app); the check of an STU reports that, and the Updates page says before
it asks that the press is what installs the file. soundtapd logs the SHA-256 of every unsigned
package it installs.

- Bose's file is never a side effect of `install`: a module of Bose's in an unsigned upload
  under `install` is refused before its payload is written, and an upload that scanned as
  Bose's firmware answers 428, naming `stock`.
- A route that fetches its package (`POST /api/core-update`, `POST /api/update`, Home
  Assistant) refuses one without a statement (`InstallStream`, `admitApp`).
- `/mnt/nv/soundtap/allow-unsigned`, the alpha-only switch this replaced, is read by nothing;
  soundtapd removes it at every start ([nv-state](../contributors/nv-state.md)).

### The first install

The first install from stock goes through Bose's updater on `:17008`, which checks only the
STU's structure, length and CRC; its trust is the owner's hands on the speaker. The update
feed the installers name (`updates/soundtouch` on `main`) is held to a signed release outside
the speaker: code-owner review, and `tools/release/feed_check.py`, which requires the index to
name a published, not withdrawn Core release asset whose `.sig` verifies
([RELEASING.md](../../RELEASING.md#the-first-install-feed)). The preview feed
(`updates/soundtouch-preview`) is held the same way to the release's preview package, which
carries its own `.sig` in this format ([ADR 0133](0133-more-soundtouch-models-from-stock.md)).

### Bose's own firmware installs without our signature, behind the press

`POST /api/firmware?install=1` takes an upload without `X-SoundTap-Signature` when it carries
`X-SoundTap-Confirm: stock`; the route already waits for the press. The way back to stock then
works as on stock, with Bose's own file. No list of stock hashes and no published stock
signatures are kept.

- Only Bose's modules reach NAND. A `soundtap-*` module, or one of our native chain, is
  refused when the scan reaches it, before its payload is written; after the scan a SoundTap
  kernel is refused before the kernel and the bootloaders are written.
- The check of such an upload reports version and confirmation `stock`.
- A signed `stock` package still installs.

The kernel's module loading, which the signed root filesystem covers, is
[ADR 0132](0132-modules-locked-after-boot.md).

Alternatives that lost: a bare signature over the digest without kind, profile and version (a
truncated `.sig` carried no version, and one signature was valid on both routes); a fallback
to an unsigned digest; an owner's own signing key, set on the Updates page behind the press and
kept in the U-Boot environment (built, then retired on 2026-11-25: the press already
authorizes an upload, and a key added a second root of trust, key handling for every owner and
an environment write, for no route the press does not cover; an install's slot selection
removes the variable `soundtap_owner_key` a build before 0.1.0 left); a second compiled key per
owner (every owner would need their own U-Boot and soundtapd build); `allow-unsigned` as the
owner's route (alpha-only, SSH-only, and it proves nothing about who built the package);
published stock signatures or a list of stock hashes for the way back (an owner has Bose's
file, not our signature over it).

## Consequences

- Someone on the network cannot install anything by upload without the press on the
  speaker, signed or not, and cannot wipe the fallback slot with a replayed `.sig`.
- Without a press, anyone on the network can have the speaker fetch and install a published
  release, older ones included (`POST /api/update` with a tag): a signed app release with a
  bug a newer one fixed can be put back by anyone on the LAN. The owner accepted this
  (2026-11-25); withdrawing a release takes it off the list the speaker fetches from, and a key
  rotation stops it everywhere.
- *Update* on the Updates page, confirmed once, installs a newer release: the app restarts itself, and after a
  successful Core install the speaker restarts into the new slot on its own.
- With the press, any package that passes the STU's checks installs unsigned: the press on the
  speaker is what stops a stranger's own build and Bose's firmware, and a household member who
  confirms a request they did not start has let it in.
- Rotation makes the reserve the active key and adds a new reserve, in a release signed with
  the old reserve, which every speaker already trusts; this works whether the active key is
  retired, leaked or lost. Only the loss of both keys needs a route outside the signed
  updater. Revoking a key takes effect only where the software that carries the list is
  replaced: the app override and each Core slot. U-Boot holds no keys, so a change of keys
  does not change `u-boot.img`.
  [RELEASING.md](../../RELEASING.md#release-keys) has the procedures and their limits.
- A stick needs `Update.stu` alone, of either profile ([ADR 0130](0130-usb-stick-recovery.md)).
- The version is only as precise as `os/VERSION`: development builds all say the same version
  and install over each other as "the same".
- An owner goes back to Bose with a browser, Bose's file and the press, or with Bose's file on a
  stick and the chord at power-on. Over the network the press is the only thing that stops a
  file shaped like Bose's firmware with other content, and such a file writes only the spare slot
  and the bootloaders stock carries. On the ST20, Bose's software
  puts its own BCO app back when it finds ours (seen by the owner;
  [BCO recovery](../hardware/st20/bco-recovery.md) stays the development route).
- The active release key is held only as an encrypted secret of the protected `release`
  environment, which one job on `main` reads; it is never a repository secret and never on a
  build host. The reserve key is held offline.

## Code

- `app/internal/release/release.go`: `release.PublicKey`, `release.ReservePublicKey`,
  `release.PublicKeys`, `release.Statement`, `release.Sign`, `release.Verify`,
  `release.Statement.Matches`, `release.StockVersion`.
- `app/internal/release/uboot_test.go`: Go and U-Boot's reader give the same verdict.
- `app/internal/web/firmware/signature.go`: `signatureBeforeWrite`, `checkSignature`,
  `unsignedStock`, `boseFirmware`, `errNeedsStock`; `app/internal/web/firmware/firmware_install.go`:
  `installRequest.unsigned`; `app/internal/web/firmware/firmware.go:InstallStream`, which refuses
  an unsigned stream and restarts the speaker after a successful install
  (`restartAfterInstall`); `app/internal/web/confirm.go:needsPress`.
- `app/internal/web/firmware/version.go`: `versionConfirm`, `versionRefusal`.
- `app/internal/web/firmware/soundtapd.go`: `soundtapdConfirm` (the app upload's policy).
- `app/cmd/soundtapd/factory_reset.go:removeRetiredFiles` (`allow-unsigned`);
  `app/internal/flash/bootselect.go:retiredOwnerKey`, which `SelectBootSlot` removes.
- `app/internal/update/update.go:admitApp` and `app/internal/update/update.go:OpenCore`: the
  release routes' policy.
- `os/bootloader/uboot/uboot-2026.07-usb-recovery.patch`: the stick, which checks no
  statement; `os/bootloader/uboot/soundtap-sig.c` and `soundtap-keys.h` are not compiled into
  U-Boot and remain for `app/internal/release/uboot_test.go`.
- `tools/release/sign.sh`, `tools/release/sign-release.sh`, `.github/workflows/sign.yml`,
  `tools/release/feed_check.py`.
