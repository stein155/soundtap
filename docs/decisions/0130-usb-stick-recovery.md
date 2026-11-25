# 0130. A button chord at power-on installs Update.stu from USB, SoundTap's or Bose's, in our U-Boot

Status: accepted — 2026-11-20, changed 2026-11-24 (no signature; Bose's file goes back to stock)

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
([`docs/hardware/wla/buttons.md`](../hardware/wla/buttons.md)), and its micro-B is usb0, the
same controller the ST10 turns into a host for the stick. Stock's WLA U-Boot has no USB emergency update.

A running speaker accepts only signed SoundTap updates ([ADR 0048](0048-signed-updates.md)): those
come automatically or from the web UI, which anyone on the network can reach. A stick is different:
someone stands at the speaker and holds its keys while it powers on, as with stock. Project decision
2026-11-24: that is the authorization, and the stick is also the way back to Bose for an owner who
has nothing but the speaker and a stick.

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
- **No signature.** The chord is the authorization; `Update.stu.sig` is not needed. What the stick
  checks is that the file arrived whole: a `BOSE` container whose every header and module CRC
  checks out, read twice to the same SHA-256.
- **A SoundTap package: the rootfs, into the slot that is not booting.** Pass one checks the CRCs
  and hashes the whole file; nothing is erased before that. Pass two writes the slot the way
  `ubiformat` does (bad blocks skipped, empty pages left erased, every block read back, the rest of
  the slot erased), marking failing blocks under [ADR 0128](0128-bad-block-marking.md), and hashes
  the file again from the very buffers it writes. `bootcmd` moves only when both passes agree, with
  the rollback state in the same save ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). An
  environment that lacks a recipe `bootcmd` names gets the whole set for the board's model.
- **Bose's own file for this model: back to stock, bootloaders included, on every model.** A
  stock STU is taken when its module chain is one of Bose's known families (the ST10/WLA, ST20,
  ST300 and Preview packages, name for name) and the version tag of its root filesystem
  (`…@sm2/burns`, `…@scm/lisa`) names this board's module and stock variant; the ST10 takes the
  package Bose ships it and the Wireless Link alike, as soundtapd decides. Kernel and rootfs must
  fit their partitions, the kernel must be Bose's and the SPL and U-Boot valid. Only the kernel,
  rootfs, environment and bootloaders are written; the BCO, CC2540, STM32, host-product and
  soundbar-processor firmware in the package are left to Bose's own software. The SoundTouch 300
  starts it with the remote combo its processor passes on at power-on, as Bose's own USB update
  does; the Lifestyle 650 module has no gesture in our U-Boot. Before anything is written the stick checks that our U-Boot reads the SPL the ROM booted
  without an ECC error. Then, in order:
  1. Bose's kernel and root filesystem into the slot that is not booting, each read back.
  2. That slot selected with our rollback state (`altbootcmd` the old slot, `bootlimit` 4) and
     saved twice; our U-Boot boots it through its stock recipe.
  3. The environment moved to the 8 KiB copy Bose's U-Boot reads, read back.
  4. Bose's boot chain, only the copies that differ from NAND (or read back worn), each read back,
     in the way-back order of [ADR 0009](0009-mainline-linux-own-bootloaders.md) and
     `install/bootloaders.go`: first U-Boot copy, SPL 1, 2, 3, 0, the spare last, so our SPL never
     starts Bose's U-Boot and Bose's SPL never starts ours.
  5. Bose's own recipes in that environment, as `/api/firmware`'s way back writes them, then a
     reset into Bose's chain.

  A power cut before step 2 boots the old slot; after it, every state boots the new one except a
  cut while step 5 rewrites the environment's one block, which leaves Bose's U-Boot on its
  built-in environment, as on `/api/firmware`. The ST20's BCO is left alone: stock's own software
  rewrites it on its first start (seen by the owner).
- **Which slot.** The slot that boots is `altbootcmd`'s once the coming boot passes `bootlimit`,
  `bootcmd`'s otherwise; the stick writes the other one, and points both commands at the booting
  slot (saved) before it erases. A final save that fails resets the board, so it boots what was
  saved.
- **The watchdog.** The SPL starts WDT1; the recovery reloads it between 1 MiB chunks, only while
  the module is clocked, so a hang anywhere else still resets the board.
- **The boot always continues.** On success the new slot boots, on failure the old one. Linux gets
  the result as `soundtap.recovery=<rc>` on its command line (a Bose system ignores it): 0
  installed, -19 no stick, -2 no `Update.stu`, -8 a file that is not an STU or not a package for
  this speaker (the lamps say refused), -74 a CRC that does not check out or a stick that read
  back otherwise, -5 a read or NAND error, a readback that differs, or a third failing block.
- **What the owner sees.** On the ST10 the Wi-Fi lamp blinks once per megabyte read or written; a
  failed install blinks it fast ten times, a refused stick slowly three times, then the old slot
  boots. The ST20 shows nothing: its lamp pad is the BCO's clock, and the recovery does not drive
  the OLED.

Alternatives that lost: a signed package only, Bose's file refused (the decision until
2026-11-24: it left an owner without a browser and a working network no way back to Bose, and a
signature guards against what can be triggered from the network, not against someone holding the
speaker's keys); Bose's kernel and rootfs without its bootloaders (Bose would boot on our U-Boot,
half stock, until a later Bose update).

## Consequences

- One recovery on every model with buttons, one code path after the key test. The stick needs a
  micro-B OTG adapter on the ST10 and the Wireless Link.
- Two ways back to Bose: the Updates page (`/api/firmware`, Bose's file and the press) and the
  stick. The stick is the only route that writes bootloaders without soundtapd, so it is the one
  whose order matters most: a failure in the bootloader phase leaves ROM USB below it.
- Anyone who can hold a speaker's keys at power-on can install anything shaped like an STU on it,
  as with stock. The threat model accepts that ([ADR 0113](0113-threat-model.md)).
- On the SoundTouch 300 the stick writes Bose's SPL and U-Boot although no ROM-USB route is known
  there: a failed bootloader write on a bar has no way back. Project decision 2026-11-24, at the
  owner's risk, and unproven until a bar has run it. Its trigger is the remote combo, as for Bose's
  own USB update: that is enough, because a stick still has to be in the bar's USB port, so
  nobody away from the speaker can start it.
- A refused stick writes nothing. A stick that serves other bytes the second time leaves the spare
  slot written and not selected, as an interrupted `/api/firmware` does; the slot that boots is
  untouched.
- Verifying costs two hashing passes on top of reading the stick twice; the
  watchdog reload per 1 MiB read means only a chunk, never the whole install, has to fit in its
  timeout.
- VOLUME DOWN + PRESET 1 at power-on (USB update plus factory reset) is not implemented; the
  runtime chord covers the reset. One WLA button alone, or both held after boot, does nothing new.
- Do not change the physical keypad mapping U-Boot uses. The remote counts as a recovery trigger
  only on the ST300, and only because it starts the stick at power-on
  ([the contract](../../CONTRIBUTING.md#recovery-invariant)). A change here changes `u-boot.img`, so the next install writes
  `NAND.U-Boot` on every speaker.
- Hardware proof is the USB-stick rows per model in
  [`docs/hardware/hardware-test.md`](../hardware/hardware-test.md#open-hardware-checks). On the ST20 the keypad path
  and which controller a stick lands on are the least proven parts; its way back below them is ROM
  USB through the busy pad ([`docs/hardware/st20/rom-usb-recovery.md`](../hardware/st20/rom-usb-recovery.md)).

## Code

- `os/bootloader/uboot/uboot-2026.07-usb-recovery.patch`: the gesture, the host switch, the two
  passes, the SoundTap and the Bose install, the save and `soundtap.recovery`.
- `app/internal/hw/keypad.go`: the keypad scan the U-Boot one follows.
- `os/tests/boot/test_uboot_usb_recovery.py`: the copied numbers match their sources, a stick that
  does not check out selects nothing, and the Bose install writes in its order.
