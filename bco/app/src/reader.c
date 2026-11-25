/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* WSRAM logger reader. The logger patch (patch-logger.py) hooks FUN_60068494 and stores one
 * word per core-ring funnel call into WSRAM at 0x3001f800:
 *   [BUF]   = MAGIC 0x52544348 ("RTCH")
 *   [BUF+4] = count (capped 256)
 *   [BUF+8 + i*4] = (cmd&0xff) | (module&0xff)<<8 | (len&0xff)<<16 | (kind&0xff)<<24
 * This app runs in the stage2 slot (0x60008000) after a warm reboot and hex-dumps the buffer
 * over the proven say() UART path. It deliberately never loads/releases the WiLD core, so the
 * WSRAM the logger left behind survives (WSRAM is outside the image-copy region). Read-only. */
#include "board.h"

#define BUF 0x3001f800u
#define MAGIC 0x52544348u

void app_main(void)
{
    matrix();
    UART[2] = 2 * ahb_hz() / BAUD - 2;

    say("RTCH reader\r\n");
    /* scan all WSRAM (0x30000000, 128 KiB) for the logger header, since 0x3001f800 may be
     * core-used. report the first hit address + records; fall back to a raw window dump. */
    volatile unsigned *ws = (volatile unsigned *)0x30000000u;
    unsigned hit = 0;
    for (unsigned i = 0; i < 0x8000; i++) {         /* 0x20000 bytes / 4 */
        if (ws[i] == MAGIC) { hit = (unsigned)&ws[i]; break; }
    }
    hex("scanhit ", hit);
    if (hit) {
        const volatile unsigned *b = (volatile unsigned *)hit;
        unsigned n = b[1];
        hex("count ", n);
        if (n > 256)
            n = 256;
        for (unsigned i = 0; i < n; i++)
            hex("rec ", b[2 + i]);
    } else {
        say("no magic in WSRAM; raw 0x3001f800:\r\n");
        const volatile unsigned *b = (volatile unsigned *)BUF;
        for (unsigned i = 0; i < 8; i++)
            hex("raw ", b[i]);
    }
    say("RTCH done\r\n");
}
