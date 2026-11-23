# 0048. Every update is signed, and each install route says which signed packages it takes

Status: accepted — 2026-11-20

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
who wants Bose's firmware back has Bose's file but no SoundTap signature over it.

## Decision

Every package a running speaker or our U-Boot installs carries a signed statement, checked
before anything is written or run. Each route's policy below is the contract; there is no
route around the signature or the press on the speaker
([ADR 0055](0055-household-token-and-windows.md)).

### Release keys

Two ed25519 release keys: the active one (`release.PublicKey`), which signs every release, and
a reserve (`release.ReservePublicKey`), which signs nothing until it replaces the active key.
The active private half, under its passphrase, is a secret of the GitHub Actions environment
`release`, limited to `main`, where `sign.yml` runs `tools/release/sign-release.sh` to sign and
publish ([ADR 0035](0035-releases.md)); the release workflow builds the draft assets without a
key. The reserve's private half stays offline with the maintainer, apart from the active one. Both public halves are compiled into
soundtapd, which accepts a statement signed by either and skips an empty one. U-Boot carries
the same keys for the USB stick (`os/bootloader/uboot/soundtap-keys.h`, generated from
`release.PublicKeys`; a test fails when the two differ). A speaker can trust one more key, its
owner's ([below](#the-owners-own-key)).

### The signed statement

`tools/release/sign.sh` publishes `<asset>.sig` beside each asset, one line of text:

```text
soundtap-package v1 kind=core profile=production version=0.1.0 sha256=<64 hex> sig=<base64>
```

- `kind` is `core` (a SoundTap Core STU) or `app` (a soundtapd binary).
- `profile` is `production` (a release; `sign-release.sh` signs only these) or `alpha` (a
  bench build, `sign.sh`'s default).
- `version` is `MAJOR.MINOR.PATCH` in decimal digits, `MAJOR.MINOR.PATCH-alpha.N` for an alpha
  ([ADR 0035](0035-releases.md); an alpha is older than the release of its number), or `stock`
  for a Bose package signed for the way back (core only). `sign.sh` takes it from `SOUNDTAP_PACKAGE_VERSION`, by default
  `os/VERSION` or `app/VERSION`; `sign-release.sh` takes it from the tag.
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
or asks for it alongside a manual upload. Three rules hold on every route:

- **Kind.** A route takes only its own kind.
- **Profile.** A production image refuses `profile=alpha` everywhere (403); an alpha image
  takes both. A route that fetches a release from GitHub takes only `production`.
- **Version.** Nothing installs an older version without a deliberate act by someone at, or
  confirming on, the speaker. A route without a press never goes back.

Before the first write soundtapd compares the signed version with `/etc/soundtap/os-version`
and requires the matching `X-SoundTap-Confirm`: `install` for the same or a newer version,
`downgrade` for an older one, `stock` for a package signed as `stock`. Any other value is
refused with 428 before the body is read. The check of a package (`POST /api/firmware`
without `install=1`) reports `version` and `confirm`, so the Updates page shows the matching
warning and sends that confirmation.

| Route | Kind | Profile | Newer | Same | Older | `stock` |
| --- | --- | --- | --- | --- | --- | --- |
| `POST /api/firmware?install=1` (upload, press) | core | image's | `install` | `install` | `downgrade` | `stock` |
| `POST /api/core-update` (press, [ADR 0118](0118-update-button-without-press.md)) | core | production | newest release only, with `install` | no | no | no |
| `POST /api/soundtapd` (upload, press) | app | image's | `soundtapd` | `soundtapd` | `downgrade` | no |
| `POST /api/update` without a tag, Home Assistant (no press, [ADR 0118](0118-update-button-without-press.md)) | app | production | yes | no | no | no |
| `POST /api/update` with a tag (press, [ADR 0118](0118-update-button-without-press.md)) | app | production | yes | no (current) | yes | no |
| USB stick, `Update.stu` + `Update.stu.sig` (gesture at power-on, U-Boot) | core | image's | yes | yes | yes | no |

"Press" is the confirmation on the speaker of [ADR 0055](0055-household-token-and-windows.md).
The release routes also require the statement's version to be the tag's (`os-vX.Y.Z` or
`app-vX.Y.Z` signed as `X.Y.Z`).

The STU route verifies the signed digest's statement before it reads the body, and the
streamed digest must match before the new slot is selected. The signature authenticates the
candidate, not every chunk: a replayed statement with other bytes can still overwrite the
spare slot before the mismatch shows, and the slot is then not selected.

**The USB stick** reads the same statement from `Update.stu.sig` next to `Update.stu` (the
release's `.sig` renamed); the STU container stays the one the stock updater and
`/api/firmware` read. The chord held at power-on is the confirmation, so any version goes in,
older included, as long as the statement is a valid `core` statement for the image's profile,
signed by a compiled key or the owner key, and the file hashes to it:

- The image's profile is U-Boot's own, compiled in as `SOUNDTAP_UBOOT_PROFILE` from the
  build's `SOUNDTAP_PROFILE`; `post-image.sh` fails a build whose `u-boot.img` names another
  profile than the image.
- `stock` and unsigned packages are refused, Bose's file included: the stick writes only
  SoundTap's root filesystem, and at power-on nothing can show what is about to be written.
- The digest is checked twice: once over all of `Update.stu` before anything is erased, and
  again while writing, from the buffers written; the slot is selected only when both match.

What U-Boot shows when it refuses a stick is in [ADR 0009](0009-mainline-linux-own-bootloaders.md).

### Development builds and the first install

`build.yml` run artifacts are unsigned. They install only while
`/mnt/nv/soundtap/allow-unsigned` exists, which only an SSH session can create, and only on
an image whose `/etc/soundtap/profile` says `alpha`; an unsigned upload has no version and
needs the plain `install`.

The first install from stock goes through Bose's updater on `:17008`, which checks only the
STU's structure, length and CRC; its trust is the owner's hands on the speaker. The update
feed the installers name (`updates/soundtouch` on `main`) is held to a signed release outside
the speaker: code-owner review, and `tools/release/feed_check.py`, which requires the index to
name a published, not withdrawn Core release asset whose `.sig` verifies
([RELEASING.md](../../RELEASING.md#the-first-install-feed)).

### Bose's own firmware installs without our signature, behind the press

`POST /api/firmware?install=1` takes an upload without `X-SoundTap-Signature` when it carries
`X-SoundTap-Confirm: stock`; the route already waits for the press. The way back to stock then
works as on stock, with Bose's own file. No list of stock hashes and no published stock
signatures are kept.

- Only Bose's modules reach NAND. A `soundtap-*` module, or one of our native chain, is
  refused when the scan reaches it, before its payload is written; after the scan a SoundTap
  kernel is refused before the kernel and the bootloaders are written.
- The check of such an upload reports version and confirmation `stock`.
- Any other unsigned upload is refused; a signed `stock` package still installs.

### The owner's own key

1. **One more key, chosen per speaker.** An owner key is an ed25519 public key spelled as
   `release.PublicKey` is (32 raw bytes, padded standard base64, 44 characters), kept as the
   U-Boot environment variable `soundtap_owner_key` (`release.OwnerKeyEnv`) in both copies of
   the redundant environment ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). U-Boot reads
   it without UBI, soundtapd with `fw_printenv`. It is not on `/mnt/nv`
   ([nv-state](../contributors/nv-state.md)).
2. **Set and removed only behind the press.** `GET /api/owner-key` says whether one is set and
   its fingerprint (`release.Fingerprint`: the first 8 bytes of the SHA-256 of the raw key, in
   hex). `PUT` with `{"key": "<base64>"}` and `DELETE` wait for the press, take the operations
   lock ([ADR 0052](0052-soundtapd-lifecycle.md)) and write through
   `flash.Installer.SetEnvEverywhere`: the change once with `fw_setenv`, once more so that it
   lands in the other copy, then both copies read back. One `fw_setenv` writes only the copy
   that is not current, so a key set once would be lost, and one removed once would come
   back, when U-Boot falls back to the older copy.
3. **Nothing else sets or clears it.** A backup does not carry it and a restore cannot write
   it; `PUT /api/settings` refuses the field; the MQTT bridge cannot reach the code that writes
   the environment. A factory reset removes it, from the web page and from the front-panel
   chord alike, so a speaker that changes hands trusts the release keys alone again.
4. **Same rules as a release key.** `/api/firmware`, `/api/soundtapd` and U-Boot's stick
   verify against the release keys and the owner key (`release.TrustedKeys`,
   `st_trusted_keys` in `soundtap-sig.c`) under the table above, unchanged. An owner-signed
   `profile=production` statement installs on a production image: a production build of
   their own is what the owner means to run. The release routes (`/api/core-update`,
   `/api/update`, Home Assistant) fetch from this project's releases and take the release
   keys alone.
5. **A value that is not a key is no key.** Go and U-Boot read the variable alike: exactly 44
   characters of strict base64. A garbled, short or empty value, or a point of small order
   (the eight torsion points and the two non-canonical encodings `crypto/ed25519` accepts,
   against which a signature with S = 0 verifies for one message in four), is ignored and
   logged; the release keys keep working. `PUT` refuses such a value before anything is
   written.
6. **Signing.** `tools/release/sign.sh` signs with the key in `SOUNDTAP_OWNER_KEY_FILE` and
   prints its public half and fingerprint; `sign-release.sh` never uses it
   ([RELEASING.md](../../RELEASING.md#signing-your-own-builds)).

The kernel's module loading, which the signed root filesystem covers, is
[ADR 0132](0132-modules-locked-after-boot.md).

Alternatives that lost: a bare signature over the digest without kind, profile and version (a
truncated `.sig` carried no version, and one signature was valid on both routes); a fallback
to an unsigned digest; the owner key on `/mnt/nv` (U-Boot would need UBI and UBIFS to read
it, and a restore or a support bundle would be one step from it); a second compiled key per
owner (every owner would need their own U-Boot and soundtapd build); `allow-unsigned` as the
owner's route (alpha-only, SSH-only, and it proves nothing about who built the package);
published stock signatures or a list of stock hashes for the way back (an owner has Bose's
file, not our signature over it).

## Consequences

- Someone on the network needs a trusted private key, not just a reachable speaker, to
  install code. With the right confirmation value they can still pick an older signed release;
  the press on the speaker is what stops that.
- Rotation makes the reserve the active key and adds a new reserve, in a release signed with
  the old reserve, which every speaker already trusts; this works whether the active key is
  retired, leaked or lost. Only the loss of both keys needs a route outside the signed
  updater. Revoking a key takes effect only where the software that carries the list is
  replaced: the app override, each Core slot and U-Boot. A change of keys changes
  `u-boot.img`, so the Core release that carries it writes `NAND.U-Boot` on every speaker.
  [RELEASING.md](../../RELEASING.md#release-keys) has the procedures and their limits.
- A stick needs both files. A bench build signed alpha rescues only an alpha speaker, and
  switching a speaker between profiles writes `NAND.U-Boot`, since the two U-Boots differ in
  the profile word.
- The version is only as precise as `os/VERSION`: development builds all say the same version
  and install over each other as "the same".
- An owner goes back to Bose with a browser, Bose's file and the press. This is the only way
  back: the stick takes a signed SoundTap package and nothing else. The press is also the only
  thing that stops a file shaped like Bose's firmware with other content, and such a file
  writes only the spare slot and the bootloaders stock carries. The ST20's BCO keeps
  SoundTap's app and is restored separately ([BCO recovery](../hardware/st20/bco-recovery.md)).
- Whoever holds an owner's private key can do on that speaker what a release key can. Adding
  one needs the press; someone with the speaker in their hands could reach it anyway through
  the UART console or a ROM-USB boot. The key survives every update, the slot switch and a
  restore, and goes with a factory reset; a rebuilt environment (`RepairEnv`, after both
  copies tore) has none, which fails safe. The speaker does not check that the owner still
  holds the private half.
- U-Boot carries the statement reader, the small-order table and the key handling within its
  size limit ([ADR 0009](0009-mainline-linux-own-bootloaders.md)).
- The active release key is held only as an encrypted secret of the protected `release`
  environment, which one job on `main` reads; it is never a repository secret and never on a
  build host. The reserve key is held offline.

## Code

- `app/internal/release/release.go`: `release.PublicKey`, `release.ReservePublicKey`,
  `release.PublicKeys`, `release.Statement`, `release.Sign`, `release.Verify`,
  `release.Statement.Matches`, `release.StockVersion`.
- `app/internal/release/owner.go`: `release.OwnerKeyEnv`, `release.ParseOwnerKey`,
  `release.Fingerprint`, `release.TrustedKeys`.
- `app/internal/release/uboot_test.go`, `app/internal/release/uboot_owner_test.go`: Go and
  U-Boot's reader give the same verdict.
- `app/internal/web/firmware/signature.go`: `signatureBeforeWrite`, `checkSignature`,
  `unsignedStock`, `boseFirmware`, `allowUnsigned`.
- `app/internal/web/firmware/version.go`: `versionConfirm`, `versionRefusal`.
- `app/internal/web/firmware/soundtapd.go`: `soundtapdConfirm` (the app upload's policy).
- `app/internal/web/firmware/ownerkey.go`: `OwnerKeyPath`, `putOwnerKey`, `deleteOwnerKey`,
  `ClearOwnerKey`; `app/internal/flash/envvar.go:SetEnvEverywhere`.
- `app/internal/update/update.go:admitApp` and `app/internal/update/update.go:OpenCore`: the
  release routes' policy.
- `os/bootloader/uboot/soundtap-sig.c`, `os/bootloader/uboot/soundtap-keys.h`,
  `os/bootloader/uboot/uboot-2026.07-usb-recovery.patch`: the stick's check.
- `os/board/image/post-image.sh`: U-Boot's profile must be the image's.
- `tools/release/sign.sh`, `tools/release/sign-release.sh`, `.github/workflows/sign.yml`,
  `tools/release/feed_check.py`.
