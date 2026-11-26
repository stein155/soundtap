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

1. switches VDD3 on: one polled I2C2 write of the value Linux leaves in the TPS65910;
2. runs soundtapd's panel reset and init sequence, bit-banged on the same pads as GPIO (SPI
   mode 3);
3. paints one frame, soundtapd's `bootFrame(0)` ("SOUNDTAP", the progress track, "STARTING"),
   run-length encoded in a generated header. A Go test regenerates and checks the header, so the
   two cannot drift.

All waits are bounded; a failed PMIC write skips the panel and the boot continues. Linux remuxes
the pads to McSPI1 and redoes everything.

Alternatives that lost: U-Boot's video and SPI drivers with a device-tree panel node (far more code
and configuration for one static frame, and the shared U-Boot device tree is the WLA's); starting
the display earlier in Linux, by a kernel driver or an init-time helper (it still waits for the
kernel and rootfs, seconds rather than one).

## Consequences

- A picture within about a second of power-on instead of after the userspace boot.
- The frame is static: the progress sweep starts moving once soundtapd takes over.
- A change to `bootFrame` needs `UPDATE_SPLASH=1 go test -run TestUbootSplash` in
  `app/internal/panel`, and reaches the speaker only with an install that changes the bootloader.

## Code

- `os/bootloader/uboot/uboot-2026.07-st20-splash.patch`: VDD3, the panel init and the frame.
- `os/bootloader/uboot/soundtap-splash.h`: the generated frame.
- `app/internal/panel/boot.go:bootFrame` and `app/internal/panel/splash_internal_test.go`: the frame and the
  test that regenerates and checks the header.
- `app/oled/spidev_linux.go`: soundtapd's panel init sequence that U-Boot repeats.
