# 0131. The ST20's U-Boot paints the first boot frame on the OLED

Status: accepted — 2026-10-07

## Context

Without help from the bootloader the ST20's display stays dark until all of SoundTap has started;
there should be a picture right after the plug goes in. The OLED (128x100, 4-bit grey, SPI) is
driven from userspace by soundtapd, and its 5 V supply, the TPS65910's VDD3 boost, is switched on
only by Linux. Without VDD3 the panel shows a faint, washed-out image.

## Decision

SoundTap's U-Boot ([ADR 0009](0009-mainline-linux-own-bootloaders.md)), on the ST20 only (SYSBOOT
10010b), from `board_init`:

1. switches VDD3 on: one polled I2C2 write of the value Linux leaves in the TPS65910 (register
   0x27, 0x05);
2. runs soundtapd's panel reset and init sequence, bit-banged on the same pads as GPIO (SPI
   mode 3);
3. paints one frame, soundtapd's `bootFrame(0)`: the empty rounded bar outline that soundtapd
   then fills, so nothing moves when it takes over. It is run-length encoded in a generated
   header; a Go test regenerates and checks the header, so the two cannot drift.

All waits are bounded; a failed PMIC write prints one console line and skips the panel, and the
boot continues. Linux remuxes the pads to McSPI1 and redoes everything.

Stock initialises the panel before it switches VDD3 on, and runs the init twice. U-Boot switches
VDD3 on first and inits once, so the controller starts on its full supply. Whether stock's order
matters is open until the ST20 boot-frame row of the hardware checklist proves ours.

The kernel keeps the frame: the ST20's device tree sets `ti,no-reset-on-init` on GPIO2's ti-sysc
target module. Without it ti-sysc soft-reset the module about 0.7 s into boot, the panel's reset
(gpio2_18) and chip enable (gpio2_24) became inputs, and the panel went dark until soundtapd
opened it, about 13 s later.

soundtapd takes the lit panel over without a blank: when it opens the panel and finds the reset
line already an output driven high (U-Boot's frame, or an earlier soundtapd), it skips the reset
pulse, which clears the controller's memory and switches the display off, and sends the init
without its display-off; its first frame then replaces U-Boot's in place. A panel whose reset line
was not left high, a dark one, gets the reset and the whole init as before. The ST20 went dark for
a moment between U-Boot's bar and soundtapd's (the owner, 2026-11-28).

Alternatives that lost: U-Boot's video and SPI drivers with a device-tree panel node (far more code
and configuration for one static frame, and the shared U-Boot device tree is the WLA's); starting
the display earlier in Linux, by a kernel driver or an init-time helper (it still waits for the
kernel and rootfs, seconds rather than one).

## Consequences

- A picture as soon as U-Boot runs instead of after the userspace boot. On the ST20 that is
  about 3.5-4 s after power-on, estimated from the code, not yet measured: before U-Boot the SPL
  asks the micro-B port for a USB host first (3 s for the RNDIS connect timeout with nothing
  plugged in, about 11 s with a host that does not answer BOOTP) and only then loads U-Boot from
  NAND. In U-Boot the frame is already the first thing `board_init` does, right after relocation
  and before NAND, the environment and autoboot.

  | Before the frame (micro-B port empty) | Before | Now |
  | --- | --- | --- |
  | BCO restart: supply off for the stock loaders' 1 s, then on | 1 s, before the ask | 0 s: off in `s_init`, on after the ask, at least 1 s later |
  | USB ask (RNDIS connect timeout) | 3 s | 3 s |
  | ROM, SPL, DDR, NAND read of U-Boot, relocation | about 0.5-1 s | about 0.5-1 s |
  | Power-on to frame | about 4.5-5 s | about 3.5-4 s |

  The BCO's supply is now off for the length of the ask (3 s with nothing plugged in) instead
  of exactly 1 s, and never less than 1 s (`spl_bco_on` waits out the rest when the ask returns
  sooner); it is back on before the SPL reads NAND or starts an image from USB. An SPL built
  without the USB ask still waits the second out in `s_init`. An earlier picture still needs the
  frame in the SPL; any SPL change rewrites `mtd0`-`mtd3`.
- The frame is static: the bar starts to fill once soundtapd takes over, from the same pixels.
- The boot frame is the evidence for soundtapd's line timing: U-Boot changes chip enable and D/C
  with nothing but a register write between them and the bytes, and the panel shows the frame.
  soundtapd waits 200 us around each line change, not the 10 ms it once did, which lets the
  panel's animations run at the 20 frames a second they are drawn at
  ([the hardware contract](../hardware/st20/hardware-contract.md#display), `app/oled/wire.go`).
- A change to `bootFrame` needs `UPDATE_SPLASH=1 go test -run TestUbootSplash` in
  `app/internal/panel`, and reaches the speaker only with an install that changes the bootloader.

## Code

- `os/bootloader/uboot/uboot-2026.07-st20-splash.patch`: VDD3, the panel init and the frame.
- `os/bootloader/uboot/soundtap-splash.h`: the generated frame.
- `app/internal/panel/boot.go:bootFrame` and `app/internal/panel/splash_internal_test.go`: the frame and the
  test that regenerates and checks the header.
- `app/oled/spidev.go`: soundtapd's panel init sequence that U-Boot repeats, and how a lit panel
  is taken over (`panelStart`); `app/oled/spidev_linux.go` opens it and `app/oled/wire.go`
  times its lines.
- `os/board/common/dts/ti/omap/am335x-soundtap-st20.dts`: GPIO2 kept out of ti-sysc's reset.
