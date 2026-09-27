/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

.syntax unified
.arm
.global _start
_start:
    /* Records at +0x00..+0x3f are filled by bco/tools/bco-pack. */
    .space 0x40
    mrs r0, cpsr
    orr r0, r0, #0xc0
    msr cpsr_c, r0
    /* BFL enters here at SRAM 0x40da00. Turning the MMU off matches the stock
     * first stage; the cache clean/invalidate transition is not yet proven on
     * hardware (ADR 0031). */
    mrc p15, 0, r0, c1, c0, 0
    bic r0, r0, #1
    mcr p15, 0, r0, c1, c0, 0
    ldr sp, =0x0040da00
    bl first_stage
1:  b 1b
