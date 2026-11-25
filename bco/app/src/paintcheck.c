/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Persistence probe: does a host-DRAM (or WSRAM) word survive a warm XMODEM reflash + reboot?
 * On every run it FIRST reports the current contents of each candidate address (so run N shows
 * what run N-1 painted), THEN paints MAGIC + 0xa5a5a5a5 into each. Flash it twice: run1 paints,
 * run2's report reveals which addresses held the paint across the warm reboot. Read-only wrt the
 * WiLD core (never loads/releases it). Picks the logger buffer location from the survivors. */
#include "board.h"

#define MAGIC 0x52544348u

static const unsigned addrs[] = {
    0x60800000u, 0x60a00000u, 0x60c00000u, 0x60e00000u, 0x60f20000u, /* host DRAM */
    0x3001f800u,                                                      /* WSRAM control */
};
#define N (sizeof(addrs) / sizeof(addrs[0]))

void app_main(void)
{
    matrix();
    UART[2] = 2 * ahb_hz() / BAUD - 2;
    say("RTCH paintcheck\r\n");
    for (unsigned i = 0; i < N; i++) {
        const volatile unsigned *p = (volatile unsigned *)addrs[i];
        hex("at ", addrs[i]);
        hex(" w0 ", p[0]);
        hex(" w1 ", p[1]);
    }
    say("-- painting --\r\n");
    for (unsigned i = 0; i < N; i++) {
        volatile unsigned *p = (volatile unsigned *)addrs[i];
        p[0] = MAGIC;
        p[1] = 0xa5a5a5a5u;
    }
    say("RTCH painted\r\n");
}
