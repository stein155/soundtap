# The ST10's recovery path, read out of its own bootloader

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Taken from `mtd4-U-Boot.oob` in `~/nand-backups/st10-rhino-20260919`, which is the
stock U-Boot captured from the speaker at 192.0.2.135. The addresses and behaviour below
describe that dump, not the SoundTap U-Boot installed later. No source-file hash was recorded
here; retain the backup identity when reproducing the analysis.

## The bootloader samples the buttons itself

`U-Boot 2014.07-05722-g93f9891` carries `Sample the button state`, `Button mask is %4.4x`,
`No buttons` and `PRESET 1`..`PRESET 6`. The sampler at `0x80101f44` returns a bitmask
and the printer at `0x801021dc` names each bit from a table at `0x801022b4`:

| bit | button | bit | button |
|---|---|---|---|
| 0 | VOLUME UP | 5 | PRESET 1 |
| 1 | PRESET 3 | 6 | PRESET 6 |
| 2 | POWER | 7 | PRESET 5 |
| 3 | VOLUME DOWN | 8 | PRESET 4 |
| 4 | PRESET 2 | 9 | SOURCE |

That order is the one `defaultKeymap` in `app/internal/hw/keymap.go` uses, with U-Boot packing
out the keypad matrix's empty rows: two independent sources agree on the ST10 keymap.

## What each combination does

The decision is at `0x8010777c`. It reads `variant`, and `rhino` takes neither the
`lisa`/`burns` branch nor `binky`'s, so it lands on the two-button tests:

| held at power-on | mask | prints | runs |
|---|---|---|---|
| **VOLUME DOWN + PRESET 4** | `0x108` | `Emergency Update M/S/T from USB - preserve...` | `sweup uboot preserve ; reset` |
| **VOLUME DOWN + PRESET 1** | `0x28` | `Emergency Update M/S/T from USB - factory reset...` | `sweup uboot ; reset` |
| nothing | `0` | — | the slot selector, below |
| anything else | — | `Unmapped keymask = %x for variant %s, no action` | normal boot |

`sweup` reads `Update.stu` from USB. The whole path lives in the bootloader, so **it needs
only SPL and U-Boot intact** — no serial console, no working kernel, no working root
filesystem.

For completeness, the Wireless Link has its own branch in the same function:
`Binky - factory default...` running `binky_setupap ; reset`, which is what a box with two
buttons and no presets can offer.

## Stock does select a slot

With no buttons held, U-Boot calls `0x80102c14` and uses its answer: **1** runs
`nand_boot2` (Pong) and **2** prints `Force boot from Primary partitions...` and runs
`nand_boot` (Ping). So `bootcmd=run nand_boot` in the environment is the fallback, not the
decision, and stock's A/B selection is a function in the bootloader rather than an
environment variable.

There is still no `bootcount`, `bootlimit` or `altbootcmd` anywhere in this U-Boot, so a
kernel that does not boot is not retried automatically. The button path is the retry.

## Scope of the stock recovery analysis

The stock USB-stick path depends on working SPL and U-Boot. It cannot repair damaged
mtd0–4. The ROM-USB measurements below establish a separate development recovery entry;
they do not by themselves prove a complete NAND restore for a new candidate.

SoundTap now installs its own SPL and U-Boot (ADR 0009). Its USB-stick path and stock-return
route are described in [the install records](../install-records.md#going-back-to-bose). Bootloader writes
still need the permission and board-specific recovery plan in [the contract](../../../CONTRIBUTING.md#recovery-invariant).

## Repairing the boot chain from the repo

`tools/recovery/rom-usb-restore.py --model st10` uses the entry measured below: the ROM takes
the RAM SPL of a U-Boot build bound to that run, the RAM U-Boot writes the release's SPL into all
four copies and its U-Boot into both halves of `NAND.U-Boot` (spare first, first copy last), and
`--backup` puts back partitions from this speaker's own `nand-backup-ssh.sh` backup. `--dry-run`
prints every range and touches nothing. Unproven on an ST10: everything past the ROM's
enumeration. See [Developer recovery tools](../install-records.md#developer-recovery-tools).

## Mask-ROM USB boot: proven on the ST10

```
11:17:38  Bus 003 Device 116: ID 0451:6141 Texas Instruments, Inc. AM335x USB
```

One second after power-on, cold, with NAND intact. The ST10's mask ROM enumerates as a
USB device and tries USB before NAND, exactly as the Wireless Link's does. That is the
recovery a bootloader replacement needs, and it is measured now rather than assumed.

With a host present USB wins and the speaker never reaches NAND, so the port goes back
off afterwards and the speaker is power-cycled into its normal boot. Both were done.

### The first attempt measured nothing and was written up as if it had

An earlier cold boot on the same cable produced no device, and that was recorded as
evidence that the two models differ. It was evidence of nothing: `usb3-port1` was
`disabled` (the software cable-pull from the Wireless Link work, left off), so nothing
could enumerate whatever the ROM intended. `usb-port.sh status` says which it is, and
reading it first is now part of the test rather than an afterthought.

The straps were read before that was found and pointed the same way.

### The straps, read

`CONTROL_STATUS` at `0x44E10040` holds the straps, but `/dev/mem` answers `Bad address` for it,
although this kernel has `# CONFIG_STRICT_DEVMEM is not set`: ARM restricts `/dev/mem` to RAM in
`valid_phys_addr_range()` whatever that option says, so no device register is reachable that
way on this architecture. A loadable module can read it, and changes nothing persistent (at the
time `insmod` was on the box and `modules_disabled` was 0; modules are now locked after boot,
[ADR 0132](../../decisions/0132-modules-locked-after-boot.md)).

A one-off read-only kernel module, `sysboot.ko` (removed since), loaded over
ssh on 2026-09-19, read the register and changed nothing:

```
CONTROL_STATUS = 0x0040030b
SYSBOOT[15:0]  = 0x400b
SYSBOOT[4:0]   = 0x0b        01011b
```

`CONTROL_STATUS` carries `SYSBOOT[7:0]` in bits 7:0 and `SYSBOOT[15:8]` in bits 23:16,
so the upper byte is `0x40`: the same upper byte an OSD335x board documents for a
working configuration, which is a useful sign that the read is right rather than
accidental.

**What it does not yet say is what `01011` means.** TRM Table 26-7 holds the mapping and
it is not reproduced in any public source that could be checked; the three rows that are
quoted in the wild (`10011` as NAND/NANDI2C/MMC0/UART0, `10111` as MMC0/SPI0/UART0,
`11000` as SPI0/MMC0/USB0/UART0) all contradict a table written from memory, which is
exactly how the first version of this module printed a confident wrong answer. The
decode has been taken out rather than left to mislead.

### The same read on a Wireless Link, which settles it without the table

Built against the Wireless Link's own Buildroot kernel tree (it already carries a
`Module.symvers`, so no kernel build was needed) and loaded the same way:

```
ST10   CONTROL_STATUS = 0x0040030b   SYSBOOT[15:0] = 0x400b   SYSBOOT[4:0] = 01011b
WLA    CONTROL_STATUS = 0x0040030b   SYSBOOT[15:0] = 0x400b   SYSBOOT[4:0] = 01011b
```

**Identical.** The two boards are strapped the same, so the mask ROM builds the same boot
device list on both, and what that list contains no longer has to be read out of a table
nobody publishes: the Wireless Link's ROM demonstrably enumerates as `0451:6141` on a cold
power-on with NAND intact, and accepts an MLO over RNDIS/DHCP/TFTP
(`os/README.md`, 2026-08-18).

The MLO it accepted hashes `7d272c4653c978eb...`, which is this ST10's own mtd0 SPL, byte
for byte: both models ship the same image, found independently.

So the ST10's ROM should take an SPL over USB the way the Wireless Link's does. What the
straps do not prove is the wiring: that USB0 reaches a connector, in peripheral mode, at
ROM time. That port reads a recovery stick through an OTG adapter on the bench, which
says it is USB0 on a connector that can do both roles.

That leaves one explanation for the negative cable test, and it is physical rather than
electrical: with an OTG adapter attached the port is a host and cannot enumerate as a
device, whatever the ROM intends.

## USB on this model: three different things

**The gadget link is a setting.** Stock `rhino` loads `g_ether` and puts `usb0` on
`203.0.113.1/30`, the same address the Wireless Link uses, so this board has always had a USB
link. Early SoundTap builds lost it twice over: `# CONFIG_USB is not set` in the kernel
fragment, and `dr_mode = "host"` in the ST10 device tree for the RAM-boot work, which read a
root filesystem from a stick. Both are reversed now: `os/board/image/linux.fragment` sets
`CONFIG_USB=y`, `os/board/common/dts/ti/omap/am335x-soundtap-st10.dts` asks for
`dr_mode = "peripheral"`, and `os/overlay/etc/init.d/gadget-console` brings up an ACM console and an
RNDIS link on 192.168.7.2: a way in that does not depend on Wi-Fi.

**Stock U-Boot can already write NAND from USB.** The environment carries
`load_MLO_from_usb`, `write_MLO_to_nand`, `load_uboot_from_usb`, `write_uboot_to_nand`,
`perform_uboot_update` and `update_all`, and `usb_uboot` loads a `u-boot.img` from a stick
and `go`es straight into it. So a USB write path exists below Linux as well.

**Mask-ROM USB boot is not a setting.** It happens before any SoundTap code, and whether it
is attempted is decided by the SYSBOOT strapping resistors on the board. No kernel option,
device tree property or environment variable reaches it; only reading the straps, as above,
tells.

The gadget gives a write path for installing; the mask ROM gives a way back when what was
installed does not run.
