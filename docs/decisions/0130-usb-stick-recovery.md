# 0130. A button chord at power-on installs a signed Update.stu from USB, in our U-Boot

Status: accepted — 2026-11-20

## Context

Stock U-Boot on the ST10 and ST20 samples the keypad at power-on. With VOLUME DOWN + PRESET 4
held it reads `Update.stu` from a FAT USB stick and installs it with the settings kept. That
gesture is the recovery an owner knows, and it goes away when `NAND.U-Boot` becomes ours
([ADR 0009](0009-mainline-linux-own-bootloaders.md)). The product ships one recovery; every other
route (ROM USB loaders, NAND restore scripts) is development tooling.

At runtime, stock maps VOLUME DOWN + PRESET 1..5 to factory reset, setup mode, Wi-Fi toggle, a
cloud update check and a test screen. A cloud update check has no source on SoundTap, so PRESET 4
gets the power-on behaviour, not the runtime one.

The Wireless Link has no preset keys. It has two buttons, Bluetooth and AUX
([`docs/hardware/wla/buttons.md`](../hardware/wla/buttons.md)), and its micro-B is usb0, the same controller the
ST10 turns into a host for the stick. Stock's WLA U-Boot has no USB emergency update.

A running speaker accepts only signed SoundTap updates ([ADR 0048](0048-signed-updates.md)), and a
stick is another way to put a rootfs on NAND.

## Decision

Our U-Boot (`wla-uboot`, shared by every model, the only build with USB storage and FAT) runs the
recovery at `EVT_LAST_STAGE_INIT`:

- **The gesture.** The model comes from the board (`rt_v`, from the SYSBOOT straps on the ST20 and
  the manufacturing partition on the others), never from the environment, which may be torn. On
  the ST10 and ST20 it scans the keypad as stock and soundtapd do and requires exactly
  VOLUME DOWN + PRESET 4; on the Wireless Link both buttons, a gesture the project chose for it.
  Why: to be confirmed by the maintainer (why both buttons on the Wireless Link).
  Either must hold on two sweeps 50 ms apart.
- **The port.** It rewrites usb0's `dr_mode` to `host` in the live control tree and binds the musb
  host driver by hand; the shared tree, the SPL and every other build keep `peripheral`. On the
  ST20, which routes the micro-B connector through `microbsel`, it tries low first, then high.
- **Only a signed SoundTap package.** `Update.stu.sig` (the release's `.sig`, renamed) lies next to
  `Update.stu`. Before anything is read from the package, the statement must verify against a
  release key compiled into U-Boot, name kind `core`, a SoundTap version (any, older included; not
  `stock`) and a profile this U-Boot takes: a production U-Boot takes only production, an alpha
  one both. Unsigned files, Bose's included, are refused; the way back to Bose is the Updates page.
- **Only the rootfs, into the slot that is not booting.** Pass one checks the header CRCs and
  hashes the whole file; nothing is erased unless that is the signed digest. Pass two writes the
  slot the way `ubiformat` does (bad blocks skipped, empty pages left erased, every block read back,
  the rest of the slot erased), marking failing blocks under
  [ADR 0128](0128-bad-block-marking.md), and hashes the file again from
  the very buffers it writes. `bootcmd` moves only when that digest is the signed one too, with the
  rollback state in the same save ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). An
  environment that lacks a recipe `bootcmd` names gets the whole set for the board's model.
- **The watchdog.** The SPL starts WDT1; the recovery reloads it between 1 MiB chunks, only while
  the module is clocked, so a hang anywhere else still resets the board.
- **The boot always continues.** On success the new slot boots, on failure the old one. Linux gets
  the result as `soundtap.recovery=<rc>` on its command line: 0 installed, -19 no stick, -2 no
  `Update.stu`, -126 no `Update.stu.sig`, -129 a statement refused or a file that does not hash to
  it, -74 a header that does not check out or a stick that read back otherwise, -5 a read or NAND
  error, a readback that differs, or a third failing block.
- **What the owner sees.** On the ST10 the Wi-Fi lamp blinks once per megabyte read or written; a
  failed install blinks it fast ten times, a refused stick slowly three times, then the old slot
  boots. The ST20 shows nothing: its lamp pad is the BCO's clock, and the recovery does not drive
  the OLED.

Alternatives that lost: accepting Bose's unsigned file on the stick (a stick would bypass the
signature every other route requires).

## Consequences

- One recovery on every model with buttons, one code path after the key test. The stick needs a
  micro-B OTG adapter on the ST10 and the Wireless Link.
- Bootloaders, kernels and stock packages are out of scope: a stick cannot replace U-Boot from
  U-Boot, and the way back to Bose is `/api/firmware` only.
- A refused stick writes nothing. A stick that serves other bytes the second time leaves the spare
  slot written and not selected, as an interrupted `/api/firmware` does; the slot that boots is
  untouched.
- Verifying costs two hashing passes and a signature check on top of reading the stick twice; the
  watchdog reload per 1 MiB read means only a chunk, never the whole install, has to fit in its
  timeout.
- VOLUME DOWN + PRESET 1 at power-on (USB update plus factory reset) is not implemented; the
  runtime chord covers the reset. One WLA button alone, or both held after boot, does nothing new.
- Do not change the physical keypad mapping U-Boot uses, and do not claim recovery from an IR
  remote ([the contract](../../CONTRIBUTING.md#recovery-invariant)). A change here changes `u-boot.img`, so the next install writes
  `NAND.U-Boot` on every speaker.
- Hardware proof is the signed-USB-stick rows per model in
  [`docs/hardware/hardware-test.md`](../hardware/hardware-test.md#open-hardware-checks). On the ST20 the keypad path
  and which controller a stick lands on are the least proven parts; its way back below them is ROM
  USB through the busy pad ([`docs/hardware/st20/rom-usb-recovery.md`](../hardware/st20/rom-usb-recovery.md)).

## Code

- `os/bootloader/uboot/uboot-2026.07-usb-recovery.patch`: the gesture, the host switch, the two
  passes, the save and `soundtap.recovery`.
- `os/bootloader/uboot/soundtap-sig.c` over `os/bootloader/uboot/tweetnacl.c`
  ([ADR 0124](0124-third-party-code.md)), with the keys in `os/bootloader/uboot/soundtap-keys.h`
  generated from `release.PublicKeys`.
- `app/internal/hw/keypad.go`: the keypad scan the U-Boot one follows.
- `os/tests/boot/test_uboot_usb_recovery.py`: the copied numbers match their sources and a refused
  stick selects nothing; `app/internal/release/uboot_test.go`: the verifier reads statements as
  soundtapd does.
