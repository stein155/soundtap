# ST20 mask-ROM USB recovery

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Recorded on 2026-09-27: the procedure brought back an ST20 whose mtd0 held an SPL that hung.
The rescue-chain history and boot-guard sections below describe that historical experiment, including
stock copies and offsets that the current install no longer preserves. They are not a current
restore recipe. Current boot/rollback is in [ADR 0009](../../decisions/0009-mainline-linux-own-bootloaders.md);
permission and a verified board-specific restore plan remain mandatory under
[the contract](../../../CONTRIBUTING.md#the-contract).

## Why the ST20 needs it

The ST20 straps are SYSBOOT 10010b, which gives the boot order NAND, NANDI2C, USB0, UART0. On the WLA and ST10 the ROM tries USB first. On the ST20 it never gets as far as USB while any of NAND blocks 0-3 carries a valid header. An SPL that is valid but hangs therefore bricks the speaker, and no remote path undoes that.

## Forcing the ROM to USB0

The ROM gives up on NAND when R/B# stays busy for its roughly 250 ms detection window. Holding R/B# low at power-on therefore sends the ROM on to USB0.

On the back of the Wi-Fi module (412568), behind the NAND U402, there is a field of round ICT test pads:

- Pad 9 is R/B#. It has a 10.0 kΩ pull-up, R409, to pad 4.
- Pad 4 is the NAND 1.8 V rail. Never pull it low.

The speaker has to be open. FFC J607, which goes to the PA board, must be connected, or the module gets no power.

1. Set a multimeter to its 2000 µA range. Put the black lead on the shield frame and the red lead on pad 9. The meter shunt limits the current if the probe slips.
2. Power on (the bench setup uses a smart-plug switch with a delay). Keep holding the probe for about 3 s after power-on.
3. The Pi sees `0451:6141 "AM335x USB"`. After that, nobody needs to hold anything.

## Repairing the boot chain from the repo

`tools/recovery/rom-usb-restore.py --model st20` writes the release's ST20 SPL into all four
SPL copies and the shared U-Boot into both halves of `NAND.U-Boot`, in the installer's order
(spare first, first copy last), and can put back partitions from this board's own
`nand-backup-ssh.sh` backup. Hold the pad as above when the tool says to power on. The ROM
gets the release's own ST20 SPL (`st20-spl/MLO` without its headers), which asks USB first and
takes the RAM U-Boot the tool builds for that run. Start with `--dry-run`: it prints every
erase and write with its flash range and touches nothing. The full description is in
[Developer recovery tools](../install-records.md#developer-recovery-tools).

Unproven on the ST20: this loader (`tools/recovery/rom-usb-loader`) serving the ST20 SPL from the
ROM, and the script writing NAND. The 2026-09-27 rescue below used dnsmasq instead, and relied
on stock copies the current chain no longer has.

## The 2026-09-27 rescue chain (history)

A dnsmasq in Docker on 198.51.100.0/24 told the stages apart by vendor class: the ROM got
`u-boot-spl.bin` (the raw ST20 SPL), the SPL got `u-boot.img`, and that U-Boot fetched a
script, which only read: it checked that mtd2 still held the stock SPL and reported what
blocks 0 and 1 held. Command scripts fetched after it erased blocks 0 and 1, verified both
read back as FF, and reset; the ROM then booted the untouched stock SPL out of mtd2. Progress
came back as TFTP requests for `mark-*` names. Those scripts relied on a local copy of the
stock SPL and were removed; the tool above replaces them.

### Host traps of that setup

- NetworkManager will fight over every new `usbN` (each stage is a new gadget). It needs an unmanaged rule, such as `unmanaged-devices=mac:de:ad:be:ef:00:00;driver:rndis_host;driver:cdc_ether`, for as long as the rescue runs. Remove the rule afterwards.
- A /30 range runs out of addresses because the ROM, the SPL and U-Boot each want a lease. Use /24.

## WP#: why the first erase changed nothing

The SCM module (ST20) routes NAND WP# to `gpmc_wpn`. The SM2 modules (ST10, WLA) use that pad for Wi-Fi. SoundTap's NAND pin mux left the pad alone, so WP# stayed low, and every erase and write "succeeded" while doing nothing. The rescue fixes this at runtime with `mw.l 0x44e10874 0x20`, which is the stock mux value. The permanent fixes are:

- U-Boot: `os/bootloader/uboot/uboot-2026.07-scm-nand-wp.patch`, applied when the environment says `module_type=scm`.
- Kernel: `nand_wp_pins` in `am335x-soundtap-st20.dts`.

## Testing an SPL before it goes near mtd0

A ROM-like test script (`spl-romlike-test.sh`, removed since) got stock U-Boot to load an SPL into SRAM, and before the jump it switched off what a NAND-first ROM leaves off. Both runs below were done on 2026-09-27.

- **The SPL that bricked the speaker:** it hung, and the Pi saw no USB at all, which is exactly the brick. So the test reproduces the ROM-entry state.
- **The fixed SPL:**
  - Its RNDIS gadget enumerated at once, and gave up 3 s later because nobody answered. The ROM-USB run with the old SPL took 45 s.
  - It then read NAND. mtd4+0x100000 was empty, so it fell back to stock U-Boot at mtd4+0x80000, and stock booted.

That covers USB first, the fallback to NAND, the check on each copy and the stock fallback, all from the ROM state. It does not prove SoundTap's U-Boot as primary copy: that needs a write to mtd4.

2026-09-27, with SoundTap's U-Boot written to mtd4+0x100000: the stock SPL in mtd2 reads only +0x80000, so that write is inert for the stock boot. The same ROM-like test then ran the whole chain in one go:

1. The SPL gadget came up for 8 s and nobody answered.
2. The SPL loaded SoundTap's U-Boot from NAND.
3. SoundTap's U-Boot showed its d022 gadget.
4. SoundTap came up, with 6.12.0 and `soundtap.bootloader=u-boot-2026.07` on the kernel command line.

A reboot returned the speaker to stock. What remains is SoundTap's SPL in mtd0.

## Boot guard: a hang before Linux ends in stock, on its own

The dead boots (1.5 W, no USB) came from the SPL calling `watchdog_disable()`: with mtd0 holding SoundTap's SPL, any hang after that point was permanent. The SPL now leaves WDT1 running for 120 s (nominal: WDT1 runs from the RC32K oscillator, and in practice it fired after about 100 s). SoundTap's U-Boot leaves it alone, and the kernel takes it over with `omap_wdt.early_enable=1`. U-Boot used to take it over with `WATCHDOG_AUTOSTART`, but then it serviced the watchdog from every polling loop. On 2026-09-27 its gadget never enumerated, and the speaker sat silent for over six minutes with no reset. The rescue script stops the watchdog itself, because its command loop never ends.

RTC SCRATCH2 (0x44e3e068) counts the attempts as `0x57200000 | tries`. A warm or watchdog reset leaves it alone; Linux's pm33xx owns SCRATCH0 and 1. SoundTap's U-Boot leaves the count standing through Linux, and soundtapd clears it after a minute (`ST20ConfirmBoot`); a deliberate `/api/reboot` clears it first. Past two attempts the SPL skips USB and SoundTap's primary copy, stops the watchdog and loads the copy at mtd4+0x80000. Until 2026-09-30 that was Bose's U-Boot, and stock Linux then rewrote mtd0 from mtd2. Since the installer puts SoundTap's U-Boot there too, it is SoundTap's second copy: it still offers the USB rescue, then boots the other slot once more, now without the watchdog until the kernel starts it (`omap_wdt.early_enable=1`). A hang before that point needs a power cycle.

ROM-like harness runs on 2026-09-27:

| Test | Setup | Result |
|---|---|---|
| A2 normal | scratch2 cleared | SPL gadget, then stock copy with the watchdog stopped; scratch2 = 0x57200001 |
| B forced fallback | scratch2 = 0x57200002 before `go` | no SPL gadget, stock in 43 s; scratch2 = 0x57200003 |
| C watchdog fire | SPL fetched a `b .` image as u-boot.img over USB TFTP | reset about 100 s after the SPL gadget, then stock; scratch2 = 0x57200001 |

Harness trap: stock U-Boot leaves the RTC module unclocked, so a `mw.l` to 0x44e3e0xx in the one-shot aborts before `go`. Write `mw.l 44e00804 2; mw.l 44e00800 2` first. The one-shot restores `bootcmd` before that point, so the cost is one power cycle.

Stock also erases mtd4+0x100000 on its own, so whatever is put there does not survive a stock boot.

The runs above all fell back to stock before SoundTap's U-Boot started, which is why they passed. In the real ROM chain, nine resets in a row never fell back. The cause was U-Boot's own `BOOTCOUNT_AM33XX`, which keeps its count in the same SCRATCH2. It wrote `0xb0010001`, stored byte-swapped and read back as `0x010001b0` by a U-Boot script on 2026-09-27. It did this at every start, so the SPL never saw its magic and counted from 1 again. The chain U-Boot is now built with `BOOTCOUNT_LIMIT` off.

Harness trap: `mark-*` TFTP requests are sometimes lost; a request times out and dnsmasq never logs it. Send each mark two or three times. To read a register, copy it to DDR with `cp.l` and test one byte at a time with `itest.b ... == 0xNN`.

## The BCO needs clkout1

The BCO's reference clock is clkout1, on pad `xdma_event_intr0` (0x9b0) in mode 3. Stock U-Boot muxes it; the pad resets to GPIO. From the ROM chain, no stage muxed it, so the BCO never enumerated and there was no LAN. Measured in the harness on 2026-09-27:

| Pad 0x9b0 before the kernel | Result |
|---|---|
| mode 7 | no cdc_ether, no LAN |
| mode 7, BCO powered on, mode 3 a few seconds later | LAN in 6 s |
| the same, followed by a BCO power cycle | LAN in 6 s |

So the clock may arrive late, and no power cycle is needed. `power_control_pins` in `am335x-soundtap-st20.dts` now muxes the pad.

## Why no gadget showed on a cold boot from mtd0

PER CLKDCOLDO (CM_CLKDCOLDO_DPLL_PER, 0x44e0047c) carries the 960 MHz USB PHY clock. It resets gated. The ROM opens it only for its own USB boot; stock U-Boot sets 0x300. SoundTap's SPL leaves the PLLs to the ROM, so from NAND it ran with the PHY unclocked. The SPL and U-Boot gadgets never enumerated, and only Linux, which opens the gate itself, showed up. The harness had inherited stock's 0x300 and so missed it.

- Reproduced: that SPL with `mw.l 44e0047c 0` before `go` showed no SPL gadget.
- Fixed: `st20_spl_clocks()` writes 0x300, and the harness now gates CLKDCOLDO by default.
- Proven 2026-09-27: that SPL, with CLKDCOLDO gated, after a cold power cycle. The SPL gadget came up, the SPL fetched `u-boot.img` over USB TFTP, SoundTap's U-Boot showed its d022 gadget, SoundTap 6.12 booted, and scratch2 read 0 afterwards. A reboot returned the speaker to stock.

## A/B slots under SoundTap's chain

Since ADR 0009 the ST20 boots the same U-Boot as the ST10 and WLA, from `NAND.U-Boot`+0, with `bootcmd` selecting the slot and the RTC bootcount falling back to the other one. The way back to stock is a stock package through `/api/firmware`, or the ROM-USB rescue.

