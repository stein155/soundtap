# 0009. Mainline Linux and SoundTap's own SPL and U-Boot

Status: accepted — 2026-11-20

## Context

SoundTap's userland on Bose's 3.14 kernel could only ever be as maintainable as a vendor kernel nobody
updates, and the package would have to carry Bose's kernel, which neither the repository nor the
STU may hold ([ADR 0049](0049-no-stock-bytes.md)).

A separate boot chain per model means every boot fix is made more than once, and the model whose
chain differs is the least tested. The project asked for the ST20 to differ from the ST10 only in
its BCO firmware. The ST10 and the Wireless Link ship byte-identical stock SPL and U-Boot (the same
sm2 module), so the WLA's DDR setup applies to the ST10 unchanged. The ST20 mask ROM boots NAND
before USB, so its SPL must try the micro-B USB port first to keep a way back. The WLA ROM loads at
most 0x7428 bytes of SPL, so anything shared must fit there.

Stock has one SPL in mtd0 and one U-Boot in mtd4. A power cut while mtd4 is written leaves a valid
SPL in front of a torn U-Boot: the ROM never looks further, and the USB-stick update lives in the
U-Boot that was just lost. On the ST20 only the busy pad, inside the speaker, gets the ROM to USB,
and the project ruled that out as a recovery for owners.

## Decision

### Mainline Linux, SoundTap's own bootloaders, one chain on every model

Speakers run mainline Linux (6.12 line) with SoundTap's own device trees and drivers (`os/bootloader/`,
`os/drivers/`). The bootloader is mainline U-Boot and its SPL, built by the project. Every model ends in the
same shape: SoundTap's own SPL, U-Boot, kernel and rootfs, and no Bose boot code kept as a fallback.

- One SPL source for all models. It enables the clocks and starts a 120 s watchdog on every model;
  only on the ST20 (SYSBOOT 0x12) does it restart the BCO and try USB ether before NAND. It builds
  twice: `wla-spl` (NAND only) and `st20-spl` (with USB ether), because the USB ether stack does
  not fit the WLA ROM's limit.
- One U-Boot (`wla-uboot`) for all models at `NAND.U-Boot`+0, with the redundant environment in
  NAND, `bootcmd` selecting the slot and the boot count falling back to the other one
  ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). The board code sets the model (`rt_v`,
  `rt_m`), so the default recipe loads that model's device tree and kernel; the model choice lives
  in C rather than in `bootcmd`, which keeps the default environment small.
- The installer and soundtapd treat every model alike: the same slot selection, recipes, boot
  confirmation and way back to stock (a stock package through `/api/firmware`, or on a stick).
- SoundTap's U-Boot carries the owner's recovery, a USB stick at power-on
  ([ADR 0130](0130-usb-stick-recovery.md)). With Bose's file on it, SoundTap's U-Boot writes Bose's SPL
  and U-Boot itself, in the order below; on the ST20 it also paints
  the first boot frame ([ADR 0131](0131-st20-boot-frame.md)).

### A spare U-Boot, and the first copy written last

mtd4 holds two copies of u-boot.img, at offset 0 and at `SecondUBootOffset` (block 8 of 15). Its
last block, 14, holds the redundant environment copy, so the first copy has blocks 0-7 and the
spare blocks 8-13. That limits u-boot.img to 0xc0000 bytes (`maxUBootSize`), and every write of a
U-Boot copy erases its own blocks and nothing past them.

The SPL loads the first copy and falls back to the spare. A CRC mismatch of the loaded data counts
as a load failure, because a cut leaves a good header in front of bad data. The SPL also prefers
the spare when the first copy checks out but is not SoundTap's (its uImage name is not SoundTap's) and the
spare is, so SoundTap's SPL never runs Bose's U-Boot, a pairing nobody has proven. A first copy that is
not SoundTap's still boots when there is no SoundTap spare.

Both installers, soundtapd and the module that runs on stock firmware, write the spare, then SPL
copies 1, 2, 3 and 0, then the first U-Boot, and verify each by reading it back. soundtapd leaves
alone every slot that already holds its bytes. The module first checks that the U-Boot booting now
(Bose's, in the first copy) ends before the spare's blocks, counted past bad blocks from its uImage
header, and refuses the boot chain otherwise, or when the first copy holds no uImage. Each U-Boot
copy is written only into its own half of `NAND.U-Boot`: one that bad blocks leave no room for
fails at the end of its half and never reaches the other copy's blocks.

The way back to stock writes in its own order: the first U-Boot copy first, while SoundTap's SPL and
spare still stand behind it, then Bose's SPL (backups before mtd0), and the spare last, which
Bose's chain never reads. No step of either route runs SoundTap's SPL with Bose's U-Boot.

### Repairing a damaged boot chain over ROM USB

The USB stick lives in SoundTap's U-Boot, so it cannot repair a damaged SPL or U-Boot. Below it is the
AM335x mask ROM, which boots over USB on every model (the ST20 only with its busy pad held) and can
never be flashed away. One development tool,
`tools/recovery/rom-usb-restore.py --model st10|st20|wla|st300`, takes the release's images (a built `output/os/images` with its
`SHA256SUMS`, or the release STU) and:

- builds a RAM U-Boot per session bound to one blob (`WLA_UBOOT_NAND_SCRIPT=0xLENGTH:crc32`): its
  bootcmd fetches `soundtap-rom-usb`, refuses it unless length and crc32 match, and `source`s the
  script image at its front;
- has the ROM load the RAM SPL of that build on the sm2 models (ST10, WLA, ST300) and the
  release's own ST20 SPL on the ST20, whose DDR differs; both ask USB for U-Boot;
- writes the boot chain in the installers' order, each range erased whole, then written one block
  at a time, every block read back and checked against its crc32, and all of them once more at the
  end; the environment is left alone unless `--erase-env` erases both copies;
- refuses before the first erase when a block it would program carries a bad-block marker,
  because U-Boot's `nand write` and `nand read` skip bad blocks and would move the first copy into
  the spare;
- restores partitions from the board's own `nand-backup-ssh.sh` backup with `--backup` and
  `--partition`; stock bytes reach the board from that backup only. A backup's `NAND.SPL*` and
  `NAND.U-Boot` go back only with `--backup-only`, U-Boot first and `NAND.SPL` last;
- prints all of it with `--dry-run` and touches nothing.

Unlike an install, the tool writes every slot even when it already holds the bytes: it repairs a
chain nobody trusts. How to run it per model is in
[`docs/hardware/install-records.md`](../hardware/install-records.md#repairing-the-boot-chain-over-rom-usb).

Alternatives that lost: keeping the Bose kernel (a dead end, and the package would carry Bose's
files); keeping stock bootloaders as a fallback (more code paths to test); a separate ST20 chain
with fixes ported to it (the drift between models this replaces); one SPL binary for all models
(the USB ether stack does not fit the WLA ROM limit); one ROM-USB build per write range (a power
cycle, and on the ST20 a busy-pad hold, per range); a generic RAM U-Boot that sources any script
(the crc32 compiled in keeps the one build that writes NAND bound to exactly one blob).

## Consequences

- One kernel body serves every model; device trees differ. A boot fix lands once.
- Drivers Bose had as out-of-tree modules (audio, IR, keypad) are SoundTap's own and are proven per model on
  hardware.
- With no Bose fallback copy, a bad bootloader means ROM-USB recovery, so every bootloader change
  is RAM-booted first.
- At every moment of an install a whole chain is on NAND: until the SPLs are done the old first
  U-Boot is untouched, and once they are, the new SPL has the verified spare, which it boots while
  the first copy is torn or Bose's.
- A board with a bad block under the boot chain's data is refused by the ROM-USB tool rather than
  written around.
- Hardware proof is in [`docs/hardware/hardware-test.md`](../hardware/hardware-test.md): the per-model ROM-USB
  restore rows are open; the ST300's ROM was seen on USB (an `AM335x` device, 2026-11-28), its
  restore not. The SPL telling
  the two U-Boots apart, and a first install from stock interrupted between SoundTap's SPL and the first
  copy, have no hardware run.

## Code

- `os/bootloader/build-wla-product-spl.sh`, `os/bootloader/build-wla-uboot.sh`,
  `os/package/soundtap-bootloaders/build-variant.sh`: the SPL and U-Boot builds.
- `os/bootloader/uboot/uboot-2026.07-spl-board.patch`: the shared SPL (watchdog, ST20 BCO restart
  and USB first); `os/bootloader/uboot/uboot-2026.07-spl-second-uboot.patch`: the spare and its
  CRC check.
- `app/internal/install/bootloaders.go`: `SecondUBootOffset`, `maxUBootSize`, `bootSlots`,
  `stockBootSlots`, `install.WriteBootloaders`.
- `os/installer/module/installer.c`: the module's boot-chain order.
- `tools/recovery/rom-usb-restore.py` and `tools/recovery/nand-backup-ssh.sh`: the ROM-USB repair.
- `os/tests/boot/test_spl_second_uboot.py` (the SPL's choice on the host),
  `app/internal/install/bootloaders_internal_test.go` (every cut during soundtapd's order) and
  `os/tests/installer/test_wla_installer_offline.py` (every cut in the module's order).
