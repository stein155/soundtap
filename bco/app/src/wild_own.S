/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Our own WiLD core image (bco/wild/fw/build.sh), for the WILD_OWN probe in wifi.c. */
    .section .rodata
    .balign 4
    .global wild_own, wild_own_end
wild_own:
    .incbin WILD_BIN
wild_own_end:
