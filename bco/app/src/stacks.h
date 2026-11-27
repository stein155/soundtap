/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* CPU mode stacks live in SDRAM (>= 16 MB, hw-map), far above the image. The 64 KB internal
 * SRAM is left to the WiLD rings and buffers, which the core must reach through UMR0
 * (0x500000 = 0x400000): vectors/trampolines 0x400000-0x401000, RX buffers 0x401000-0x404020
 * (7 x 0x6e0), TX buffers 0x504100-0x5070b0 (7 x 0x6d0, wifi.c), command rings 0x507220-0x5095a0, frame +
 * supply rings 0x509600-0x50a140; the wired port's MACB rings and buffers 0x50a200-0x50d020
 * (wired.c). The budget: docs/hardware/st20/bco-network-card.md, "The SRAM budget".
 * Used by src/app.S. */
#define STACK_UND 0x60f01000
#define STACK_ABT 0x60f02000
#define STACK_FIQ 0x60f03000
#define STACK_IRQ 0x60f04000
#define STACK_SVC 0x60f10000
