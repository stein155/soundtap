# 0128. A slot block that fails to erase or program is marked bad

Status: accepted — 2026-11-20

## Context

NAND blocks wear out, and the datasheet's answer to a block whose erase or program reports FAIL
is to retire it. Without that, a speaker with one worn-out block in its inactive slot could never
take a first install from stock or a USB stick: the writer would give up at the first block that
failed. A mark is for good on these boards: there is no on-flash bad-block table, the marker is
in the block's own spare area, and only an expert's `nand scrub` takes it away. Marking a good
block by mistake loses it for ever.

Two writers in this repository write a slot without `ubiformat`: the installer that runs on stock
firmware (`os/installer/module`) and the USB-stick recovery in SoundTap's U-Boot
([ADR 0130](0130-usb-stick-recovery.md)).

## Decision

Both writers mark a block bad under the same rules.

- **Only on the NAND's own verdict.** A block is marked only when the driver fails its erase or
  its program with `EIO`, the chip's FAIL status: `MEMERASE` or `write()` on the stock kernel,
  `mtd_erase` or `mtd_write` in U-Boot. In U-Boot an erase counts only when `fail_addr` names
  that block, so a write-protected chip, which fails every erase with `EIO` and names none,
  marks nothing. Any other error (a timeout, `EROFS`, `EINVAL`), a failed read (ECC errors
  included) and a readback or CRC that differs never mark: they fail the write. A mark that
  does not take fails it too.
- **Only in the slot being written.** The installer marks only in `NAND.Kernel-Ping/Pong` and
  `NAND.File-System-Ping/Pong`; U-Boot only inside the rootfs slot it writes. The SPL copies,
  `NAND.U-Boot` and `NAND.U-Boot-Env` are read at fixed places by the ROM, the SPL and U-Boot:
  a failure there refuses the write and marks nothing. `NAND.Persistent-Storage`,
  `NAND.Update-Staging` and `NAND.Manufacturing-Data` are never written by an install, and the
  installer refuses to mark them by name all the same.
- **Bounded.** At most 2 blocks per partition and 3 per install; the stick writes one partition
  and stops at 2. A healthy chip almost never retires a block during an install; more than that
  in one write is a driver, timing or power fault, and marking is what would make it permanent. One
  failure past the limit stops the install before anything selects the new slot, and the running
  slot boots. Factory marks do not count.
- **Written around where the layout allows it.** A block that fails to erase is skipped. When a
  program fails, what this write had put into that block is written again, from RAM, into the
  next good block, erased first, and the write goes on there, as `nandwrite -m` and `ubiformat`
  do. Every reader of the slot skips bad blocks (UBI, U-Boot's `nand read`, the readback), so
  the image reads the same. The kernel's first block is the exception: the installer writes it
  last, behind data already on flash, so it cannot move; it is marked and the install fails with
  the slot unbootable. The next install starts the kernel one block later.
- **Logged.** Every mark names the partition, the block, its offset, the operation and the
  error, with the counts so far.

soundtapd keeps `ubiformat`'s own rules for the rootfs (it tortures a block after a failed
program before it marks it); its kernel and bootloader writes mark nothing.

Alternatives that lost: marking nothing (no mark can be wrong, but one worn block stops every
install); torturing a block before marking it, as `ubiformat` does (it writes patterns into a
block that just failed, in an installer that runs on stock with no way back but the old slot; the
datasheet retires a block on a FAIL status, and the limits bound what that costs); `ubiformat`'s
limit of four bad blocks in a row (it allows any number spread over a chip, and one install has
no reason to retire that many).

## Consequences

- A worn block in the inactive slot does not block a first install or a stick; a chip that fails
  more than the limits still does, with the old slot booting.
- The rule is proven on the host only. A program or erase failure cannot be provoked on a
  speaker without damaging it, so no row of
  [`docs/hardware/hardware-test.md`](../hardware/hardware-test.md) can prove it; the install
  rows keep proving the path where nothing fails. nandsim's `badblocks=`
  only sets factory marks, so layer 1 of the install gate covers the skipping, not the marking.

## Code

- `os/installer/module/mtd.c`, `os/installer/module/mtd.h`: `mtd_allow_mark_bad`, the limits
  `MTD_MARK_BAD_PER_PARTITION` and `MTD_MARK_BAD_PER_RUN`.
- `os/bootloader/uboot/uboot-2026.07-usb-recovery.patch`: the stick's write and its limit
  `RT_MARK_BAD_MAX`.
- `os/tests/installer/test_wla_installer_offline.py` (`TestBadBlockMarking`,
  `TestMarkBadPartitions`): the installer against its file backend with failures injected per
  block; `os/tests/boot/test_uboot_usb_recovery.py` (`UsbRecoveryBadBlocks`): the stick's write
  against a fake NAND.
